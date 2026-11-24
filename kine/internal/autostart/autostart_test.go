package autostart

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxLoginEntry(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux entry")
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("DISPLAY", ":0")
	exe := home + "/kinerouter"
	if !Enable(exe) {
		t.Fatal("enable failed")
	}
	if !Enabled() {
		t.Fatal("not enabled after enable")
	}
	body, err := os.ReadFile(linuxDir() + "/kinerouter.desktop")
	if err != nil || string(body) == "" {
		t.Fatalf("entry unreadable: %v", err)
	}
	if !strings.Contains(string(body), "Exec="+exe+" --tray --skip-update\n") {
		t.Fatalf("unexpected Exec line:\n%s", body)
	}
	if !Disable() || Enabled() {
		t.Fatal("disable failed")
	}
}

// npm keeps the gateway in the data dir, so an entry pointing there survives
// `npm uninstall -g kinerouter` and boots the app at the next login. The entry
// must run the npm launcher (node + cli.js) instead; that path disappears with
// the package, which makes uninstall mean uninstall (npm v7+ runs no uninstall
// scripts, so there is nothing else to hook).
func TestInstallCommandPrefersNpmLauncher(t *testing.T) {
	t.Setenv("KINEROUTER_INSTALL", "")
	t.Setenv("KINEROUTER_NODE", "")
	t.Setenv("KINEROUTER_SHIM", "")
	self := "/home/u/.kinerouter/runtime/kinerouter_0.1.9_linux_amd64"
	got := InstallCommand(self)
	if got.Path != self || strings.Join(got.Args, " ") != "--tray --skip-update" {
		t.Fatalf("plain install: %#v", got)
	}

	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	shim := filepath.Join(dir, "cli.js")
	for _, p := range []string{node, shim} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("KINEROUTER_INSTALL", "npm")
	t.Setenv("KINEROUTER_NODE", node)
	t.Setenv("KINEROUTER_SHIM", shim)
	got = InstallCommand(self)
	if got.Path != node {
		t.Fatalf("npm install ran %q, want node", got.Path)
	}
	if strings.Join(got.Args, " ") != shim+" --tray --skip-update" {
		t.Fatalf("npm args: %#v", got.Args)
	}

	// Package already uninstalled: never write an entry for a missing script.
	if err := os.Remove(shim); err != nil {
		t.Fatal(err)
	}
	if got = InstallCommand(self); got.Path != "" {
		t.Fatalf("missing shim produced a command: %#v", got)
	}
	if EnableCommand(got) {
		t.Fatal("EnableCommand must refuse a command without a path")
	}

	// Older shim that does not export the paths: keep the feature working.
	t.Setenv("KINEROUTER_SHIM", "")
	if got = InstallCommand(self); got.Path != self {
		t.Fatalf("incomplete npm env fell back to %q", got.Path)
	}
}

func TestLinuxEntryQuotesPathsWithSpaces(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux entry")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DISPLAY", ":0")
	space := filepath.Join(dir, "My Node")
	if err := os.MkdirAll(space, 0o755); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(space, "node")
	shim := filepath.Join(space, "cli.js")
	for _, p := range []string{node, shim} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("KINEROUTER_INSTALL", "npm")
	t.Setenv("KINEROUTER_NODE", node)
	t.Setenv("KINEROUTER_SHIM", shim)
	if !EnableInstall("/home/u/.kinerouter/runtime/kinerouter_0.1.9_linux_amd64") {
		t.Fatal("enable failed")
	}
	body, err := os.ReadFile(linuxDir() + "/kinerouter.desktop")
	if err != nil {
		t.Fatal(err)
	}
	want := `Exec="` + node + `" "` + shim + `" --tray --skip-update`
	if !strings.Contains(string(body), want) {
		t.Fatalf("want %q in:\n%s", want, body)
	}
	if strings.Contains(string(body), "runtime/kinerouter_") {
		t.Fatalf("entry still points at the data-dir binary:\n%s", body)
	}
}

func TestDesktopExecEscaping(t *testing.T) {
	got := desktopExec(Command{Path: "/bin/a b", Args: []string{`/tmp/$x"y`, "--tray"}})
	if !strings.Contains(got, `"/bin/a b"`) {
		t.Fatalf("space path not quoted: %q", got)
	}
	if !strings.Contains(got, `"/tmp/\$x\"y"`) {
		t.Fatalf("reserved chars not escaped: %q", got)
	}
	if !strings.HasSuffix(got, " --tray") {
		t.Fatalf("plain arg was quoted: %q", got)
	}
}

func TestWindowsEntry(t *testing.T) {
	line := strings.ReplaceAll(vbsCommand(Command{Path: `C:\Program Files\node.exe`, Args: []string{`C:\Users\a b\cli.js`, "--tray", "--skip-update"}}), `"`, `""`)
	want := `""C:\Program Files\node.exe"" ""C:\Users\a b\cli.js"" --tray --skip-update`
	if line != want {
		t.Fatalf("vbs line %q, want %q", line, want)
	}
}

func TestXMLEscape(t *testing.T) {
	if got := xmlEscape(`/a&b<c>"d"`); got != "/a&amp;b&lt;c&gt;&quot;d&quot;" {
		t.Fatalf("xmlEscape: %q", got)
	}
}
