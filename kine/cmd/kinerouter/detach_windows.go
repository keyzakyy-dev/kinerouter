//go:build windows

package main

import "syscall"

// DETACHED_PROCESS (0x8) + CREATE_NEW_PROCESS_GROUP (0x200): no console to
// inherit and no Ctrl+C delivered from the parent's console.
const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

func detachProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}
