//go:build !windows

package main

import "syscall"

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func signalTerm(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// signalKill is the escalation step when a tray ignores SIGTERM, so no ghost
// icon is left behind (KeyRouter's killTray escalates the same way).
func signalKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
