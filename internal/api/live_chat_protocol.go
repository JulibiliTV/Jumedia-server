package api

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
)

const liveChatMaxMessage = 64 << 10

var errLivePacket = errors.New("invalid live chat packet")

type livePacket struct {
	operation uint32
	body      []byte
}

func encodeLivePacket(operation uint32, body []byte) []byte {
	packet := make([]byte, 16+len(body))
	binary.BigEndian.PutUint32(packet, uint32(len(packet)))
	binary.BigEndian.PutUint16(packet[4:], 16)
	binary.BigEndian.PutUint32(packet[8:], operation)
	binary.BigEndian.PutUint32(packet[12:], 1)
	copy(packet[16:], body)
	return packet
}

func decodeLivePackets(data []byte) ([]livePacket, error) {
	budget := liveChatMaxMessage
	return decodeLivePacketLevel(data, 0, &budget)
}

func decodeLivePacketLevel(data []byte, depth int, budget *int) ([]livePacket, error) {
	if len(data) == 0 || len(data) > *budget || depth > 3 {
		return nil, errLivePacket
	}
	*budget -= len(data)
	var packets []livePacket
	for len(data) > 0 {
		if len(data) < 16 {
			return nil, errLivePacket
		}
		size := uint64(binary.BigEndian.Uint32(data))
		header := binary.BigEndian.Uint16(data[4:])
		version := binary.BigEndian.Uint16(data[6:])
		if header != 16 || size < 16 || size > uint64(len(data)) || binary.BigEndian.Uint32(data[12:]) != 1 {
			return nil, errLivePacket
		}
		body := data[16:int(size)]
		switch version {
		case 0, 1:
			packets = append(packets, livePacket{binary.BigEndian.Uint32(data[8:]), body})
		case 2:
			reader, err := zlib.NewReader(bytes.NewReader(body))
			if err != nil {
				return nil, errLivePacket
			}
			unpacked, err := io.ReadAll(io.LimitReader(reader, int64(*budget)+1))
			_ = reader.Close()
			if err != nil {
				return nil, errLivePacket
			}
			nested, err := decodeLivePacketLevel(unpacked, depth+1, budget)
			if err != nil {
				return nil, err
			}
			packets = append(packets, nested...)
		default:
			return nil, errLivePacket
		}
		data = data[int(size):]
	}
	return packets, nil
}
