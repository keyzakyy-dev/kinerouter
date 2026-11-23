//go:build windows

package main

// Windows has no SIGHUP; closing the parent console does not signal the tray.
func ignoreHangup() {}
