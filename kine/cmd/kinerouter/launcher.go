package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/keyzakyy-dev/kinerouter/internal/autostart"
	"github.com/keyzakyy-dev/kinerouter/internal/gateway"
	"github.com/keyzakyy-dev/kinerouter/internal/launcherpid"
)

// The tray outlives nothing: it belongs to the gateway that spawned it, so its
// PID is kept here for reuse (9Router inits one icon at menu start instead of
// creating a second one when the user hides to tray) and for cleanup on exit.
var (
	trayMu       sync.Mutex
	trayPID      int
	dataDirRef   string
	instancePort int
)

// ensureTray shows the menu-time icon once and reuses it afterwards.
func ensureTray() bool {
	trayMu.Lock()
	defer trayMu.Unlock()
	if trayPID > 0 && processAlive(trayPID) {
		return true
	}
	pid, ok := startTray()
	if !ok {
		return false
	}
	trayPID = pid
	recordInstance(pid)
	return true
}

// stopTray removes the icon before a handoff, so the detached copy never runs
// two trays (9Router's cleanup does the same).
func stopTray() {
	trayMu.Lock()
	pid := trayPID
	trayPID = 0
	trayMu.Unlock()
	if pid <= 0 || !processAlive(pid) {
		return
	}
	_ = signalTerm(pid)
	for i := 0; i < 30 && processAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

// recordInstance rewrites <data dir>/launcher.pid with the tray this gateway
// owns, so the next launch can stop both.
func recordInstance(pid int) {
	if dataDirRef == "" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	trayExe, _ := trayBinary()
	_ = launcherpid.Write(dataDirRef, launcherpid.Record{
		PID: os.Getpid(), Port: instancePort, Exe: self, TrayPID: pid, TrayExe: trayExe,
	})
}

// runLauncher is the interactive menu shown when `kinerouter` is started with
// no arguments from a terminal: serve the gateway, then let the user pick the
// web dashboard, the CLI setup, the tray, or an update. A non-TTY start
// (Docker, systemd, piped output) returns immediately and behaves exactly as
// before.
//
// ownsServer is false when another instance already holds the port; the menu
// still works against it, but offers no tray/continue options that would
// pretend this process owns the gateway.
func runLauncher(port int, ownsServer bool) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	if !waitPort(port, 3*time.Second) {
		return
	}

	// 9Router inits the tray alongside the menu, so the icon is already there
	// when the user picks Hide.
	if ownsServer {
		ensureTray()
	}

	// Update check runs once, in parallel with the menu being read. Its 4s
	// timeout is the only network wait here; an offline host just gets no item.
	latest, hasUpdate := gateway.UpdateInfo()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	in := bufio.NewReader(os.Stdin)

	// Same five entries, same order as 9Router's showInterfaceMenu: update (only
	// when one exists), Web UI, Terminal UI, Hide to Tray, Exit.
	type item struct{ label, action string }
	var opts []item
	if hasUpdate {
		opts = append(opts, item{fmt.Sprintf("Update to v%s (current: v%s)", latest, gateway.Version), "update"})
	}
	opts = append(opts, item{"Web UI (Open in Browser)", "web"})
	opts = append(opts, item{"Terminal UI (Interactive CLI)", "terminal"})
	if ownsServer {
		opts = append(opts, item{"Hide to Tray (Background)", "hide"})
	}
	opts = append(opts, item{"Exit", "exit"})

	labels := make([]string, len(opts))
	for i, o := range opts {
		labels[i] = o.label
	}
	// 9Router paints only the URL green inside the dim subtitle.
	subtitle := "🚀 Server: \x1b[32m" + base + "\x1b[0m"
	if !ownsServer {
		subtitle += " (started outside this terminal)"
	}

	for {
		n, err := chooseMenu("Choose Interface (v"+gateway.Version+")", subtitle, labels)
		if err != nil {
			return
		}
		if n < 0 {
			// Esc cancels the menu; 9Router treats that as Exit.
			n = len(opts) - 1
		}
		switch opts[n].action {
		case "update":
			showUpdate(latest, ownsServer, in)
		case "web":
			if err := openBrowser(base + "/dashboard"); err != nil {
				fmt.Printf("could not open a browser: %v\nopen %s manually\n", err, base+"/dashboard")
			}
			pause(in)
		case "terminal":
			runTUI(base, port, dataDirRef, in)
		case "hide":
			if runtime.GOOS == "darwin" {
				// A detached child on macOS sits outside the login session and
				// NSStatusItem silently fails there, so this process stays and
				// only drops the terminal (9Router does the same). The icon from
				// menu time is the one that stays.
				if !ensureTray() {
					pause(in)
					continue
				}
				enableAutostart()
				ignoreHangup()
				fmt.Printf("\nKineRouter is running in the tray (PID %d)\n  Server: %s\nClose this terminal anytime; quit from the tray icon.\n", os.Getpid(), base)
				return
			}
			// Linux/Windows: hand the gateway to a detached copy in a new
			// session, then stop this one so the child can claim the port. The
			// child starts its own tray, exactly like `9router --tray`.
			stopTray()
			fmt.Printf("\nStarting background process... (tray icon will appear shortly)\n")
			if !hideToTray(port) {
				pause(in)
				continue
			}
			enableAutostart()
			fmt.Printf("KineRouter is now running in the background\n  Server: %s\nClose this terminal anytime; quit from the tray icon.\n", base)
			stopSelf()
			return
		default:
			// Exit stops the gateway this process owns, like 9Router's cleanup():
			// leaving it running would hide a server the user just closed.
			if ownsServer {
				stopSelf()
			}
			return
		}
	}
}

// enableAutostart installs the OS login entry now that the user asked for the
// background mode; 9Router enables autostart at that same moment.
func enableAutostart() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	autostart.Enable(self)
}

// showUpdate prints the one update command that matches how this binary was
// installed, then exits so the installer can replace the running file. This is
// 9Router's flow: never update underneath a live process.
func showUpdate(latest string, ownsServer bool, in *bufio.Reader) {
	fmt.Printf("\nUpdate v%s → v%s\n\nRun after this exits:\n\n  %s\n\n", gateway.Version, latest, gateway.InstallCommand())
	if !ownsServer {
		fmt.Println("Another KineRouter owns the port; stop it first (pkill -x kinerouter).")
		pause(in)
		return
	}
	stopSelf()
}

// stopSelf asks the main goroutine to shut the server down cleanly. On Windows
// signal delivery to the own process fails, so fall back to an immediate exit.
func stopSelf() {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		if err := p.Signal(syscall.SIGTERM); err == nil {
			time.Sleep(time.Second)
			return
		}
	}
	os.Exit(0)
}

// runningVersion answers which KineRouter is on port, or "" when the port is
// free or held by something else. Used to attach to an existing instance
// instead of dying with "bind: address already in use".
func runningVersion(port int) string {
	client := &http.Client{Timeout: 600 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/version")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	return out.Version
}

// waitPort polls until the gateway accepts connections so the menu never opens
// on a URL that is not answering yet.
func waitPort(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func pause(in *bufio.Reader) {
	fmt.Print("\nPress Enter to return to the menu...")
	_, _ = in.ReadString('\n')
}

// hideToTray starts a detached copy of this binary in tray mode. That copy
// waits for this process to release the port, serves the gateway and starts the
// tray, so closing the terminal no longer touches either. Ported from 9Router's
// `spawn(..., { detached: true })` + `cleanup()` + `process.exit()` flow.
func hideToTray(port int) bool {
	self, err := os.Executable()
	if err != nil {
		fmt.Printf("background: %v\n", err)
		return false
	}
	cmd := exec.Command(self, "--tray")
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port))
	// nil stdio gives the child the null device; with a new session it has no
	// controlling terminal to receive SIGHUP from.
	cmd.SysProcAttr = detachProcAttr()
	if err := cmd.Start(); err != nil {
		fmt.Printf("background: %v\n", err)
		return false
	}
	return cmd.Process.Release() == nil
}

// waitPortFree blocks until nothing answers on the port, so a handoff child
// starts serving only after the parent's listener is closed.
func waitPortFree(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return true
		}
		conn.Close()
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// trayBinary resolves the companion tray binary that sits next to this one.
func trayBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := "kinerouter-tray"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(filepath.Dir(self), name)
	if _, err := os.Stat(bin); err != nil {
		return "", err
	}
	return bin, nil
}

// startTray launches the companion tray binary and returns its PID, so the
// instance record can stop it later (9Router's cleanup kills the tray too).
func startTray() (int, bool) {
	bin, err := trayBinary()
	if err != nil {
		fmt.Printf("tray: %v\n", err)
		fmt.Println("npm and release installs include it; from source, build it beside this binary:")
		fmt.Println("  go build -tags tray -o kinerouter-tray ./cmd/kinerouter-tray")
		return 0, false
	}
	cmd := exec.Command(bin)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	// The tray quits the gateway it belongs to, like 9Router's tray Quit. Only
	// the process that started it may be stopped, so the PID travels explicitly
	// rather than trusting PPID (which changes if the gateway detaches again).
	cmd.Env = append(os.Environ(), "KINEROUTER_OWNER_PID="+strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		fmt.Printf("tray: %v\n", err)
		return 0, false
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		fmt.Printf("tray: %v\n", err)
		return 0, false
	}
	return pid, true
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// ignoreHangup keeps the gateway alive after the launching terminal closes,
// which is what "hide to tray" promises.
func ignoreHangup() {
	signal.Ignore(syscall.SIGHUP)
}
