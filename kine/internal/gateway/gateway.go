// Package gateway is the HTTP surface: the OpenAI/Anthropic/Gemini-compatible
// /v1 API plus the dashboard management API. Route table mirrors
// src/app/api/** and the rewrites in next.config.mjs.
package gateway

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/auth"
	"github.com/keyzakyy-dev/kinerouter/internal/config"
	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/logbuf"
	"github.com/keyzakyy-dev/kinerouter/internal/mitm"
	"github.com/keyzakyy-dev/kinerouter/internal/oauth"
	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

type Gateway struct {
	cfg          *config.Config
	store        *store.Store
	reg          *providerdata.Registry
	exec         *executor.Executor
	pricing      *pricingTable
	limiter      *auth.Limiter
	oauth        *oauth.Manager
	machineID    string
	cliMachineID string
	maxBody      int64
	static       http.Handler
	tunnel       *tunnelManager
	px           pxpipeState // PXpipe library warm/install state

	proxyMu   sync.Mutex
	proxyNext map[string]int // per-provider cursor for rotateStrategy=round-robin

	// Background updater state (see autoupdate.go).
	updMu        sync.Mutex
	updUpdating  bool
	updLastCheck time.Time
}

func New(cfg *config.Config, st *store.Store, static http.Handler) *Gateway {
	return &Gateway{
		cfg:          cfg,
		store:        st,
		reg:          providerdata.Get(),
		exec:         executor.New(),
		pricing:      loadPricing(st),
		limiter:      auth.NewLimiter(),
		oauth:        oauth.NewManager(),
		machineID:    auth.LoadMachineID(cfg.DataDir, cfg.MachineIDSalt),
		cliMachineID: auth.LoadMachineID(cfg.DataDir, auth.CLIAuthSalt),
		maxBody:      128 << 20,
		static:       static,
		tunnel:       newTunnelManager(cfg.DataDir, st, cfg.Port),
		proxyNext:    map[string]int{},
	}
}

// tunnels returns the process-wide Cloudflare/Tailscale manager.
func (g *Gateway) tunnels() *tunnelManager { return g.tunnel }

// ResumeTunnels re-enables whatever was on when the process last stopped.
func (g *Gateway) ResumeTunnels() { g.tunnel.resume() }

// Shutdown releases what this process started outside itself: the Cloudflare /
// Tailscale tunnels and the privileged MITM proxy on :443. KeyRouter does the
// same in cleanup(), and for the same reason — both point at this port, so
// leaving them running advertises a URL that answers nothing, and the MITM
// server also leaves tool hosts redirected in /etc/hosts.
//
// Called on every exit path (signal, quit from the tray). Settings are not
// touched, so the user's tunnel/MITM choices survive a restart. Best effort:
// a failure here never blocks exit.
func (g *Gateway) Shutdown() {
	g.tunnel.Shutdown()
	mitm.Shutdown(g.mitmPaths(), mitm.LoadEncryptedPassword())
}

// Handler returns the root mux with API routes and SPA/static fallback.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	g.routes(mux)
	return g.withRequestLog(g.withPeerTrust(g.withCORS(mux)))
}

func (g *Gateway) routes(mux *http.ServeMux) {
	// ---- V1 compatibility API (rewrites happen in the mux patterns) ----
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { g.handleChat(w, r, "openai") })
	mux.HandleFunc("POST /api/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { g.handleChat(w, r, "openai") })
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) { g.handleChat(w, r, "claude") })
	mux.HandleFunc("POST /api/v1/messages", func(w http.ResponseWriter, r *http.Request) { g.handleChat(w, r, "claude") })
	mux.HandleFunc("POST /responses", func(w http.ResponseWriter, r *http.Request) { g.handleChat(w, r, "responses") })
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) { g.handleChat(w, r, "responses") })
	mux.HandleFunc("POST /api/v1/responses", func(w http.ResponseWriter, r *http.Request) { g.handleChat(w, r, "responses") })
	mux.HandleFunc("POST /v1/messages/count_tokens", g.handleCountTokens)
	mux.HandleFunc("POST /api/v1/messages/count_tokens", g.handleCountTokens)
	mux.HandleFunc("POST /v1/embeddings", g.handleEmbeddings)
	mux.HandleFunc("POST /api/v1/embeddings", g.handleEmbeddings)
	mux.HandleFunc("POST /v1/images/generations", g.handleImages)
	mux.HandleFunc("POST /api/v1/images/generations", g.handleImages)
	mux.HandleFunc("POST /v1/audio/speech", g.handleTTS)
	mux.HandleFunc("POST /api/v1/audio/speech", g.handleTTS)
	mux.HandleFunc("POST /v1/audio/transcriptions", g.handleTranscriptions)
	mux.HandleFunc("POST /api/v1/audio/transcriptions", g.handleTranscriptions)
	for _, prefix := range []string{"/v1", "/api/v1"} {
		mux.HandleFunc("POST "+prefix+"/search", g.handleSearch)
		mux.HandleFunc("POST "+prefix+"/systemone", g.handleSystemone)
		mux.HandleFunc("POST "+prefix+"/web/fetch", g.handleWebFetch)
		mux.HandleFunc("POST "+prefix+"/videos/{action}", func(w http.ResponseWriter, r *http.Request) {
			g.handleVideoCreate(w, r, r.PathValue("action"))
		})
		mux.HandleFunc("GET "+prefix+"/videos/{id}", g.handleVideoGet)
	}
	mux.HandleFunc("POST /codex/{path...}", func(w http.ResponseWriter, r *http.Request) { g.handleChat(w, r, "responses") })
	mux.HandleFunc("GET /v1/models", g.handleListModels)
	mux.HandleFunc("GET /api/v1/models", g.handleListModels)
	mux.HandleFunc("GET /v1/models/{path...}", g.handleModelsByPath)
	mux.HandleFunc("GET /api/v1/models/{path...}", g.handleModelsByPath)
	mux.HandleFunc("OPTIONS /v1/{path...}", corsPreflight)
	mux.HandleFunc("OPTIONS /api/v1/{path...}", corsPreflight)

	// ---- Dashboard API ----
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	mux.HandleFunc("POST /api/auth/login", g.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		auth.ClearSessionCookie(w)
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/auth/status", g.handleAuthStatus)
	mux.HandleFunc("GET /api/auth/oidc/start", g.handleOIDCStart)
	mux.HandleFunc("GET /api/auth/oidc/callback", g.handleOIDCCallback)
	mux.HandleFunc("POST /api/auth/oidc/test", g.handleOIDCTest)
	mux.HandleFunc("GET /api/auth/saml/start", g.handleSAMLStart)
	mux.HandleFunc("POST /api/auth/saml/acs", g.handleSAMLACS)
	mux.HandleFunc("GET /api/auth/saml/metadata", g.handleSAMLMetadata)
	mux.HandleFunc("POST /api/auth/saml/test", g.handleSAMLTest)

	mux.HandleFunc("GET /api/keys", g.requireAuth(g.handleListKeys))
	mux.HandleFunc("POST /api/keys", g.requireAuth(g.handleCreateKey))
	mux.HandleFunc("GET /api/keys/{id}", g.requireAuth(g.handleGetKey))
	mux.HandleFunc("PATCH /api/keys/{id}", g.requireAuth(g.handleUpdateKey))
	mux.HandleFunc("PUT /api/keys/{id}", g.requireAuth(g.handleUpdateKey))
	mux.HandleFunc("DELETE /api/keys/{id}", g.requireAuth(g.handleDeleteKey))

	mux.HandleFunc("GET /api/providers", g.requireAuth(g.handleListProviders))
	mux.HandleFunc("GET /api/providers/{id}", g.requireAuth(g.handleGetProvider))
	g.registerProvidersClientRoutes(mux)
	g.registerConnectionRoutes(mux)
	g.registerProviderNodeRoutes(mux)
	g.registerProviderAdminRoutes(mux)
	g.registerV1Extras(mux)
	g.registerTranslatorDebugRoutes(mux)
	g.registerProxyPoolRoutes(mux)
	g.registerCatalogRoutes(mux)
	g.registerCliToolRoutes(mux)
	g.registerMitmRoutes(mux)

	mux.HandleFunc("GET /api/combos", g.requireAuth(g.handleListCombos))
	mux.HandleFunc("POST /api/combos", g.requireAuth(g.handleCreateCombo))
	mux.HandleFunc("GET /api/combos/{id}", g.requireAuth(g.handleGetCombo))
	mux.HandleFunc("PUT /api/combos/{id}", g.requireAuth(g.handleUpdateCombo))
	mux.HandleFunc("DELETE /api/combos/{id}", g.requireAuth(g.handleDeleteCombo))

	mux.HandleFunc("GET /api/settings", g.requireAuth(g.handleGetSettings))
	mux.HandleFunc("PATCH /api/settings", g.requireAuth(g.handlePatchSettings))
	mux.HandleFunc("GET /api/settings/require-login", g.handleRequireLogin)

	mux.HandleFunc("GET /api/models", g.requireAuth(g.handleListModels))
	mux.HandleFunc("GET /api/models/alias", g.requireAuth(g.handleGetAliases))
	mux.HandleFunc("PUT /api/models/alias", g.requireAuth(g.handleSetAlias))
	mux.HandleFunc("DELETE /api/models/alias", g.requireAuth(g.handleDeleteAlias))

	mux.HandleFunc("GET /api/usage/stats", g.requireAuth(g.handleUsageStats))
	mux.HandleFunc("GET /api/usage/chart", g.requireAuth(g.handleUsageChart))
	mux.HandleFunc("GET /api/usage/logs", g.requireAuth(g.handleUsageLogs))
	mux.HandleFunc("GET /api/usage/request-details", g.requireAuth(g.handleRequestDetails))
	mux.HandleFunc("GET /api/usage/stream", g.requireAuth(g.handleUsageStream))

	g.registerUsageAdminRoutes(mux)
	g.registerAdRoutes(mux)
	g.registerJailbreakRoutes(mux)
	g.registerTerminalRoutes(mux)
	g.registerConsoleLogRoutes(mux)
	g.registerModelsAdminRoutes(mux)
	g.registerMiscAdminRoutes(mux)

	g.registerOAuthRoutes(mux)
	g.registerGeminiRoutes(mux)
	g.registerImportRoutes(mux)
	g.registerKiroSocialRoutes(mux)
	g.registerDesktopImportRoutes(mux)
	g.registerLeaseRoutes(mux)
	g.registerIflowCookieRoute(mux)
	g.registerMCPRoutes(mux)

	mux.HandleFunc("POST /api/tunnel/enable", g.requireAuth(g.handleTunnelEnable))
	mux.HandleFunc("POST /api/tunnel/disable", g.requireAuth(g.handleTunnelDisable))
	mux.HandleFunc("GET /api/tunnel/status", g.requireAuth(g.handleTunnelStatus))
	mux.HandleFunc("POST /api/tunnel/tailscale-enable", g.requireAuth(g.handleTailscaleEnable))
	mux.HandleFunc("POST /api/tunnel/tailscale-disable", g.requireAuth(g.handleTailscaleDisable))
	mux.HandleFunc("GET /api/tunnel/tailscale-check", g.requireAuth(g.handleTailscaleCheck))
	mux.HandleFunc("POST /api/tunnel/tailscale-install", g.requireAuth(g.handleTailscaleInstall))

	g.registerHeadroomRoutes(mux)
	g.registerPxpipeRoutes(mux)

	mux.HandleFunc("GET /api/version", g.handleVersion)

	// Unknown API paths must answer JSON, not the SPA shell: a fetch() that
	// receives index.html fails JSON.parse and hides the real status. Registered
	// after every concrete route so only unmatched ones land here.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "Not found")
	})
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "Not found")
	})

	// SPA + static assets.
	mux.Handle("/", g.static)
}

// ---------------------------------------------------------------- middleware

// withPeerTrust reproduces custom-server.js: the TCP peer address is
// authoritative and client-supplied forwarding headers are ignored unless the
// peer is loopback (a local reverse proxy).
func (g *Gateway) withPeerTrust(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !isLoopback(host) {
			r.Header.Del("X-Forwarded-For")
			r.Header.Del("X-Real-IP")
			r.Header.Del("X-Kine-Real-IP")
			// Proto/Host decide the session cookie's Secure flag and the OAuth
			// redirect_uri scheme. A remote client must not be able to send
			// "http" and get a session cookie that also travels in the clear.
			r.Header.Del("X-Forwarded-Proto")
			r.Header.Del("X-Forwarded-Host")
			r.Header.Del("X-Forwarded-Ssl")
			r.Header.Del("Front-End-Https")
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

func (g *Gateway) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions && (strings.HasPrefix(r.URL.Path, "/v1") || strings.HasPrefix(r.URL.Path, "/api/v1")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Gateway) isAuthenticated(r *http.Request) bool {
	if token := r.Header.Get("x-kine-cli-token"); token != "" && auth.ValidateCLIToken(token, g.cliMachineID) {
		return true
	}
	if _, ok := auth.Verify(g.cfg.JWTSecret, auth.SessionToken(r)); ok {
		return true
	}
	// Local, unproxied requests with no login requirement.
	return false
}

func (g *Gateway) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.isAuthenticated(r) {
			next(w, r)
			return
		}
		requireLogin := g.store.SettingBool("requireLogin", true)
		if !requireLogin && isLoopback(clientHost(r)) {
			next(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "Unauthorized")
	}
}

func clientHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": message, "type": errType(status),
	}})
}

func errType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusNotFound:
		return "not_found_error"
	}
	return "api_error"
}

func corsPreflight(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }

// Leveled log helpers: the console viewer shows the severity and tag the call
// site names, so a row never has its level guessed from the text. Every line
// still goes to stderr for `kinerouter logs` and a foreground CLI.
func logAt(level logbuf.Level, tag, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	prefix := level.String()[:3]
	prefix = strings.ToUpper(prefix)
	if tag != "" {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", prefix, tag, msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s %s\n", prefix, msg)
	}
	logbuf.Default().Emit(level, tag, "%s", msg)
}

func logf(format string, args ...any) { logAt(logbuf.Info, "", format, args...) }

func logInfo(tag, format string, args ...any) { logAt(logbuf.Info, tag, format, args...) }

func logWarn(tag, format string, args ...any) { logAt(logbuf.Warn, tag, format, args...) }

func logError(tag, format string, args ...any) { logAt(logbuf.Error, tag, format, args...) }

func logDebug(tag, format string, args ...any) { logAt(logbuf.Debug, tag, format, args...) }

// statusWriter remembers the status code so the request line can report it.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withRequestLog prints one row per finished request, the reference's
// "request POST /chat/completions status=200 duration=…" line. Streams are
// skipped: their duration is the whole turn and the line would arrive late.
func (g *Gateway) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/console-logs/stream") {
			next.ServeHTTP(w, r)
			return
		}
		// Static assets and the favicon would bury the request rows an
		// operator actually reads.
		if strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/favicon.ico" || r.URL.Path == "/favicon.svg" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		elapsed := time.Since(start)
		line := fmt.Sprintf("%s %s status=%d duration=%s", r.Method, r.URL.Path, status, elapsed)
		if status >= 400 {
			logWarn("request", "%s", line)
		} else {
			logInfo("request", "%s", line)
		}
	})
}

var _ = time.Now
