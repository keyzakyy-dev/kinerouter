package main

import (
	"bufio"
	"fmt"
	"net"
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
)

// runLauncher is the interactive menu shown when `kinerouter` is started with
// no arguments from a terminal: serve the gateway, then let the user pick the
// web dashboard, the CLI setup, or the tray. A non-TTY start (Docker, systemd,
// piped output) returns immediately and behaves exactly as before.
func runLauncher(port int) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	if !waitPort(port, 3*time.Second) {
		return
	}

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("\nKineRouter is running at %s\n", base)
		fmt.Println("  1) Web UI  — open the dashboard in a browser")
		fmt.Println("  2) CLI    — configure local coding tools")
		fmt.Println("  3) Hide to tray — keep serving in the background")
		fmt.Println("  4) Continue without the menu")
		fmt.Print("\nChoose [1-4]: ")

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
			if startTray() {
				ignoreHangup()
				fmt.Printf("\nTray started. Gateway: %s\nClose this terminal anytime; Ctrl+C stops the gateway.\n", base)
				return
			}
			pause(in)
		default:
			return
		}
	}
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
		fmt.Println("The tray ships with Windows releases only; elsewhere build it beside this binary:")
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
