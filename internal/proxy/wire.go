package proxy

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
)

const maxPacket = 1 << 20
const (
	failure           = 5
	identitiesRequest = 11
	identitiesAnswer  = 12
	signRequest       = 13
)

func readPacket(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxPacket {
		return nil, errors.New("invalid SSH agent packet length")
	}
	packet := make([]byte, n)
	_, err := io.ReadFull(r, packet)
	return packet, err
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func writePacket(w io.Writer, packet []byte) error {
	if len(packet) == 0 || len(packet) > maxPacket {
		return errors.New("invalid SSH agent packet length")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(packet)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, packet)
}

func takeString(data []byte) (value, rest []byte, err error) {
	if len(data) < 4 {
		return nil, nil, io.ErrUnexpectedEOF
	}
	n := uint64(binary.BigEndian.Uint32(data))
	if n > uint64(len(data)-4) {
		return nil, nil, io.ErrUnexpectedEOF
	}
	return data[4 : 4+n], data[4+n:], nil
}

func identities(packet []byte) ([][]byte, error) {
	if len(packet) < 5 || packet[0] != identitiesAnswer {
		return nil, errors.New("invalid identities response")
	}
	count := binary.BigEndian.Uint32(packet[1:5])
	data := packet[5:]
	if uint64(count) > uint64(len(data)/8) {
		return nil, errors.New("invalid identity count")
	}
	keys := make([][]byte, 0, count)
	for range count {
		key, rest, err := takeString(data)
		if err != nil {
			return nil, err
		}
		_, data, err = takeString(rest)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if len(data) != 0 {
		return nil, errors.New("trailing identities data")
	}
	return keys, nil
}

func fingerprint(key []byte) string {
	sum := sha256.Sum256(key)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}
