package gateway

// MiniMax Code desktop auth-lease lane: a connection can borrow the running
// desktop app's sign-in instead of holding its own OAuth token.
// Ported from src/app/api/oauth/[provider]/[action]/route.js (lease-status,
// lease-connect) and open-sse/services/minimaxCodeLease.js.
//
// Security: both routes are behind requireAuth. The client may only name a
// data dir (absolute) — the capability value and the token never leave the
// server; the connection row stores just the dir, never a credential.

import (
	"net/http"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/lease"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

var leaseProviders = map[string]bool{"minimax-code": true, "minimax-code-global": true}

func (g *Gateway) registerLeaseRoutes(mux *http.ServeMux) {
	for _, p := range []string{"minimax-code", "minimax-code-global"} {
		mux.HandleFunc("GET /api/oauth/"+p+"/lease-status", g.requireAuth(g.handleLeaseStatus))
		mux.HandleFunc("POST /api/oauth/"+p+"/lease-connect", g.requireAuth(g.handleLeaseConnect))
	}
}

func (g *Gateway) leaseDataDir(r *http.Request, bodyDir string) string {
	if s := strings.TrimSpace(bodyDir); s != "" {
		return lease.ResolveDataDir(s)
	}
	return lease.ResolveDataDir(r.URL.Query().Get("data_dir"))
}

func leaseProviderOf(r *http.Request) string {
	// /api/oauth/{provider}/lease-{status,connect}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		return ""
	}
	return parts[2]
}

func (g *Gateway) handleLeaseStatus(w http.ResponseWriter, r *http.Request) {
	provider := leaseProviderOf(r)
	if !leaseProviders[provider] {
		writeErr(w, http.StatusBadRequest, "lease-status only supported for minimax-code/minimax-code-global")
		return
	}
	dataDir := g.leaseDataDir(r, "")
	st, err := lease.RequestStatus(dataDir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"found": false, "dataDir": dataDir,
			"error": string(lease.ErrCode(err)), "message": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found": true, "dataDir": dataDir,
		"status": st.Status, "generation": st.Generation, "expiresAtMs": st.ExpiresAtMs,
	})
}

func (g *Gateway) handleLeaseConnect(w http.ResponseWriter, r *http.Request) {
	provider := leaseProviderOf(r)
	if !leaseProviders[provider] {
		writeErr(w, http.StatusBadRequest, "lease-connect only supported for minimax-code/minimax-code-global")
		return
	}
	var body struct {
		DataDir string `json:"dataDir"`
	}
	_ = decodeBody(r, &body)
	dataDir := g.leaseDataDir(r, body.DataDir)

	// Validate up front so the user sees the broker's exact failure reason
	// instead of a connection that can never work.
	if _, err := lease.RequestLease(dataDir, lease.MaxMinValidityMs); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": err.Error(), "code": string(lease.ErrCode(err)),
		})
		return
	}

	// Idempotent: one lease connection per (provider, data dir).
	conns, _ := g.store.ListConnections(provider, false)
	for _, c := range conns {
		if lease.IsLeaseAuth(c.AuthType) && lease.ResolveDataDir(c.SpecificStr("mcodeDataDir")) == dataDir {
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true, "reused": true,
				"connection": map[string]any{"id": c.ID, "provider": c.Provider},
			})
			return
		}
	}

	name := "MiniMax Desktop (Lease)"
	if provider == "minimax-code-global" {
		name = "MiniMax Desktop (Lease, Global)"
	}
	conn := &store.Connection{
		Provider: provider, AuthType: "mcode-lease", Name: name, IsActive: true,
		Data: map[string]any{
			"providerSpecificData": map[string]any{"mcodeDataDir": dataDir},
			"testStatus":           "active",
		},
	}
	if err := g.store.UpsertConnection(conn); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to save connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "connection": map[string]any{"id": conn.ID, "provider": conn.Provider},
	})
}
