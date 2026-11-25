package mitm

// Process supervision for the privileged MITM server. The server itself is
// this same binary run as `kinerouter mitm-server` under sudo; this file owns
// the PID/lock files, health probe, port-443 ownership and auto-restart, and
// mirrors src/mitm/manager.js (Linux/macOS paths only).

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
)

const (
	mitmPort           = 443
	maxRestarts        = 5
	restartWindowReset = 60 * time.Second
	healthPollTimeout  = 8 * time.Second
)

// defaultRouterBase is the loopback origin used when no mitmRouterBaseUrl is
// set; InitHooks replaces it with the port the gateway actually serves on.
var defaultRouterBase = "http://localhost:" + strconv.Itoa(config.DefaultPort)

var restartDelays = []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second, 60 * time.Second}

// Settings hooks, wired by the gateway (mirrors initDbHooks in manager.js).
var (
	getSettings    func() (map[string]any, error)
	updateSettings func(map[string]any) (map[string]any, error)
)

func InitHooks(
	get func() (map[string]any, error),
	update func(map[string]any) (map[string]any, error),
	port int,
) {
	getSettings, updateSettings = get, update
	if port > 0 {
		defaultRouterBase = "http://localhost:" + strconv.Itoa(port)
	}
}

// RouterBaseURL resolves the configured router base (settings.mitmRouterBaseUrl),
// falling back to the default when absent or not http(s).
func RouterBaseURL() string {
	if getSettings == nil {
		return defaultRouterBase
	}
	s, err := getSettings()
	if err != nil {
		return defaultRouterBase
	}
	raw, _ := s["mitmRouterBaseUrl"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultRouterBase
	}
	u, err := ParseRouterURL(raw)
	if err != nil {
		return defaultRouterBase
	}
	return u
}

// ParseRouterURL normalizes a user-supplied router base: http/https only,
// no trailing slashes. (SSRF note: this URL is an operator setting used by a
// root process to reach their own gateway; it never comes from an upstream.)
func ParseRouterURL(raw string) (string, error) {
	t := strings.TrimRight(strings.TrimSpace(raw), "/")
	if t == "" {
		return defaultRouterBase, nil
	}
	lower := strings.ToLower(t)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", errors.New("MITM router URL must use http or https")
	}
	if len(t) < len("http://")+1 {
		return "", errors.New("Invalid MITM router URL")
	}
	return t, nil
}

// ---------------------------------------------------------------- password cache

var (
	pwdMu     sync.Mutex
	cachedPwd string
)

func CachedPassword() string {
	pwdMu.Lock()
	defer pwdMu.Unlock()
	return cachedPwd
}

func SetCachedPassword(p string) {
	pwdMu.Lock()
	cachedPwd = p
	pwdMu.Unlock()
}

// Password returns the caller-supplied password, else the cached one.
func Password(provided string) string {
	if provided != "" {
		return provided
	}
	return CachedPassword()
}

// ---------------------------------------------------------------- status

type Status struct {
	Running     bool            `json:"running"`
	PID         int             `json:"pid"`
	CertExists  bool            `json:"certExists"`
	CertTrusted bool            `json:"certTrusted"`
	DNSStatus   map[string]bool `json:"dnsStatus"`
}

func GetStatus(p Paths) Status {
	pid := 0
	if b, err := os.ReadFile(p.PIDFile); err == nil {
		n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if n > 0 && isAlive(n) {
			pid = n
		} else {
			os.Remove(p.PIDFile)
		}
	}
	_, certErr := os.Stat(p.RootCert)
	_, keyErr := os.Stat(p.RootKey)
	certExists := certErr == nil && keyErr == nil
	st := Status{
		Running:    pid > 0,
		PID:        pid,
		CertExists: certExists,
		DNSStatus:  DNSStatus(),
	}
	if certExists {
		st.CertTrusted = CertTrusted(p)
	}
	return st
}

func isAlive(pid int) bool { return processAlive(pid) }

// ---------------------------------------------------------------- port 443

// PortInUse reports whether :443 cannot be bound (another process owns it).
func PortInUse() bool {
	ln, err := net.Listen("tcp", "127.0.0.1:443")
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

type PortOwner struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

var ssUsersRe = regexp.MustCompile(`users:\(\("([^"]+)",pid=(\d+)`)

// Port443Owner finds the process listening on TCP 443 via `ss`. Returns nil
// when nothing (or no ss) is available.
func Port443Owner() *PortOwner {
	out, err := exec.Command("ss", "-lnptH", "sport = :443").Output()
	if err != nil {
		return nil
	}
	if m := ssUsersRe.FindStringSubmatch(string(out)); m != nil {
		pid, _ := strconv.Atoi(m[2])
		if pid > 0 {
			return &PortOwner{PID: pid, Name: m[1]}
		}
	}
	return nil
}

// ---------------------------------------------------------------- start / stop

var (
	runMu     sync.Mutex
	childCmd  *exec.Cmd
	childDone chan struct{}

	restartMu    sync.Mutex
	restartCount int
	lastStart    time.Time
	stopping     bool
)

// ErrPortBusy carries the process owning :443 so the route can answer 409.
type ErrPortBusy struct{ Owner *PortOwner }

func (e *ErrPortBusy) Error() string {
	if e.Owner == nil {
		return "Port 443 is already in use"
	}
	return fmt.Sprintf("Port 443 is already in use by %q (PID %d).", e.Owner.Name, e.Owner.PID)
}

// Start ensures the privileged server is running. apiKey is the router key the
// server presents to the local gateway; sudoPass feeds sudo -S.
func Start(p Paths, apiKey, sudoPass string, forceKill443 bool) (int, error) {
	runMu.Lock()
	defer runMu.Unlock()

	if st := GetStatus(p); st.Running {
		if err := saveMitmSettings(true, sudoPass); err == nil && sudoPass != "" {
			SetCachedPassword(sudoPass)
		}
		return st.PID, nil
	}

	if !acquireLock(p.LockFile) {
		return 0, errors.New("MITM server is already starting (lock contention)")
	}
	defer os.Remove(p.LockFile)

	killLeftover(p, sudoPass)

	if PortInUse() {
		owner := Port443Owner()
		if owner != nil {
			if !forceKill443 {
				return 0, &ErrPortBusy{Owner: owner}
			}
			ExecPrivileged("kill -9 "+strconv.Itoa(owner.PID), sudoPass)
			time.Sleep(800 * time.Millisecond)
		}
	}

	if err := EnsureRootCA(p); err != nil {
		return 0, err
	}
	if !CertTrusted(p) {
		if SudoAvailable() && SudoPasswordRequired() && sudoPass == "" && !IsRoot() {
			return 0, errors.New("Sudo password required to install Root CA certificate")
		}
		if _, err := TrustCert(p, sudoPass); err != nil {
			return 0, fmt.Errorf("Failed to trust certificate: %v", err)
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	base := RouterBaseURL()
	home, _ := os.UserHomeDir()
	inline := strings.Join([]string{
		"HOME=" + shellQuote(home),
		"DATA_DIR=" + shellQuote(p.DirBase()),
		"ROUTER_API_KEY=" + shellQuote(apiKey),
		"MITM_ROUTER_BASE=" + shellQuote(base),
		shellQuote(exe), "mitm-server",
	}, " ")

	var cmd *exec.Cmd
	if SudoAvailable() && !IsRoot() {
		cmd = exec.Command("sudo", "-S", "sh", "-c", inline)
		cmd.Stdin = strings.NewReader(sudoPass + "\n")
	} else {
		cmd = exec.Command("sh", "-c", inline)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	childCmd = cmd
	done := make(chan struct{})
	childDone = done
	pid := cmd.Process.Pid
	os.WriteFile(p.PIDFile, []byte(strconv.Itoa(pid)), 0o600)
	lastStart = time.Now()

	go supervise(p, cmd, done, stderr.String, apiKey)

	if !pollHealth() {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = "Check sudo password or port 443 access."
		}
		if strings.Contains(reason, "incorrect password") || strings.Contains(reason, "no password was provided") {
			SetCachedPassword("")
			return 0, errors.New("Wrong sudo password")
		}
		return 0, fmt.Errorf("MITM server failed to start. %s", reason)
	}

	saveMitmSettings(true, sudoPass)
	updateSetting("mitmCertInstalled", true)
	if sudoPass != "" {
		SetCachedPassword(sudoPass)
	}
	return pid, nil
}

// Shutdown stops a MITM server left running by a previous session and removes
// the hosts entries it installed. Mirrors KeyRouter's killProxyByPidFile, which
// runs on every exit: quitting must not leave a root process on :443 pointing
// at a port nobody serves, nor tool hosts redirected to a dead proxy.
//
// Unlike Stop() this never touches the mitmEnabled setting — the user's choice
// survives a restart; only the process and the hosts lines go. Best effort: with
// no sudo password, killing a root process and rewriting /etc/hosts fail
// silently, exactly like the JS.
func Shutdown(p Paths, sudoPass string) {
	if b, err := os.ReadFile(p.PIDFile); err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid > 0 && pid != os.Getpid() && isAlive(pid) {
			// SIGTERM first: the server strips its own hosts entries on the way
			// out, so a graceful stop leaves /etc/hosts clean even if this
			// process cannot rewrite it.
			killProcesses(pid, false, sudoPass)
			deadline := time.Now().Add(1500 * time.Millisecond)
			for isAlive(pid) && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			if isAlive(pid) {
				killProcesses(pid, true, sudoPass)
			}
			os.Remove(p.PIDFile)
		}
	}
	RemoveAllDNS(sudoPass)
}

// Stop removes DNS entries for every tool, then terminates the server.
func Stop(p Paths, sudoPass string) error {
	runMu.Lock()
	stopSupervision()
	pid := 0
	if b, err := os.ReadFile(p.PIDFile); err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	runMu.Unlock()

	if pid > 0 && isAlive(pid) {
		killProcesses(pid, false, sudoPass)
		time.Sleep(time.Second)
		if isAlive(pid) {
			killProcesses(pid, true, sudoPass)
		}
	}

	RemoveAllDNS(sudoPass)
	os.Remove(p.PIDFile)
	os.Remove(p.LockFile)
	saveMitmSettings(false, "")
	return nil
}

func killLeftover(p Paths, sudoPass string) {
	if b, err := os.ReadFile(p.PIDFile); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 && isAlive(pid) {
			if pid != os.Getpid() {
				killProcesses(pid, true, sudoPass)
				time.Sleep(500 * time.Millisecond)
			}
		}
		os.Remove(p.PIDFile)
	}
}

func killProcesses(pid int, force bool, sudoPass string) {
	flag := "-TERM"
	if force {
		flag = "-KILL"
	}
	ExecPrivileged(fmt.Sprintf("kill %s %d 2>/dev/null || true", flag, pid), sudoPass)
}

// supervise watches the child and applies the 5-attempt restart policy.
func supervise(p Paths, cmd *exec.Cmd, done chan struct{}, stderr func() string, apiKey string) {
	err := cmd.Wait()
	close(done)
	runMu.Lock()
	childCmd, childDone = nil, nil
	os.Remove(p.PIDFile)
	os.Remove(p.LockFile)
	runMu.Unlock()

	if err == nil || isStopping() {
		return
	}
	if s := stderr(); strings.Contains(s, "incorrect password") || strings.Contains(s, "no password was provided") {
		SetCachedPassword("")
		return
	}

	restartMu.Lock()
	if time.Since(lastStart) >= restartWindowReset {
		restartCount = 0
	}
	if restartCount >= maxRestarts {
		restartMu.Unlock()
		return
	}
	attempt := restartCount
	delay := restartDelays[attempt]
	if attempt >= len(restartDelays) {
		delay = restartDelays[len(restartDelays)-1]
	}
	restartCount++
	restartMu.Unlock()

	time.Sleep(delay)
	if isStopping() {
		return
	}
	if getSettings != nil {
		if s, err := getSettings(); err == nil {
			if b, _ := s["mitmEnabled"].(bool); !b {
				return
			}
		}
	}
	if pwd := CachedPassword(); pwd != "" {
		Start(p, apiKey, pwd, false)
	}
}

func stopSupervision() {
	restartMu.Lock()
	stopping = true
	restartCount = 0
	restartMu.Unlock()
	child := childCmd
	if child != nil && child.Process != nil {
		child.Process.Signal(syscall.SIGTERM)
	}
	if childDone != nil {
		select {
		case <-childDone:
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

func isStopping() bool {
	restartMu.Lock()
	defer restartMu.Unlock()
	return stopping
}

// acquireLock claims the start lock; a stale lock (dead owner) is replaced.
func acquireLock(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprint(f, os.Getpid())
		f.Close()
		return true
	}
	b, rerr := os.ReadFile(path)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if rerr == nil && pid > 0 && isAlive(pid) {
		return false
	}
	os.Remove(path)
	return acquireLock(path)
}

// pollHealth waits for /_mitm_health on :443 to answer ok.
func pollHealth() bool {
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	deadline := time.Now().Add(healthPollTimeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get("https://127.0.0.1:443/_mitm_health")
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			var m map[string]any
			if json.Unmarshal(body, &m) == nil {
				if ok, _ := m["ok"].(bool); ok {
					return true
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// ---------------------------------------------------------------- DNS / trust

// EnableDNS adds the tool's hosts entries. The server must be running.
func EnableDNS(p Paths, tool, sudoPass string) error {
	if !GetStatus(p).Running {
		return errors.New("MITM server is not running. Start the server first.")
	}
	if err := AddDNS(tool, Password(sudoPass)); err != nil {
		return err
	}
	return saveDNSToolState(tool, true)
}

// DisableDNS removes the tool's hosts entries.
func DisableDNS(tool, sudoPass string) error {
	if err := RemoveDNS(tool, Password(sudoPass)); err != nil {
		return err
	}
	return saveDNSToolState(tool, false)
}

// TrustCertOnly installs the Root CA without starting the server.
func TrustCertOnly(p Paths, sudoPass string) error {
	if _, err := os.Stat(p.RootCert); err != nil {
		return errors.New("Root CA not found. Start server first to generate it.")
	}
	if !SudoAvailable() {
		return nil
	}
	pwd := Password(sudoPass)
	if pwd == "" && SudoPasswordRequired() && !IsRoot() {
		return errors.New("Sudo password required to trust certificate")
	}
	if _, err := TrustCert(p, pwd); err != nil {
		return err
	}
	if pwd != "" {
		SetCachedPassword(pwd)
	}
	return nil
}

// ---------------------------------------------------------------- settings glue

func saveMitmSettings(enabled bool, password string) error {
	if updateSettings == nil {
		return nil
	}
	updates := map[string]any{"mitmEnabled": enabled}
	if password != "" {
		updates["mitmSudoEncrypted"] = EncryptPassword(password)
	}
	_, err := updateSettings(updates)
	return err
}

func saveDNSToolState(tool string, enabled bool) error {
	if getSettings == nil || updateSettings == nil {
		return nil
	}
	cur, err := getSettings()
	if err != nil {
		return nil
	}
	next, _ := cur["dnsToolEnabled"].(map[string]any)
	if next == nil {
		next = map[string]any{}
	}
	next[tool] = enabled
	_, err = updateSettings(map[string]any{"dnsToolEnabled": next})
	return err
}

func updateSetting(key string, value any) {
	if updateSettings == nil {
		return
	}
	updateSettings(map[string]any{key: value})
}

// RestoreToolDNS re-applies hosts entries for tools enabled earlier.
func RestoreToolDNS(sudoPass string) {
	if getSettings == nil {
		return
	}
	s, err := getSettings()
	if err != nil {
		return
	}
	state, _ := s["dnsToolEnabled"].(map[string]any)
	pwd := Password(sudoPass)
	if pwd == "" {
		pwd = LoadEncryptedPassword()
	}
	for tool, v := range state {
		if on, _ := v.(bool); !on {
			continue
		}
		if _, ok := ToolHosts[tool]; !ok {
			continue
		}
		AddDNS(tool, pwd)
	}
}

// DataDir is the mitm dir's parent (the DIR env the child needs).
func (p Paths) DirBase() string { return filepath.Dir(p.Dir) }
