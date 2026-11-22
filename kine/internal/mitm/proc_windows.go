//go:build windows

package mitm

import "os"

// ponytail: Windows has no signal 0. Opening the process handle is the closest
// portable liveness probe; MITM itself answers 501 on Windows anyway.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
