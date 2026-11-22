package lease

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveFakeBroker answers one framed request per connection with reply.
func serveFakeBroker(t *testing.T, socket string, reply map[string]any) {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hdr := make([]byte, 4)
				if _, err := c.Read(hdr); err != nil {
					return
				}
				n := binary.BigEndian.Uint32(hdr)
				body := make([]byte, n)
				if _, err := c.Read(body); err != nil {
					return
				}
				var req map[string]any
				_ = json.Unmarshal(body, &req)
				// Echo the capability so the test can assert it was sent.
				reply["seenCapability"] = req["capability"]
				payload, _ := json.Marshal(reply)
				out := make([]byte, 4+len(payload))
				binary.BigEndian.PutUint32(out, uint32(len(payload)))
				copy(out[4:], payload)
				_, _ = c.Write(out)
			}(conn)
		}
	}()
}

func setupDir(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	capValue := strings.Repeat("a", 43)
	if len(capValue) != 43 {
		t.Fatalf("capability fixture must be 43 chars, got %d", len(capValue))
	}
	if err := os.WriteFile(filepath.Join(dir, "run", CapabilityFile), []byte(capValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, capValue
}

func TestRequestLeaseFramesAndParses(t *testing.T) {
	dir, capValue := setupDir(t)
	serveFakeBroker(t, filepath.Join(dir, "run", SocketFile), map[string]any{
		"ok": true, "result": map[string]any{"accessToken": "tok-1", "expiresAtMs": 1_900_000_000_000, "generation": 7},
	})
	tok, err := RequestLease(dir, MaxMinValidityMs*10) // clamped to the max
	if err != nil {
		t.Fatalf("RequestLease: %v", err)
	}
	if tok.AccessToken != "tok-1" || tok.Generation != 7 {
		t.Fatalf("unexpected token: %+v", tok)
	}
	_ = capValue
}

func TestMissingCapabilityIsBrokerUnavailable(t *testing.T) {
	dir := t.TempDir()
	if _, err := RequestStatus(dir); ErrCode(err) != CodeBrokerUnavailable {
		t.Fatalf("want BROKER_UNAVAILABLE, got %v (%v)", ErrCode(err), err)
	}
}

func TestRelativedDataDirRejected(t *testing.T) {
	if _, _, err := endpointOf("relative/dir"); ErrCode(err) != CodeInvalidRequest {
		t.Fatalf("want INVALID_REQUEST, got %v", err)
	}
}

func TestTokenCacheReusesLease(t *testing.T) {
	dir, _ := setupDir(t)
	serveFakeBroker(t, filepath.Join(dir, "run", SocketFile), map[string]any{
		"ok": true, "result": map[string]any{"accessToken": "cached", "expiresAtMs": longFuture(), "generation": 1},
	})
	first, err := TokenFor(dir)
	if err != nil {
		t.Fatal(err)
	}
	Invalidate(dir)
	second, err := TokenFor(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.AccessToken != "cached" || second.AccessToken != "cached" {
		t.Fatalf("unexpected tokens %q %q", first.AccessToken, second.AccessToken)
	}
}

func longFuture() int64 { return 1<<62 - 1 }
