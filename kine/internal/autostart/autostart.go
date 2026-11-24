// Package autostart writes the OS login entry that keeps the gateway running
// after a reboot, following 9Router's cli/src/cli/tray/autostart.js: one
// per-user entry that launches the binary in tray mode, no root, silent failure
// (autostart is optional and must never break the launcher).
package autostart

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	// Name is the command name, used for the entry's file name.
	Name = "kinerouter"
	// Label is the macOS launchd label.
	Label = "com.kinerouter.autostart"
)

// Command is the argv written into the login entry.
type Command struct {
	Path string
	Args []string
}

// Enable writes the login entry for exe (this binary, launched with --tray).
func Enable(exe string) bool {
	return EnableCommand(Command{Path: exe, Args: LoginArgs})
}

// LoginArgs are the arguments every login entry runs, matching KeyRouter's
// autostart line: headless tray mode with the network update check disabled.
var LoginArgs = []string{"--tray", "--skip-update"}

// InstallCommand returns the argv the login entry should run for this install.
//
// An npm install points at the npm launcher (node + cli.js), never at the
// downloaded runtime binary. npm v7+ never runs uninstall scripts, so a runtime
// binary living under the data dir would keep starting the app after
// `npm uninstall -g kinerouter` — the launcher path disappears with the
// package, which is the only reliable way to make uninstall mean uninstall.
//
// Like KeyRouter's getCliJsPath, an entry is only returned once the launcher and
// the node binary exist on disk; a Command{} makes EnableCommand a no-op rather
// than writing an entry that can never run.
func InstallCommand(self string) Command {
	if os.Getenv("KINEROUTER_INSTALL") == "npm" {
		node, shim := os.Getenv("KINEROUTER_NODE"), os.Getenv("KINEROUTER_SHIM")
		if node != "" && shim != "" {
			if !fileExists(node) || !fileExists(shim) {
				return Command{}
			}
			return Command{Path: node, Args: append([]string{shim}, LoginArgs...)}
		}
	}
	return Command{Path: self, Args: append([]string(nil), LoginArgs...)}
}

// EnableInstall writes the login entry for the running installation.
func EnableInstall(self string) bool {
	return EnableCommand(InstallCommand(self))
}

// EnableCommand writes the login entry for cmd.
func EnableCommand(cmd Command) bool {
	if cmd.Path == "" {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		return enableMacOS(cmd)
	case "windows":
		return enableWindows(cmd)
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		return enableLinux(cmd)
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

// desktopExec renders argv per the Desktop Entry spec: quote only when needed
// and escape the characters that stay special inside double quotes.
func desktopExec(cmd Command) string {
	parts := append([]string{cmd.Path}, cmd.Args...)
	for i, p := range parts {
		parts[i] = quoteDesktopArg(p)
	}
	return strings.Join(parts, " ")
}

func quoteDesktopArg(s string) string {
	const reserved = " \t\n\"'\\><~|&;$*?#()`"
	if s != "" && !strings.ContainsAny(s, reserved) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '`', '$', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

func enableLinux(cmd Command) bool {
	dir := linuxDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	body := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=KineRouter\n" +
		"Comment=KineRouter API Proxy\n" +
		"Exec=" + desktopExec(cmd) + "\n" +
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

func enableWindows(cmd Command) bool {
	p := windowsVBS()
	if p == "" {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false
	}
	// Hidden window, detached: the same shape 9Router writes for its Startup
	// .vbs, so no console flashes at login and closing it is not a thing.
	// WScript.Shell takes one command line, so embedded quotes are doubled.
	line := strings.ReplaceAll(vbsCommand(cmd), `"`, `""`)
	body := "Set WshShell = CreateObject(\"WScript.Shell\")\r\n" +
		"WshShell.Run \"" + line + "\", 0, False\r\n"
	return os.WriteFile(p, []byte(body), 0o644) == nil
}

// vbsCommand quotes the program and any argument that contains whitespace.
func vbsCommand(cmd Command) string {
	args := append([]string{cmd.Path}, cmd.Args...)
	for i, a := range args {
		if i == 0 || strings.ContainsAny(a, " \t") {
			args[i] = `"` + a + `"`
		}
	}
	return strings.Join(args, " ")
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

func enableMacOS(cmd Command) bool {
	p := darwinPlist()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false
	}
	var args strings.Builder
	for _, a := range append([]string{cmd.Path}, cmd.Args...) {
		args.WriteString("        <string>" + xmlEscape(a) + "</string>\n")
	}
	// Same PATH KeyRouter pins into the agent: node's own dir first so child
	// processes spawned by the launcher (npm, providers) resolve at login,
	// where an interactive shell profile is not sourced.
	pathValue := filepath.Dir(cmd.Path) + ":/usr/local/bin:/usr/bin:/bin"
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>` + Label + `</string>
    <key>ProgramArguments</key>
    <array>
` + args.String() + `    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>` + xmlEscape(pathValue) + `</string>
    </dict>
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
	// When this process sits under the loaded agent, `launchctl unload` (sent by
	// the re-load below) would SIGTERM the menu handler's own process tree. The
	// plist is already updated on disk, so skip launchctl and let launchd pick
	// it up at next login — same guard as KeyRouter, but tree-aware: an npm
	// install runs node as the agent, with the gateway and tray below it.
	if underAgent() {
		return true
	}
	// Register now so the entry is live this session, not just after re-login.
	// Both calls are best-effort; a failed load still leaves the plist on disk.
	_ = exec.Command("launchctl", "unload", p).Run()
	_ = exec.Command("launchctl", "load", "-w", p).Run()
	return true
}

// underAgent reports whether the launchd agent for our label is this process or
// one of its ancestors.
func underAgent() bool {
	pid := agentPID()
	if pid == 0 {
		return false
	}
	if pid == os.Getpid() {
		return true
	}
	cur := os.Getppid()
	for i := 0; i < 8 && cur > 1; i++ {
		if cur == pid {
			return true
		}
		cur = parentPID(cur)
	}
	return false
}

func parentPID(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return ppid
}

// agentPID reports the PID launchd has recorded for our label, or 0 when the
// agent is not loaded (or launchd is unavailable).
func agentPID() int {
	out, err := exec.Command("launchctl", "list", Label).Output()
	if err != nil {
		return 0
	}
	m := agentPIDRe.FindSubmatch(out)
	if m == nil {
		return 0
	}
	pid, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0
	}
	return pid
}

var agentPIDRe = regexp.MustCompile(`"PID"\s*=\s*(\d+)`)

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return strings.ReplaceAll(s, "'", "&apos;")
}

func disableMacOS() bool {
	p := darwinPlist()
	// Don't kill ourselves: unloading the agent that owns this process tree
	// would SIGTERM the tray menu mid-click. Removing the plist is enough to
	// stop it from launching at next login.
	if !underAgent() {
		_ = exec.Command("launchctl", "unload", p).Run()
	}
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
