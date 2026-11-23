//go:build !windows

package main

import "syscall"

// detachProcAttr puts the child in a new session with no controlling terminal,
// which is what keeps it alive after `kinerouter`'s terminal closes.
func detachProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
