// Package launcherpid records the instance that owns the port so the next
// launch stops exactly that process instead of scanning the machine, the same
// fast path 9Router keeps in <dataDir>/launcher.pid. A stale record can never
// kill an unrelated program: the PID must still resolve to the recorded
// executable name.
package launcherpid

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File is the record name inside the data directory.
const File = "launcher.pid"

type Record struct {
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Exe     string `json:"exe"`
	TrayPID int    `json:"tray,omitempty"`
	TrayExe string `json:"trayExe,omitempty"`
}

// Write stores this instance's record, replacing any previous one.
func Write(dataDir string, r Record) error {
	r.Exe = filepath.Base(r.Exe)
	if r.TrayExe != "" {
		r.TrayExe = filepath.Base(r.TrayExe)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, File)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Read returns the stored record, or nil when there is none or it is unusable.
func Read(dataDir string) *Record {
	body, err := os.ReadFile(filepath.Join(dataDir, File))
	if err != nil {
		return nil
	}
	var r Record
	if json.Unmarshal(body, &r) != nil || r.PID <= 0 || r.Exe == "" {
		return nil
	}
	return &r
}

// RemoveIfOwner deletes the record only when it still names pid, so a newer
// instance's record is never removed by a shutting-down older one.
func RemoveIfOwner(dataDir string, pid int) {
	if r := Read(dataDir); r != nil && r.PID == pid {
		_ = os.Remove(filepath.Join(dataDir, File))
	}
}

// Stop kills the recorded tray and gateway when each is still alive and still
// runs the recorded executable, tray first so the icon goes before the server.
// Returns the gateway PID it stopped, 0 when there was nothing to stop.
func Stop(dataDir string, selfPid int) int {
	r := Read(dataDir)
	if r == nil || r.PID == selfPid {
		return 0
	}
	if r.TrayPID > 0 && r.TrayPID != selfPid && alive(r.TrayPID) && matchesExe(r.TrayPID, r.TrayExe) {
		_ = kill(r.TrayPID)
	}
	if !alive(r.PID) || !matchesExe(r.PID, r.Exe) {
		return 0
	}
	if err := kill(r.PID); err != nil {
		return 0
	}
	// Wait for it to go, so the port is free before the caller binds it.
	deadline := time.Now().Add(3 * time.Second)
	for alive(r.PID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	return r.PID
}

func matchesExe(pid int, exe string) bool {
	got := exeName(pid)
	if got == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(filepath.Base(got), ".exe"), strings.TrimSuffix(exe, ".exe"))
}
