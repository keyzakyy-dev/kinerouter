package main

// `kinerouter mitm-server` — the privileged half of the MITM feature. The
// unprivileged gateway spawns this same binary under sudo with DATA_DIR,
// ROUTER_API_KEY and MITM_ROUTER_BASE in the environment.

import (
	"fmt"
	"os"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
	"github.com/keyzakyy-dev/kinerouter/internal/mitm"
)

func runMitmServer() int {
	dataDir := config.DataDir()
	if err := mitm.Run(mitm.NewPaths(dataDir)); err != nil {
		fmt.Fprintln(os.Stderr, "mitm-server:", err)
		return 1
	}
	return 0
}
