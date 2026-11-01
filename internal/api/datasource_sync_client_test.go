package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncRequestBudget(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	var active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		time.Sleep(320 * time.Millisecond)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	c, err := newSourceSyncClient(server.URL, "fixture", "", true, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out any
			if err := c.call(t.Context(), "GET", "/test", nil, "", nil, &out); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak.Load() != 2 {
		t.Fatalf("peak=%d, want overlapping requests bounded by 2", peak.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 8 || starts[7].Sub(starts[0]) < 1350*time.Millisecond {
		t.Fatal("rate", starts)
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 180*time.Millisecond {
			t.Fatal("burst", starts)
		}
	}
}

func TestSyncRequestRetries(t *testing.T) {
	for _, kind := range []string{"429", "503", "502", "504", "disconnect", "body", "400", "exhausted", "login"} {
		t.Run(kind, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := count.Add(1)
				if kind == "exhausted" || kind == "400" || n == 1 {
					switch kind {
					case "429":
						w.Header().Set("Retry-After", "1")
						w.WriteHeader(429)
						return
					case "503", "exhausted", "login":
						w.WriteHeader(503)
						return
					case "502":
						w.WriteHeader(502)
						return
					case "504":
						w.WriteHeader(504)
						return
					case "400":
						w.WriteHeader(400)
						return
					case "disconnect":
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					case "body":
						w.Header().Set("Content-Length", "100")
						fmt.Fprint(w, `{`)
						return
					}
				}
				fmt.Fprint(w, `{"AccessToken":"token","User":{"Id":"user"}}`)
			}))
			defer server.Close()
			c, err := newSourceSyncClient(server.URL, "fixture", "", true, 50, 2)
			if err != nil {
				t.Fatal(err)
			}
			defer c.close()
			var delays []time.Duration
			c.sleep = func(ctx context.Context, d time.Duration) error { delays = append(delays, d); return ctx.Err() }
			var out any
			if kind == "login" {
				_, _, err = c.authorize(t.Context(), "")
			} else {
				err = c.call(t.Context(), "GET", "/test", nil, "", nil, &out)
			}
			want := int32(2)
			if kind == "400" {
				want = 1
			}
			if kind == "exhausted" {
				want = 4
			}
			if count.Load() != want {
				t.Fatalf("attempts %d want %d", count.Load(), want)
			}
			if (err != nil) != (kind == "400" || kind == "exhausted") {
				t.Fatal(err)
			}
			if kind == "exhausted" && fmt.Sprint(delays) != "[1s 2s 4s]" {
				t.Fatal(delays)
			}
			if kind == "429" && (len(delays) != 1 || delays[0] != time.Second) {
				t.Fatal(delays)
			}
		})
	}
}

func TestSyncRequestCancellationAndRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "120"); w.WriteHeader(429) }))
	defer server.Close()
	c, err := newSourceSyncClient(server.URL, "fixture", "", true, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	ctx, cancel := context.WithCancel(t.Context())
	c.sleep = func(ctx context.Context, d time.Duration) error {
		if d != 60*time.Second {
			t.Errorf("cap=%s", d)
		}
		cancel()
		return sourceSyncSleep(ctx, d)
	}
	var out any
	start := time.Now()
	if err = c.call(ctx, "GET", "/test", url.Values{}, "", nil, &out); err != context.Canceled || time.Since(start) > time.Second {
		t.Fatal(err)
	}
	// Rate and concurrency waits must also be cancelable.
	for _, full := range []bool{false, true} {
		if full {
			c.slots <- struct{}{}
		}
		start = time.Now()
		if err = c.call(ctx, "GET", "/test", nil, "", nil, &out); err != context.Canceled || time.Since(start) > 100*time.Millisecond {
			t.Fatal(err)
		}
		if full {
			<-c.slots
		}
	}
}

func TestSyncUnauthorizedRenewal(t *testing.T) {
	var logins atomic.Int32
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/AuthenticateByName") {
			n := logins.Add(1)
			fmt.Fprintf(w, `{"AccessToken":"token%d","User":{"Id":"user"}}`, n)
			return
		}
		gets.Add(1)
		if r.Header.Get("X-Emby-Token") == "token1" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"Items":[]}`)
	}))
	defer server.Close()
	c, err := newSourceSyncClient(server.URL, "fixture", "", true, 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	// Concurrent rejected requests share the renewal session and budget.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Episodes(t.Context(), "series"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if logins.Load() != 2 || gets.Load() < 5 {
		t.Fatal(logins.Load(), gets.Load())
	}
}

func TestSyncRetryAfterDelays(t *testing.T) {
	for _, raw := range []string{"2", "0", "invalid", "-1", "120"} {
		t.Run(raw, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", raw); w.WriteHeader(429) }))
			defer server.Close()
			c, err := newSourceSyncClient(server.URL, "fixture", "", true, 50, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer c.close()
			var delays []time.Duration
			c.sleep = func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }
			var out any
			if c.call(t.Context(), "GET", "/", nil, "", nil, &out) == nil {
				t.Fatal("expected exhausted retry")
			}
			want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
			if raw == "2" {
				want = []time.Duration{2 * time.Second, 2 * time.Second, 2 * time.Second}
			}
			if raw == "120" {
				want = []time.Duration{60 * time.Second, 60 * time.Second, 60 * time.Second}
			}
			if !reflect.DeepEqual(want, delays) {
				t.Fatal(want, delays)
			}
		})
	}
}
