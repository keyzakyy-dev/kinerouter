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
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/keyzakyy-dev/kinerouter/internal/autostart"
	"github.com/keyzakyy-dev/kinerouter/internal/gateway"
)

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
		opts = append(opts, item{fmt.Sprintf("Update to v%s (current v%s)", latest, gateway.Version), "update"})
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
	subtitle := "Server: " + base
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
			runCLISetup(base, in)
		case "hide":
			if runtime.GOOS == "darwin" {
				// A detached child on macOS sits outside the login session and
				// NSStatusItem silently fails there, so this process stays and
				// only drops the terminal (9Router does the same).
				if _, ok := startTray(); !ok {
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

// runCLISetup runs this same binary's own `connect` command, so the CLI path
// never depends on PATH containing another kinerouter.
func runCLISetup(base string, in *bufio.Reader) {
	self, err := os.Executable()
	if err != nil {
		fmt.Printf("cannot locate this executable: %v\n", err)
		return
	}
	cmd := exec.Command(self, "connect", base)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("connect: %v\n", err)
	}
	pause(in)
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
