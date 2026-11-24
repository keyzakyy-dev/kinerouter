package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.0", 1}, {"0.1.0", "0.1.0", 0}, {"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1}, {"v", "0.0.0", 0}, {"0.1.10", "0.1.9", 1},
		{"1.9.7-rc1", "1.9.7", -1}, {"1.9.7", "1.9.7-rc1", 1},
		{"1.9.7-rc.2", "1.9.7-rc.10", -1}, {"1.9.7-rc", "1.9.7-rc.1", -1},
		{"1.9.7-alpha", "1.9.7-beta", -1}, {"1.9.7+build", "1.9.7", 0},
	}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Fatalf("compareSemver(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

// An unattended update never turns a final release into an RC, but a user
// already on an RC keeps moving forward.
func TestAutoApplyAllowed(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.10", "0.2.0", true},
		{"0.1.10", "0.2.0-rc1", false},
		{"0.2.0-rc1", "0.2.0-rc2", true},
		{"0.2.0-rc1", "0.2.0", true},
	}
	for _, c := range cases {
		if got := autoApplyAllowed(c.current, c.latest); got != c.want {
			t.Errorf("autoApplyAllowed(%q,%q)=%v want %v", c.current, c.latest, got, c.want)
		}
	}
}

// Only kine-v tags may drive the update check: the same repository carries
// other tracks, and a tag from one of them is not a binary this app can use.
// The release body becomes the notes shown in the update modal.
func TestFetchLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/x/y/releases") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"tag_name":"v9.9.9","body":"wrong track"},
			{"tag_name":"kine-v0.1.10","body":"Fixed update restart.\nNo more races."}
		]`))
	}))
	defer srv.Close()
	t.Setenv("KINEROUTER_UPDATE_API", srv.URL)
	t.Setenv("UPDATE_REPO", "x/y")

	v, notes := fetchLatestRelease()
	if v != "0.1.10" || !strings.Contains(notes, "No more races") {
		t.Fatalf("fetchLatestRelease = %q, %q", v, notes)
	}

	t.Setenv("KINEROUTER_UPDATE_API", "http://127.0.0.1:1")
	if v, notes := fetchLatestRelease(); v != "" || notes != "" {
		t.Fatalf("unreachable API returned %q, %q", v, notes)
	}
}

// Update advice must follow the binary's real delivery path. A custom DATA_DIR
// keeps the npm layout but drops the ".kinerouter" marker, and the old check
// called that a script install.
func TestInstallMethodFor(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/home/u/.kinerouter/runtime/kinerouter_0.1.10_linux_amd64", "npm"},
		{"/opt/kine/runtime/kinerouter_0.1.10_linux_amd64", "npm"},
		{"/usr/local/lib/node_modules/kinerouter/cli.js", "npm"},
		{"C:\\Users\\u\\AppData\\Local\\kinerouter\\runtime\\kinerouter_0.1.10_windows_amd64.exe", "npm"},
		{"/usr/local/bin/kinerouter", "script"},
		{"/home/u/.local/bin/kinerouter", "script"},
	}
	for _, c := range cases {
		if got := installMethodFor(c.path); got != c.want {
			t.Errorf("installMethodFor(%q)=%q want %q", c.path, got, c.want)
		}
	}
}

// Replacing the binary is local-operator work: a remote caller is refused even
// with a valid session, and a working-copy build is refused outright.
func TestHandleVersionUpdateGuards(t *testing.T) {
	g := testGateway(t)
	call := func(remote string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/api/version/update", nil)
		req.RemoteAddr = remote
		rr := httptest.NewRecorder()
		g.handleVersionUpdate(rr, req)
		return rr.Code, rr.Body.String()
	}
	t.Setenv("KINEROUTER_DEV", "1")
	if code, _ := call("127.0.0.1:5555"); code != http.StatusForbidden {
		t.Fatalf("dev build code = %d, want 403", code)
	}
	t.Setenv("KINEROUTER_DEV", "")
	if code, body := call("203.0.113.9:5555"); code != http.StatusForbidden || !strings.Contains(body, "local machine") {
		t.Fatalf("remote code = %d body = %s", code, body)
	}
}

// The opt-in persists in settings and is visible in the status payload; a dev
// build reports no release, so the test never depends on the network.
func TestAutoUpdateToggle(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	mux := http.NewServeMux()
	g.registerMiscAdminRoutes(mux)
	do := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}
	t.Setenv("KINEROUTER_DEV", "1")
	if code, body := do("POST", "/api/version/auto-update", `{"enabled":true}`); code != 200 || !strings.Contains(body, "true") {
		t.Fatalf("enable = %d %s", code, body)
	}
	code, body := do("GET", "/api/version/status", "")
	if code != 200 || !strings.Contains(body, `"autoUpdateEnabled":true`) || !strings.Contains(body, `"checkIntervalHours":6`) {
		t.Fatalf("status = %d %s", code, body)
	}
	if code, _ := do("POST", "/api/version/auto-update", `not-json`); code != 400 {
		t.Fatalf("bad body = %d", code)
	}
	if !g.store.SettingBool("autoUpdate", false) {
		t.Fatal("autoUpdate was not persisted")
	}
	if !g.beginUpdate() {
		t.Fatal("first update should acquire the guard")
	}
	if g.beginUpdate() {
		t.Fatal("second update should be refused while one is running")
	}
	g.endUpdate()
	if !g.beginUpdate() {
		t.Fatal("guard was not released")
	}
	g.endUpdate()
}
