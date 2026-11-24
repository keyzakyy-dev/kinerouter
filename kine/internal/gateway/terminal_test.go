package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalExec(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	mux := http.NewServeMux()
	g.registerTerminalRoutes(mux)
	call := func(remote, body string) (int, string) {
		req := httptest.NewRequest("POST", "/api/terminal/exec", strings.NewReader(body))
		req.RemoteAddr = remote
		req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}

	code, body := call("127.0.0.1:5000", `{"command":"echo hello"}`)
	if code != 200 || !strings.Contains(body, `"output":"hello\n"`) || !strings.Contains(body, `"exitCode":0`) {
		t.Fatalf("echo: %d %s", code, body)
	}
	code, body = call("127.0.0.1:5000", `{"command":"exit 7"}`)
	if code != 200 || !strings.Contains(body, `"exitCode":7`) {
		t.Fatalf("exit code: %d %s", code, body)
	}

	// cd is resolved server-side and reported back for the next command.
	dir := t.TempDir()
	code, body = call("127.0.0.1:5000", `{"command":"cd `+dir+`"}`)
	if code != 200 || !strings.Contains(body, dir) || !strings.Contains(body, `"exitCode":0`) {
		t.Fatalf("cd: %d %s", code, body)
	}
	code, body = call("127.0.0.1:5000", `{"command":"pwd","cwd":"`+dir+`"}`)
	if code != 200 || !strings.Contains(body, strings.TrimSpace(dir)) {
		t.Fatalf("pwd in cwd: %d %s", code, body)
	}
	code, body = call("127.0.0.1:5000", `{"command":"cd /definitely/not/here"}`)
	if code != 200 || !strings.Contains(body, `"exitCode":1`) {
		t.Fatalf("bad cd: %d %s", code, body)
	}

	// A remote peer needs the explicit opt-in.
	code, body = call("203.0.113.9:5000", `{"command":"echo nope"}`)
	if code != 403 || strings.Contains(body, "nope") {
		t.Fatalf("remote peer: %d %s", code, body)
	}
	if _, err := g.store.UpdateSettings(map[string]any{"terminalAllowRemote": true}); err != nil {
		t.Fatal(err)
	}
	if code, body := call("203.0.113.9:5000", `{"command":"echo allowed"}`); code != 200 || !strings.Contains(body, "allowed") {
		t.Fatalf("remote opt-in: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), "nope")); err == nil {
		t.Fatal("sanity: unexpected temp file")
	}
}

func TestTerminalInfo(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	mux := http.NewServeMux()
	g.registerTerminalRoutes(mux)
	req := httptest.NewRequest("GET", "/api/terminal", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"allowed":true`) {
		t.Fatalf("info: %d %s", rr.Code, rr.Body.String())
	}
}
