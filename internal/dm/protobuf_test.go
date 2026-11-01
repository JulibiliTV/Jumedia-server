package dm

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"jumedia-server/internal/store"
)

type field struct {
	n, wire int
	v       uint64
	b       []byte
}

func decode(t *testing.T, b []byte) []field {
	t.Helper()
	out := []field{}
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			t.Fatal("bad tag")
		}
		b = b[n:]
		f := field{n: int(tag >> 3), wire: int(tag & 7)}
		switch f.wire {
		case 0:
			f.v, n = binary.Uvarint(b)
			if n <= 0 {
				t.Fatal("bad varint")
			}
			b = b[n:]
		case 2:
			var size uint64
			size, n = binary.Uvarint(b)
			if n <= 0 || size > uint64(len(b)-n) {
				t.Fatal("bad bytes")
			}
			b = b[n:]
			f.b = b[:size]
			b = b[size:]
		case 5:
			if len(b) < 4 {
				t.Fatal("bad float")
			}
			f.b = b[:4]
			b = b[4:]
		default:
			t.Fatal("bad wire")
		}
		out = append(out, f)
	}
	return out
}
func TestWriterWireContract(t *testing.T) {
	b := (&Writer{}).Uint(1, 150).String(2, "中").Bool(3, false).Float(4, .5).Finish()
	if hex.EncodeToString(b) != "0896011203e4b8ad1800250000003f" {
		t.Fatalf("%x", b)
	}
	if len((&Writer{}).Uint(1, 0).String(2, "").Finish()) != 0 {
		t.Fatal("zero omitted")
	}
}
func TestSegment(t *testing.T) {
	items := []store.Danmaku{{ID: 1536, CID: 50001, Progress: 1000, Mode: 1, Fontsize: 25, Color: 0xffffff, MID: 1, Content: "测试", Ctime: 100, Weight: 7}, {ID: 1537, CID: 50001, Progress: 360000}, {ID: 1538, CID: 50002, Progress: 1000}}
	root := decode(t, Segment(items, 50001, 1))
	if len(root) != 1 || root[0].n != 1 {
		t.Fatal(root)
	}
	fields := decode(t, root[0].b)
	got := map[int]field{}
	for _, f := range fields {
		got[f.n] = f
	}
	if got[1].v != 1536 || got[2].v != 1000 || got[3].v != 1 || got[4].v != 25 || got[5].v != 0xffffff || string(got[6].b) != "1" || string(got[7].b) != "测试" || got[8].v != 100 || got[9].v != 7 || string(got[12].b) != "1536" {
		t.Fatal(got)
	}
}
func TestWebView(t *testing.T) {
	root := decode(t, WebView(nil))
	if len(root) != 2 || root[0].n != 1 || root[0].v != 1 || root[1].n != 10 {
		t.Fatal(root)
	}
	fields := decode(t, root[1].b)
	got := map[int]field{}
	for _, f := range fields {
		got[f.n] = f
	}
	for _, n := range []int{1, 4, 5, 6, 7, 9, 10} {
		if got[n].v != 1 {
			t.Fatal(got)
		}
	}
	if got[2].v != 0 || got[3].v != 1 || got[12].v != 50 || math.Float32frombits(binary.LittleEndian.Uint32(got[11].b)) != .8 {
		t.Fatal(got)
	}
	root = decode(t, WebView(map[string]string{"dm_switch": "false", "ai_switch": "true", "opacity": "0.5"}))
	for _, f := range decode(t, root[1].b) {
		if f.n == 1 && f.v != 0 || f.n == 2 && f.v != 1 {
			t.Fatal(f)
		}
	}
}

func TestWebViewNumberConversion(t *testing.T) {
	root := decode(t, WebView(map[string]string{"opacity": "", "speedplus": "invalid", "fontsize": "0x2"}))
	got := map[int]float32{}
	for _, f := range decode(t, root[1].b) {
		if f.wire == 5 {
			got[f.n] = math.Float32frombits(binary.LittleEndian.Uint32(f.b))
		}
	}
	if got[11] != 0 || !math.IsNaN(float64(got[13])) || got[14] != 2 {
		t.Fatal(got)
	}
}
