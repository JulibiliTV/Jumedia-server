package plex

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"jumedia-server/internal/plex/testdata"
)

func TestPlexAccountServerPermission(t *testing.T) {
	testdata.Account(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/identity" || len(body) > 0 || r.Header.Get("X-Plex-Token") != "" {
			t.Error("credentials sent to unauthorized server")
		}
		io.WriteString(w, `{"MediaContainer":{"machineIdentifier":"unauthorized-server"}}`)
	}))
	defer up.Close()
	c, _ := New(up.URL, "fixture-user", "fixture-password", true)
	defer c.Close()
	if _, err := c.Probe(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatal("missing server permission not rejected", err)
	}
}
