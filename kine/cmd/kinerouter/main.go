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

	// CLI subcommands (connect / show) run instead of, and before, the server.
	if handled, code := runCLI(os.Args[1:]); handled {
		os.Exit(code)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
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

	// Interactive launcher: Web UI / CLI / tray. Skipped when stdin is not a
	// terminal, so Docker and systemd keep the old behaviour.
	go runLauncher(cfg.Port)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
