package api

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"
)

func TestLiveChatPackets(t *testing.T) {
	a := encodeLivePacket(7, []byte(`{"uid":0}`))
	b := encodeLivePacket(2, nil)
	joined := append(append([]byte{}, a...), b...)
	var compressed bytes.Buffer
	w := zlib.NewWriter(&compressed)
	_, _ = w.Write(joined)
	_ = w.Close()
	packed := encodeLivePacket(5, compressed.Bytes())
	binary.BigEndian.PutUint16(packed[6:8], 2)
	for _, data := range [][]byte{joined, packed} {
		packets, err := decodeLivePackets(data)
		if err != nil || len(packets) != 2 || packets[0].operation != 7 || string(packets[0].body) != `{"uid":0}` || packets[1].operation != 2 {
			t.Fatalf("packets: %v %v", packets, err)
		}
	}
	// Swift's packet writer uses v1 even when the auth JSON requests protover=3.
	binary.BigEndian.PutUint16(a[6:8], 1)
	if _, err := decodeLivePackets(a); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, a[:15], append(joined, 0), packed[:len(packed)-1]} {
		if _, err := decodeLivePackets(data); err == nil {
			t.Fatalf("accepted invalid packet %x", data)
		}
	}
	for _, offset := range []int{0, 4, 6, 12} {
		bad := append([]byte{}, a...)
		if offset == 0 {
			binary.BigEndian.PutUint32(bad, 15)
		} else if offset == 12 {
			binary.BigEndian.PutUint32(bad[12:], 0)
		} else {
			binary.BigEndian.PutUint16(bad[offset:], 3)
		}
		if _, err := decodeLivePackets(bad); err == nil {
			t.Fatalf("accepted bad header at %d", offset)
		}
	}
	compressed.Reset()
	w = zlib.NewWriter(&compressed)
	_, _ = w.Write(bytes.Repeat([]byte{0}, liveChatMaxMessage+1))
	_ = w.Close()
	bomb := encodeLivePacket(5, compressed.Bytes())
	binary.BigEndian.PutUint16(bomb[6:], 2)
	if _, err := decodeLivePackets(bomb); err == nil {
		t.Fatal("accepted decompression bomb")
	}
}
