package browser

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/nacl/box"
	"keepassxc-agent-middleware/internal/transport"
)

func TestUnlockEncryptedProtocol(t *testing.T) {
	for _, badNonce := range []bool{false, true} {
		t.Run(map[bool]string{false: "locked-then-unlocked", true: "reject-wrong-nonce"}[badNonce], func(t *testing.T) {
			endpoint, cleanup := testEndpoint(t)
			defer cleanup()
			listener, err := transport.Listen(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				serverDone <- emulate(conn, badNonce)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = (Client{Endpoint: endpoint}).Unlock(ctx)
			if badNonce && err == nil {
				t.Fatal("accepted a response with wrong nonce")
			}
			if !badNonce && err != nil {
				t.Fatal(err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func emulate(conn net.Conn, badNonce bool) error {
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	decoder, encoder := json.NewDecoder(conn), json.NewEncoder(conn)
	var request map[string]string
	if err := decoder.Decode(&request); err != nil {
		return err
	}
	clientBytes, err := base64.StdEncoding.DecodeString(request["publicKey"])
	if err != nil {
		return err
	}
	var clientKey [32]byte
	copy(clientKey[:], clientBytes)
	serverPublic, serverSecret, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	nBytes, _ := base64.StdEncoding.DecodeString(request["nonce"])
	var n [24]byte
	copy(n[:], nBytes)
	n = increment(n)
	if err := encoder.Encode(map[string]string{"action": "change-public-keys", "nonce": encode(n[:]), "publicKey": encode(serverPublic[:])}); err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		request = nil
		if err := decoder.Decode(&request); err != nil {
			return err
		}
		ciphertext, _ := base64.StdEncoding.DecodeString(request["message"])
		nBytes, _ := base64.StdEncoding.DecodeString(request["nonce"])
		copy(n[:], nBytes)
		plain, ok := box.Open(nil, ciphertext, &n, &clientKey, serverSecret)
		if !ok || string(plain) != `{"action":"get-databasehash"}` {
			return io.ErrUnexpectedEOF
		}
		if attempt == 0 {
			if request["triggerUnlock"] != "true" {
				return io.ErrUnexpectedEOF
			}
			if err := encoder.Encode(map[string]string{"action": "get-databasehash", "errorCode": "1", "error": "Database not opened"}); err != nil {
				return err
			}
			// Concatenated unsolicited messages must not be mistaken for responses.
			if err := encoder.Encode(map[string]string{"action": "database-unlocked"}); err != nil {
				return err
			}
			continue
		}
		if request["triggerUnlock"] != "" {
			return io.ErrUnexpectedEOF
		}
		responseNonce := increment(n)
		if badNonce {
			responseNonce = increment(responseNonce)
		}
		response := box.Seal(nil, []byte(`{"success":"true","hash":"test-database"}`), &responseNonce, &clientKey, serverSecret)
		return encoder.Encode(map[string]string{"action": "get-databasehash", "nonce": encode(responseNonce[:]), "message": encode(response)})
	}
	return nil
}

func TestIncrementNonce(t *testing.T) {
	var n [24]byte
	n[0] = 255
	n[1] = 255
	got := increment(n)
	if got[0] != 0 || got[1] != 0 || got[2] != 1 {
		t.Fatal("not little endian")
	}
}

func TestUnlockTimeoutClosesConnection(t *testing.T) {
	endpoint, cleanup := testEndpoint(t)
	defer cleanup()
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		// Consume the key exchange but deliberately never reply.
		_, _ = io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := (Client{Endpoint: endpoint}).Unlock(ctx); err == nil {
		t.Fatal("missing timeout")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancelled browser connection left open")
	}
}
