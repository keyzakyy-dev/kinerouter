//go:build windows || tray

package main

import (
	_ "embed"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/getlantern/systray"

	"github.com/keyzakyy-dev/kinerouter/internal/autostart"
)

// Windows takes an .ico; everywhere else systray decodes the PNG.
//
//go:embed icons/tray.ico
var trayICO []byte

//go:embed icons/tray.png
var trayPNG []byte

func trayIcon() []byte {
	if runtime.GOOS == "windows" {
		return trayICO
	}
	return trayPNG
}

// runGUI blocks for the life of the tray icon.
func runGUI() error {
	systray.Run(onReady, func() {})
	return nil
}

func onReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle("")
	systray.SetTooltip(fmt.Sprintf("KineRouter - Port %d", port))

	// Same four entries as 9Router's buildMenuItems: status, dashboard,
	// autostart toggle, quit.
	status := systray.AddMenuItem(fmt.Sprintf("KineRouter (Port %d)", port), "Server is running")
	status.Disable()
	dash := systray.AddMenuItem("Open Dashboard", "Open in browser")
	autostartItem := systray.AddMenuItem(autostartLabel(), "Run on OS startup")
	quit := systray.AddMenuItem("Quit", "Stop server and exit")

	// One select loop for every channel: systray delivers each click once, so a
	// second reader of the same ClickedCh would steal events at random.
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	refresh := func() {
		if autostart.Enabled() {
			autostartItem.SetTitle("✓ Auto-start Enabled")
		} else {
			autostartItem.SetTitle("Enable Auto-start")
		}
	}

	// The tray belongs to one gateway. If that gateway dies (crash, Ctrl+C in
	// the terminal it came from), the icon must not linger as a ghost.
	owner := 0
	if v := os.Getenv("KINEROUTER_OWNER_PID"); v != "" {
		owner, _ = strconv.Atoi(v)
	}

	refresh()
	for {
		select {
		case <-tick.C:
			if owner > 0 && !ownerAlive(owner) {
				systray.Quit()
				return
			}
			refresh()
		case <-dash.ClickedCh:
			_ = openBrowser(urlFor("/"))
		case <-autostartItem.ClickedCh:
			if autostart.Enabled() {
				autostart.Disable()
			} else if exe, err := gatewayPath(); err == nil {
				autostart.EnableInstall(exe)
			}
			refresh()
		case <-quit.ClickedCh:
			// 9Router's Quit stops the gateway it belongs to; a tray that only
			// removed its icon would leave an orphan server behind.
			stopOwner()
			systray.Quit()
			return
		}
	}
}

func autostartLabel() string {
	if autostart.Enabled() {
		return "✓ Auto-start Enabled"
	}
	return "Enable Auto-start"
}

// stopOwner asks the gateway that spawned this tray to shut down cleanly.
func stopOwner() {
	v := os.Getenv("KINEROUTER_OWNER_PID")
	pid, err := strconv.Atoi(v)
	if err != nil || pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Signal(syscall.SIGTERM)
	}
}
