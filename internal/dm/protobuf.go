// Package dm implements the small protobuf subset used by the Web/TV clients.
package dm

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"

	"jumedia-server/internal/store"
)

type Writer struct{ data []byte }

func (w *Writer) varint(n uint64) {
	for n >= 128 {
		w.data = append(w.data, byte(n)|128)
		n >>= 7
	}
	w.data = append(w.data, byte(n))
}
func (w *Writer) Uint(field int, value int64) *Writer {
	if value != 0 {
		w.varint(uint64(field * 8))
		w.varint(uint64(max(0, value)))
	}
	return w
}
func (w *Writer) Bool(field int, value bool) *Writer {
	w.varint(uint64(field * 8))
	if value {
		w.varint(1)
	} else {
		w.varint(0)
	}
	return w
}
func (w *Writer) Float(field int, value float32) *Writer {
	w.varint(uint64(field*8 + 5))
	w.data = binary.LittleEndian.AppendUint32(w.data, math.Float32bits(value))
	return w
}
func (w *Writer) Bytes(field int, value []byte) *Writer {
	w.varint(uint64(field*8 + 2))
	w.varint(uint64(len(value)))
	w.data = append(w.data, value...)
	return w
}
func (w *Writer) String(field int, value string) *Writer {
	if value != "" {
		w.Bytes(field, []byte(value))
	}
	return w
}
func (w *Writer) Finish() []byte { return append([]byte{}, w.data...) }
func Segment(items []store.Danmaku, cid int64, index int) []byte {
	w := &Writer{}
	lo, hi := (index-1)*360000, index*360000
	for _, d := range items {
		if d.CID != cid || d.Progress < lo || d.Progress >= hi {
			continue
		}
		v := (&Writer{}).Uint(1, d.ID).Uint(2, int64(d.Progress)).Uint(3, int64(d.Mode)).Uint(4, int64(d.Fontsize)).Uint(5, int64(d.Color)).String(6, strconv.FormatInt(d.MID, 10)).String(7, d.Content).Uint(8, d.Ctime).Uint(9, int64(d.Weight)).String(12, strconv.FormatInt(d.ID, 10))
		w.Bytes(1, v.Finish())
	}
	return w.Finish()
}
func DmSegMobileReply(items []store.Danmaku, cid int64, index int) []byte {
	return Segment(items, cid, index)
}
func WebView(settings map[string]string) []byte {
	b := func(k string, def bool) bool {
		v, ok := settings[k]
		if !ok {
			return def
		}
		return v == "true"
	}
	n := func(k string, def float64) float64 {
		v, ok := settings[k]
		if !ok || v == "" {
			return def
		}
		f := number(v)
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return def
		}
		return f
	}
	f := func(k string, def float64) float64 {
		v, ok := settings[k]
		if !ok {
			return def
		}
		return number(v)
	}
	cfg := (&Writer{}).Bool(1, b("dm_switch", true)).Bool(2, b("ai_switch", false)).Uint(3, int64(n("ai_level", 1))).Bool(4, b("blocktop", true)).Bool(5, b("blockscroll", true)).Bool(6, b("blockbottom", true)).Bool(7, b("blockcolor", true)).Bool(9, b("preventshade", true)).Bool(10, b("dmask", true)).Float(11, float32(f("opacity", .8))).Uint(12, int64(n("dmarea", 50))).Float(13, float32(f("speedplus", 1))).Float(14, float32(f("fontsize", 1)))
	return (&Writer{}).Uint(1, 1).Bytes(10, cfg.Finish()).Finish()
}

// number mirrors Number(string) used by the Mock's floating-point fields.
func number(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			v, err := strconv.ParseUint(s[2:], base, 64)
			if err != nil {
				return math.NaN()
			}
			return float64(v)
		}
	}
	if s == "Infinity" || s == "+Infinity" {
		return math.Inf(1)
	}
	if s == "-Infinity" {
		return math.Inf(-1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}
