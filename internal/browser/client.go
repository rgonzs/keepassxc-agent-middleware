// Package browser requests unlock only; it never reads vault entries or stores credentials.
package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/nacl/box"
	"keepassxc-agent-middleware/internal/transport"
)

type Client struct{ Endpoint string }

type session struct {
	conn                   net.Conn
	decoder                *json.Decoder
	clientID               string
	public, secret, server *[32]byte
}

func nonce() ([24]byte, error) { var n [24]byte; _, err := rand.Read(n[:]); return n, err }
func increment(n [24]byte) [24]byte {
	for i := range n {
		n[i]++
		if n[i] != 0 {
			break
		}
	}
	return n
}
func encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// exchange uses the raw JSON stream of KeePassXC's local browser socket, NOT
// the little-endian framing used between a browser and its native-message host.
func (s *session) exchange(action string, request map[string]string) (map[string]string, error) {
	request["action"], request["clientID"] = action, s.clientID
	if err := json.NewEncoder(s.conn).Encode(request); err != nil {
		return nil, err
	}
	for {
		var reply map[string]string
		if err := s.decoder.Decode(&reply); err != nil {
			return nil, err
		}
		if reply["action"] == "database-locked" || reply["action"] == "database-unlocked" {
			continue
		}
		if reply["action"] != action {
			return nil, errors.New("unexpected KeePassXC response action")
		}
		if reply["errorCode"] != "" {
			return nil, &protocolError{reply["errorCode"], reply["error"]}
		}
		return reply, nil
	}
}

type protocolError struct{ code, message string }

func (e *protocolError) Error() string { return "KeePassXC: " + e.message + " (code " + e.code + ")" }

func (s *session) hash(trigger bool) error {
	n, err := nonce()
	if err != nil {
		return err
	}
	plain := []byte(`{"action":"get-databasehash"}`)
	request := map[string]string{"nonce": encode(n[:]), "message": encode(box.Seal(nil, plain, &n, s.server, s.secret))}
	if trigger {
		request["triggerUnlock"] = "true"
	}
	reply, err := s.exchange("get-databasehash", request)
	if err != nil {
		return err
	}
	expected := increment(n)
	actual, err := base64.StdEncoding.DecodeString(reply["nonce"])
	if err != nil || !bytes.Equal(actual, expected[:]) {
		return errors.New("invalid KeePassXC response nonce")
	}
	encrypted, err := base64.StdEncoding.DecodeString(reply["message"])
	if err != nil {
		return err
	}
	decrypted, ok := box.Open(nil, encrypted, &expected, s.server, s.secret)
	if !ok {
		return errors.New("KeePassXC response authentication failed")
	}
	var result struct {
		Success string `json:"success"`
		Hash    string `json:"hash"`
	}
	if err = json.Unmarshal(decrypted, &result); err != nil {
		return err
	}
	if result.Success != "true" || result.Hash == "" {
		return errors.New("KeePassXC did not confirm database unlock")
	}
	return nil
}

func (c Client) Unlock(ctx context.Context) error {
	conn, err := transport.Dial(ctx, c.Endpoint)
	if err != nil {
		return fmt.Errorf("connect to KeePassXC (running with browser integration enabled): %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	pub, secret, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	id, err := nonce()
	if err != nil {
		return err
	}
	s := session{conn: conn, decoder: json.NewDecoder(io.LimitReader(conn, 1<<20)), clientID: encode(id[:]), public: pub, secret: secret}
	n, err := nonce()
	if err != nil {
		return err
	}
	reply, err := s.exchange("change-public-keys", map[string]string{"publicKey": encode(pub[:]), "nonce": encode(n[:])})
	if err != nil {
		return err
	}
	expected := increment(n)
	actual, err := base64.StdEncoding.DecodeString(reply["nonce"])
	if err != nil || !bytes.Equal(actual, expected[:]) {
		return errors.New("invalid key-exchange nonce")
	}
	server, err := base64.StdEncoding.DecodeString(reply["publicKey"])
	if err != nil || len(server) != 32 {
		return errors.New("invalid KeePassXC public key")
	}
	s.server = new([32]byte)
	copy(s.server[:], server)
	// Trigger once. Poll without re-opening a prompt if the user dismisses it.
	err = s.hash(true)
	for err != nil {
		var pe *protocolError
		if !errors.As(err, &pe) || pe.code != "1" {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		err = s.hash(false)
	}
	return nil
}
