package gateway

// Proxy-pool management. Ported from src/app/api/proxy-pools/route.js and
// /[id]/route.js: a pool is a shared outbound proxy an account can be bound to
// through providerSpecificData.proxyPoolId. The proxied call itself lives in
// executor (Credentials.ProxyURL), so Go's http.Transport does the tunnelling —
// no per-provider proxy code.
//
// Security: a pool's proxyUrl is user-supplied and is used as an outbound
// transport target, so it is checked with checkPublicURL at save time (scheme +
// netguard), the same guard the gitlab/import routes use.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

var validProxyTypes = map[string]bool{
	"http": true, "vercel": true, "cloudflare": true, "deno": true, "netlify": true,
}

func (g *Gateway) registerProxyPoolRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/proxy-pools", g.requireAuth(g.handleListProxyPools))
	mux.HandleFunc("POST /api/proxy-pools", g.requireAuth(g.handleCreateProxyPool))
	mux.HandleFunc("GET /api/proxy-pools/{id}", g.requireAuth(g.handleGetProxyPool))
	mux.HandleFunc("PUT /api/proxy-pools/{id}", g.requireAuth(g.handleUpdateProxyPool))
	mux.HandleFunc("DELETE /api/proxy-pools/{id}", g.requireAuth(g.handleDeleteProxyPool))
	mux.HandleFunc("POST /api/proxy-pools/vercel-deploy", g.requireAuth(g.handleVercelDeploy))
	mux.HandleFunc("POST /api/proxy-pools/cloudflare-deploy", g.requireAuth(g.handleCloudflareDeploy))
	mux.HandleFunc("POST /api/proxy-pools/deno-deploy", g.requireAuth(g.handleDenoDeploy))
	mux.HandleFunc("POST /api/proxy-pools/netlify-deploy", g.requireAuth(g.handleNetlifyDeploy))
}

func (g *Gateway) handleListProxyPools(w http.ResponseWriter, r *http.Request) {
	var filter *bool
	switch r.URL.Query().Get("isActive") {
	case "true":
		t := true
		filter = &t
	case "false":
		f := false
		filter = &f
	}
	pools, err := g.store.ListProxyPools(filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch proxy pools")
		return
	}
	if r.URL.Query().Get("includeUsage") != "true" {
		writeJSON(w, http.StatusOK, map[string]any{"proxyPools": pools})
		return
	}
	out := make([]map[string]any, 0, len(pools))
	for _, p := range pools {
		b, _ := p.MarshalJSON()
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["boundConnectionCount"] = g.store.CountConnectionsUsingPool(p.ID)
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxyPools": out})
}

func (g *Gateway) handleGetProxyPool(w http.ResponseWriter, r *http.Request) {
	p, err := g.store.GetProxyPool(r.PathValue("id"))
	if err != nil || p == nil {
		writeErr(w, http.StatusNotFound, "Proxy pool not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxyPool": p})
}

func (g *Gateway) handleCreateProxyPool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		ProxyURL    string `json:"proxyUrl"`
		NoProxy     string `json:"noProxy"`
		Type        string `json:"type"`
		IsActive    *bool  `json:"isActive"`
		StrictProxy bool   `json:"strictProxy"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	proxyURL := strings.TrimSpace(body.ProxyURL)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "Name is required")
		return
	}
	if proxyURL == "" {
		writeErr(w, http.StatusBadRequest, "Proxy URL is required")
		return
	}
	// A proxy URL may be a bare host:port without a scheme; default to http.
	if !strings.Contains(proxyURL, "://") {
		proxyURL = "http://" + proxyURL
	}
	if err := checkProxyPoolURL(proxyURL); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ptype := body.Type
	if !validProxyTypes[ptype] {
		ptype = "http"
	}
	active := body.IsActive == nil || *body.IsActive
	p := &store.ProxyPool{
		IsActive: active,
		Data: map[string]any{
			"name": name, "proxyUrl": proxyURL, "noProxy": strings.TrimSpace(body.NoProxy),
			"type": ptype, "strictProxy": body.StrictProxy, "lastTestedAt": nil, "lastError": nil,
		},
	}
	if err := g.store.SaveProxyPool(p); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to create proxy pool")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"proxyPool": p})
}

func (g *Gateway) handleUpdateProxyPool(w http.ResponseWriter, r *http.Request) {
	p, err := g.store.GetProxyPool(r.PathValue("id"))
	if err != nil || p == nil {
		writeErr(w, http.StatusNotFound, "Proxy pool not found")
		return
	}
	var body map[string]any
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if p.Data == nil {
		p.Data = map[string]any{}
	}
	if v, ok := body["name"]; ok {
		name := strings.TrimSpace(strAny(v))
		if name == "" {
			writeErr(w, http.StatusBadRequest, "Name is required")
			return
		}
		p.Data["name"] = name
	}
	if v, ok := body["proxyUrl"]; ok {
		u := strings.TrimSpace(strAny(v))
		if u == "" {
			writeErr(w, http.StatusBadRequest, "Proxy URL is required")
			return
		}
		if !strings.Contains(u, "://") {
			u = "http://" + u
		}
		if err := checkProxyPoolURL(u); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		p.Data["proxyUrl"] = u
	}
	if v, ok := body["noProxy"]; ok {
		p.Data["noProxy"] = strings.TrimSpace(strAny(v))
	}
	if v, ok := body["type"]; ok {
		ptype := strAny(v)
		if !validProxyTypes[ptype] {
			ptype = "http"
		}
		p.Data["type"] = ptype
	}
	if v, ok := body["isActive"]; ok {
		b, _ := v.(bool)
		p.IsActive = b
	}
	if v, ok := body["strictProxy"]; ok {
		b, _ := v.(bool)
		p.Data["strictProxy"] = b
	}
	if err := g.store.SaveProxyPool(p); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to update proxy pool")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxyPool": p})
}

func (g *Gateway) handleDeleteProxyPool(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if g.store.CountConnectionsUsingPool(id) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "Proxy pool is currently in use", "boundConnectionCount": g.store.CountConnectionsUsingPool(id),
		})
		return
	}
	ok, err := g.store.DeleteProxyPool(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to delete proxy pool")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "Proxy pool not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
