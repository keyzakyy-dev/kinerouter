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
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

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
	for {
		if ownsServer {
			fmt.Printf("\nKineRouter %s is running at %s\n", gateway.Version, base)
		} else {
			fmt.Printf("\nKineRouter is already running at %s\n", base)
		}
		fmt.Println("  1) Web UI  — open the dashboard in a browser")
		fmt.Println("  2) CLI    — configure local coding tools")
		if ownsServer {
			fmt.Println("  3) Hide to tray — keep serving in the background")
			fmt.Println("  4) Continue without the menu")
		} else {
			fmt.Println("  4) Exit")
		}
		if hasUpdate {
			fmt.Printf("  5) Update to v%s (current v%s)\n", latest, gateway.Version)
		}
		fmt.Print("\nChoose [1-5]: ")

		line, err := in.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		switch strings.TrimSpace(line) {
		case "1":
			if err := openBrowser(base + "/dashboard"); err != nil {
				fmt.Printf("could not open a browser: %v\nopen %s manually\n", err, base+"/dashboard")
			}
			pause(in)
		case "2":
			runCLISetup(base, in)
		case "3":
			if !ownsServer {
				continue
			}
			if startTray() {
				ignoreHangup()
				fmt.Printf("\nTray started. Gateway: %s\nClose this terminal anytime; Ctrl+C stops the gateway.\n", base)
				return
			}
			pause(in)
		case "5":
			if !hasUpdate {
				continue
			}
			showUpdate(latest, ownsServer, in)
		default:
			return
		}
	}
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

// startTray launches the companion tray binary that sits next to this one.
func startTray() bool {
	self, err := os.Executable()
	if err != nil {
		fmt.Printf("tray: %v\n", err)
		return false
	}
	name := "kinerouter-tray"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(filepath.Dir(self), name)
	if _, err := os.Stat(bin); err != nil {
		fmt.Printf("tray: %s not found.\n", bin)
		fmt.Println("npm and release installs include it; from source, build it beside this binary:")
		fmt.Println("  go build -tags tray -o kinerouter-tray ./cmd/kinerouter-tray")
		return false
	}
	cmd := exec.Command(bin)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Printf("tray: %v\n", err)
		return false
	}
	if err := cmd.Process.Release(); err != nil {
		fmt.Printf("tray: %v\n", err)
		return false
	}
	return true
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
