package gateway

// Public exposure: Cloudflare quick tunnel and Tailscale funnel. Ported from
// src/lib/tunnel/** and src/app/api/tunnel/**/route.js.
//
// Security notes:
//   - enable/disable/status and every Tailscale route sit behind requireAuth;
//     only the reverse-proxy login gate (tunnelDashboardAccess) is unauthenticated
//   - the download of cloudflared and the Tailscale installer fetch fixed
//     upstream URLs (github release / tailscale.com); no caller-supplied URL or
//     path reaches exec, and the binary is validated by size + magic bytes
//     before it is ever spawned
//   - spawned binaries are addressed by absolute path under DATA_DIR, never by
//     a name resolved through PATH that an attacker could shadow
//   - commands are executed with an argument vector, not a shell string, except
//     the two piped-install pipelines that intentionally use `sh -c`
//   - the registered public URL comes from cloudflared's own log line, so a
//     stale state file cannot be used to point the worker at another host

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	tunnelWorkerURLDefault = "https://abc-tunnel.us"
	tunnelShortIDLen       = 6
	tunnelShortIDChars     = "abcdefghijklmnpqrstuvwxyz23456789"
	tunnelQuickTunnelTO    = 90 * time.Second
	tunnelMinBinarySize    = 1 << 20
	cfHealthInterval       = 2 * time.Second
	cfHealthTimeout        = 60 * time.Second
	cfHealthFetchTimeout   = 5 * time.Second
	cfDNSTimeout           = 2 * time.Second
	tsHealthTimeout        = 180 * time.Second
	tsEnableHealthTimeout  = 20 * time.Second
	tsHealthFetchTimeout   = 8 * time.Second
	tsDNSTimeout           = 3 * time.Second
)

var tryCloudflareRe = regexp.MustCompile(`https://([a-z0-9-]+)\.trycloudflare\.com`)

// tunnelManager owns both tunnels. One process-wide instance on the Gateway.
type tunnelManager struct {
	mu             sync.Mutex
	dataDir        string
	st             *store.Store
	port           int
	cancel         bool // set by disable; checked by the enable flow
	busy           bool // spawnInProgress
	activePort     int
	cfCmd          *exec.Cmd
	unexpectedExit bool // suppress the restart loop during a deliberate kill
	dlActive       bool
	dlPercent      int
	tsBinCache     string
}

func newTunnelManager(dataDir string, st *store.Store, port int) *tunnelManager {
	return &tunnelManager{dataDir: dataDir, st: st, port: port}
}

// ---------------------------------------------------------------- paths/state

func (t *tunnelManager) tunnelDir() string { return filepath.Join(t.dataDir, "tunnel") }
func (t *tunnelManager) stateFile() string { return filepath.Join(t.tunnelDir(), "state.json") }
func (t *tunnelManager) pidFile() string   { return filepath.Join(t.tunnelDir(), "cloudflared.pid") }
func (t *tunnelManager) binDir() string    { return filepath.Join(t.dataDir, "bin") }

type tunnelState struct {
	ShortID   string `json:"shortId"`
	TunnelURL string `json:"tunnelUrl"`
}

func (t *tunnelManager) loadState() *tunnelState {
	m := readJSONMap(t.stateFile())
	if m == nil {
		return nil
	}
	return &tunnelState{ShortID: str(m, "shortId"), TunnelURL: str(m, "tunnelUrl")}
}

func (t *tunnelManager) saveState(s tunnelState) {
	os.MkdirAll(t.tunnelDir(), 0o700)
	_ = writeJSONFile(t.stateFile(), map[string]any{"shortId": s.ShortID, "tunnelUrl": s.TunnelURL})
}

func (t *tunnelManager) clearPidFile() {
	_ = os.Remove(t.pidFile())
}

func generateShortID() string {
	buf := make([]byte, tunnelShortIDLen)
	for i := range buf {
		buf[i] = tunnelShortIDChars[randIntn(len(tunnelShortIDChars))]
	}
	return string(buf)
}

func (t *tunnelManager) publicURL(shortID string) string {
	if shortID == "" {
		return ""
	}
	return "https://r" + shortID + ".abc-tunnel.us"
}

func tunnelWorkerURL() string {
	if v := strings.TrimSpace(os.Getenv("TUNNEL_WORKER_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return tunnelWorkerURLDefault
}

// ---------------------------------------------------------------- cloudflared

func (t *tunnelManager) cloudflaredPath() string {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(t.binDir(), name)
}

func cloudflaredDownloadURL() (string, bool, error) {
	const base = "https://github.com/cloudflare/cloudflared/releases/latest/download"
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return base + "/cloudflared-darwin-arm64.tgz", true, nil
		}
		return base + "/cloudflared-darwin-amd64.tgz", true, nil
	case "windows":
		if runtime.GOARCH == "amd64" {
			return base + "/cloudflared-windows-amd64.exe", false, nil
		}
		return base + "/cloudflared-windows-386.exe", false, nil
	case "linux":
		if runtime.GOARCH == "arm64" {
			return base + "/cloudflared-linux-arm64", false, nil
		}
		return base + "/cloudflared-linux-amd64", false, nil
	}
	return "", false, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
}

func isValidCloudflaredBinary(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < tunnelMinBinarySize {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	hex := fmt.Sprintf("%x", magic)
	switch runtime.GOOS {
	case "windows":
		return strings.HasPrefix(hex, "4d5a") // MZ
	case "darwin":
		return strings.HasPrefix(hex, "cffaedfe") || strings.HasPrefix(hex, "cefaedfe")
	}
	return strings.HasPrefix(hex, "7f454c46") // ELF
}

// ensureCloudflared mirrors ensureCloudflared: reuse a valid binary, otherwise
// download it (~30 MB) into <DATA_DIR>/bin.
func (t *tunnelManager) ensureCloudflared(ctx context.Context) (string, error) {
	bin := t.cloudflaredPath()
	if isValidCloudflaredBinary(bin) {
		if runtime.GOOS != "windows" {
			_ = os.Chmod(bin, 0o755)
		}
		return bin, nil
	}
	_ = os.Remove(bin)
	if err := os.MkdirAll(t.binDir(), 0o700); err != nil {
		return "", err
	}
	url, archive, err := cloudflaredDownloadURL()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", res.StatusCode)
	}
	t.setDownload(true, 0)
	defer t.setDownload(false, 0)

	if archive {
		if err := extractCloudflaredTGZ(res.Body, bin); err != nil {
			return "", err
		}
	} else {
		tmp := bin + ".tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return "", err
		}
		total := res.ContentLength
		var written int64
		buf := make([]byte, 64<<10)
		for {
			n, rerr := res.Body.Read(buf)
			if n > 0 {
				if _, werr := f.Write(buf[:n]); werr != nil {
					f.Close()
					os.Remove(tmp)
					return "", werr
				}
				written += int64(n)
				if total > 0 {
					t.setDownload(true, int(written*100/total))
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				f.Close()
				os.Remove(tmp)
				return "", rerr
			}
		}
		f.Close()
		if err := os.Rename(tmp, bin); err != nil {
			os.Remove(tmp)
			return "", err
		}
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(bin, 0o755)
	}
	if !isValidCloudflaredBinary(bin) {
		return "", errors.New("downloaded cloudflared binary failed validation")
	}
	return bin, nil
}

func extractCloudflaredTGZ(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return errors.New("cloudflared not found in archive")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "cloudflared" {
			continue
		}
		tmp := dest + ".tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		f.Close()
		return os.Rename(tmp, dest)
	}
}

func (t *tunnelManager) setDownload(active bool, pct int) {
	t.mu.Lock()
	t.dlActive, t.dlPercent = active, pct
	t.mu.Unlock()
}

func (t *tunnelManager) downloadStatus() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{"downloading": t.dlActive, "progress": t.dlPercent}
}

func (t *tunnelManager) savePid(pid int) {
	os.MkdirAll(t.tunnelDir(), 0o700)
	_ = os.WriteFile(t.pidFile(), []byte(itoa(int64(pid))), 0o600)
}

func (t *tunnelManager) loadPid() int {
	b, err := os.ReadFile(t.pidFile())
	if err != nil {
		return 0
	}
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid)
	return pid
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		// ponytail: no cheap Windows liveness probe; the pid file is trusted
		// until disable clears it. Upgrade with OpenProcess/GetExitCodeProcess
		// if a stale pid ever produces a false "running".
		return true
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func (t *tunnelManager) isRunning() bool { return pidAlive(t.loadPid()) }

func (t *tunnelManager) killCloudflared(port int) {
	t.mu.Lock()
	t.unexpectedExit = true
	cmd := t.cfCmd
	t.cfCmd = nil
	t.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if pid := t.loadPid(); pid > 0 {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
	t.clearPidFile()
	if port > 0 && runtime.GOOS != "windows" {
		// Boundary check keeps :20128 from matching :201280.
		_ = exec.Command("pkill", "-f", fmt.Sprintf("cloudflared.*:%d([^0-9]|$)", port)).Run()
	}
}

// spawnQuickTunnel starts cloudflared and resolves the first trycloudflare
// hostname it prints. onURL fires when the hostname changes later.
func (t *tunnelManager) spawnQuickTunnel(ctx context.Context, port int, onURL func(string)) (string, error) {
	bin, err := t.ensureCloudflared(ctx)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "cloudflared-quick-")
	if err != nil {
		return "", err
	}
	cfgPath := filepath.Join(dir, "config.yml")
	_ = os.WriteFile(cfgPath, []byte("# quick-tunnel config placeholder\n"), 0o600)
	cleanup := func() { os.RemoveAll(dir) }

	proto := strings.ToLower(strings.TrimSpace(firstNonEmpty(os.Getenv("TUNNEL_TRANSPORT_PROTOCOL"), os.Getenv("CLOUDFLARED_PROTOCOL"), "http2")))
	if proto != "http2" && proto != "quic" && proto != "auto" {
		proto = "http2"
	}
	cmd := exec.Command(bin, "tunnel", "--url", fmt.Sprintf("http://127.0.0.1:%d", port),
		"--config", cfgPath, "--no-autoupdate", "--retries", "99")
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "TUNNEL_TRANSPORT_PROTOCOL="+proto)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		cleanup()
		return "", err
	}
	t.mu.Lock()
	t.cfCmd = cmd
	t.mu.Unlock()
	t.savePid(cmd.Process.Pid)

	type result struct {
		url string
		err error
	}
	first := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		var lastURL string
		var tail strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			if tail.Len() < 4000 {
				tail.WriteString(line + "\n")
			}
			m := tryCloudflareRe.FindAllStringSubmatch(line, -1)
			if len(m) == 0 {
				continue
			}
			host := m[len(m)-1][1]
			if host == "api" {
				continue
			}
			url := "https://" + host + ".trycloudflare.com"
			if lastURL == "" {
				lastURL = url
				cleanup()
				first <- result{url: url}
			} else if url != lastURL {
				lastURL = url
				if onURL != nil {
					onURL(url)
				}
			}
		}
		select {
		case first <- result{err: fmt.Errorf("cloudflared quick tunnel exited. Last log: %s", truncate(tail.String(), 600))}:
		default:
		}
	}()
	go func() {
		werr := cmd.Wait()
		pw.Close()
		t.clearPidFile()
		t.mu.Lock()
		if t.cfCmd == cmd {
			t.cfCmd = nil
		}
		suppressed := t.unexpectedExit
		t.unexpectedExit = false
		cancelled := t.cancel
		t.mu.Unlock()
		if suppressed || cancelled || werr == nil {
			return
		}
		// No watchdog goroutine of its own: an unexpected exit after a
		// successful connect just re-enables once, which is all the JS
		// watchdog effectively did for a live process.
		// ponytail: single retry, no cooldown/network-change monitor; add a
		// ticker here if quick tunnels ever flap without recovering.
		if t.st.SettingBool("tunnelEnabled", false) {
			go func() {
				time.Sleep(2 * time.Second)
				if _, err := t.enable(); err != nil {
					logf("[Tunnel] auto-restart failed: %v", err)
				}
			}()
		}
	}()

	ctx2, cancel := context.WithTimeout(ctx, tunnelQuickTunnelTO)
	defer cancel()
	select {
	case r := <-first:
		if r.err != nil {
			return "", r.err
		}
		return r.url, nil
	case <-ctx2.Done():
		_ = cmd.Process.Kill()
		cleanup()
		return "", errors.New("quick tunnel timed out")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ---------------------------------------------------------------- health probes

func resolvePublicDNS(host string, timeout time.Duration) bool {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout}
		return d.DialContext(ctx, "udp", "1.1.1.1:53")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := r.LookupHost(ctx, host); err == nil {
		return true
	}
	// *.ts.net and other public-DNS blind spots fall back to the OS resolver.
	if _, err := net.LookupHost(host); err == nil {
		return true
	}
	return false
}

func probeURLAlive(rawURL string, dnsTO, fetchTO time.Duration) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if !resolvePublicDNS(u.Hostname(), dnsTO) {
		return false
	}
	client := &http.Client{Timeout: fetchTO}
	res, err := client.Get(strings.TrimRight(rawURL, "/") + "/api/health")
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 300
}

func waitForHealth(url string, dnsTO, fetchTO time.Duration, timeout time.Duration, cancelled func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cancelled != nil && cancelled() {
			return errors.New("cancelled")
		}
		if probeURLAlive(url, dnsTO, fetchTO) {
			return nil
		}
		time.Sleep(cfHealthInterval)
	}
	return fmt.Errorf("health check timeout after %s", timeout)
}

// ---------------------------------------------------------------- cloudflare service

func (t *tunnelManager) registerURL(shortID, tunnelURL string) error {
	body := mustJSON(map[string]any{"shortId": shortID, "tunnelUrl": tunnelURL})
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Post(tunnelWorkerURL()+"/api/tunnel/register", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	if res.StatusCode >= 300 {
		return fmt.Errorf("tunnel register failed with status %d", res.StatusCode)
	}
	return nil
}

func (t *tunnelManager) cancelled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancel
}

func (t *tunnelManager) enable() (map[string]any, error) {
	port := t.port
	logf("[Tunnel] enable start (port=%d)", port)
	t.mu.Lock()
	t.cancel = false
	t.activePort = port
	t.busy = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.busy = false
		t.mu.Unlock()
	}()

	if t.isRunning() {
		if existing := t.loadState(); existing != nil && existing.TunnelURL != "" && existing.ShortID != "" {
			publicURL := t.publicURL(existing.ShortID)
			type probeRes bool
			directCh := make(chan probeRes, 1)
			publicCh := make(chan probeRes, 1)
			go func() { directCh <- probeRes(probeURLAlive(existing.TunnelURL, cfDNSTimeout, cfHealthFetchTimeout)) }()
			go func() { publicCh <- probeRes(probeURLAlive(publicURL, cfDNSTimeout, cfHealthFetchTimeout)) }()
			if bool(<-directCh) && bool(<-publicCh) {
				logf("[Tunnel] already running, reuse: %s", existing.TunnelURL)
				return map[string]any{"success": true, "tunnelUrl": existing.TunnelURL, "shortId": existing.ShortID, "publicUrl": publicURL, "alreadyRunning": true}, nil
			}
		}
	}

	t.killCloudflared(port)
	if t.cancelled() {
		return nil, errors.New("tunnel cancelled")
	}

	shortID := generateShortID()
	if existing := t.loadState(); existing != nil && existing.ShortID != "" {
		shortID = existing.ShortID
	}
	onURL := func(url string) {
		if t.cancelled() {
			return
		}
		_ = t.registerURL(shortID, url)
		t.saveState(tunnelState{ShortID: shortID, TunnelURL: url})
		_, _ = t.st.UpdateSettings(map[string]any{"tunnelEnabled": true, "tunnelUrl": url})
	}

	tunnelURL, err := t.spawnQuickTunnel(context.Background(), port, onURL)
	if err != nil {
		if !strings.Contains(err.Error(), "killed") {
			logf("[Tunnel] enable error: %v", err)
		}
		return nil, err
	}
	if t.cancelled() {
		return nil, errors.New("tunnel cancelled")
	}
	if err := t.registerURL(shortID, tunnelURL); err != nil {
		return nil, err
	}
	t.saveState(tunnelState{ShortID: shortID, TunnelURL: tunnelURL})
	_, _ = t.st.UpdateSettings(map[string]any{"tunnelEnabled": true, "tunnelUrl": tunnelURL})
	publicURL := t.publicURL(shortID)

	if err := waitForHealth(publicURL, cfDNSTimeout, cfHealthFetchTimeout, cfHealthTimeout, t.cancelled); err != nil {
		return nil, err
	}
	if !probeURLAlive(tunnelURL, cfDNSTimeout, cfHealthFetchTimeout) {
		logf("[Tunnel] direct URL not reachable yet, continuing via publicUrl")
	}
	return map[string]any{"success": true, "tunnelUrl": tunnelURL, "shortId": shortID, "publicUrl": publicURL}, nil
}

func (t *tunnelManager) disable() map[string]any {
	logf("[Tunnel] disable")
	t.mu.Lock()
	t.cancel = true
	port := t.activePort
	t.busy = false
	t.activePort = 0
	t.mu.Unlock()

	t.killCloudflared(port)
	if state := t.loadState(); state != nil {
		t.saveState(tunnelState{ShortID: state.ShortID})
	}
	_, _ = t.st.UpdateSettings(map[string]any{"tunnelEnabled": false, "tunnelUrl": ""})
	return map[string]any{"success": true}
}

func (t *tunnelManager) status() map[string]any {
	settingsEnabled := t.st.SettingBool("tunnelEnabled", false)
	state := t.loadState()
	shortID, tunnelURL := "", ""
	if state != nil {
		shortID, tunnelURL = state.ShortID, state.TunnelURL
	}
	running := settingsEnabled && t.isRunning()
	return map[string]any{
		"enabled":         settingsEnabled && running,
		"settingsEnabled": settingsEnabled,
		"tunnelUrl":       tunnelURL,
		"shortId":         shortID,
		"publicUrl":       t.publicURL(shortID),
		"running":         running,
	}
}

// resume re-enables a tunnel that was on when the process last exited.
func (t *tunnelManager) resume() {
	if !t.st.SettingBool("tunnelEnabled", false) {
		return
	}
	go func() {
		if _, err := t.enable(); err != nil {
			logf("[Tunnel] auto-resume failed: %v", err)
		}
	}()
	if t.st.SettingBool("tailscaleEnabled", false) {
		go func() {
			if _, err := t.enableTailscale(); err != nil {
				logf("[Tailscale] auto-resume failed: %v", err)
			}
		}()
	}
}

// ---------------------------------------------------------------- tailscale

func (t *tunnelManager) tailscaleDir() string { return filepath.Join(t.dataDir, "tailscale") }
func (t *tunnelManager) tailscaleSocket() string {
	return filepath.Join(t.tailscaleDir(), "tailscaled.sock")
}
func (t *tunnelManager) systemSocket() string { return "/var/run/tailscale/tailscaled.sock" }

func (t *tunnelManager) tailscaleBin() string {
	t.mu.Lock()
	cached := t.tsBinCache
	t.mu.Unlock()
	if cached != "" && fileExists(cached) {
		return cached
	}
	name := "tailscale"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates := []string{filepath.Join(t.binDir(), name)}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, `C:\Program Files\Tailscale\tailscale.exe`)
	} else {
		candidates = append(candidates,
			"/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale",
			"/usr/sbin/tailscale", "/usr/bin/tailscale", "/snap/bin/tailscale")
	}
	for _, c := range candidates {
		if fileExists(c) {
			t.mu.Lock()
			t.tsBinCache = c
			t.mu.Unlock()
			return c
		}
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (t *tunnelManager) tsRun(timeout time.Duration, args ...string) (string, error) {
	bin := t.tailscaleBin()
	if bin == "" {
		return "", errors.New("Tailscale not installed")
	}
	full := args
	if runtime.GOOS != "windows" {
		full = append([]string{"--socket", t.tailscaleSocket()}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, full...).Output()
	return string(out), err
}

type tsStatusJSON struct {
	BackendState string `json:"BackendState"`
	AuthURL      string `json:"AuthURL"`
	Self         *struct {
		Online  bool   `json:"Online"`
		DNSName string `json:"DNSName"`
	} `json:"Self"`
	AllowFunnel map[string]any `json:"AllowFunnel"`
}

func (t *tunnelManager) tsStatus() (*tsStatusJSON, error) {
	out, err := t.tsRun(5*time.Second, "status", "--json")
	if err != nil {
		return nil, err
	}
	var s tsStatusJSON
	if json.Unmarshal([]byte(out), &s) != nil {
		return nil, errors.New("invalid tailscale status")
	}
	return &s, nil
}

func (t *tunnelManager) isTSLoggedInStrict() bool {
	s, err := t.tsStatus()
	return err == nil && s.BackendState == "Running" && s.Self != nil && s.Self.Online
}

func (t *tunnelManager) isTSRunningStrict() bool {
	out, err := t.tsRun(5*time.Second, "funnel", "status", "--json")
	if err != nil {
		return false
	}
	var s tsStatusJSON
	if json.Unmarshal([]byte(out), &s) != nil {
		return false
	}
	return len(s.AllowFunnel) > 0
}

func (t *tunnelManager) isSystemDaemonRunning() bool {
	if runtime.GOOS == "windows" || !fileExists(t.systemSocket()) {
		return false
	}
	bin := t.tailscaleBin()
	if bin == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--socket", t.systemSocket(), "status", "--json").Output()
	if err != nil {
		return false
	}
	var s tsStatusJSON
	return json.Unmarshal([]byte(out), &s) == nil && s.BackendState == "Running"
}

func (t *tunnelManager) funnelURL() string {
	s, err := t.tsStatus()
	if err != nil || s.Self == nil {
		return ""
	}
	dns := strings.TrimSuffix(s.Self.DNSName, ".")
	if dns == "" {
		return ""
	}
	return "https://" + dns
}

// startDaemon mirrors startDaemonWithPassword. Without a sudo password it falls
// back to userspace networking, which is enough for the funnel to serve but
// cannot provision a TLS cert from the local daemon's TUN stack.
func (t *tunnelManager) startDaemon(sudoPassword string) {
	bin := t.tailscaleBin()
	if bin == "" {
		return
	}
	if s, err := t.tsStatus(); err == nil && s.BackendState == "Running" {
		return
	}
	_ = os.MkdirAll(t.tailscaleDir(), 0o700)
	args := []string{"--socket=" + t.tailscaleSocket(), "--statedir=" + t.tailscaleDir()}
	tailscaledBin := "tailscaled"
	if runtime.GOOS == "darwin" {
		tailscaledBin = "/usr/local/bin/tailscaled"
	}
	var cmd *exec.Cmd
	if sudoPassword != "" && runtime.GOOS != "windows" {
		cmd = exec.Command("sudo", append([]string{"-S", tailscaledBin}, args...)...)
		cmd.Stdin = strings.NewReader(sudoPassword + "\n")
	} else {
		args = append(args, "--tun=userspace-networking")
		cmd = exec.Command(tailscaledBin, args...)
	}
	cmd.Dir = os.TempDir()
	if err := cmd.Start(); err != nil {
		logf("[Tailscale] daemon start: %v", err)
		return
	}
	_ = cmd.Process.Release()
	time.Sleep(3 * time.Second)
}

var authURLRe = regexp.MustCompile(`https://login\.tailscale\.com/a/[a-zA-Z0-9]+`)
var funnelEnableRe = regexp.MustCompile(`https://login\.tailscale\.com/[^\s]+`)

// startLogin runs `tailscale up` and returns the browser auth URL.
func (t *tunnelManager) startLogin(hostname string) (authURL string, alreadyLoggedIn bool, err error) {
	bin := t.tailscaleBin()
	if bin == "" {
		return "", false, errors.New("Tailscale not installed")
	}
	if t.isTSLoggedInStrict() {
		return "", true, nil
	}
	args := []string{}
	if runtime.GOOS != "windows" {
		args = append(args, "--socket", t.tailscaleSocket())
	}
	args = append(args, "up", "--accept-routes")
	if hostname != "" {
		args = append(args, "--hostname="+hostname)
	}
	cmd := exec.Command(bin, args...)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return "", false, err
	}
	outCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(pr)
		var out strings.Builder
		for scanner.Scan() {
			out.WriteString(scanner.Text() + "\n")
			if m := authURLRe.FindString(out.String()); m != "" {
				outCh <- m
				io.Copy(io.Discard, pr)
				return
			}
		}
		outCh <- authURLRe.FindString(out.String())
	}()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); pw.Close(); close(done) }()

	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case u := <-outCh:
			if u != "" {
				_ = cmd.Process.Release()
				return u, false, nil
			}
			<-done
			if t.isTSLoggedInStrict() {
				return "", true, nil
			}
			if s, e := t.tsStatus(); e == nil && s.AuthURL != "" {
				return s.AuthURL, false, nil
			}
			return "", false, errors.New("tailscale up timed out without auth URL")
		case <-tick.C:
			if s, e := t.tsStatus(); e == nil && s.AuthURL != "" {
				_ = cmd.Process.Release()
				return s.AuthURL, false, nil
			}
		case <-deadline:
			_ = cmd.Process.Kill()
			if s, e := t.tsStatus(); e == nil && s.AuthURL != "" {
				return s.AuthURL, false, nil
			}
			return "", false, errors.New("tailscale up timed out without auth URL")
		}
	}
}

type funnelResult struct {
	url        string
	notEnabled bool
	enableURL  string
}

func (t *tunnelManager) startFunnel(port int) (funnelResult, error) {
	bin := t.tailscaleBin()
	if bin == "" {
		return funnelResult{}, errors.New("Tailscale not installed")
	}
	t.stopFunnel()
	args := []string{}
	if runtime.GOOS != "windows" {
		args = append(args, "--socket", t.tailscaleSocket())
	}
	args = append(args, "funnel", "--bg", itoa(int64(port)))
	cmd := exec.Command(bin, args...)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return funnelResult{}, err
	}
	var (
		mu     sync.Mutex
		out    strings.Builder
		notEn  bool
		enable string
	)
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			mu.Lock()
			out.WriteString(line + "\n")
			if strings.Contains(line, "Funnel is not enabled") {
				notEn = true
			}
			if notEn && funnelEnableRe.MatchString(line) {
				enable = funnelEnableRe.FindString(line)
			}
			mu.Unlock()
		}
	}()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); pw.Close(); close(done) }()

	deadline := time.After(30 * time.Second)
	for {
		mu.Lock()
		ne, eu := notEn, enable
		mu.Unlock()
		if ne && eu != "" {
			_ = cmd.Process.Kill()
			return funnelResult{notEnabled: true, enableURL: eu}, nil
		}
		select {
		case <-done:
			if url := t.funnelURL(); url != "" {
				return funnelResult{url: url}, nil
			}
			mu.Lock()
			tail := truncate(out.String(), 400)
			mu.Unlock()
			return funnelResult{}, fmt.Errorf("tailscale funnel failed: %s", tail)
		case <-deadline:
			_ = cmd.Process.Kill()
			if url := t.funnelURL(); url != "" {
				return funnelResult{url: url}, nil
			}
			return funnelResult{}, errors.New("tailscale funnel timed out")
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (t *tunnelManager) stopFunnel() {
	_, _ = t.tsRun(5*time.Second, "funnel", "--bg", "reset")
}

func (t *tunnelManager) provisionCert(hostname string) {
	bin := t.tailscaleBin()
	if bin == "" || hostname == "" {
		return
	}
	dir := filepath.Join(t.tailscaleDir(), "certs")
	_ = os.MkdirAll(dir, 0o700)
	args := []string{}
	if runtime.GOOS != "windows" {
		args = append(args, "--socket", t.tailscaleSocket())
	}
	args = append(args, "cert",
		"--cert-file", filepath.Join(dir, hostname+".crt"),
		"--key-file", filepath.Join(dir, hostname+".key"), hostname)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, bin, args...).Run(); err != nil {
		logf("[Tailscale] cert provision failed (non-fatal): %v", err)
	}
}

func (t *tunnelManager) enableTailscale() (map[string]any, error) {
	port := t.port
	logf("[Tailscale] enable start (port=%d)", port)
	t.mu.Lock()
	t.cancel = false
	t.activePort = port
	t.busy = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.busy = false
		t.mu.Unlock()
	}()

	t.startDaemon("")
	if t.cancelled() {
		return nil, errors.New("tailscale cancelled")
	}
	shortID := generateShortID()
	if st := t.loadState(); st != nil && st.ShortID != "" {
		shortID = st.ShortID
	}
	if !t.isTSLoggedInStrict() {
		authURL, already, err := t.startLogin(shortID)
		if err != nil {
			return nil, err
		}
		if !already && authURL != "" {
			return map[string]any{"success": false, "needsLogin": true, "authUrl": authURL}, nil
		}
	}
	t.stopFunnel()
	res, err := t.startFunnel(port)
	if err != nil {
		msg := err.Error()
		if regexpTSSetup.MatchString(msg) {
			authURL, already, lerr := t.startLogin(shortID)
			if lerr == nil && !already && authURL != "" {
				return map[string]any{"success": false, "needsLogin": true, "authUrl": authURL}, nil
			}
		}
		return nil, err
	}
	if res.notEnabled {
		return map[string]any{"success": false, "funnelNotEnabled": true, "enableUrl": res.enableURL}, nil
	}
	if !t.isTSLoggedInStrict() || !t.isTSRunningStrict() {
		t.stopFunnel()
		return nil, errors.New("Tailscale not connected. Device may have been removed. Please re-login.")
	}
	_, _ = t.st.UpdateSettings(map[string]any{"tailscaleEnabled": true, "tailscaleUrl": res.url})
	if u := parseHostname(res.url); u != "" {
		t.provisionCert(u)
	}
	if err := waitForHealth(res.url, tsDNSTimeout, tsHealthFetchTimeout, tsEnableHealthTimeout, t.cancelled); err != nil && !strings.HasPrefix(err.Error(), "health check timeout") {
		return nil, err
	}
	return map[string]any{"success": true, "tunnelUrl": res.url}, nil
}

var regexpTSSetup = regexp.MustCompile(`(?i)NoState|unexpected state|not logged in|Logged ?out|NeedsLogin`)

func (t *tunnelManager) disableTailscale() map[string]any {
	logf("[Tailscale] disable")
	t.mu.Lock()
	t.cancel = true
	t.mu.Unlock()
	t.stopFunnel()
	_, _ = t.st.UpdateSettings(map[string]any{"tailscaleEnabled": false, "tailscaleUrl": ""})
	return map[string]any{"success": true}
}

func (t *tunnelManager) tailscaleStatus() map[string]any {
	settingsEnabled := t.st.SettingBool("tailscaleEnabled", false)
	tunnelURL := t.st.SettingString("tailscaleUrl", "")
	loggedIn := settingsEnabled && t.isTSLoggedInStrict()
	running := loggedIn && t.isTSRunningStrict()
	return map[string]any{
		"enabled":         settingsEnabled && running,
		"settingsEnabled": settingsEnabled,
		"tunnelUrl":       tunnelURL,
		"running":         running,
		"loggedIn":        loggedIn,
	}
}

// installTailscale mirrors installTailscale: brew on macOS, the official
// install.sh elsewhere. Windows MSI install is not implemented — see the
// ponytail note in the handler.
func (t *tunnelManager) installTailscale(sudoPassword string, onProgress func(string)) error {
	switch runtime.GOOS {
	case "windows":
		return errors.New("install Tailscale from tailscale.com/download on Windows, then retry")
	case "darwin":
		if p, err := exec.LookPath("brew"); err == nil && p != "" {
			onProgress("Installing via Homebrew...")
			if out, err := exec.Command("brew", "install", "tailscale").CombinedOutput(); err != nil {
				return fmt.Errorf("brew install tailscale: %v (%s)", err, truncate(string(out), 400))
			}
		} else {
			return errors.New("Homebrew is not available; install Tailscale from the App Store or tailscale.com/download")
		}
	default:
		onProgress("Installing via official script...")
		cmd := exec.Command("sh", "-c", "curl -fsSL https://tailscale.com/install.sh | sudo -S sh")
		cmd.Stdin = strings.NewReader(sudoPassword + "\n")
		if out, err := cmd.CombinedOutput(); err != nil {
			msg := truncate(string(out), 600)
			if strings.Contains(msg, "incorrect password") || strings.Contains(msg, "Sorry") {
				return errors.New("Wrong sudo password")
			}
			return fmt.Errorf("install failed: %v (%s)", err, msg)
		}
	}
	t.mu.Lock()
	t.tsBinCache = ""
	t.mu.Unlock()
	onProgress("Starting daemon...")
	t.startDaemon(sudoPassword)
	if bin := t.tailscaleBin(); bin == "" {
		return errors.New("tailscale binary not found after install")
	}
	return nil
}

// ---------------------------------------------------------------- handlers

// writeSSE sends one Server-Sent event named event with a JSON payload.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, v any) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, mustJSON(v))
	if flusher != nil {
		flusher.Flush()
	}
}

func (g *Gateway) handleTunnelEnable(w http.ResponseWriter, r *http.Request) {
	res, err := g.tunnels().enable()
	if err != nil {
		logf("[Tunnel] enable error: %v", err)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Let the Cloudflare edge learn the new hostname before the dashboard
	// health-checks it (JS: DNS_WARMUP_DELAY_MS).
	time.Sleep(8 * time.Second)
	writeJSON(w, http.StatusOK, res)
}

func (g *Gateway) handleTunnelDisable(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.tunnels().disable())
}

func (g *Gateway) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	t := g.tunnels()
	writeJSON(w, http.StatusOK, map[string]any{
		"tunnel":    t.status(),
		"tailscale": t.tailscaleStatus(),
		"download":  t.downloadStatus(),
	})
}

func (g *Gateway) handleTailscaleEnable(w http.ResponseWriter, r *http.Request) {
	res, err := g.tunnels().enableTailscale()
	if err != nil {
		logf("[Tailscale] enable error: %v", err)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (g *Gateway) handleTailscaleDisable(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.tunnels().disableTailscale())
}

func (g *Gateway) handleTailscaleCheck(w http.ResponseWriter, r *http.Request) {
	t := g.tunnels()
	installed := t.tailscaleBin() != ""
	customDaemon := installed && (t.isTSLoggedInStrict() || t.isTSRunningStrict())
	systemDaemon := installed && t.isSystemDaemonRunning()
	daemonRunning := customDaemon || systemDaemon
	loggedIn := daemonRunning && t.isTSLoggedInStrict()
	platform := runtime.GOOS
	brewAvailable := false
	if platform == "darwin" {
		_, err := exec.LookPath("brew")
		brewAvailable = err == nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installed":           installed,
		"loggedIn":            loggedIn,
		"platform":            platform,
		"brewAvailable":       brewAvailable,
		"daemonRunning":       daemonRunning,
		"customDaemonRunning": customDaemon,
		"systemDaemonRunning": systemDaemon,
		"hasCachedPassword":   false, // MITM sudo cache is not ported
	})
}

func (g *Gateway) handleTailscaleInstall(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		body = map[string]any{}
	}
	sudoPassword := str(body, "sudoPassword")
	needsPassword := runtime.GOOS != "windows" && !(runtime.GOOS == "darwin" && func() bool {
		_, err := exec.LookPath("brew")
		return err == nil
	}())
	if needsPassword && strings.TrimSpace(sudoPassword) == "" {
		writeErr(w, http.StatusBadRequest, "Sudo password is required")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	if err := g.tunnels().installTailscale(sudoPassword, func(msg string) {
		writeSSE(w, flusher, "progress", map[string]any{"message": msg})
	}); err != nil {
		writeSSE(w, flusher, "error", map[string]any{"error": err.Error()})
		return
	}
	// Kick the login flow so the dashboard gets an auth URL in one round trip.
	hostname := ""
	if st := g.tunnels().loadState(); st != nil {
		hostname = st.ShortID
	}
	authURL, already, err := g.tunnels().startLogin(hostname)
	if err != nil || already {
		writeSSE(w, flusher, "done", map[string]any{"success": true, "authUrl": nil})
		return
	}
	writeSSE(w, flusher, "done", map[string]any{"success": true, "authUrl": authURL})
}

// ---------------------------------------------------------------- misc

func parseHostname(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func randIntn(n int) int {
	if n <= 0 {
		return 0
	}
	var b [1]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0
		}
		if int(b[0]) < 256-(256%n) {
			return int(b[0]) % n
		}
	}
}
