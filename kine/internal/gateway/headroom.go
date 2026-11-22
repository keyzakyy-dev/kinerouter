package gateway

// Headroom + PXpipe sidecar management — ported from src/lib/headroom/*
// (detect.js, process.js), src/lib/pxpipe/* (install.js, loader.js, events.js,
// service.js) and the src/app/api/{headroom,pxpipe}/** routes.
//
// Security notes:
//   - every route is behind requireAuth; the ones that spawn processes or read
//     host files (start/stop/restart/proxy/install) additionally require a
//     loopback peer, like LOCAL_ONLY_PATHS in src/dashboardGuard.js
//   - the headroom proxy route forwards only to the admin-configured headroomUrl
//     (settings blob); cookies and Authorization are dropped unless that host is
//     loopback, so dashboard credentials never leave the machine
//   - pip/npm invocations use argv arrays, never a shell, and the extras list is
//     a fixed whitelist (HEADROOM_COMPRESSION_EXTRAS)
//   - the PXpipe transform runs in a node subprocess with the request body on
//     stdin; the package path is fixed under DATA_DIR/pxpipe. A fresh node
//     process per transform is deliberate: the JS keeps the module warm in
//     memory, Go cannot share a V8 heap, and a per-call launch costs ~50ms on
//     bodies that already exceed the 25k-char threshold.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/rtk"
)

const (
	headroomDefaultURL       = "http://localhost:8787"
	headroomDefaultPort      = 8787
	headroomStartupTimeout   = 8 * time.Second
	headroomPipTimeout       = 8 * time.Second
	pxpipePackage            = "pxpipe-proxy"
	pxpipeInstallTimeout     = 5 * time.Minute
	pxpipeDefaultMinChars    = 25000
	pxpipeDefaultTimeoutMS   = 15000
	pxpipeEstCharsPerToken   = 4
	pxpipeEventsMaxBytes     = 5 << 20
	pxpipeTransformMaxOutput = 64 << 20
)

// headroomExtras is the closed whitelist (HEADROOM_COMPRESSION_EXTRAS).
var headroomExtras = []string{"code", "ml"}

var headroomExtraMarkers = map[string][]string{
	"code": {"tree-sitter", "tree-sitter-language-pack"},
	"ml":   {"torch", "huggingface-hub"},
}

// ---------------------------------------------------------------- headroom detect

func headroomDir(g *Gateway) string { return filepath.Join(g.cfg.DataDir, "headroom") }

func (g *Gateway) headroomPidFile() string {
	return filepath.Join(headroomDir(g), "proxy.pid")
}
func (g *Gateway) headroomLogFile() string {
	return filepath.Join(headroomDir(g), "proxy.log")
}
func (g *Gateway) headroomInstallLog() string {
	return filepath.Join(headroomDir(g), "install.log")
}

// extraBins extends PATH the way detect.js does — packaged/launchd envs miss
// the Python script dirs.
func extraBins() []string {
	home, _ := os.UserHomeDir()
	return []string{
		"/usr/local/bin", "/opt/homebrew/bin",
		"/Library/Frameworks/Python.framework/Versions/3.13/bin",
		"/Library/Frameworks/Python.framework/Versions/3.12/bin",
		"/Library/Frameworks/Python.framework/Versions/3.11/bin",
		"/Library/Frameworks/Python.framework/Versions/3.10/bin",
		filepath.Join(home, ".local", "bin"),
		"/usr/bin", "/bin",
	}
}

func extendedEnv() []string {
	path := os.Getenv("PATH")
	dirs := strings.Join(extraBins(), string(os.PathListSeparator))
	if path != "" {
		dirs += string(os.PathListSeparator) + path
	}
	env := os.Environ()
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = "PATH=" + dirs
			return env
		}
	}
	return append(env, "PATH="+dirs)
}

func findHeadroomBinary() string {
	out, err := exec.Command("which", "headroom").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return line
}

var pythonCandidates = []string{"python3.13", "python3.12", "python3.11", "python3.10", "python3", "python"}

func runExtended(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = extendedEnv()
	out, err := cmd.Output()
	return string(out), err
}

// findPython310 returns the first interpreter ≥3.10 that can see headroom-ai,
// else the first version-eligible one (needed for the initial install).
func findPython310() string {
	var candidates []string
	if bin := findHeadroomBinary(); bin != "" {
		dir := filepath.Dir(bin)
		for _, n := range []string{"python3", "python3.13", "python"} {
			candidates = append(candidates, filepath.Join(dir, n))
		}
	}
	for _, dir := range extraBins() {
		for _, n := range pythonCandidates {
			candidates = append(candidates, filepath.Join(dir, n))
		}
	}
	candidates = append(candidates, pythonCandidates...)

	fallback := ""
	for _, c := range candidates {
		ver, err := runExtended(c, "--version")
		if err != nil {
			continue
		}
		var major, minor int
		if _, err := fmt.Sscanf(ver, "Python %d.%d", &major, &minor); err != nil {
			continue
		}
		if major < 3 || (major == 3 && minor < 10) {
			continue
		}
		if fallback == "" {
			fallback = c
		}
		if _, err := runExtended(c, "-m", "pip", "show", "headroom-ai"); err == nil {
			return c
		}
	}
	return fallback
}

type headroomExtrasInfo struct {
	Installed bool            `json:"installed"`
	Version   any             `json:"version"`
	Extras    map[string]bool `json:"extras"`
}

// installedHeadroomExtras answers version + which extras are present from one
// `pip list --format=json` call.
func installedHeadroomExtras(py string) headroomExtrasInfo {
	out := headroomExtrasInfo{Extras: map[string]bool{"code": false, "ml": false}}
	if py == "" {
		py = findPython310()
	}
	if py == "" {
		return out
	}
	raw, err := runExtended(py, "-m", "pip", "list", "--format=json", "--disable-pip-version-check")
	if err != nil {
		return out
	}
	var pkgs []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(raw), &pkgs) != nil {
		return out
	}
	names := map[string]bool{}
	for _, p := range pkgs {
		names[strings.ToLower(p.Name)] = true
		if strings.ToLower(p.Name) == "headroom-ai" {
			out.Version = p.Version
		}
	}
	if !names["headroom-ai"] {
		return out
	}
	out.Installed = true
	for _, extra := range headroomExtras {
		for _, marker := range headroomExtraMarkers[extra] {
			if names[marker] {
				out.Extras[extra] = true
				break
			}
		}
	}
	return out
}

func isLoopbackHeadroomURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.Trim(u.Hostname(), "[]")) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}

func probeHeadroom(url string) bool {
	if url == "" {
		return false
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	res, err := client.Get(strings.TrimSuffix(url, "/") + "/health")
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode < 300
}

// ---------------------------------------------------------------- headroom process

func (g *Gateway) managedHeadroomPid() int {
	raw, err := os.ReadFile(g.headroomPidFile())
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid > 0 && pidAlive(pid) {
		return pid
	}
	return 0
}

// headroomProxyArgs mirrors extrasProxyArgs: [code] is off by default in
// headroom, [ml] is on by default.
func headroomProxyArgs(codeAware, kompress bool) []string {
	var args []string
	if codeAware {
		args = append(args, "--code-aware")
	}
	if !kompress {
		args = append(args, "--disable-kompress")
	}
	return args
}

func (g *Gateway) startHeadroomProxy(port int, codeAware, kompress bool) (map[string]any, error) {
	if port <= 0 || port >= 65536 {
		port = headroomDefaultPort
	}
	bin := findHeadroomBinary()
	if bin == "" {
		return nil, errors.New("Headroom CLI not installed")
	}
	if pid := g.managedHeadroomPid(); pid > 0 {
		return map[string]any{"pid": pid, "alreadyRunning": true}, nil
	}
	if err := os.MkdirAll(headroomDir(g), 0o700); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(g.headroomLogFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	args := append([]string{"proxy", "--port", strconv.Itoa(port)}, headroomProxyArgs(codeAware, kompress)...)
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, errors.New("Failed to spawn headroom proxy: " + err.Error())
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(g.headroomPidFile(), []byte(strconv.Itoa(pid)), 0o600)

	// Wait until it either stays alive briefly (success) or exits fast.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
		logf.Close()
		os.Remove(g.headroomPidFile())
		return nil, errors.New("headroom proxy exited early — see proxy.log")
	case <-time.After(headroomStartupTimeout):
		if !pidAlive(pid) {
			logf.Close()
			os.Remove(g.headroomPidFile())
			return nil, errors.New("headroom proxy exited during startup — see proxy.log")
		}
	}
	// Detach the wait goroutine; the child outlives this process.
	go func() { logf.Close() }()
	return map[string]any{"pid": pid, "alreadyRunning": false}, nil
}

func signalPid(pid int, sig os.Signal) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Signal(sig)
	}
}

func (g *Gateway) stopHeadroomProxy() map[string]any {
	pid := g.managedHeadroomPid()
	if pid == 0 {
		return map[string]any{"stopped": false, "reason": "not_running"}
	}
	signalPid(pid, os.Interrupt) // SIGTERM equivalent on unix, ignored on windows
	go func() {
		time.Sleep(2 * time.Second)
		if pidAlive(pid) {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		}
	}()
	os.Remove(g.headroomPidFile())
	return map[string]any{"stopped": true, "pid": pid}
}

func (g *Gateway) restartHeadroomProxy(port int, codeAware, kompress bool) (map[string]any, error) {
	pid := g.managedHeadroomPid()
	if pid > 0 {
		signalPid(pid, os.Interrupt)
		for i := 0; i < 30 && pidAlive(pid); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if pidAlive(pid) {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
			time.Sleep(300 * time.Millisecond)
		}
		os.Remove(g.headroomPidFile())
	}
	return g.startHeadroomProxy(port, codeAware, kompress)
}

func logTail(path string, maxLines int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// runPipExtras runs pip install/uninstall with the log truncated so progress
// polling sees only the current run.
func (g *Gateway) runPipExtras(py string, args []string) error {
	if err := os.MkdirAll(headroomDir(g), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(g.headroomInstallLog(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(py, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = extendedEnv()
	return cmd.Run()
}

func parsePortFromURL(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil || p <= 0 || p >= 65536 {
		return 0
	}
	return p
}

// headroomSettingsURL returns the configured URL with the JS default applied.
func headroomURL(settings map[string]any) string {
	if v, ok := settings["headroomUrl"].(string); ok && v != "" {
		return v
	}
	return headroomDefaultURL
}

func localOnly(g *Gateway, w http.ResponseWriter, r *http.Request) bool {
	if !isLoopback(clientHost(r)) {
		writeErr(w, http.StatusForbidden, "This endpoint is only available from localhost")
		return false
	}
	return true
}

// ---------------------------------------------------------------- headroom handlers

func (g *Gateway) handleHeadroomStatus(w http.ResponseWriter, r *http.Request) {
	settings, _ := g.store.GetSettings()
	u := headroomURL(settings)
	bin := findHeadroomBinary()
	py := findPython310()
	installed := bin != ""
	extras := headroomExtrasInfo{Extras: map[string]bool{"code": false, "ml": false}}
	if installed {
		extras = installedHeadroomExtras(py)
	}
	localURL := isLoopbackHeadroomURL(u)
	writeJSON(w, http.StatusOK, map[string]any{
		"installed":  installed,
		"path":       nulIfEmpty(bin),
		"running":    probeHeadroom(u),
		"python":     nulIfEmpty(py),
		"localUrl":   localURL,
		"canStart":   installed && localURL,
		"version":    extras.Version,
		"extras":     extras.Extras,
		"url":        u,
		"managedPid": nullableInt(g.managedHeadroomPid()),
	})
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// headroomTarget resolves the configured URL and rejects non-http schemes.
func headroomTarget(settings map[string]any) (*url.URL, error) {
	target, err := url.Parse(headroomURL(settings))
	if err != nil {
		return nil, err
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, errors.New("Headroom URL must use http or https")
	}
	return target, nil
}

func (g *Gateway) handleHeadroomStart(w http.ResponseWriter, r *http.Request) {
	if !localOnly(g, w, r) {
		return
	}
	settings, _ := g.store.GetSettings()
	u := headroomURL(settings)
	if !isLoopbackHeadroomURL(u) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "External Headroom proxies must be started outside KineRouter", "code": "EXTERNAL_PROXY"})
		return
	}
	port := parsePortFromURL(u)
	if port == 0 {
		port = headroomDefaultPort
	}
	res, err := g.startHeadroomProxy(port, boolSetting(settings, "headroomCodeAware"), settings["headroomKompress"] != false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "code": "NOT_INSTALLED"})
		return
	}
	out := map[string]any{"success": true}
	for k, v := range res {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) handleHeadroomRestart(w http.ResponseWriter, r *http.Request) {
	if !localOnly(g, w, r) {
		return
	}
	settings, _ := g.store.GetSettings()
	u := headroomURL(settings)
	if !isLoopbackHeadroomURL(u) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "External Headroom proxies must be started outside KineRouter", "code": "EXTERNAL_PROXY"})
		return
	}
	port := parsePortFromURL(u)
	if port == 0 {
		port = headroomDefaultPort
	}
	res, err := g.restartHeadroomProxy(port, boolSetting(settings, "headroomCodeAware"), settings["headroomKompress"] != false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "code": "NOT_INSTALLED"})
		return
	}
	out := map[string]any{"success": true}
	for k, v := range res {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) handleHeadroomStop(w http.ResponseWriter, r *http.Request) {
	if !localOnly(g, w, r) {
		return
	}
	res := g.stopHeadroomProxy()
	status := http.StatusOK
	if stopped, _ := res["stopped"].(bool); !stopped {
		status = http.StatusConflict
	}
	writeJSON(w, status, res)
}

func (g *Gateway) handleHeadroomExtras(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("log") == "1" {
			writeJSON(w, http.StatusOK, map[string]any{"log": logTail(g.headroomInstallLog(), 15)})
			return
		}
		st := installedHeadroomExtras(findPython310())
		writeJSON(w, http.StatusOK, map[string]any{
			"available": headroomExtras, "installed": st.Installed,
			"version": st.Version, "extras": st.Extras,
		})
	case http.MethodPost, http.MethodDelete:
		var body map[string]any
		_ = decodeBody(r, &body)
		requested := filterExtras(body["extras"])
		py := findPython310()
		if py == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Python >= 3.10 not found", "code": "NO_PYTHON"})
			return
		}
		var args []string
		if r.Method == http.MethodPost {
			if findHeadroomBinary() == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "headroom-ai not installed (run `pip install headroom-ai[proxy]` first)", "code": "NOT_INSTALLED"})
				return
			}
			spec := "headroom-ai[" + strings.Join(append([]string{"proxy"}, requested...), ",") + "]"
			args = []string{"-m", "pip", "install", "--upgrade", spec}
		} else {
			var pkgs []string
			seen := map[string]bool{}
			for _, e := range requested {
				for _, m := range headroomExtraMarkers[e] {
					if !seen[m] {
						seen[m] = true
						pkgs = append(pkgs, m)
					}
				}
			}
			if len(pkgs) == 0 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "No valid extras to remove", "code": "INVALID_EXTRAS"})
				return
			}
			args = append([]string{"-m", "pip", "uninstall", "-y"}, pkgs...)
		}
		if err := g.runPipExtras(py, args); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": "INSTALL_FAILED"})
			return
		}
		st := installedHeadroomExtras(py)
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "extras": requested, "installed": st.Installed,
			"version": st.Version,
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func filterExtras(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, it := range arr {
		s, _ := it.(string)
		for _, allowed := range headroomExtras {
			if s == allowed {
				out = append(out, s)
			}
		}
	}
	return out
}

// handleHeadroomProxy reverse-proxies /api/headroom/proxy/* to the configured
// Headroom dashboard, rewriting its fetch() paths so the UI works behind the
// KineRouter prefix.
func (g *Gateway) handleHeadroomProxy(w http.ResponseWriter, r *http.Request) {
	if !localOnly(g, w, r) {
		return
	}
	settings, _ := g.store.GetSettings()
	base, err := headroomTarget(settings)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sub := strings.TrimPrefix(r.URL.Path, "/api/headroom/proxy")
	sub = "/" + strings.TrimPrefix(sub, "/")
	loopbackTarget := isLoopbackHeadroomURL(base.String())
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = base.Scheme
			req.URL.Host = base.Host
			req.URL.Path = singleJoiningSlash(base.Path, sub)
			req.Host = base.Host
			if !loopbackTarget {
				req.Header.Del("Cookie")
				req.Header.Del("Authorization")
			}
		},
		ModifyResponse: func(res *http.Response) error {
			if sub == "/dashboard" && strings.Contains(res.Header.Get("Content-Type"), "text/html") {
				body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
				res.Body.Close()
				if err != nil {
					return err
				}
				rewritten := rewriteHeadroomDashboard(string(body))
				res.Body = io.NopCloser(strings.NewReader(rewritten))
				res.ContentLength = int64(len(rewritten))
				res.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeErr(w, http.StatusBadGateway, err.Error())
		},
	}
	rp.ServeHTTP(w, r)
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

var headroomFetchPrefixes = []string{"/stats", "/health", "/stats-history", "/transformations/feed"}

func rewriteHeadroomDashboard(html string) string {
	for _, p := range headroomFetchPrefixes {
		html = strings.ReplaceAll(html, "fetch('"+p, "fetch('/api/headroom/proxy"+p)
	}
	return html
}

// ---------------------------------------------------------------- pxpipe

func pxpipeDir(g *Gateway) string { return filepath.Join(g.cfg.DataDir, "pxpipe") }

func (g *Gateway) pxpipePackageRoot() string {
	return filepath.Join(pxpipeDir(g), "node_modules", pxpipePackage)
}

func (g *Gateway) pxpipeLibraryEntry() string {
	return filepath.Join(g.pxpipePackageRoot(), "dist", "core", "library.js")
}

func (g *Gateway) pxpipeInstallLog() string {
	return filepath.Join(pxpipeDir(g), "install.log")
}

func (g *Gateway) pxpipeEventsFile() string {
	return filepath.Join(pxpipeDir(g), "events.jsonl")
}

type pxpipeInstallInfo struct {
	Installed bool `json:"installed"`
	Version   any  `json:"version"`
	Path      any  `json:"path"`
}

func (g *Gateway) pxpipeInfo() pxpipeInstallInfo {
	var info pxpipeInstallInfo
	pkgJSON := filepath.Join(g.pxpipePackageRoot(), "package.json")
	if _, err := os.Stat(pkgJSON); err != nil {
		return info
	}
	if _, err := os.Stat(g.pxpipeLibraryEntry()); err != nil {
		return info
	}
	raw, err := os.ReadFile(pkgJSON)
	if err != nil {
		return info
	}
	var pkg struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(raw, &pkg)
	info.Installed = true
	info.Version = nulIfEmpty(pkg.Version)
	info.Path = g.pxpipePackageRoot()
	return info
}

// pxpipeState is the in-process "loaded" flag (library mode: load = warm, stop
// = drop; requests fail open meanwhile).
type pxpipeState struct {
	mu       sync.Mutex
	loadedAt time.Time
	version  string
	install  sync.Mutex // serializes npm install
	busy     bool
}

func (g *Gateway) pxpipeStatus() map[string]any {
	info := g.pxpipeInfo()
	g.px.mu.Lock()
	loadedAt := g.px.loadedAt
	g.px.mu.Unlock()
	var uptime int64
	running := !loadedAt.IsZero()
	if running {
		uptime = time.Since(loadedAt).Milliseconds()
	}
	_, npmErr := exec.LookPath("npm")
	return map[string]any{
		"installed": info.Installed, "installing": g.pxInstalling(),
		"version": info.Version, "path": info.Path,
		"running": running, "loadedAt": nilIfZero(loadedAt), "uptimeMs": uptime,
		"npmAvailable": npmErr == nil, "mode": "library",
	}
}

func nilIfZero(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func (g *Gateway) pxInstalling() bool {
	g.px.mu.Lock()
	defer g.px.mu.Unlock()
	return g.px.busy
}

// installPxpipe runs `npm install pxpipe-proxy@latest` in DATA_DIR/pxpipe.
func (g *Gateway) installPxpipe() error {
	g.px.install.Lock()
	defer g.px.install.Unlock()
	g.px.mu.Lock()
	g.px.busy = true
	g.px.mu.Unlock()
	defer func() {
		g.px.mu.Lock()
		g.px.busy = false
		g.px.mu.Unlock()
	}()

	npm, err := exec.LookPath("npm")
	if err != nil {
		return errors.New("npm not found on PATH — Node.js/npm is required to install PXPIPE")
	}
	if err := os.MkdirAll(pxpipeDir(g), 0o700); err != nil {
		return err
	}
	pkgJSON := filepath.Join(pxpipeDir(g), "package.json")
	if _, err := os.Stat(pkgJSON); err != nil {
		_ = os.WriteFile(pkgJSON, []byte(`{"name":"kinerouter-pxpipe-host","private":true}`), 0o600)
	}
	logf, err := os.OpenFile(g.pxpipeInstallLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	fmt.Fprintf(logf, "\n[%s] npm install %s@latest\n", time.Now().UTC().Format(time.RFC3339), pxpipePackage)

	cmd := exec.Command(npm, "install", pxpipePackage+"@latest", "--no-audit", "--no-fund", "--omit=dev")
	cmd.Dir = pxpipeDir(g)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = extendedEnv()
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return errors.New("npm install failed — see install.log")
		}
	case <-time.After(pxpipeInstallTimeout):
		_ = cmd.Process.Kill()
		return errors.New("npm install timed out after 5 minutes — see install.log")
	}
	if !g.pxpipeInfo().Installed {
		return errors.New("install finished but package is missing — see install.log")
	}
	return nil
}

// pxpipeTransformResult is the subprocess reply.
type pxpipeTransformResult struct {
	Applied bool           `json:"applied"`
	Body    string         `json:"body"` // base64
	Reason  string         `json:"reason"`
	Detail  string         `json:"detail"`
	Info    map[string]any `json:"info"`
	Cache   map[string]any `json:"cache"`
	Error   string         `json:"error"`
}

// pxpipeNodeHarness imports the installed library and runs one transform. The
// request JSON arrives on stdin, the result JSON leaves on stdout.
const pxpipeNodeHarness = `
const [entry, model, minChars] = process.argv.slice(1);
let data = "";
process.stdin.on("data", (c) => (data += c));
process.stdin.on("end", async () => {
  try {
    const mod = await import(entry);
    const body = new TextEncoder().encode(data);
    const result = await mod.transformAnthropicMessages({
      body, model, options: { minCompressChars: Number(minChars) || 0 },
    });
    const out = {
      applied: !!result?.applied,
      body: result?.body ? Buffer.from(result.body).toString("base64") : "",
      reason: result?.reason || "",
      detail: result?.detail || "",
      info: result?.info || {},
      cache: result?.cache || {},
    };
    process.stdout.write(JSON.stringify(out));
  } catch (e) {
    process.stdout.write(JSON.stringify({ applied: false, error: String(e && e.message || e) }));
  }
});
`

// PxpipeSummary mirrors the JS summary object surfaced in stats/observability.
type PxpipeSummary = map[string]any

// compressWithPxpipe transforms a Claude-format body. Fail-open: returns the
// original body and a summary with applied=false on any error.
func (g *Gateway) compressWithPxpipe(body map[string]any, provider string, settings map[string]any, timeoutMS int) (map[string]any, PxpipeSummary) {
	skip := func(reason string) (map[string]any, PxpipeSummary) {
		return body, PxpipeSummary{"applied": false, "reason": reason}
	}
	if !g.pxpipeInfo().Installed {
		return skip("not_installed")
	}
	if timeoutMS <= 0 {
		timeoutMS = pxpipeDefaultTimeoutMS
	}
	minChars := int(num(settings["pxpipeMinChars"]))
	if minChars <= 0 {
		minChars = pxpipeDefaultMinChars
	}
	raw, _ := json.Marshal(body)
	originalChars := len(raw)
	if originalChars < minChars {
		return body, PxpipeSummary{"applied": false, "reason": "below_threshold", "originalChars": originalChars, "threshold": minChars}
	}
	started := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "-e", pxpipeNodeHarness, g.pxpipeLibraryEntry(), "claude", strconv.Itoa(minChars))
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Env = os.Environ()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return body, PxpipeSummary{"applied": false, "reason": "timeout", "originalChars": originalChars, "durationMs": time.Since(started).Milliseconds()}
	}
	if runErr != nil {
		return body, PxpipeSummary{"applied": false, "reason": "transform_error", "detail": runErr.Error(), "originalChars": originalChars, "durationMs": time.Since(started).Milliseconds()}
	}
	if stdout.Len() > pxpipeTransformMaxOutput {
		return skip("transform_error")
	}
	var res pxpipeTransformResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return body, PxpipeSummary{"applied": false, "reason": "transform_error", "detail": err.Error(), "durationMs": time.Since(started).Milliseconds()}
	}
	if res.Error != "" {
		return body, PxpipeSummary{"applied": false, "reason": "transform_error", "detail": res.Error, "durationMs": time.Since(started).Milliseconds()}
	}
	if !res.Applied || res.Body == "" {
		reason := res.Reason
		if reason == "" {
			reason = "passthrough"
		}
		return body, PxpipeSummary{"applied": false, "reason": reason, "detail": res.Detail, "originalChars": originalChars, "durationMs": time.Since(started).Milliseconds()}
	}
	decoded, err := base64.StdEncoding.DecodeString(res.Body)
	if err != nil {
		return body, PxpipeSummary{"applied": false, "reason": "transform_error", "detail": err.Error()}
	}
	var newBody map[string]any
	if err := json.Unmarshal(decoded, &newBody); err != nil {
		return body, PxpipeSummary{"applied": false, "reason": "transform_error", "detail": err.Error()}
	}
	newRaw, _ := json.Marshal(newBody)
	compressedChars := int64(len(newRaw))
	imagedChars := int64(numAny(res.Info["compressedChars"]))
	imageCount := int64(numAny(res.Info["imageCount"]))
	imageTokens := int64(numAny(res.Info["imageTokens"]))
	if imageTokens == 0 {
		if px := int64(numAny(res.Info["imagePixels"])); px > 0 {
			imageTokens = px / 750
		} else {
			imageTokens = imageCount * 4761
		}
	}
	tokensBefore := int64(numAny(res.Info["baselineTokens"]))
	if tokensBefore == 0 {
		tokensBefore = int64(originalChars) / pxpipeEstCharsPerToken
	}
	remaining := originalChars - int(imagedChars)
	if remaining < 0 {
		remaining = 0
	}
	tokensAfter := int64(remaining)/pxpipeEstCharsPerToken + imageTokens
	saved := tokensBefore - tokensAfter
	if saved < 0 {
		saved = 0
	}
	pct := float64(0)
	if tokensBefore > 0 {
		pct = float64(saved) / float64(tokensBefore) * 100
	}
	summary := PxpipeSummary{
		"applied": true, "reason": "applied",
		"originalChars": originalChars, "compressedBodyChars": compressedChars,
		"imagedChars": imagedChars, "imageCount": imageCount,
		"imageBytes":      int64(numAny(res.Info["imageBytes"])),
		"tokensBeforeEst": tokensBefore, "tokensAfterEst": tokensAfter,
		"tokensSavedEst": saved, "savedPct": round2(pct),
		"durationMs":       time.Since(started).Milliseconds(),
		"cacheOwnsControl": res.Cache["ownsCacheControl"] == true,
	}
	g.appendPxpipeEvent(map[string]any{"provider": provider, "model": ""}, summary)
	return newBody, summary
}

func numAny(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

// appendPxpipeEvent is fire-and-forget; stats must never break the request path.
func (g *Gateway) appendPxpipeEvent(base map[string]any, summary PxpipeSummary) {
	defer func() { _ = recover() }()
	if err := os.MkdirAll(pxpipeDir(g), 0o700); err != nil {
		return
	}
	if st, err := os.Stat(g.pxpipeEventsFile()); err == nil && st.Size() > pxpipeEventsMaxBytes {
		_ = os.Rename(g.pxpipeEventsFile(), g.pxpipeEventsFile()+".1")
	}
	ev := map[string]any{"ts": time.Now().UnixMilli()}
	for k, v := range base {
		ev[k] = v
	}
	for k, v := range summary {
		ev[k] = v
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f, err := os.OpenFile(g.pxpipeEventsFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

func (g *Gateway) readPxpipeEvents(sinceMS int64) []map[string]any {
	var events []map[string]any
	for _, file := range []string{g.pxpipeEventsFile() + ".1", g.pxpipeEventsFile()} {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if line == "" {
				continue
			}
			var ev map[string]any
			if json.Unmarshal([]byte(line), &ev) != nil {
				continue
			}
			if sinceMS > 0 && int64(numAny(ev["ts"])) < sinceMS {
				continue
			}
			events = append(events, ev)
		}
	}
	sortByTimestamp(events)
	return events
}

func sortByTimestamp(events []map[string]any) {
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && numAny(events[j-1]["ts"]) > numAny(events[j]["ts"]); j-- {
			events[j-1], events[j] = events[j], events[j-1]
		}
	}
}

func emptyTotals() map[string]any {
	return map[string]any{
		"requests": int64(0), "compressed": int64(0), "bypassed": int64(0), "errors": int64(0),
		"tokensBeforeEst": int64(0), "tokensAfterEst": int64(0), "tokensSavedEst": int64(0), "savedPct": 0,
		"imagesGenerated": int64(0), "compressionTimeMs": int64(0), "avgCompressionMs": int64(0),
	}
}

func accumulateTotals(t map[string]any, ev map[string]any) {
	t["requests"] = t["requests"].(int64) + 1
	if applied, _ := ev["applied"].(bool); applied {
		t["compressed"] = t["compressed"].(int64) + 1
		t["tokensBeforeEst"] = t["tokensBeforeEst"].(int64) + int64(numAny(ev["tokensBeforeEst"]))
		t["tokensAfterEst"] = t["tokensAfterEst"].(int64) + int64(numAny(ev["tokensAfterEst"]))
		t["tokensSavedEst"] = t["tokensSavedEst"].(int64) + int64(numAny(ev["tokensSavedEst"]))
		t["imagesGenerated"] = t["imagesGenerated"].(int64) + int64(numAny(ev["imageCount"]))
		t["compressionTimeMs"] = t["compressionTimeMs"].(int64) + int64(numAny(ev["durationMs"]))
		return
	}
	switch ev["reason"] {
	case "transform_error", "timeout":
		t["errors"] = t["errors"].(int64) + 1
	default:
		t["bypassed"] = t["bypassed"].(int64) + 1
	}
}

func finalizeTotals(t map[string]any) {
	before := t["tokensBeforeEst"].(int64)
	saved := t["tokensSavedEst"].(int64)
	if before > 0 {
		t["savedPct"] = round2(float64(saved) / float64(before) * 100)
	}
	if c := t["compressed"].(int64); c > 0 {
		t["avgCompressionMs"] = t["compressionTimeMs"].(int64) / c
	}
}

const dayMS = int64(24 * 60 * 60 * 1000)

// pxpipeStats aggregates all-time + windowed totals and a daily timeline.
func (g *Gateway) pxpipeStats(timelineDays, recentLimit int) map[string]any {
	if timelineDays <= 0 {
		timelineDays = 30
	}
	if recentLimit <= 0 {
		recentLimit = 100
	}
	events := g.readPxpipeEvents(0)
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()

	windows := map[string]map[string]any{
		"all": emptyTotals(), "today": emptyTotals(), "yesterday": emptyTotals(),
		"last7d": emptyTotals(), "last30d": emptyTotals(),
	}
	timeline := map[string]map[string]any{}
	order := make([]string, 0, timelineDays)
	for i := timelineDays - 1; i >= 0; i-- {
		day := time.UnixMilli(startOfToday - int64(i)*dayMS).UTC().Format("2006-01-02")
		timeline[day] = map[string]any{"date": day, "tokensSavedEst": int64(0), "compressed": int64(0), "requests": int64(0)}
		order = append(order, day)
	}
	for _, ev := range events {
		ts := int64(numAny(ev["ts"]))
		accumulateTotals(windows["all"], ev)
		if ts >= startOfToday {
			accumulateTotals(windows["today"], ev)
		} else if ts >= startOfToday-dayMS {
			accumulateTotals(windows["yesterday"], ev)
		}
		if ts >= now.UnixMilli()-7*dayMS {
			accumulateTotals(windows["last7d"], ev)
		}
		if ts >= now.UnixMilli()-30*dayMS {
			accumulateTotals(windows["last30d"], ev)
		}
		key := time.UnixMilli(ts).UTC().Format("2006-01-02")
		if b := timeline[key]; b != nil {
			b["requests"] = b["requests"].(int64) + 1
			if applied, _ := ev["applied"].(bool); applied {
				b["compressed"] = b["compressed"].(int64) + 1
				b["tokensSavedEst"] = b["tokensSavedEst"].(int64) + int64(numAny(ev["tokensSavedEst"]))
			}
		}
	}
	for _, w := range windows {
		finalizeTotals(w)
	}
	var tl []map[string]any
	for _, d := range order {
		tl = append(tl, timeline[d])
	}
	recent := events
	if len(recent) > recentLimit {
		recent = recent[len(recent)-recentLimit:]
	}
	rev := make([]map[string]any, 0, len(recent))
	for i := len(recent) - 1; i >= 0; i-- {
		rev = append(rev, recent[i])
	}
	return map[string]any{"windows": windows, "timeline": tl, "recent": rev}
}

// ---------------------------------------------------------------- pxpipe handlers

func (g *Gateway) pxpipeSettingsView(settings map[string]any) map[string]any {
	out := g.pxpipeStatus()
	out["enabled"] = boolSetting(settings, "pxpipeEnabled")
	out["autoInstall"] = boolSetting(settings, "pxpipeAutoInstall")
	out["minChars"] = settings["pxpipeMinChars"]
	out["timeoutMs"] = settings["pxpipeTimeoutMs"]
	return out
}

func (g *Gateway) handlePxpipeStatus(w http.ResponseWriter, r *http.Request) {
	settings, _ := g.store.GetSettings()
	writeJSON(w, http.StatusOK, g.pxpipeSettingsView(settings))
}

func (g *Gateway) markPxpipeLoaded() {
	g.px.mu.Lock()
	g.px.loadedAt = time.Now()
	g.px.mu.Unlock()
}

func (g *Gateway) handlePxpipeStart(w http.ResponseWriter, r *http.Request) {
	if !g.pxpipeInfo().Installed {
		settings, _ := g.store.GetSettings()
		if !boolSetting(settings, "pxpipeAutoInstall") {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "PXPIPE is not installed", "code": "NOT_INSTALLED"})
			return
		}
		if err := g.installPxpipe(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": "INSTALL_FAILED"})
			return
		}
	}
	g.markPxpipeLoaded()
	settings, _ := g.store.GetSettings()
	writeJSON(w, http.StatusOK, g.pxpipeSettingsView(settings))
}

func (g *Gateway) handlePxpipeStop(w http.ResponseWriter, r *http.Request) {
	g.px.mu.Lock()
	wasLoaded := !g.px.loadedAt.IsZero()
	g.px.loadedAt = time.Time{}
	g.px.mu.Unlock()
	out := map[string]any{"stopped": wasLoaded}
	for k, v := range g.pxpipeStatus() {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) handlePxpipeRestart(w http.ResponseWriter, r *http.Request) {
	if !g.pxpipeInfo().Installed {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "PXPIPE is not installed", "code": "NOT_INSTALLED"})
		return
	}
	g.markPxpipeLoaded()
	writeJSON(w, http.StatusOK, g.pxpipeStatus())
}

func (g *Gateway) handlePxpipeInstall(w http.ResponseWriter, r *http.Request) {
	if err := g.installPxpipe(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": "INSTALL_FAILED"})
		return
	}
	info := g.pxpipeInfo()
	out := map[string]any{"installed": info.Installed, "version": info.Version, "path": info.Path}
	out["health"] = g.runPxpipeHealth()
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) handlePxpipeLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > 500 {
		limit = 500
	}
	events := g.readPxpipeEvents(0)
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	rev := make([]map[string]any, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		rev = append(rev, events[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installLog": logTail(g.pxpipeInstallLog(), 200), "events": rev,
	})
}

func (g *Gateway) handlePxpipeStats(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > 500 {
		limit = 500
	}
	writeJSON(w, http.StatusOK, g.pxpipeStats(30, limit))
}

// runPxpipeHealth is the PRD checklist: installed → module loads → a synthetic
// request transforms.
func (g *Gateway) runPxpipeHealth() map[string]any {
	var checks []map[string]any
	info := g.pxpipeInfo()
	checks = append(checks, map[string]any{"id": "installed", "label": "PXPIPE installed", "ok": info.Installed, "detail": info.Version})
	if !info.Installed {
		return map[string]any{"healthy": false, "checks": checks, "error": "pxpipe not installed"}
	}
	checks = append(checks, map[string]any{"id": "module", "label": "Transform module loads", "ok": true, "detail": info.Version})

	// One tiny synthetic Claude request through the transform.
	probe := map[string]any{
		"model": "claude-fable-5", "max_tokens": 16,
		"messages": []any{map[string]any{"role": "user", "content": "ping"}},
	}
	started := time.Now()
	if _, err := exec.LookPath("node"); err != nil {
		checks = append(checks, map[string]any{"id": "transform", "label": "Test request transforms", "ok": false, "detail": "node not found"})
		return map[string]any{"healthy": false, "checks": checks, "error": "Self-test failed: node not found"}
	}
	raw, _ := json.Marshal(probe)
	cmd := exec.Command("node", "-e", pxpipeNodeHarness, g.pxpipeLibraryEntry(), "claude-fable-5", "0")
	cmd.Stdin = bytes.NewReader(raw)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		checks = append(checks, map[string]any{"id": "transform", "label": "Test request transforms", "ok": false, "detail": err.Error()})
		return map[string]any{"healthy": false, "checks": checks, "error": "Self-test failed: " + err.Error()}
	}
	var res pxpipeTransformResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil || res.Error != "" {
		detail := "unexpected transform reply"
		if res.Error != "" {
			detail = res.Error
		}
		checks = append(checks, map[string]any{"id": "transform", "label": "Test request transforms", "ok": false, "detail": detail})
		return map[string]any{"healthy": false, "checks": checks, "error": "Self-test failed: " + detail}
	}
	reason := res.Reason
	if reason == "" {
		reason = "passthrough"
	}
	checks = append(checks, map[string]any{
		"id": "transform", "label": "Test request transforms", "ok": true,
		"detail": fmt.Sprintf("%dms (%s)", time.Since(started).Milliseconds(), reason),
	})
	return map[string]any{"healthy": true, "checks": checks, "error": nil}
}

// pxpipeTransformForChat returns the Claude-format transform hook for the chat
// pipeline; nil when PXpipe is off or not installed (fail-open).
func (g *Gateway) pxpipeTransformForChat(settings map[string]any) func(map[string]any) (map[string]any, PxpipeSummary) {
	if !boolSetting(settings, "pxpipeEnabled") || !g.pxpipeInfo().Installed {
		return nil
	}
	timeoutMS := int(num(settings["pxpipeTimeoutMs"]))
	return func(body map[string]any) (map[string]any, PxpipeSummary) {
		return g.compressWithPxpipe(body, "", settings, timeoutMS)
	}
}

// headroomForChat builds the proxy-compression options from settings.
func headroomForChat(settings map[string]any, model, format string) rtk.HeadroomOptions {
	return rtk.HeadroomOptions{
		Enabled:              boolSetting(settings, "headroomEnabled"),
		URL:                  headroomURL(settings),
		Model:                model,
		Format:               format,
		CompressUserMessages: boolSetting(settings, "headroomCompressUserMessages"),
		Timeout:              time.Duration(int64(numAny(settings["headroomTimeoutMs"]))) * time.Millisecond,
	}
}

// ---------------------------------------------------------------- routes

// registerHeadroomRoutes mirrors src/app/api/headroom/**: status/extras are
// dashboard-wide, while start/stop/restart/proxy are loopback-only (they spawn
// a process or act as an authenticated outbound proxy).
func (g *Gateway) registerHeadroomRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/headroom/status", g.requireAuth(g.handleHeadroomStatus))
	mux.HandleFunc("GET /api/headroom/extras", g.requireAuth(g.handleHeadroomExtras))
	mux.HandleFunc("POST /api/headroom/extras", g.requireAuth(g.handleHeadroomExtras))
	mux.HandleFunc("DELETE /api/headroom/extras", g.requireAuth(g.handleHeadroomExtras))
	mux.HandleFunc("POST /api/headroom/start", g.requireAuth(g.handleHeadroomStart))
	mux.HandleFunc("POST /api/headroom/restart", g.requireAuth(g.handleHeadroomRestart))
	mux.HandleFunc("POST /api/headroom/stop", g.requireAuth(g.handleHeadroomStop))
	proxy := g.requireAuth(g.handleHeadroomProxy)
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		mux.HandleFunc(m+" /api/headroom/proxy/{path...}", proxy)
	}
}

// registerPxpipeRoutes mirrors src/app/api/pxpipe/**.
func (g *Gateway) registerPxpipeRoutes(mux *http.ServeMux) {
	a := g.requireAuth
	mux.HandleFunc("GET /api/pxpipe/status", a(g.handlePxpipeStatus))
	mux.HandleFunc("GET /api/pxpipe/health", a(g.handlePxpipeHealth))
	mux.HandleFunc("POST /api/pxpipe/health", a(g.handlePxpipeHealth))
	mux.HandleFunc("POST /api/pxpipe/start", a(g.handlePxpipeStart))
	mux.HandleFunc("POST /api/pxpipe/stop", a(g.handlePxpipeStop))
	mux.HandleFunc("POST /api/pxpipe/restart", a(g.handlePxpipeRestart))
	mux.HandleFunc("POST /api/pxpipe/install", a(g.handlePxpipeInstall))
	mux.HandleFunc("GET /api/pxpipe/logs", a(g.handlePxpipeLogs))
	mux.HandleFunc("GET /api/pxpipe/stats", a(g.handlePxpipeStats))
}

func (g *Gateway) handlePxpipeHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.runPxpipeHealth())
}
