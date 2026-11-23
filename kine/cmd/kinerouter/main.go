// Command kinerouter is a single-binary AI gateway: HTTP gateway, SQLite
// persistence and the embedded React dashboard.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
	"github.com/keyzakyy-dev/kinerouter/internal/gateway"
	"github.com/keyzakyy-dev/kinerouter/internal/logbuf"
	"github.com/keyzakyy-dev/kinerouter/internal/oauth"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/web"
)

func main() {
	log.SetFlags(log.Ltime)
	// Tee process logs into the ring the dashboard console viewer reads.
	log.SetOutput(io.MultiWriter(os.Stderr, logbuf.Default()))

	// `--tray` is the detached handoff target used by the launcher's "Hide to
	// tray": serve, show the icon, and never show the menu. It is removed
	// before subcommand dispatch so runCLI does not print its help for it.
	trayMode := false
	var cliArgs []string
	for _, a := range os.Args[1:] {
		if a == "--tray" {
			trayMode = true
			continue
		}
		cliArgs = append(cliArgs, a)
	}

	// CLI subcommands (connect / show) run instead of, and before, the server.
	if handled, code := runCLI(cliArgs); handled {
		os.Exit(code)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if trayMode {
		// The launcher spawns this copy and then stops, so wait for its
		// listener to close rather than attaching to a process that is exiting.
		if !waitPortFree(cfg.Port, 20*time.Second) {
			log.Fatalf("port %d is still in use; cannot take over", cfg.Port)
		}
	} else if v := runningVersion(cfg.Port); v != "" {
		// A second `kinerouter` must not die with "bind: address already in
		// use". If the port already serves KineRouter, attach the launcher to
		// it; anything else is a genuine conflict for ListenAndServe to report.
		log.Printf("KineRouter %s is already running on port %d", v, cfg.Port)
		runLauncher(cfg.Port, false)
		return
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close()

	static, err := web.Handler()
	if err != nil {
		log.Fatalf("web assets: %v", err)
	}

	gw := gateway.New(cfg, st, static)

	// OAuth refresh runs for the lifetime of the process (same 5-minute cadence
	// as KineRouter's backgroundTokenRefresh).
	refreshCtx, stopRefresh := context.WithCancel(context.Background())
	defer stopRefresh()
	oauth.NewManager().StartBackgroundRefresh(refreshCtx, st)
	// Daily models.dev catalog refresh (MODEL_CATALOG_SYNC=off disables).
	gw.StartCatalogSync()
	// Tunnel auto-resume (settings.tunnelEnabled / tailscaleEnabled) runs in the
	// background, like runHeavyStartup's deferred resume.
	gw.ResumeTunnels()
	addr := cfg.Host + ":" + strconv.Itoa(cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           gw.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: streaming responses stay open for the whole turn.
	}

	go func() {
		log.Printf("KineRouter %s listening on http://%s (data dir %s)", gateway.Version, addr, cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	if trayMode {
		// Give the icon to the detached process only: this one has no terminal
		// and no menu, so nothing else needs the tray.
		go func() {
			if !waitPort(cfg.Port, 15*time.Second) {
				log.Printf("tray: gateway did not become ready")
				return
			}
			if !startTray() {
				log.Printf("tray: companion binary not found next to %s", os.Args[0])
			}
		}()
		// No controlling terminal, but a stray HUP from a launcher wrapper must
		// not stop the gateway either.
		ignoreHangup()
	} else {
		// Interactive launcher: Web UI / CLI / tray. Skipped when stdin is not a
		// terminal, so Docker and systemd keep the old behaviour.
		go runLauncher(cfg.Port, true)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
