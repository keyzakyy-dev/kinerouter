package gateway

// GET /api/oauth/zed/auto-import — read the signed-in Zed IDE session straight
// out of the OS secret store so the dashboard does not have to ask the user to
// paste a token. Mirrors src/lib/oauth/utils/zedCredentials.js.
//
//   Linux   secret-tool search url=<credentials_url>  (label zed-github-account)
//   macOS   security find-internet-password -s <url> -g
//   Windows Credential Manager target zed:url=<url>
//
// The system_id Zed sends to cloud.zed.dev lives in its local kv_store; when it
// is absent a fresh UUID is generated, exactly like the JS.
//
// Security: dashboard session required (requireAuth), the token is returned to
// the SPA over the authenticated channel and never written to a log. Only local
// processes are spawned with fixed argument vectors (no shell, no user input
// interpolated into a command line beyond the settings-derived URL, which is
// passed as a single argv element).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const zedDefaultCredentialsURL = "https://zed.dev"

// zedCmdTimeout bounds every keyring helper; they all answer in milliseconds.
const zedCmdTimeout = 8 * time.Second

func (g *Gateway) autoImportZed(w http.ResponseWriter, r *http.Request) {
	credsURL := zedCredentialsURL()
	res, err := readZedKeyring(credsURL)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"found": false, "error": err.Error(), "credentialsUrl": credsURL,
		})
		return
	}
	systemID := zedSystemID()
	if systemID == "" {
		systemID = randomUUID()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found": true, "userId": res.userID, "accessToken": res.accessToken,
		"systemId": systemID, "credentialsUrl": credsURL,
	})
}

type zedPair struct{ userID, accessToken string }

// ---------------------------------------------------------------- settings

// zedCredentialsURL honours settings.json credentials_url → server_url, else
// the default https://zed.dev.
func zedCredentialsURL() string {
	for _, p := range zedSettingsPaths() {
		raw, err := readFileLimited(p, 1<<20)
		if err != nil {
			continue
		}
		var s map[string]any
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		u := firstNonEmpty(strAny(s["credentials_url"]), strAny(s["server_url"]))
		if u != "" {
			return strings.TrimRight(u, "/")
		}
	}
	return zedDefaultCredentialsURL
}

func zedSettingsPaths() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library/Application Support/Zed/settings.json")}
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		return []string{filepath.Join(local, "Zed", "settings.json")}
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	return []string{filepath.Join(xdg, "zed", "settings.json")}
}

// ---------------------------------------------------------------- kv_store

func zedGlobalDBPaths() []string {
	home, _ := os.UserHomeDir()
	channels := []string{"0-global", "0-stable", "0-preview"}
	var base string
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(home, "Library/Application Support/Zed/db")
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		base = filepath.Join(local, "Zed", "db")
	default:
		xdg := os.Getenv("XDG_DATA_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".local", "share")
		}
		base = filepath.Join(xdg, "zed", "db")
	}
	out := make([]string, 0, len(channels))
	for _, c := range channels {
		out = append(out, filepath.Join(base, c, "db.sqlite"))
	}
	return out
}

// zedSystemID reads the system_id Zed sends upstream from its kv_store.
func zedSystemID() string {
	for _, p := range zedGlobalDBPaths() {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if v := kvStoreValue(p, "system_id"); v != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// kvStoreValue opens Zed's DB read-only. modernc is already linked for the
// store package, so no extra driver; the sqlite3 CLI is the fallback for a DB
// the driver cannot open (schema drift).
func kvStoreValue(path, key string) string {
	if db, err := sql.Open("sqlite", "file:"+path+"?mode=ro"); err == nil {
		defer db.Close()
		db.SetMaxOpenConns(1)
		var v sql.NullString
		row := db.QueryRow("SELECT value FROM kv_store WHERE key = ? LIMIT 1", key)
		if row.Scan(&v) == nil && v.Valid && v.String != "" {
			return v.String
		}
	}
	out, err := runCmd("sqlite3", path, "SELECT value FROM kv_store WHERE key='"+strings.ReplaceAll(key, "'", "''")+"' LIMIT 1;")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ---------------------------------------------------------------- keyring

func readZedKeyring(credsURL string) (*zedPair, error) {
	switch runtime.GOOS {
	case "linux":
		return readLinuxKeyring(credsURL)
	case "darwin":
		return readMacKeychain(credsURL)
	case "windows":
		return readWindowsCredential(credsURL)
	}
	return nil, errors.New("Zed auto-import is not supported on " + runtime.GOOS)
}

var (
	reSecretUser = regexp.MustCompile(`attribute\.username\s*=\s*(\S+)`)
	reSecretSec  = regexp.MustCompile(`(?m)^\s*secret\s*=\s*(.*)$`)
)

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(zedCmdTimeout):
		cmd.Process.Kill()
		<-done
		return buf.String(), errors.New(name + " timed out")
	}
}

func readLinuxKeyring(credsURL string) (*zedPair, error) {
	text, err := runCmd("secret-tool", "search", "--all", "url", credsURL)
	if err != nil && len(text) == 0 {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("secret-tool not found (install libsecret / secret-tools)")
		}
		return nil, errors.New("Failed to read Linux keyring: " + err.Error())
	}
	if u, s := parseSecretTool(text); u != "" && s != "" {
		return &zedPair{userID: u, accessToken: s}, nil
	}

	secret, err := runCmd("secret-tool", "lookup", "url", credsURL)
	if err != nil || strings.TrimSpace(secret) == "" {
		return nil, errors.New("Empty Zed keyring secret")
	}
	search, _ := runCmd("secret-tool", "search", "--all", "url", credsURL)
	u, _ := parseSecretTool(search)
	if u == "" {
		return nil, errors.New("Zed keyring entry missing username (user id)")
	}
	return &zedPair{userID: u, accessToken: strings.TrimSpace(secret)}, nil
}

func parseSecretTool(text string) (user, secret string) {
	if m := reSecretUser.FindStringSubmatch(text); m != nil {
		user = m[1]
	}
	if m := reSecretSec.FindStringSubmatch(text); m != nil {
		secret = strings.TrimSpace(m[1])
	}
	return user, secret
}

var reSecurityPw = regexp.MustCompile(`(?m)password:\s*"(.*)"\s*$`)
var reSecurityAcct = regexp.MustCompile(`"acct"<blob>=(?:0x[0-9A-Fa-f]+\s+)?"([^"]*)"`)

func readMacKeychain(credsURL string) (*zedPair, error) {
	servers := uniqueStrings([]string{credsURL, stripURLScheme(credsURL), "zed.dev"})
	var lastErr error
	for _, server := range servers {
		text, err := runCmd("security", "find-internet-password", "-s", server, "-g")
		if err != nil && text == "" {
			lastErr = err
			continue
		}
		password := ""
		if m := reSecurityPw.FindStringSubmatch(text); m != nil {
			password = regexp.MustCompile(`\\(.)`).ReplaceAllString(m[1], "$1")
		}
		account := ""
		if m := reSecurityAcct.FindStringSubmatch(text); m != nil {
			account = m[1]
		}
		if password != "" && account != "" {
			return &zedPair{userID: account, accessToken: password}, nil
		}
		if password != "" {
			lastErr = errors.New("Found Zed keychain password but missing account (user id)")
		}
	}
	if lastErr == nil {
		lastErr = errors.New("Zed credentials not found in macOS Keychain")
	}
	return nil, lastErr
}

// readWindowsCredential reads target "zed:url=<url>" through CredRead. The
// P/Invoke blob is fixed text; the URL is passed as -EncodedCommand-free
// single-quoted literal to avoid any shell metacharacter surface.
func readWindowsCredential(credsURL string) (*zedPair, error) {
	target := "zed:url=" + credsURL
	script := strings.ReplaceAll(zedWindowsPS, "__TARGET__", "'"+strings.ReplaceAll(target, "'", "''")+"'")
	text, err := runCmd("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 2") {
			return nil, errors.New("Zed credentials not found in Windows Credential Manager")
		}
		return nil, err
	}
	text = strings.TrimPrefix(text, "\ufeff")
	nl := strings.IndexByte(text, '\n')
	if nl < 0 {
		return nil, errors.New("Malformed Windows credential payload")
	}
	user := strings.TrimSpace(text[:nl])
	token := strings.TrimRight(text[nl+1:], "\r\n")
	if user == "" || token == "" {
		return nil, errors.New("Windows Credential Manager entry missing username or secret")
	}
	return &zedPair{userID: user, accessToken: token}, nil
}

const zedWindowsPS = `
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
using System.Text;
public class ZedCred {
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  public struct CREDENTIAL {
    public uint Flags; public uint Type; public string TargetName; public string Comment;
    public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
    public uint CredentialBlobSize; public IntPtr CredentialBlob; public uint Persist;
    public uint AttributeCount; public IntPtr Attributes; public string TargetAlias; public string UserName;
  }
  [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  public static extern bool CredRead(string target, uint type, uint reservedFlag, out IntPtr credentialPtr);
  [DllImport("advapi32.dll", SetLastError = true)]
  public static extern void CredFree(IntPtr buffer);
  public static string Read(string target) {
    IntPtr p;
    if (!CredRead(target, 1, 0, out p)) return null;
    try {
      var c = (CREDENTIAL)Marshal.PtrToStructure(p, typeof(CREDENTIAL));
      string secret = "";
      if (c.CredentialBlob != IntPtr.Zero && c.CredentialBlobSize > 0) {
        byte[] bytes = new byte[c.CredentialBlobSize];
        Marshal.Copy(c.CredentialBlob, bytes, 0, (int)c.CredentialBlobSize);
        secret = Encoding.UTF8.GetString(bytes);
      }
      return (c.UserName ?? "") + "\n" + secret;
    } finally { CredFree(p); }
  }
}
"@
$r = [ZedCred]::Read(__TARGET__)
if ($null -eq $r) { exit 2 }
Write-Output $r
`

// ---------------------------------------------------------------- helpers

func stripURLScheme(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	return strings.TrimRight(u, "/")
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
