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
		ProviderSpecific   map[string]any `json:"providerSpecificData"`
		ConnectionProxy    *bool          `json:"connectionProxyEnabled"`
		ConnectionProxyURL string         `json:"connectionProxyUrl"`
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
	// Free / no-auth providers need no key; everything else does. This build also
	// allows providers whose credential lives in a local file (ollama-local).
	if apiKey == "" && !g.exec.IsNoAuth(provider) && authType != "none" && provider != "ollama-local" {
		writeErr(w, http.StatusBadRequest, "API Key is required")
		return
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
	if body.ConnectionProxy != nil || body.ConnectionProxyURL != "" {
		if specific == nil {
			specific = map[string]any{}
		}
		specific["connectionProxyEnabled"] = body.ConnectionProxy != nil && *body.ConnectionProxy
		specific["connectionProxyUrl"] = strings.TrimSpace(body.ConnectionProxyURL)
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
	conn := &store.Connection{
		Provider: provider,
		AuthType: "apikey",
		Name:     name,
		Priority: body.Priority,
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
		ProviderSpecific   map[string]any `json:"providerSpecificData"`
		ConnectionProxy    *bool          `json:"connectionProxyEnabled"`
		ConnectionProxyURL *string        `json:"connectionProxyUrl"`
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
	if body.ProviderSpecific != nil || body.ConnectionProxy != nil || body.ConnectionProxyURL != nil {
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
