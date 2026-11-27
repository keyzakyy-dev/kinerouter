package gateway

// API-key connection CRUD. Ported from src/app/api/providers/route.js and
// src/app/api/providers/[id]/route.js, which own the connections
// table (/api/providers/* stays the registry view).
//
// Security:
//   - Credentials (apiKey, tokens) are never returned: sanitizeConn strips them,
//     matching toProviderConnectionResponse.
//   - Provider must be a known registry entry on create, so an arbitrary string
//     can never become an upstream host.
//   - This router owns the credential store, so storing the incoming provider
//     URL from providerSpecificData would be an open SSRF; unknown keys are
//     dropped and base URLs are ignored (transport stays registry-driven).

import (
	"net/http"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// sanitizeConn returns the connection with credential fields removed, matching
// toProviderConnectionResponse in src/lib/providerConnectionResponse.js.
func sanitizeConn(c *store.Connection) map[string]any {
	out := map[string]any{}
	for k, v := range c.Data {
		out[k] = v
	}
	for _, k := range []string{"apiKey", "accessToken", "refreshToken", "idToken", "sessionToken", "cookie"} {
		delete(out, k)
	}
	out["id"] = c.ID
	out["provider"] = c.Provider
	out["authType"] = c.AuthType
	out["isActive"] = c.IsActive
	out["createdAt"] = c.CreatedAt
	out["updatedAt"] = c.UpdatedAt
	if c.Name != "" {
		out["name"] = c.Name
	}
	if c.Email != "" {
		out["email"] = c.Email
	}
	if c.Priority != 0 {
		out["priority"] = c.Priority
	}
	return out
}

// pickSpecific keeps only the providerSpecificData keys the runtime reads, so
// a client cannot smuggle a baseUrl (SSRF) or any other override.
func pickSpecific(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	allow := map[string]bool{
		"connectionProxyEnabled": true, "connectionProxyUrl": true, "connectionNoProxy": true,
		"proxyPoolId": true, "defaultModel": true, "nodeName": true,
		// agy-acp: credential source, worker isolation and permission mode.
		"sourceConnection": true, "permissionMode": true, "agyPath": true, "accountRoot": true,
		// Credential shape the executors read (aws.go, quirks.go's azure lane,
		// systemone.go, usageadmin.go). None of these reach a URL by themselves:
		// azureEndpoint is re-validated by checkPublicURL below.
		"accountId": true, "workspaceId": true, "chatgptAccountId": true,
		"azureEndpoint": true, "apiVersion": true, "deployment": true,
		"region": true, "profile": true, "accessKeyId": true, "sessionToken": true,
	}
	out := map[string]any{}
	for k, v := range raw {
		if allow[k] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// checkProxy validates an optional connection-level proxy URL, and the Azure
// endpoint when one is supplied: quirks.go interpolates azureEndpoint straight
// into the upstream URL, so it needs the same public-address guard as a proxy.
func checkProxy(specific map[string]any) (string, bool) {
	if specific == nil {
		return "", true
	}
	if ep, _ := specific["azureEndpoint"].(string); strings.TrimSpace(ep) != "" {
		if err := checkPublicURL(strings.TrimSpace(ep)); err != nil {
			return "Invalid Azure endpoint: " + err.Error(), false
		}
	}
	enabled, _ := specific["connectionProxyEnabled"].(bool)
	url, _ := specific["connectionProxyUrl"].(string)
	url = strings.TrimSpace(url)
	if enabled && url == "" {
		return "Connection proxy URL is required when connection proxy is enabled", false
	}
	if url != "" {
		if err := checkPublicURL(url); err != nil {
			return err.Error(), false
		}
	}
	return "", true
}

// checkProxyPool mirrors normalizeProxyPoolId in the reference route: an empty
// id or "__none__" clears the binding, and an id no pool answers to is refused
// instead of being stored as a dangling reference the runtime cannot resolve.
func (g *Gateway) checkProxyPool(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	if id == "" || id == "__none__" {
		return "", true
	}
	pool, err := g.store.GetProxyPool(id)
	if err != nil || pool == nil {
		return "", false
	}
	return id, true
}

// apiKeyOptional reports whether the provider accepts a providerSpecificData
// field in place of an API key (registry apiKeyOptionalWith — bedrock's
// `profile`). Without it, following such a provider's own setup notice is
// answered with "API Key is required".
func apiKeyOptional(entry *providerdata.Entry, specific map[string]any) bool {
	if entry == nil || entry.APIKeyOptionalWith == "" || specific == nil {
		return false
	}
	v, _ := specific[entry.APIKeyOptionalWith].(string)
	return strings.TrimSpace(v) != ""
}

func (g *Gateway) registerConnectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/connections", g.requireAuth(g.handleListConnections))
	mux.HandleFunc("POST /api/connections", g.requireAuth(g.handleCreateConnection))
	mux.HandleFunc("GET /api/connections/{id}", g.requireAuth(g.handleGetConnection))
	mux.HandleFunc("PATCH /api/connections/{id}", g.requireAuth(g.handleUpdateConnection))
	mux.HandleFunc("PUT /api/connections/{id}", g.requireAuth(g.handleUpdateConnection))
	mux.HandleFunc("DELETE /api/connections/{id}", g.requireAuth(g.handleDeleteConnection))
}

func (g *Gateway) handleListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := g.store.ListConnections(r.URL.Query().Get("provider"), false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch connections")
		return
	}
	out := make([]map[string]any, 0, len(conns))
	for _, c := range conns {
		out = append(out, sanitizeConn(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func (g *Gateway) handleGetConnection(w http.ResponseWriter, r *http.Request) {
	c, err := g.store.GetConnection(r.PathValue("id"))
	if err != nil || c == nil {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": sanitizeConn(c)})
}

func (g *Gateway) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider           string         `json:"provider"`
		Name               string         `json:"name"`
		APIKey             string         `json:"apiKey"`
		Priority           int            `json:"priority"`
		IsActive           *bool          `json:"isActive"`
		DefaultModel       string         `json:"defaultModel"`
		ProxyPoolID        string         `json:"proxyPoolId"`
		ProviderSpecific   map[string]any `json:"providerSpecificData"`
		ConnectionProxy    *bool          `json:"connectionProxyEnabled"`
		ConnectionProxyURL string         `json:"connectionProxyUrl"`
		ConnectionNoProxy  string         `json:"connectionNoProxy"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	provider := g.reg.ResolveAlias(strings.TrimSpace(body.Provider))
	// A custom node id is a valid provider too; the node row supplies its name
	// and endpoint, so the connection never carries a client-chosen base URL.
	var node *store.ProviderNode
	if isNodeProvider(provider) {
		node, _ = g.store.GetProviderNode(provider)
	}
	if provider == "" || (!g.reg.IsKnownProvider(provider) && node == nil) {
		writeErr(w, http.StatusBadRequest, "Invalid provider")
		return
	}
	apiKey := strings.TrimSpace(body.APIKey)
	entry := g.reg.ByID[provider]
	authType := "none"
	if entry != nil {
		authType = entry.AuthType
	}
	specific := pickSpecific(body.ProviderSpecific)
	// The node owns prefix / apiType / baseUrl / nodeName; those keys are never
	// taken from the request body.
	if node != nil {
		if specific == nil {
			specific = map[string]any{}
		}
		for k, v := range nodeSpecific(node) {
			specific[k] = v
		}
	}
	// Free / no-auth providers need no key; everything else does. This build also
	// allows providers whose credential lives in a local file (ollama-local) and
	// those whose registry entry names a providerSpecificData field in place of a
	// key (bedrock's `profile`).
	if apiKey == "" && !apiKeyOptional(entry, specific) &&
		!g.exec.IsNoAuth(provider) && authType != "none" && provider != "ollama-local" {
		writeErr(w, http.StatusBadRequest, "API Key is required")
		return
	}
	if dm := strings.TrimSpace(body.DefaultModel); dm != "" {
		if specific == nil {
			specific = map[string]any{}
		}
		specific["defaultModel"] = dm
	}
	poolID, poolOK := g.checkProxyPool(body.ProxyPoolID)
	if !poolOK {
		writeErr(w, http.StatusBadRequest, "Proxy pool not found")
		return
	}
	if poolID != "" {
		if specific == nil {
			specific = map[string]any{}
		}
		specific["proxyPoolId"] = poolID
	}
	if body.ConnectionProxy != nil || body.ConnectionProxyURL != "" || body.ConnectionNoProxy != "" {
		if specific == nil {
			specific = map[string]any{}
		}
		if body.ConnectionProxy != nil {
			specific["connectionProxyEnabled"] = *body.ConnectionProxy
		}
		if body.ConnectionProxyURL != "" {
			specific["connectionProxyUrl"] = strings.TrimSpace(body.ConnectionProxyURL)
		}
		if body.ConnectionNoProxy != "" {
			specific["connectionNoProxy"] = strings.TrimSpace(body.ConnectionNoProxy)
		}
	}
	if msg, ok := checkProxy(specific); !ok {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" && entry != nil {
		name = entry.Display.Name
	}
	if name == "" && node != nil {
		name = node.Name
	}
	if name == "" {
		name = provider
	}
	// A name collision is refused rather than silently overwriting a key.
	existing, _ := g.store.ListConnections(provider, false)
	for _, e := range existing {
		if strings.EqualFold(e.Name, name) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "A connection with this name already exists", "existingId": e.ID,
			})
			return
		}
	}
	// The reference defaults a new account to priority 1 ("priority || 1"), so a
	// fresh connection lands at the end of the ordering instead of the front.
	priority := body.Priority
	if priority <= 0 {
		priority = 1
	}
	conn := &store.Connection{
		Provider: provider,
		AuthType: "apikey",
		Name:     name,
		Priority: priority,
		IsActive: body.IsActive == nil || *body.IsActive,
		Data:     map[string]any{"apiKey": apiKey},
	}
	if specific != nil {
		conn.Data["providerSpecificData"] = specific
	}
	if err := g.store.UpsertConnection(conn); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to save connection")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"connection": sanitizeConn(conn)})
}

func (g *Gateway) handleUpdateConnection(w http.ResponseWriter, r *http.Request) {
	cur, err := g.store.GetConnection(r.PathValue("id"))
	if err != nil || cur == nil {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return
	}
	var body struct {
		Name               *string        `json:"name"`
		APIKey             *string        `json:"apiKey"`
		Priority           *int           `json:"priority"`
		IsActive           *bool          `json:"isActive"`
		DefaultModel       *string        `json:"defaultModel"`
		ProxyPoolID        *string        `json:"proxyPoolId"`
		ProviderSpecific   map[string]any `json:"providerSpecificData"`
		ConnectionProxy    *bool          `json:"connectionProxyEnabled"`
		ConnectionProxyURL *string        `json:"connectionProxyUrl"`
		ConnectionNoProxy  *string        `json:"connectionNoProxy"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if cur.Data == nil {
		cur.Data = map[string]any{}
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		cur.Name = strings.TrimSpace(*body.Name)
	}
	if body.Priority != nil {
		cur.Priority = *body.Priority
	}
	if body.IsActive != nil {
		cur.IsActive = *body.IsActive
	}
	// Re-keying: empty string keeps the old key, "clear" removes it.
	if body.APIKey != nil {
		if k := strings.TrimSpace(*body.APIKey); k != "" && k != "clear" {
			cur.Data["apiKey"] = k
		} else if k == "clear" {
			delete(cur.Data, "apiKey")
		}
	}
	if body.ProviderSpecific != nil || body.ConnectionProxy != nil || body.ConnectionProxyURL != nil ||
		body.ConnectionNoProxy != nil || body.ProxyPoolID != nil || body.DefaultModel != nil {
		existing, _ := cur.Data["providerSpecificData"].(map[string]any)
		merged := map[string]any{}
		for k, v := range existing {
			merged[k] = v
		}
		for k, v := range pickSpecific(body.ProviderSpecific) {
			merged[k] = v
		}
		if body.ConnectionProxy != nil {
			merged["connectionProxyEnabled"] = *body.ConnectionProxy
		}
		if body.ConnectionProxyURL != nil {
			merged["connectionProxyUrl"] = strings.TrimSpace(*body.ConnectionProxyURL)
		}
		if body.ConnectionNoProxy != nil {
			merged["connectionNoProxy"] = strings.TrimSpace(*body.ConnectionNoProxy)
		}
		if body.DefaultModel != nil {
			merged["defaultModel"] = strings.TrimSpace(*body.DefaultModel)
		}
		if body.ProxyPoolID != nil {
			poolID, poolOK := g.checkProxyPool(*body.ProxyPoolID)
			if !poolOK {
				writeErr(w, http.StatusBadRequest, "Proxy pool not found")
				return
			}
			if poolID == "" {
				delete(merged, "proxyPoolId")
			} else {
				merged["proxyPoolId"] = poolID
			}
		}
		if msg, ok := checkProxy(merged); !ok {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		cur.Data["providerSpecificData"] = merged
	}
	if err := g.store.UpsertConnection(cur); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to update connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": sanitizeConn(cur)})
}

func (g *Gateway) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	ok, err := g.store.DeleteConnection(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to delete connection")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
