//go:build !windows

package main

import "syscall"

// ownerAlive reports whether the gateway that spawned this tray still runs.
func ownerAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
