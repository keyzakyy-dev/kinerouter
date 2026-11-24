package main

import (
	"context"
	"fmt"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
	"github.com/keyzakyy-dev/kinerouter/internal/gateway"
	"github.com/keyzakyy-dev/kinerouter/internal/updater"
)

// runVersion prints this build and the newest release. The check is
// best-effort: an offline machine still learns its own version.
func runVersion() int {
	fmt.Printf("kinerouter %s\n", gateway.Version)
	if gateway.IsDevBuild() {
		return 0
	}
	latest, hasUpdate := gateway.UpdateInfo()
	if latest == "" {
		fmt.Println("Update check failed (offline?)")
		return 0
	}
	fmt.Printf("latest: %s\n", latest)
	if hasUpdate {
		fmt.Println("run 'kinerouter update' to install")
	} else {
		fmt.Println("up to date")
	}
	return 0
}

// runUpdate replaces this install using its own delivery channel. The server
// is not restarted from here: the caller's supervisor or launcher does that,
// and a CLI run may be updating a stopped install.
func runUpdate() int {
	if gateway.IsDevBuild() {
		fmt.Println("dev build: there is no release to move to")
		return 1
	}
	latest, hasUpdate := gateway.UpdateInfo()
	if latest == "" {
		fmt.Println("update check failed: cannot reach the release list")
		return 1
	}
	if !hasUpdate {
		fmt.Printf("kinerouter %s is already the latest\n", gateway.Version)
		return 0
	}
	method := gateway.InstallMethod()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	exe, err := updater.Apply(ctx, method, latest, config.DataDir())
	if err != nil {
		fmt.Printf("update failed: %v\n", err)
		return 1
	}
	fmt.Printf("updated %s -> %s (%s)\n", gateway.Version, latest, method)
	fmt.Printf("restart the server to run %s\n", exe)
	return 0
}
