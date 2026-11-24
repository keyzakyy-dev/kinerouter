// Command kinerouter is a single-binary AI gateway: HTTP gateway, SQLite
// persistence and the embedded React dashboard.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
	"github.com/keyzakyy-dev/kinerouter/internal/gateway"
	"github.com/keyzakyy-dev/kinerouter/internal/launcherpid"
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
	// tray": serve, show the icon, and never show the menu. `--skip-update`
	// suppresses the update check, which is what the login entry passes so a
	// background start never waits on the network. `-p/--port` and `-H/--host`
	// match KeyRouter's server options. All of them are removed before subcommand
	// dispatch so runCLI does not print its help for them.
	opts, cliArgs := parseServerFlags(os.Args[1:])
	trayMode, skipUpdate := opts.tray, opts.skipUpdate

	// CLI subcommands (connect / show) run instead of, and before, the server.
	if handled, code := runCLI(cliArgs); handled {
		os.Exit(code)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	// Flags win over the environment, like `-p` in KeyRouter's cli.js.
	if opts.port > 0 {
		cfg.Port = opts.port
	}
	if opts.host != "" {
		cfg.Host = opts.host
	}
	if trayMode {
		// The launcher spawns this copy and then stops, so wait for its
		// listener to close rather than stopping a process that is already
		// exiting.
		if !waitPortFree(cfg.Port, 20*time.Second) {
			log.Fatalf("port %d is still in use; cannot take over", cfg.Port)
		}
	} else if pid := launcherpid.Stop(cfg.DataDir, os.Getpid()); pid != 0 {
		// One instance per machine, like 9Router: the record names the exact
		// previous process, so a fresh launch replaces it instead of leaving
		// two servers that update on their own schedules.
		log.Printf("stopped previous instance (PID %d)", pid)
	} else if v := runningVersion(cfg.Port); v != "" {
		// No record means an older build (or a manually started copy) owns the
		// port; attaching to it beats dying with "bind: address already in use".
		log.Printf("KineRouter %s is already running on port %d", v, cfg.Port)
		runLauncher(cfg.Port, false, skipUpdate)
		return
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close()

	// Lets the next launch stop exactly this process (9Router's launcher.pid).
	dataDirRef = cfg.DataDir
	instancePort = cfg.Port
	self, _ := os.Executable()
	_ = launcherpid.Write(cfg.DataDir, launcherpid.Record{PID: os.Getpid(), Port: cfg.Port, Exe: self})
	defer launcherpid.RemoveIfOwner(cfg.DataDir, os.Getpid())
	defer stopTray()

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
		// KeyRouter prints the same warning: 0.0.0.0 is the default, so a user who
		// never thought about it is reachable from the whole LAN and should know.
		if cfg.Host == "0.0.0.0" || cfg.Host == "" {
			if ip := lanIP(); ip != "" {
				log.Printf("network-exposed: reachable at http://%s:%d — bind -H 127.0.0.1 for local-only", ip, cfg.Port)
			}
		}
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
			if !ensureTray() {
				log.Printf("tray: companion binary not found next to %s", os.Args[0])
			}
		}()
		// No controlling terminal, but a stray HUP from a launcher wrapper must
		// not stop the gateway either.
		ignoreHangup()
	} else {
		// Interactive launcher: Web UI / CLI / tray. Skipped when stdin is not a
		// terminal, so Docker and systemd keep the old behaviour.
		go runLauncher(cfg.Port, true, skipUpdate)
	}

	stop := make(chan os.Signal, 1)
	// SIGHUP is in the list for the same reason KeyRouter handles it: closing the
	// terminal that started the gateway sends one, and without cleanup the MITM
	// server on :443 and the tunnels outlive the gateway.
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	<-stop

	// Stop what this process spawned before the listener closes, in KeyRouter's
	// cleanup() order: tunnels and the privileged MITM proxy point at this port,
	// so leaving them up would advertise a URL that answers nothing. Best effort,
	// and neither writes a setting.
	gw.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// parseServerFlags pulls the launcher flags out of argv before CLI dispatch.
// `--tray` and `--skip-update` mirror KeyRouter's login entry; `-p/--port` and
// `-H/--host` mirror its server options, so an autostart line written by hand
// (or copied from KeyRouter) works instead of dying on "unknown flag".
//
// A subcommand wins: `kinerouter connect ... --password x` keeps its argv
// untouched, because connect has flags of its own.
func parseServerFlags(args []string) (opts serverOpts, rest []string) {
	if len(args) > 0 && isSubcommand(args[0]) {
		return opts, args
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--tray", "-t":
			opts.tray = true
		case "--skip-update":
			opts.skipUpdate = true
		case "--port", "-p":
			if i+1 < len(args) {
				i++
				opts.port = atoiOr(args[i], 0)
			}
		case "--host", "-H":
			if i+1 < len(args) {
				i++
				opts.host = args[i]
			}
		default:
			rest = append(rest, a)
		}
	}
	return opts, rest
}

type serverOpts struct {
	tray       bool
	skipUpdate bool
	port       int
	host       string
}

func isSubcommand(a string) bool {
	switch a {
	case "connect", "show", "mitm-server", "tray", "version", "help", "-v", "--version", "-h", "--help":
		return true
	}
	return false
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// lanIP returns the first non-internal IPv4 address, the address a remote peer
// actually reaches when the server binds 0.0.0.0. Empty when there is none
// (offline machine, container without a network), in which case no warning is
// printed rather than a wrong one.
func lanIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.To4() == nil {
			continue
		}
		return n.IP.String()
	}
	return ""
}
