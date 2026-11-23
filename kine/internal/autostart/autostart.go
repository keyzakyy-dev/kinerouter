// Package autostart writes the OS login entry that keeps the gateway running
// after a reboot, following 9Router's cli/src/cli/tray/autostart.js: one
// per-user entry that launches the binary in tray mode, no root, silent failure
// (autostart is optional and must never break the launcher).
package autostart

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	// Name is the command name, used for the entry's file name.
	Name = "kinerouter"
	// Label is the macOS launchd label.
	Label = "com.kinerouter.autostart"
)

// Enable writes the login entry for exe (this binary, launched with --tray).
func Enable(exe string) bool {
	if exe == "" {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		return enableMacOS(exe)
	case "windows":
		return enableWindows(exe)
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		return enableLinux(exe)
	}
	return false
}

// Disable removes the login entry.
func Disable() bool {
	switch runtime.GOOS {
	case "darwin":
		return disableMacOS()
	case "windows":
		return disableWindows()
	case "linux":
		return disableLinux()
	}
	return false
}

// Enabled reports whether the entry exists and, on macOS, is loaded.
func Enabled() bool {
	switch runtime.GOOS {
	case "darwin":
		return enabledMacOS()
	case "windows":
		return fileExists(windowsVBS())
	case "linux":
		return fileExists(filepath.Join(linuxDir(), Name+".desktop"))
	}
	return false
}

func linuxDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home(), ".config")
	}
	return filepath.Join(base, "autostart")
}

func enableLinux(exe string) bool {
	dir := linuxDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	body := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=KineRouter\n" +
		"Comment=KineRouter API Proxy\n" +
		"Exec=" + exe + " --tray\n" +
		"Hidden=false\n" +
		"NoDisplay=false\n" +
		"X-GNOME-Autostart-enabled=true\n"
	return os.WriteFile(filepath.Join(dir, Name+".desktop"), []byte(body), 0o644) == nil
}

func disableLinux() bool {
	err := os.Remove(filepath.Join(linuxDir(), Name+".desktop"))
	return err == nil || os.IsNotExist(err)
}

func windowsVBS() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", Name+".vbs")
}

func enableWindows(exe string) bool {
	p := windowsVBS()
	if p == "" {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false
	}
	// Hidden window, detached: the same shape 9Router writes for its Startup
	// .vbs, so no console flashes at login and closing it is not a thing.
	body := "Set WshShell = CreateObject(\"WScript.Shell\")\r\n" +
		"WshShell.Run \"\"\"" + exe + "\"\" --tray\", 0, False\r\n"
	return os.WriteFile(p, []byte(body), 0o644) == nil
}

func disableWindows() bool {
	p := windowsVBS()
	if p == "" {
		return false
	}
	err := os.Remove(p)
	return err == nil || os.IsNotExist(err)
}

func darwinPlist() string {
	return filepath.Join(home(), "Library", "LaunchAgents", Label+".plist")
}

func enableMacOS(exe string) bool {
	p := darwinPlist()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false
	}
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>` + Label + `</string>
    <key>ProgramArguments</key>
    <array>
        <string>` + exe + `</string>
        <string>--tray</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>/tmp/kinerouter.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/kinerouter.error.log</string>
</dict>
</plist>
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		return false
	}
	// Register now so the entry is live this session, not just after re-login.
	// Both calls are best-effort; a failed load still leaves the plist on disk.
	_ = exec.Command("launchctl", "unload", p).Run()
	_ = exec.Command("launchctl", "load", "-w", p).Run()
	return true
}

func disableMacOS() bool {
	p := darwinPlist()
	_ = exec.Command("launchctl", "unload", p).Run()
	err := os.Remove(p)
	return err == nil || os.IsNotExist(err)
}

func enabledMacOS() bool {
	if !fileExists(darwinPlist()) {
		return false
	}
	return exec.Command("launchctl", "list", Label).Run() == nil
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
