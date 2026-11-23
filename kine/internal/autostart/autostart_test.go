package autostart

import (
	"os"
	"runtime"
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
	if !Disable() || Enabled() {
		t.Fatal("disable failed")
	}
}
