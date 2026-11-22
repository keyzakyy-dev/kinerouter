package gateway

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

// Self-check: the Netlify digest paths need a valid stored ZIP and the same
// SHA-256 Netlify will compute, plus a sanitized site name.
func TestStoredZipAndHashes(t *testing.T) {
	payload := []byte("exports.handler = async () => {};\n")
	z := storedZip("relay.js", payload)
	zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "relay.js" {
		t.Fatalf("unexpected zip entries: %+v", zr.File)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("zip content mismatch: %q", got)
	}
	if sha256Hex(z) == "" || sha1Hex(payload) == "" {
		t.Fatal("empty hash")
	}
	if sanitizeSiteName("My Relay Site!") != "my-relay-site" {
		t.Fatalf("sanitize: %q", sanitizeSiteName("My Relay Site!"))
	}
	if sanitizeSiteName("") == "" {
		t.Fatal("empty name must fall back to a generated site name")
	}
	if anyPath(map[string]any{"result": map[string]any{"subdomain": "abc"}}, "result", "subdomain") != "abc" {
		t.Fatal("anyPath")
	}
}
