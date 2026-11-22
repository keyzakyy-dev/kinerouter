package gateway

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// Self-check for the Zed auto-import parsers and path resolution. Nothing here
// spawns a real keyring: the helper output formats are parsed directly, and the
// kv_store reader runs against a tiny temp SQLite DB.
func TestZedAutoImport(t *testing.T) {
	// --- settings.json → credentials_url, server_url fallback, default
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	if got := zedCredentialsURL(); got != "https://zed.dev" {
		t.Fatalf("default credentials url: %q", got)
	}
	cfg := filepath.Join(home, ".config", "zed")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"server_url":"https://z.example/"}`), 0o644)
	if got := zedCredentialsURL(); got != "https://z.example" {
		t.Fatalf("server_url fallback: %q", got)
	}
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"credentials_url":"https://c.example"}`), 0o644)
	if got := zedCredentialsURL(); got != "https://c.example" {
		t.Fatalf("credentials_url: %q", got)
	}

	// --- secret-tool output (attributes on stderr, label/secret on stdout)
	user, secret := parseSecretTool("attribute.url = https://zed.dev\nattribute.username = 12345\n" +
		"[/60]\nlabel = zed-github-account\nsecret = {\"k\":\"v\"}\n")
	if user != "12345" || secret != `{"k":"v"}` {
		t.Fatalf("parseSecretTool: %q %q", user, secret)
	}

	// --- macOS security output
	if m := reSecurityPw.FindStringSubmatch(`password: "ab\"c"`); m == nil || m[1] != `ab\"c` {
		t.Fatalf("reSecurityPw: %v", m)
	}
	if m := reSecurityAcct.FindStringSubmatch(`"acct"<blob>=0x41 "user@x"`); m == nil || m[1] != "user@x" {
		t.Fatalf("reSecurityAcct: %v", m)
	}
	if got := stripURLScheme("https://zed.dev/"); got != "zed.dev" {
		t.Fatalf("stripURLScheme: %q", got)
	}
	if got := uniqueStrings([]string{"a", "a", "", "b"}); len(got) != 2 {
		t.Fatalf("uniqueStrings: %v", got)
	}

	// --- kv_store: system_id read back from a real temp DB
	dbPath := filepath.Join(home, "zed.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.Exec(`CREATE TABLE kv_store (key TEXT PRIMARY KEY, value TEXT)`)
	db.Exec(`INSERT INTO kv_store VALUES ('system_id','sys-123')`)
	db.Close()
	if got := kvStoreValue(dbPath, "system_id"); got != "sys-123" {
		t.Fatalf("kvStoreValue: %q", got)
	}
	if got := kvStoreValue(dbPath, "missing"); got != "" {
		t.Fatalf("kvStoreValue missing: %q", got)
	}
}
