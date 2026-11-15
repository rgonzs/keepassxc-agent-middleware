package proxy

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestPacketRejectsUnboundedLengths(t *testing.T) {
	for _, n := range []uint32{0, maxPacket + 1, 0xffffffff} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], n)
		if _, err := readPacket(bytes.NewReader(header[:])); err == nil {
			t.Fatalf("accepted %d", n)
		}
	}
	if _, err := readPacket(bytes.NewReader([]byte{0, 0, 0, 2, 11})); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated packet: %v", err)
	}
}

func FuzzIdentities(f *testing.F) {
	f.Add([]byte{12, 0, 0, 0, 0})
	f.Add([]byte{12, 255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, packet []byte) { _, _ = identities(packet) })
}
