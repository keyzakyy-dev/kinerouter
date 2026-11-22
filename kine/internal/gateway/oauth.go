package gateway

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/keyzakyy-dev/kinerouter/internal/oauth"
)

// OAuth routes drive the browser login flow: /authorize hands back the
// upstream URL (the SPA opens it), /exchange swaps the callback code for a
// stored connection.
func (g *Gateway) registerOAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/oauth/providers", g.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"providers": g.oauth.Providers(),
			"device":    g.oauth.DeviceProviders(),
		})
	}))

	mux.HandleFunc("GET /api/oauth/{provider}/authorize", g.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		provider := r.PathValue("provider")
		if provider == "vertex" {
			writeErr(w, http.StatusBadRequest, "vertex uses a service-account JSON, not a browser flow")
			return
		}
		redirectURI := g.cfg.BaseURL + "/callback"
		if redirectURI == "/callback" {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			redirectURI = scheme + "://" + r.Host + "/callback"
		}
		meta := map[string]string{}
		for k, v := range r.URL.Query() {
			if k != "redirect_uri" && len(v) > 0 {
				meta[k] = v[0]
			}
		}
		sess, authURL, err := g.oauth.Start(provider, redirectURI, meta)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		out := map[string]any{
			"authorizeUrl": authURL, "state": sess.State, "redirectUri": redirectURI,
		}
		// gitlab needs the same instance/clientId back at exchange time.
		for k, v := range meta {
			out[k] = v
		}
		writeJSON(w, http.StatusOK, out)
	}))

	mux.HandleFunc("POST /api/oauth/{provider}/exchange", g.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code  string `json:"code"`
			State string `json:"state"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		conn, err := g.oauth.Exchange(r.Context(), g.store, body.State, body.Code)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, conn)
	}))

	// ---- device-code flow (grok-cli, kimi, muse, nous, github, minimax-code)

	mux.HandleFunc("POST /api/oauth/{provider}/device", g.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		out, err := g.oauth.DeviceStart(r.Context(), r.PathValue("provider"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}))

	mux.HandleFunc("GET /api/oauth/{provider}/poll", g.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state == "" {
			writeErr(w, http.StatusBadRequest, "Missing state")
			return
		}
		out, err := g.oauth.DevicePoll(r.Context(), g.store, state)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}))
}

var _ = oauth.Session{}
