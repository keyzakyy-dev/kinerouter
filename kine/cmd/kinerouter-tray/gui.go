//go:build windows || tray

package main

import (
	_ "embed"
	"runtime"
	"time"

	"github.com/getlantern/systray"
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
	systray.SetTitle("KineRouter")
	systray.SetTooltip("KineRouter — " + baseURL)

	status := systray.AddMenuItem("Checking…", "Gateway status")
	status.Disable()
	systray.AddSeparator()
	start := systray.AddMenuItem("Start Gateway", "Run the gateway on "+baseURL)
	stop := systray.AddMenuItem("Stop Gateway", "Stop the gateway this tray started")
	systray.AddSeparator()
	dash := systray.AddMenuItem("Open Dashboard", baseURL)
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit Tray", "Remove the icon (leaves a running gateway alone)")

	// One select loop for every channel: systray delivers each click once, so a
	// second reader of the same ClickedCh would steal events at random.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	refresh := func() {
		st := probeState()
		mine := startedByTray()
		switch st {
		case stateRunning:
			status.SetTitle("Running — " + baseURL)
		case stateStopped:
			status.SetTitle("Stopped")
		default:
			status.SetTitle("Unknown — no answer on " + baseURL)
		}
		// Start is pointless when something already answers; Stop is offered only
		// for a gateway this tray started, never for one the user launched.
		setEnabled(start, st != stateRunning)
		setEnabled(stop, mine)
	}

	refresh()
	for {
		select {
		case <-tick.C:
			refresh()
		case <-start.ClickedCh:
			if err := startGateway(); err != nil {
				systray.SetTooltip("KineRouter — " + err.Error())
			}
			refresh()
		case <-stop.ClickedCh:
			if err := stopGateway(); err != nil {
				systray.SetTooltip("KineRouter — " + err.Error())
			}
			refresh()
		case <-dash.ClickedCh:
			_ = openBrowser(urlFor("/"))
		case <-quit.ClickedCh:
			// Releases the icon only. Rule C4: quitting the tray must not stop a
			// running gateway, which may be serving requests.
			systray.Quit()
			return
		}
	}
}

func setEnabled(item *systray.MenuItem, on bool) {
	if on {
		item.Enable()
		return
	}
	item.Disable()
}
