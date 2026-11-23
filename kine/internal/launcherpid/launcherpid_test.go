package launcherpid

import (
	"os"
	"testing"
)

func TestRecordRoundTripAndOwner(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Record{PID: 4242, Port: 20128, Exe: "/opt/bin/kinerouter", TrayPID: 4343, TrayExe: "/opt/bin/kinerouter-tray"}); err != nil {
		t.Fatal(err)
	}
	r := Read(dir)
	if r == nil || r.PID != 4242 || r.Exe != "kinerouter" || r.TrayExe != "kinerouter-tray" {
		t.Fatalf("bad record: %+v", r)
	}
	// A different owner must not delete the record; the owner must.
	RemoveIfOwner(dir, 1)
	if Read(dir) == nil {
		t.Fatal("record removed by non-owner")
	}
	RemoveIfOwner(dir, 4242)
	if _, err := os.Stat(dir + "/" + File); !os.IsNotExist(err) {
		t.Fatal("record kept by owner")
	}
}

func TestStopIgnoresStaleRecord(t *testing.T) {
	dir := t.TempDir()
	// PIDs this high are not in use; even if one were, the exe guard must fail.
	if err := Write(dir, Record{PID: 1 << 22, Port: 1, Exe: "definitely-not-running"}); err != nil {
		t.Fatal(err)
	}
	if pid := Stop(dir, os.Getpid()); pid != 0 {
		t.Fatalf("stopped stale record: %d", pid)
	}
}
