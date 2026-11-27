package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/auth"
	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// ---------------------------------------------------------------- auth

func (g *Gateway) handleLogin(w http.ResponseWriter, r *http.Request) {
	key := "login:" + g.clientIP(r)
	if wait, ok := g.limiter.Check(key); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "Too many failed attempts", "retryAfter": int(wait.Seconds()),
		})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	settings, _ := g.store.GetSettings()
	// A request arriving through an exposed tunnel must not log in when
	// tunnel dashboard access is switched off (JS isTunnelRequest).
	if isTunnelRequest(r, settings) && !g.store.SettingBool("tunnelDashboardAccess", true) {
		writeErr(w, http.StatusForbidden, "Dashboard access via tunnel is disabled")
		return
	}
	// SSO mode disables password login when its provider is configured; a
	// half-configured SSO setup must not lock the operator out, so the gate
	// only fires once isSamlConfigured/isOidcConfigured is true.
	if msg := g.ssoPasswordLoginBlocked(); msg != "" {
		writeErr(w, http.StatusForbidden, msg)
		return
	}
	storedHash, _ := settings["password"].(string)
	if !auth.VerifyPassword(storedHash, body.Password, g.cfg.InitialPassword) {
		g.limiter.Fail(key)
		writeErr(w, http.StatusUnauthorized, "Invalid password")
		return
	}
	// A fresh install still on the public default password, reached from a
	// non-loopback client, gets no session: issuing a JWT there would let any
	// remote attacker authenticate and PATCH /api/settings to disable auth
	// entirely (CVE-2026-56679 class). The password must be rotated from the
	// local machine or INITIAL_PASSWORD set before first launch — this is a
	// deliberate trade-off that leaves no remote self-service path, because
	// any credential issued before rotation re-opens the same chain.
	if storedHash == "" && g.cfg.InitialPassword == "" && !isLoopback(clientHost(r)) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"success":            false,
			"mustChangePassword": true,
			"error":              "Default password must be changed before remote access. Change it from the local machine (or set INITIAL_PASSWORD).",
		})
		return
	}
	g.limiter.Reset(key)
	token, err := auth.Sign(g.cfg.JWTSecret, 24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to sign session")
		return
	}
	auth.SetSessionCookie(w, r, token, g.cfg.AuthCookieSecure)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "success": true, "mustChangePassword": false})
}

// isTunnelRequest reports whether the request arrived via the configured
// Cloudflare or Tailscale tunnel host, mirroring the JS route.
func isTunnelRequest(r *http.Request, settings map[string]any) bool {
	host := strings.ToLower(clientHost(r))
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	match := func(key string) bool {
		u := strings.TrimSpace(str(settings, key))
		if u == "" {
			return false
		}
		parsed, err := url.Parse(u)
		return err == nil && strings.ToLower(parsed.Hostname()) == host && host != ""
	}
	return match("tunnelUrl") || match("tailscaleUrl")
}

// handleAuthStatus mirrors src/app/api/auth/status/route.js: the SSO fields
// the login page needs, the identity claims the OIDC/SAML session carries, plus
// requireApiKey/needsSetup which the dashboard SPA reads.
func (g *Gateway) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	settings, _ := g.store.GetSettings()
	claims, authed := auth.ParseClaims(g.cfg.JWTSecret, auth.SessionToken(r))
	if !authed {
		claims = map[string]any{}
	}
	_, oidcCfg := g.oidcSettings()
	mode := firstNonEmpty(str(settings, "authMode"), "password")
	ssoType := firstNonEmpty(str(settings, "ssoType"), "oidc")
	oidcName := strings.TrimSpace(str(claims, "oidcName"))
	oidcEmail := strings.TrimSpace(str(claims, "oidcEmail"))
	samlName := strings.TrimSpace(str(claims, "samlName"))
	samlEmail := strings.TrimSpace(str(claims, "samlEmail"))
	isSAML, _ := claims["saml"].(bool)
	isOIDC, _ := claims["oidc"].(bool)
	displayName := firstNonEmpty(samlName, samlEmail, oidcName, oidcEmail)
	if displayName == "" {
		switch {
		case isSAML:
			displayName = "SAML user"
		case isOIDC:
			displayName = "OIDC user"
		default:
			displayName = "Password user"
		}
	}
	loginMethod := "Password"
	if isSAML {
		loginMethod = "SAML"
	} else if isOIDC {
		loginMethod = "OIDC"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":  authed || g.isAuthenticated(r),
		"requireLogin":   g.store.SettingBool("requireLogin", true),
		"requireApiKey":  settings["requireApiKey"],
		"needsSetup":     str(settings, "password") == "",
		"authMode":       mode,
		"ssoType":        ssoType,
		"oidcConfigured": isOIDCConfigured(oidcCfg),
		"oidcLoginLabel": oidcCfg.loginLabel,
		"samlConfigured": isSAMLConfigured(settings),
		"samlLoginLabel": firstNonEmpty(strings.TrimSpace(str(settings, "samlLoginLabel")), samlDefaultLoginLbl),
		"hasPassword":    str(settings, "password") != "",
		"displayName":    displayName,
		"loginMethod":    loginMethod,
		"oidcName":       nulIfEmpty(oidcName),
		"oidcEmail":      nulIfEmpty(oidcEmail),
		"oidcLogin":      isOIDC,
		"samlName":       nulIfEmpty(samlName),
		"samlEmail":      nulIfEmpty(samlEmail),
		"samlLogin":      isSAML,
	})
}

func (g *Gateway) handleRequireLogin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"requireLogin": g.store.SettingBool("requireLogin", true)})
}

// ---------------------------------------------------------------- api keys

func (g *Gateway) handleListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := g.store.ListAPIKeys()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys, "machineId": g.machineID})
}

func (g *Gateway) handleGetKey(w http.ResponseWriter, r *http.Request) {
	k, err := g.store.GetAPIKeyByID(r.PathValue("id"))
	if err != nil || k == nil {
		writeErr(w, http.StatusNotFound, "Key not found")
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (g *Gateway) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	key, err := auth.GenerateAPIKey(g.machineID, g.cfg.APIKeySecret)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	k, err := g.store.CreateAPIKey(body.Name, g.machineID, key)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (g *Gateway) handleUpdateKey(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	k, err := g.store.UpdateAPIKey(r.PathValue("id"), patch)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if k == nil {
		writeErr(w, http.StatusNotFound, "Key not found")
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (g *Gateway) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	ok, err := g.store.DeleteAPIKey(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "Key not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------- providers

// providerView is the dashboard's provider summary: registry display metadata
// plus the connection count the UI shows.
type providerView struct {
	ID          string               `json:"id"`
	Alias       string               `json:"alias,omitempty"`
	Category    string               `json:"category"`
	AuthType    string               `json:"authType"`
	HasOAuth    bool                 `json:"hasOAuth"`
	Display     providerdata.Display `json:"display"`
	ModelsCount int                  `json:"modelsCount"`
	Connections int                  `json:"connections"`
	Active      int                  `json:"activeConnections"`
	// ServiceKinds is the providerMedia `serviceKinds` list the media-provider
	// pages filter on; default llm when the provider has no media entry.
	ServiceKinds []string `json:"serviceKinds"`
	// NoAuth marks a provider that needs no stored credential, so the picker
	// can offer its models without a connection (NO_AUTH_PROVIDER_IDS).
	NoAuth bool `json:"noAuth"`
	// Models is the providerModels table the model picker groups by provider.
	Models []providerdata.Model `json:"models"`
	// Priority is the registry sort key.
	Priority int `json:"priority"`
	// AuthModes lists every credential kind the provider accepts, so a card can
	// pick the right add-connection modal (dualAuthTypes in the reference).
	AuthModes []string `json:"authModes,omitempty"`
	// UIAlias is the "ag/model" short prefix the dashboard shows.
	UIAlias string `json:"uiAlias,omitempty"`
	// Deprecated marks a provider the dashboard warns about before adding a key.
	Deprecated bool `json:"deprecated,omitempty"`
	// Hidden keeps a media-only registry entry out of the providers grid.
	Hidden bool `json:"hidden,omitempty"`
}

// serviceKindsOf mirrors getProvidersByKind's `p.serviceKinds ?? ["llm"]`.
func serviceKindsOf(reg *providerdata.Registry, id string) []string {
	media := reg.MediaFor(id)
	if media == nil {
		return []string{"llm"}
	}
	arr, _ := media["serviceKinds"].([]any)
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{"llm"}
	}
	return out
}

func (g *Gateway) handleListProviders(w http.ResponseWriter, r *http.Request) {
	conns, err := g.store.ListConnections("", false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	counts := map[string][2]int{}
	for _, c := range conns {
		v := counts[c.Provider]
		v[0]++
		if c.IsActive {
			v[1]++
		}
		counts[c.Provider] = v
	}
	out := make([]providerView, 0, len(g.reg.Entries))
	for _, e := range g.reg.Entries {
		c := counts[e.ID]
		// The picker resolves `alias/model`, so the table must be keyed by alias
		// (falling back to the entry's own models when the table has none).
		models := g.reg.Models[e.Alias]
		if len(models) == 0 {
			models = e.Models
		}
		out = append(out, providerView{
			ID: e.ID, Alias: e.Alias, Category: e.Category, AuthType: e.AuthType,
			HasOAuth: e.OAuth != nil, Display: e.Display,
			ModelsCount: len(e.Models), Connections: c[0], Active: c[1],
			ServiceKinds: serviceKindsOf(g.reg, e.ID),
			NoAuth:       e.NoAuth,
			Models:       models,
			Priority:     e.Priority,
			AuthModes:    e.AuthModes,
			UIAlias:      e.UIAlias,
			Deprecated:   e.Display.Deprecated,
			Hidden:       e.Hidden,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Display.Name) < strings.ToLower(out[j].Display.Name)
	})
	// `providers` is the registry view this router's own pages use; `connections`
	// is what the ported dashboard pages read from the same URL. Both are served
	// so a verbatim page port needs no client-side change.
	connsOut := make([]map[string]any, 0, len(conns))
	for _, c := range conns {
		connsOut = append(connsOut, sanitizeConn(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out, "connections": connsOut})
}

func (g *Gateway) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	id := g.reg.ResolveAlias(r.PathValue("id"))
	// A custom compatible node is not in the registry: serve the node-shaped
	// detail the dashboard's node card edits (prefix, apiType, baseUrl).
	// Checked before the registry lookup — node ids never resolve to an entry.
	if n, _ := g.store.GetProviderNode(r.PathValue("id")); n != nil {
		conns, _ := g.store.ListConnections(n.ID, false)
		connsOut := make([]map[string]any, 0, len(conns))
		for _, c := range conns {
			connsOut = append(connsOut, sanitizeConn(c))
		}
		name := n.Name
		if name == "" {
			name = n.ID
		}
		// Same fallbacks as the reference dashboard — the node card needs a color
		// and text icon because a node has no registry display block.
		color, textIcon := "#10A37F", "OC"
		if n.Type == "anthropic-compatible" {
			color, textIcon = "#D97757", "AC"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": n.ID, "alias": n.ID, "category": "compatible", "authType": "apikey",
			"display": map[string]any{"name": name, "color": color, "textIcon": textIcon},
			"models":  []any{}, "connections": connsOut, "node": n,
		})
		return
	}
	entry, ok := g.reg.ByID[id]
	if !ok {
		writeErr(w, http.StatusNotFound, "Provider not found")
		return
	}
	// Connections are sanitized here: ListConnections returns the raw rows
	// (apiKey, accessToken, refreshToken, idToken, sessionToken, cookie) and
	// returning them would hand dashboard JS live upstream credentials.
	conns, _ := g.store.ListConnections(id, false)
	connsOut := make([]map[string]any, 0, len(conns))
	for _, c := range conns {
		connsOut = append(connsOut, sanitizeConn(c))
	}
	// modelMeta carries the fields the dashboard needs per model but that live
	// in server-side tables (thinking levels, capability badges), so the SPA
	// never has to duplicate them.
	// Entry-level models are optional; providerModels (keyed by alias) is the
	// table the picker groups by, so fall back to it rather than serving null.
	models := entry.Models
	if len(models) == 0 {
		models = g.reg.Models[entry.Alias]
	}
	if models == nil {
		models = []providerdata.Model{}
	}
	modelMeta := map[string]any{}
	for _, m := range models {
		c := providerdata.CapabilitiesFor(entry.ID, m.ID)
		modelMeta[m.ID] = map[string]any{
			"thinkingLevels": providerdata.ThinkingLevels(entry.ID, m.ID),
			"caps": map[string]any{
				"vision": c.Vision, "reasoning": c.Reasoning,
				"contextWindow": c.ContextWindow, "maxOutput": c.MaxOutput,
			},
		}
	}
	// providerMedia config for the media-provider detail page's info card
	// (searchConfig/fetchConfig/ttsConfig/…). The registry copy holds no
	// credentials — only endpoints, quotas and pricing — so it can be served.
	mediaOut := g.reg.MediaFor(entry.ID)
	if mediaOut == nil {
		mediaOut = map[string]any{}
	}
	mediaOut["id"] = entry.ID
	mediaOut["alias"] = entry.Alias
	mediaOut["category"] = entry.Category
	mediaOut["authType"] = entry.AuthType
	mediaOut["display"] = entry.Display
	mediaOut["models"] = models
	mediaOut["oauth"] = entry.OAuth
	mediaOut["transport"] = entry.Transport
	mediaOut["connections"] = connsOut
	mediaOut["uiAlias"] = entry.UIAlias
	mediaOut["authModes"] = entry.AuthModes
	mediaOut["authHint"] = entry.AuthHint
	mediaOut["noAuth"] = entry.NoAuth
	mediaOut["hasOAuth"] = entry.OAuth != nil
	mediaOut["modelMeta"] = modelMeta
	mediaOut["modelsFetcher"] = entry.ModelsFetcher
	mediaOut["credentialForm"] = entry.CredentialForm
	mediaOut["regions"] = entry.Regions
	mediaOut["defaultRegion"] = entry.DefaultRegion
	writeJSON(w, http.StatusOK, mediaOut)
}

// ---------------------------------------------------------------- combos

func (g *Gateway) handleListCombos(w http.ResponseWriter, r *http.Request) {
	combos, err := g.store.ListCombos()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"combos": combos})
}

func (g *Gateway) handleGetCombo(w http.ResponseWriter, r *http.Request) {
	c, err := g.store.GetCombo(r.PathValue("id"))
	if err != nil || c == nil {
		writeErr(w, http.StatusNotFound, "Combo not found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// comboNameRe mirrors VALID_NAME_REGEX in the reference combos route; combo
// names become API model ids, so they stay URL- and config-file safe.
var comboNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.\-]+$`)

func (g *Gateway) handleCreateCombo(w http.ResponseWriter, r *http.Request) {
	c := &store.Combo{}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(c); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	// The reference dashboard creates media combos with no members yet and
	// fills them on the detail page, so only the name is required.
	if c.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if !comboNameRe.MatchString(c.Name) {
		writeErr(w, http.StatusBadRequest, "Name can only contain letters, numbers, -, _ and .")
		return
	}
	if existing, err := g.store.GetComboByName(c.Name); err == nil && existing != nil {
		writeErr(w, http.StatusBadRequest, "Combo name already exists")
		return
	}
	saved, err := g.store.SaveCombo(c)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (g *Gateway) handleUpdateCombo(w http.ResponseWriter, r *http.Request) {
	cur, err := g.store.GetCombo(r.PathValue("id"))
	if err != nil || cur == nil {
		writeErr(w, http.StatusNotFound, "Combo not found")
		return
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(cur); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	saved, err := g.store.SaveCombo(cur)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	resetComboRotation(saved.Name)
	writeJSON(w, http.StatusOK, saved)
}

func (g *Gateway) handleDeleteCombo(w http.ResponseWriter, r *http.Request) {
	cur, _ := g.store.GetCombo(r.PathValue("id"))
	ok, err := g.store.DeleteCombo(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "Combo not found")
		return
	}
	if cur != nil {
		resetComboRotation(cur.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------- settings

func (g *Gateway) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := g.store.GetSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Never leak the password hash or the OIDC client secret to the UI; the
	// JS GET route does the same and reports only whether OIDC is configured.
	secret := str(settings, "oidcClientSecret")
	hasPassword := str(settings, "password") != ""
	delete(settings, "password")
	delete(settings, "oidcClientSecret")
	settings["oidcConfigured"] = str(settings, "oidcIssuerUrl") != "" &&
		str(settings, "oidcClientId") != "" && secret != ""
	settings["hasPassword"] = hasPassword
	applyEnvCapabilityFlags(settings)
	writeJSON(w, http.StatusOK, settings)
}

// applyEnvCapabilityFlags reports the deploy-time switches the reference
// settings route adds to its GET response (src/app/api/settings/route.js).
// They are environment-gated, not stored settings.
//
// The dashboard reads enableTranslator from GET /api/settings to decide whether
// the Translator debug page appears in the rail (src/components/layout.tsx), so
// a settings response without it leaves the ported /dashboard/translator
// unreachable no matter what the operator sets in the environment.
func applyEnvCapabilityFlags(settings map[string]any) {
	settings["enableRequestLogs"] = os.Getenv("ENABLE_REQUEST_LOGS") == "true"
	settings["enableTranslator"] = os.Getenv("ENABLE_TRANSLATOR") == "true"
}

// patchTouches reports whether the PATCH body carried any of the keys, which is
// how the reference decides to re-apply a derived side effect.
func patchTouches(patch map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := patch[k]; ok {
			return true
		}
	}
	return false
}

func (g *Gateway) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	// Secrets are never mass-assigned from the body (CWE-915), mirroring the
	// JS PROTECTED_SETTING_KEYS strip. A password change must go through
	// newPassword and prove knowledge of the current password.
	delete(patch, "password")
	cur, _ := g.store.GetSettings()
	if next, ok := patch["newPassword"].(string); ok && next != "" {
		curHash := str(cur, "password")
		if curHash != "" {
			curPW, _ := patch["currentPassword"].(string)
			if curPW == "" || !auth.VerifyPassword(curHash, curPW, "") {
				writeErr(w, http.StatusUnauthorized, "Invalid current password")
				return
			}
		} else if curPW, _ := patch["currentPassword"].(string); curPW != "" && curPW != auth.DEFAULT_PASSWORD {
			writeErr(w, http.StatusUnauthorized, "Invalid current password")
			return
		}
		hash, err := auth.HashPassword(next)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		patch["password"] = hash
	}
	delete(patch, "newPassword")
	delete(patch, "currentPassword")
	// An empty oidcClientSecret means "leave the stored one alone", like the JS.
	if v, ok := patch["oidcClientSecret"]; ok {
		if s, _ := v.(string); strings.TrimSpace(s) == "" {
			delete(patch, "oidcClientSecret")
		}
	}
	settings, err := g.store.UpdateSettings(patch)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Behavioural settings apply immediately, like the reference route does after
	// updateSettings: combo rotation state is dropped when a strategy key moves,
	// and the outbound proxy snapshot is rebuilt for the next upstream call.
	if patchTouches(patch, "comboStrategy", "comboStickyRoundRobinLimit", "comboStrategies") {
		resetComboRotation("")
	}
	if patchTouches(patch, "outboundProxyEnabled", "outboundProxyUrl", "outboundNoProxy") {
		g.refreshOutboundProxy()
	}
	secret := str(settings, "oidcClientSecret")
	hasPassword := str(settings, "password") != ""
	delete(settings, "password")
	delete(settings, "oidcClientSecret")
	settings["oidcConfigured"] = str(settings, "oidcIssuerUrl") != "" &&
		str(settings, "oidcClientId") != "" && secret != ""
	// The dashboard merges this response into its state, so it must carry the
	// same derived flags as GET or the password form loses its Current Password
	// field right after the user sets one.
	settings["hasPassword"] = hasPassword
	writeJSON(w, http.StatusOK, settings)
}

// ---------------------------------------------------------------- models

func (g *Gateway) handleListModels(w http.ResponseWriter, r *http.Request) {
	conns, _ := g.store.ListConnections("", false)
	have := map[string]bool{}
	for _, c := range conns {
		if c.IsActive {
			have[c.Provider] = true
		}
	}
	aliases, _ := g.store.GetModelAliases()
	combos, _ := g.store.ListCombos()

	disabled, _ := g.store.DisabledModels()
	disabledOf := func(alias, id string) bool {
		for _, d := range disabled[alias] {
			if d == id || d == "*" {
				return true
			}
		}
		return false
	}
	data := []map[string]any{}
	for _, e := range g.reg.Entries {
		if !have[e.ID] {
			continue
		}
		for _, m := range e.Models {
			if m.Kind != "" && m.Kind != "llm" {
				continue
			}
			if disabledOf(e.Alias, m.ID) || disabledOf(e.ID, m.ID) {
				continue
			}
			c := providerdata.CapabilitiesFor(e.ID, m.ID)
			data = append(data, map[string]any{
				"id": e.ID + "/" + m.ID, "object": "model", "owned_by": e.ID,
				// caps rides along for the composer's capability badges; the
				// OpenAI list shape ignores it (mirrors /api/models).
				"caps": map[string]any{
					"vision": c.Vision, "reasoning": c.Reasoning,
					"contextWindow": c.ContextWindow, "maxOutput": c.MaxOutput,
				},
			})
		}
	}
	for alias := range aliases {
		data = append(data, map[string]any{"id": alias, "object": "model", "owned_by": "alias"})
	}
	for _, c := range combos {
		data = append(data, map[string]any{"id": c.Name, "object": "model", "owned_by": "combo"})
	}
	data = g.appendCustomModels(data, have, disabledOf)
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (g *Gateway) handleGetAliases(w http.ResponseWriter, r *http.Request) {
	aliases, err := g.store.GetModelAliases()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"aliases": aliases})
}

func (g *Gateway) handleSetAlias(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alias  string `json:"alias"`
		Model  string `json:"model"`
		Target string `json:"target"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	if body.Alias == "" {
		writeErr(w, http.StatusBadRequest, "alias is required")
		return
	}
	target := body.Model
	if target == "" {
		target = body.Target
	}
	if err := g.store.SetModelAlias(body.Alias, target); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (g *Gateway) handleDeleteAlias(w http.ResponseWriter, r *http.Request) {
	alias := r.URL.Query().Get("alias")
	if alias == "" {
		var body struct {
			Alias string `json:"alias"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		alias = body.Alias
	}
	if _, err := g.store.DeleteKV(store.ScopeModelAliases, alias); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------- usage

func (g *Gateway) handleUsageStats(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "today"
	}
	stats, err := g.store.UsageStats(period)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (g *Gateway) handleUsageChart(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "7d"
	}
	chart, err := g.store.UsageChart(period)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chart)
}

func (g *Gateway) handleUsageLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := g.store.RecentUsage(200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": rows})
}

func (g *Gateway) handleRequestDetails(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	pageSize := atoiDefault(q.Get("pageSize"), 25)
	details, err := g.store.RequestDetails(page, pageSize,
		q.Get("provider"), q.Get("model"), q.Get("connectionId"), q.Get("status"),
		q.Get("startDate"), q.Get("endDate"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Redact conversation payloads: the stored details hold full request bodies
	// (user prompts, tool calls) and provider responses. Returning them lets any
	// dashboard-authenticated user read every user's conversation history, so
	// only the metadata (model, tokens, latency, status) leaves the server.
	if list, ok := details["details"].([]any); ok {
		for _, d := range list {
			if rec, ok := d.(map[string]any); ok {
				for _, key := range []string{"request", "providerRequest", "providerResponse", "response"} {
					if _, has := rec[key]; has {
						rec[key] = map[string]any{"redacted": true}
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, details)
}

// handleUsageStream is the EventSource feed the dashboard subscribes to.
func (g *Gateway) handleUsageStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsubscribe := g.store.SubscribeUsage()
	defer unsubscribe()

	send := func() bool {
		// Only the live fields ride the stream; the full stats payload costs a
		// period-wide scan per notify and the client refetches it on demand.
		stats, err := g.store.LiveUsage()
		if err != nil {
			return true
		}
		b, _ := json.Marshal(stats)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send() {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if !send() {
				return
			}
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

var _ = translate.FormatOpenAI

// handleCountTokens answers POST /v1/messages/count_tokens with the same
// character-based estimate the reference uses (no tokenizer call).
func (g *Gateway) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	chars := countValueChars(body["system"]) + countValueChars(body["tools"])
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			chars += countMessageChars(m)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"input_tokens": (chars + 3) / 4})
}

func countValueChars(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case string:
		return len(x)
	case float64, bool:
		return len(fmt.Sprint(x))
	case []any:
		n := 0
		for _, item := range x {
			n += countValueChars(item)
		}
		return n
	case map[string]any:
		n := 0
		for k, item := range x {
			n += len(k) + countValueChars(item)
		}
		return n
	}
	return 0
}

func countMessageChars(m any) int {
	msg, _ := m.(map[string]any)
	if msg == nil {
		return countValueChars(m)
	}
	content := msg["content"]
	switch c := content.(type) {
	case string:
		return len(c)
	case []any:
		n := 0
		for _, block := range c {
			n += countContentBlockChars(block)
		}
		return n
	}
	return countValueChars(content)
}

func countContentBlockChars(block any) int {
	bm, ok := block.(map[string]any)
	if !ok {
		return countValueChars(block)
	}
	switch bm["type"] {
	case "text":
		return countValueChars(bm["text"])
	case "tool_use":
		return countValueChars(bm["name"]) + countValueChars(bm["input"])
	case "tool_result":
		return countValueChars(bm["content"])
	case "thinking":
		return countValueChars(bm["thinking"])
	}
	return countValueChars(bm)
}
