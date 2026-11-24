package gateway

import (
	"net/http"

	"github.com/google/uuid"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/lease"
	"github.com/keyzakyy-dev/kinerouter/internal/providerusage"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// registerUsageAdminRoutes adds the per-connection quota + log feed routes the
// JS dashboard calls (src/app/api/usage/*).
func (g *Gateway) registerUsageAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/usage/history", g.requireAuth(g.handleUsageHistory))
	mux.HandleFunc("GET /api/usage/providers", g.requireAuth(g.handleUsageProviders))
	mux.HandleFunc("GET /api/usage/request-logs", g.requireAuth(g.handleUsageRequestLogs))
	mux.HandleFunc("GET /api/usage/{connectionId}", g.requireAuth(g.handleConnectionUsage))
	mux.HandleFunc("POST /api/usage/{connectionId}/claude-reset", g.requireAuth(g.handleClaudeReset))
	mux.HandleFunc("GET /api/usage/{connectionId}/codex-reset-credits", g.requireAuth(g.handleCodexResetCredits))
	mux.HandleFunc("POST /api/usage/{connectionId}/codex-reset-credits", g.requireAuth(g.handleCodexResetCreditsConsume))
}

// handleUsageHistory returns the all-time aggregate. The JS route returns the
// full getUsageStats shape; the simple shape below is what the Go SPA reads,
// and no dashboard component calls /api/usage/history directly (they use
// /api/usage/stats), so it stays an alias.
// ponytail: add byAccount/byApiKey/last10Minutes here only if a UI needs them.
func (g *Gateway) handleUsageHistory(w http.ResponseWriter, r *http.Request) {
	stats, err := g.store.UsageStats("all")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch usage stats")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleUsageProviders lists distinct providers seen in usage, named by
// providerNodes first, then the registry display name.
func (g *Gateway) handleUsageProviders(w http.ResponseWriter, r *http.Request) {
	ids, err := g.store.DistinctUsageProviders()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch providers")
		return
	}
	nodes, _ := g.store.ProviderNodes()
	providers := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		name := id
		if n := nodes[id]; n != "" {
			name = n
		} else if e := g.reg.ByID[id]; e != nil && e.Display.Name != "" {
			name = e.Display.Name
		}
		providers = append(providers, map[string]any{"id": id, "name": name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

// handleUsageRequestLogs returns a raw JSON array of pre-formatted log lines
// (RequestLogger.js reads the body as an array).
func (g *Gateway) handleUsageRequestLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := g.store.UsageLogLines(200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch logs")
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

// connectionUsageConn is the resolved credential set for a usage fetch.
func usageConn(c *store.Connection, force bool) providerusage.Conn {
	return providerusage.Conn{
		Provider:    c.Provider,
		AuthType:    c.AuthType,
		AccessToken: c.Str("accessToken"),
		APIKey:      c.Str("apiKey"),
		Specific:    c.Specific(),
		Force:       force,
	}
}

// refreshUsageConnection refreshes an OAuth connection when its token is near
// expiry and returns the re-read row.
func (g *Gateway) refreshUsageConnection(r *http.Request, c *store.Connection) *store.Connection {
	if c.AuthType != "oauth" {
		return c
	}
	if _, err := g.oauth.RefreshOne(r.Context(), g.store, c, time.Minute); err == nil {
		if fresh, err := g.store.GetConnection(c.ID); err == nil && fresh != nil {
			return fresh
		}
	}
	return c
}

// usageAllowed mirrors the JS gate: OAuth connections always, api-key
// connections only when the registry whitelists their provider.
func (g *Gateway) usageAllowed(c *store.Connection) bool {
	if c.AuthType == "oauth" || lease.IsLeaseAuth(c.AuthType) {
		return true
	}
	if c.AuthType == "apikey" || c.AuthType == "api_key" {
		return g.reg.IsKnownProvider(g.reg.ResolveAlias(c.Provider)) &&
			g.reg.ByID[g.reg.ResolveAlias(c.Provider)].UsageApikey()
	}
	return false
}

func (g *Gateway) handleConnectionUsage(w http.ResponseWriter, r *http.Request) {
	c, err := g.store.GetConnection(r.PathValue("connectionId"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if c == nil {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return
	}
	if demoQuota() {
		writeJSON(w, http.StatusOK, demoUsage(c))
		return
	}
	if !g.usageAllowed(c) {
		writeJSON(w, http.StatusOK, map[string]any{"message": "Usage not available for this connection"})
		return
	}
	force := r.URL.Query().Get("force") == "1"
	c = g.refreshUsageConnection(r, c)
	uc := usageConn(c, force)
	if lease.IsLeaseAuth(c.AuthType) {
		// Lease connections carry no stored token; borrow the desktop app's.
		if tok, lerr := lease.TokenFor(lease.ResolveDataDir(c.SpecificStr("mcodeDataDir"))); lerr == nil {
			uc.AccessToken = tok.AccessToken
		}
	}
	usage := providerusage.Fetch(r.Context(), uc)

	// A provider reported an expired token: force one refresh and retry once.
	if c.AuthType == "oauth" && c.Str("refreshToken") != "" && isAuthExpiredMessage(usage) {
		if _, err := g.oauth.RefreshOne(r.Context(), g.store, c, 24*time.Hour); err == nil {
			if fresh, err := g.store.GetConnection(c.ID); err == nil && fresh != nil {
				c = fresh
			}
		}
		usage = providerusage.Fetch(r.Context(), usageConn(c, force))
	}
	writeJSON(w, http.StatusOK, usage)
}

var authExpiredPatterns = []string{"expired", "authentication", "unauthorized", "401", "re-authorize"}

func isAuthExpiredMessage(v map[string]any) bool {
	msg, _ := v["message"].(string)
	msg = strings.ToLower(msg)
	if msg == "" {
		return false
	}
	for _, p := range authExpiredPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// handleClaudeReset spends one free Claude limit-reset grant. Irreversible.
func (g *Gateway) handleClaudeReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GrantID string `json:"grantId"`
	}
	_ = decodeBody(r, &body)
	c, err := g.store.GetConnection(r.PathValue("connectionId"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if c == nil {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return
	}
	if c.Provider != "claude" || c.AuthType != "oauth" {
		writeErr(w, http.StatusBadRequest, "Limit reset is only available for Claude OAuth connections.")
		return
	}
	c = g.refreshUsageConnection(r, c)
	e := g.reg.ByID[g.reg.ResolveAlias(c.Provider)]
	result, err := providerusage.ConsumeClaudeResetGrant(r.Context(),
		providerusage.Client(connProxyURL(c)), e, c.Str("accessToken"), body.GrantID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ok, _ := result["ok"].(bool); ok {
		writeJSON(w, http.StatusOK, result)
		return
	}
	status := http.StatusConflict
	if n, ok := result["status"].(int); ok && n >= 400 && n < 500 {
		status = n
	}
	if result["message"] == nil {
		result["message"] = "Reset not applied: " + firstNonEmpty(strAny(result["reason"]), strAny(result["result"]), "unknown")
	}
	writeJSON(w, status, result)
}

func (g *Gateway) codexConnection(w http.ResponseWriter, r *http.Request) *store.Connection {
	c, err := g.store.GetConnection(r.PathValue("connectionId"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil
	}
	if c == nil {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return nil
	}
	if c.Provider != "codex" {
		writeErr(w, http.StatusBadRequest, "Codex reset credits are only available for Codex connections.")
		return nil
	}
	if c.AuthType != "oauth" && c.AuthType != "access_token" {
		writeErr(w, http.StatusBadRequest, "Codex reset credits require an OAuth or access-token connection.")
		return nil
	}
	return c
}

func codexAccountID(c *store.Connection) string {
	for _, k := range []string{"workspaceId", "accountId", "chatgptAccountId"} {
		if s := c.SpecificStr(k); s != "" {
			return s
		}
	}
	return ""
}

func (g *Gateway) handleCodexResetCredits(w http.ResponseWriter, r *http.Request) {
	c := g.codexConnection(w, r)
	if c == nil {
		return
	}
	c = g.refreshUsageConnection(r, c)
	e := g.reg.ByID[g.reg.ResolveAlias(c.Provider)]
	client := providerusage.Client(connProxyURL(c))
	result, err := providerusage.CodexResetCredits(r.Context(), client, e,
		c.Str("accessToken"), codexAccountID(c))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleCodexResetCreditsConsume spends one credit. Irreversible — the redeem
// id is generated server-side so a client cannot replay a spent redemption.
func (g *Gateway) handleCodexResetCreditsConsume(w http.ResponseWriter, r *http.Request) {
	c := g.codexConnection(w, r)
	if c == nil {
		return
	}
	c = g.refreshUsageConnection(r, c)
	e := g.reg.ByID[g.reg.ResolveAlias(c.Provider)]
	client := providerusage.Client(connProxyURL(c))
	redeemID := newUUID()
	result := providerusage.ConsumeCodexResetCredit(r.Context(), client, e, c.Str("accessToken"), redeemID)

	if ok, _ := result["ok"].(bool); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"code":            result["code"],
			"reset":           true,
			"windows_reset":   result["windowsReset"],
			"redeemRequestId": redeemID,
			"credit":          result["credit"],
		})
		return
	}
	if noCredit, _ := result["noCredit"].(bool); noCredit {
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":          "no_credit",
			"reset":         false,
			"windows_reset": result["windowsReset"],
			"message":       "No Codex reset credits available.",
		})
		return
	}
	status := http.StatusBadGateway
	if n, ok := result["status"].(int); ok && n >= 400 && n < 500 {
		status = n
	}
	writeJSON(w, status, map[string]any{
		"code":          firstNonEmpty(strAny(result["code"]), "unknown_response"),
		"reset":         false,
		"windows_reset": result["windowsReset"],
		"message":       firstNonEmpty(strAny(result["message"]), "Codex reset credit consume returned an unexpected response."),
	})
}

// connProxyURL reads the connection-level proxy used for outbound quota calls.
func connProxyURL(c *store.Connection) string {
	if on, _ := c.Specific()["connectionProxyEnabled"].(bool); !on {
		return ""
	}
	return c.SpecificStr("connectionProxyUrl")
}

func newUUID() string { return uuid.NewString() }
