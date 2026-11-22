package gateway

// Small dashboard routes that do not belong to a larger feature group:
// pricing CRUD, the Ollama model catalog for /api/tags, combo presets,
// process shutdown/update, password reset, locale, health-init, whole-database
// export/import, the proxy reachability probe and the per-pool test.
//
// Security notes:
//   - /api/shutdown, /api/version/{update,shutdown}, /api/auth/reset-password
//     and /api/settings/database are destructive or credential-bearing. Each is
//     behind requireAuth (session JWT or the local CLI token) and, where the JS
//     also demands it, a password re-check or SHUTDOWN_SECRET.
//   - /api/settings/proxy-test and /api/proxy-pools/{id}/test make the server
//     dial a client-supplied URL through a client-supplied proxy. The proxy is
//     resolved from the stored pool for the pool route; on the settings route
//     the URL is user-supplied by design but restricted to http(s) and a private
//     target is refused unless it is loopback-free (netguard).

import (
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/auth"
	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

func (g *Gateway) registerMiscAdminRoutes(mux *http.ServeMux) {
	r := g.requireAuth
	mux.HandleFunc("GET /api/pricing", r(g.handleGetPricing))
	mux.HandleFunc("PATCH /api/pricing", r(g.handlePatchPricing))
	mux.HandleFunc("DELETE /api/pricing", r(g.handleResetPricing))
	mux.HandleFunc("GET /api/tags", g.handleOllamaTags)
	mux.HandleFunc("OPTIONS /api/tags", corsPreflight)
	mux.HandleFunc("GET /api/combos/presets", r(g.handleComboPresetsPreview))
	mux.HandleFunc("POST /api/combos/presets", r(g.handleComboPresetsCreate))
	mux.HandleFunc("POST /api/shutdown", r(g.handleShutdown))
	mux.HandleFunc("POST /api/version/update", r(g.handleVersionUpdate))
	mux.HandleFunc("POST /api/version/shutdown", r(g.handleVersionShutdown))
	mux.HandleFunc("POST /api/auth/reset-password", r(g.handleResetPassword))
	mux.HandleFunc("GET /api/init", g.handleInit)
	mux.HandleFunc("POST /api/locale", g.handleLocale)
	mux.HandleFunc("GET /api/settings/database", r(g.handleExportDatabase))
	mux.HandleFunc("POST /api/settings/database", r(g.handleImportDatabase))
	mux.HandleFunc("POST /api/settings/proxy-test", r(g.handleProxyTest))
	mux.HandleFunc("POST /api/proxy-pools/{id}/test", r(g.handleProxyPoolTest))
}

// ---------------------------------------------------------------- pricing

func (g *Gateway) handleGetPricing(w http.ResponseWriter, r *http.Request) {
	g.pricing = loadPricing(g.store)
	writeJSON(w, http.StatusOK, g.pricing.merged())
}

// handlePatchPricing validates the same five numeric fields the JS route does,
// then merges per provider into the kv scope and rebuilds the cost table.
func (g *Gateway) handlePatchPricing(w http.ResponseWriter, r *http.Request) {
	var body map[string]map[string]map[string]any
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid pricing data format")
		return
	}
	valid := map[string]bool{"input": true, "output": true, "cached": true, "reasoning": true, "cache_creation": true}
	for provider, models := range body {
		if models == nil {
			writeErr(w, http.StatusBadRequest, "Invalid pricing for provider: "+provider)
			return
		}
		for model, fields := range models {
			if fields == nil {
				writeErr(w, http.StatusBadRequest, "Invalid pricing for model: "+provider+"/"+model)
				return
			}
			for key, raw := range fields {
				if !valid[key] {
					writeErr(w, http.StatusBadRequest, fmt.Sprintf("Invalid pricing field: %s for %s/%s", key, provider, model))
					return
				}
				n, ok := raw.(float64)
				if !ok || n < 0 {
					writeErr(w, http.StatusBadRequest,
						fmt.Sprintf("Invalid pricing value for %s in %s/%s: must be non-negative number", key, provider, model))
					return
				}
			}
			if err := g.mergePricing(provider, model, fields); err != nil {
				writeErr(w, http.StatusInternalServerError, "Failed to update pricing")
				return
			}
		}
	}
	g.pricing = loadPricing(g.store)
	writeJSON(w, http.StatusOK, g.pricing.userOnly())
}

// mergePricing merges one provider/model entry into the stored kv blob.
func (g *Gateway) mergePricing(provider, model string, fields map[string]any) error {
	raw, err := g.store.GetKV(store.ScopePricing)
	if err != nil {
		return err
	}
	current := map[string]any{}
	if v, ok := raw[provider]; ok {
		_ = jsonUnmarshalStr(v, &current)
	}
	entry, _ := current[model].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	for k, v := range fields {
		entry[k] = v
	}
	current[model] = entry
	return g.store.SetKV(store.ScopePricing, provider, stringifyAny(current))
}

func (g *Gateway) handleResetPricing(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	provider, model := q.Get("provider"), q.Get("model")
	if provider == "" {
		if raw, err := g.store.GetKV(store.ScopePricing); err == nil {
			for k := range raw {
				_, _ = g.store.DeleteKV(store.ScopePricing, k)
			}
		}
	} else {
		raw, err := g.store.GetKV(store.ScopePricing)
		if err == nil {
			if v, ok := raw[provider]; ok {
				if model == "" {
					_, _ = g.store.DeleteKV(store.ScopePricing, provider)
				} else {
					current := map[string]any{}
					_ = jsonUnmarshalStr(v, &current)
					delete(current, model)
					if len(current) == 0 {
						_, _ = g.store.DeleteKV(store.ScopePricing, provider)
					} else {
						_ = g.store.SetKV(store.ScopePricing, provider, stringifyAny(current))
					}
				}
			}
		}
	}
	g.pricing = loadPricing(g.store)
	writeJSON(w, http.StatusOK, g.pricing.merged())
}

// ---------------------------------------------------------------- tags

// ollamaModels mirrors open-sse/config/ollamaModels.js (a static stub catalog).
var ollamaModels = map[string]any{
	"models": []any{
		map[string]any{
			"name": "llama3.2", "modified_at": "2025-12-26T00:00:00Z", "size": 2000000000, "digest": "abc123def456",
			"details": map[string]any{"format": "gguf", "family": "llama", "parameter_size": "3B", "quantization_level": "Q4_K_M"},
		},
		map[string]any{
			"name": "qwen2.5", "modified_at": "2025-12-26T00:00:00Z", "size": 4000000000, "digest": "def456abc123",
			"details": map[string]any{"format": "gguf", "family": "qwen", "parameter_size": "7B", "quantization_level": "Q4_K_M"},
		},
	},
}

func (g *Gateway) handleOllamaTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	writeJSON(w, http.StatusOK, ollamaModels)
}

// ---------------------------------------------------------------- combo presets

// buildPresetItems mirrors lib/comboPresets.js for both sources.
func buildPresetItems(source string, existing map[string]bool) []map[string]any {
	alias := "cc"
	if source == "cursor" {
		alias = "cu"
	}
	seen := map[string]bool{}
	cfg := registryModels(alias)
	out := make([]map[string]any, 0, len(cfg))
	add := func(name, model string) {
		if name == "" || seen[name] || model == "" {
			return
		}
		seen[name] = true
		out = append(out, map[string]any{"name": name, "models": []string{model}, "exists": existing[name]})
	}
	for _, m := range cfg {
		add(m.id, alias+"/"+m.id)
	}
	if source == "claude" {
		for _, d := range claudePresetDefaults {
			add(d.alias, d.target)
		}
		add("default", "cc/claude-sonnet-5")
		add("opusplan", "cc/claude-opus-5")
	}
	return out
}

type presetModel struct{ id, name string }

// claudePresetDefaults mirrors CLI_TOOLS.claude.defaultModels.
var claudePresetDefaults = []struct{ alias, target string }{
	{"fable", "cc/claude-fable-5"},
	{"opus", "cc/claude-opus-5"},
	{"sonnet", "cc/claude-sonnet-5"},
	{"haiku", "cc/claude-haiku-4-5-20251001"},
}

func (g *Gateway) existingComboNames() map[string]bool {
	existing := map[string]bool{}
	if combos, err := g.store.ListCombos(); err == nil {
		for _, c := range combos {
			existing[c.Name] = true
		}
	}
	return existing
}

func (g *Gateway) handleComboPresetsPreview(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source != "cursor" && source != "claude" {
		writeErr(w, http.StatusBadRequest, "source must be 'cursor' or 'claude'")
		return
	}
	items := buildPresetItems(source, g.existingComboNames())
	toCreate, toSkip := 0, 0
	for _, it := range items {
		if it["exists"] == true {
			toSkip++
		} else {
			toCreate++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": source, "items": items, "toCreate": toCreate, "toSkip": toSkip,
	})
}

func (g *Gateway) handleComboPresetsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source string `json:"source"`
	}
	_ = decodeBody(r, &body)
	if body.Source != "cursor" && body.Source != "claude" {
		writeErr(w, http.StatusBadRequest, "source must be 'cursor' or 'claude'")
		return
	}
	items := buildPresetItems(body.Source, g.existingComboNames())
	created := []any{}
	skipped := []string{}
	for _, it := range items {
		name, _ := it["name"].(string)
		if it["exists"] == true {
			skipped = append(skipped, name)
			continue
		}
		models, _ := it["models"].([]string)
		c, err := g.store.SaveCombo(&store.Combo{Name: name, Models: models})
		if err != nil {
			continue
		}
		created = append(created, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": body.Source, "created": created, "skipped": skipped,
		"createdCount": len(created), "skippedCount": len(skipped),
	})
}

// ---------------------------------------------------------------- lifecycle

// handleShutdown mirrors the JS route: forbidden in production, requires
// SHUTDOWN_SECRET as a bearer token, then exits after the response flushes.
func (g *Gateway) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if !g.devMode() {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "Not allowed in production"})
		return
	}
	secret := os.Getenv("SHUTDOWN_SECRET")
	if secret == "" || r.Header.Get("Authorization") != "Bearer "+secret {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Shutting down..."})
	g.exitSoon()
}

// handleVersionUpdate is the CLI updater hook. KineRouter ships as a static Go
// binary; there is no npm pack to swap in place, so the endpoint reports the
// same production gate and exits, leaving the restart to the launcher.
func (g *Gateway) handleVersionUpdate(w http.ResponseWriter, r *http.Request) {
	if !g.devMode() {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"success": false, "message": "Update is only available in production build (kinerouter CLI)",
		})
		return
	}
	g.exitSoon()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Updater started. This app will exit shortly."})
}

func (g *Gateway) handleVersionShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Shutting down for manual update..."})
	g.exitSoon()
}

// devMode reports whether this process is a development build. The Go binary
// has no NODE_ENV; KINEROUTER_ENV=production opts in, matching the launcher.
func (g *Gateway) devMode() bool {
	return os.Getenv("KINEROUTER_ENV") != "production" && os.Getenv("NODE_ENV") != "production"
}

// exitSoon lets the response flush before the process exits.
func (g *Gateway) exitSoon() {
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}

// handleResetPassword clears the stored hash, returning the dashboard to the
// default password. Loopback only — the JS dashboardGuard enforces the same.
func (g *Gateway) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(clientHost(r)) {
		writeErr(w, http.StatusForbidden, "Password reset is only available from the local machine")
		return
	}
	if _, err := g.store.UpdateSettings(map[string]any{"password": nil}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (g *Gateway) handleInit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "Initialized")
}

// ---------------------------------------------------------------- locale

var supportedLocales = map[string]bool{
	"en": true, "vi": true, "zh-CN": true, "zh-TW": true, "ja": true, "pt-BR": true, "pt-PT": true,
	"ko": true, "es": true, "de": true, "fr": true, "he": true, "ar": true, "ru": true, "pl": true,
	"cs": true, "nl": true, "tr": true, "uk": true, "tl": true, "id": true, "th": true, "km": true,
	"hi": true, "bn": true, "ur": true, "ro": true, "sv": true, "it": true, "el": true, "hu": true,
	"fi": true, "da": true, "no": true, "fa": true,
}

func (g *Gateway) handleLocale(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Locale string `json:"locale"`
	}
	if decodeBody(r, &body) != nil || !supportedLocales[body.Locale] {
		writeErr(w, http.StatusBadRequest, "Invalid locale")
		return
	}
	normalized := body.Locale
	if normalized == "zh" {
		normalized = "zh-CN"
	}
	http.SetCookie(w, &http.Cookie{
		Name: "locale", Value: normalized, Path: "/", MaxAge: 31536000,
		SameSite: http.SameSiteLaxMode, Secure: g.cfg.AuthCookieSecure,
	})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "locale": normalized})
}

// ---------------------------------------------------------------- database

// verifyDashboardPassword re-checks the operator password for the destructive
// database routes. A CLI-token request skips it, as the JS isCliRequest does.
func (g *Gateway) verifyPasswordHeader(r *http.Request, supplied string) bool {
	if auth.ValidateCLIToken(r.Header.Get("x-kine-cli-token"), g.cliMachineID) {
		return true
	}
	settings, _ := g.store.GetSettings()
	hash, _ := settings["password"].(string)
	return auth.VerifyPassword(hash, supplied, g.cfg.InitialPassword)
}

func (g *Gateway) handleExportDatabase(w http.ResponseWriter, r *http.Request) {
	if !g.verifyPasswordHeader(r, r.Header.Get("x-kine-password")) {
		writeErr(w, http.StatusUnauthorized, "Invalid password")
		return
	}
	payload, err := g.store.ExportDB()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to export database")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (g *Gateway) handleImportDatabase(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	pw, _ := body["password"].(string)
	delete(body, "password")
	if !g.verifyPasswordHeader(r, pw) {
		writeErr(w, http.StatusUnauthorized, "Invalid password")
		return
	}
	if _, ok := body["settings"].(map[string]any); !ok {
		writeErr(w, http.StatusBadRequest, "Invalid database payload")
		return
	}
	if err := g.store.ImportDB(body); err != nil {
		writeErr(w, http.StatusBadRequest, firstNonEmpty(err.Error(), "Failed to import database"))
		return
	}
	g.pricing = loadPricing(g.store)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------------------------------------------------------------- proxy tests

type proxyTestResult struct {
	OK         bool   `json:"ok"`
	Status     int    `json:"status,omitempty"`
	StatusText string `json:"statusText,omitempty"`
	URL        string `json:"url,omitempty"`
	ElapsedMs  int64  `json:"elapsedMs"`
	Error      string `json:"error,omitempty"`
}

// testProxyURL probes testURL through proxyURL with a HEAD request, mirroring
// lib/network/proxyTest.js.
func testProxyURL(proxyURL, testURL string, timeout time.Duration) proxyTestResult {
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 8 * time.Second
	}
	u, err := neturl.Parse(proxyURL)
	if err != nil || u.Host == "" {
		return proxyTestResult{OK: false, Status: 400, Error: "Invalid proxy URL: " + errText(err)}
	}
	transport := &http.Transport{Proxy: http.ProxyURL(u), DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}
	client := &http.Client{Transport: transport, Timeout: timeout}
	start := time.Now()
	req, err := http.NewRequest(http.MethodHead, testURL, nil)
	if err != nil {
		return proxyTestResult{OK: false, Status: 400, Error: err.Error()}
	}
	req.Header.Set("User-Agent", "KineRouter")
	res, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return proxyTestResult{OK: false, Status: 500, Error: "Proxy test timed out"}
	}
	defer res.Body.Close()
	return proxyTestResult{
		OK: res.StatusCode >= 200 && res.StatusCode < 400, Status: res.StatusCode,
		StatusText: res.Status, URL: testURL, ElapsedMs: elapsed,
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (g *Gateway) handleProxyTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProxyURL  string `json:"proxyUrl"`
		TestURL   string `json:"testUrl"`
		TimeoutMs int    `json:"timeoutMs"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	proxyURL := strings.TrimSpace(body.ProxyURL)
	if proxyURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "proxyUrl is required"})
		return
	}
	testURL := strings.TrimSpace(body.TestURL)
	if testURL == "" {
		testURL = "https://google.com/"
	}
	if err := checkPublicURL(testURL); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "testUrl is not allowed: " + err.Error()})
		return
	}
	result := testProxyURL(proxyURL, testURL, time.Duration(body.TimeoutMs)*time.Millisecond)
	if result.OK {
		writeJSON(w, http.StatusOK, result)
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": result.Error})
}

// testVercelRelay probes a relay pool via its x-relay-* headers.
func testVercelRelay(relayURL string, timeout time.Duration) proxyTestResult {
	client := &http.Client{Timeout: timeout}
	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, relayURL, nil)
	if err != nil {
		return proxyTestResult{OK: false, Status: 500, Error: err.Error()}
	}
	req.Header.Set("x-relay-target", "https://httpbin.org")
	req.Header.Set("x-relay-path", "/get")
	res, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return proxyTestResult{OK: false, Status: 500, Error: "Relay test timed out"}
	}
	defer res.Body.Close()
	return proxyTestResult{OK: res.StatusCode >= 200 && res.StatusCode < 400, Status: res.StatusCode, StatusText: res.Status, ElapsedMs: elapsed}
}

func (g *Gateway) handleProxyPoolTest(w http.ResponseWriter, r *http.Request) {
	p, err := g.store.GetProxyPool(r.PathValue("id"))
	if err != nil || p == nil {
		writeErr(w, http.StatusNotFound, "Proxy pool not found")
		return
	}
	proxyURL, _ := p.Data["proxyUrl"].(string)
	ptype, _ := p.Data["type"].(string)
	var result proxyTestResult
	switch ptype {
	case "vercel", "cloudflare", "deno", "netlify":
		result = testVercelRelay(proxyURL, 10*time.Second)
	default:
		result = testProxyURL(proxyURL, "https://google.com/", 8*time.Second)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p.TestStatus = "error"
	if result.OK {
		p.TestStatus = "active"
	}
	p.IsActive = result.OK
	if p.Data == nil {
		p.Data = map[string]any{}
	}
	p.Data["lastTestedAt"] = now
	if result.OK {
		p.Data["lastError"] = nil
	} else {
		msg := result.Error
		if msg == "" {
			msg = fmt.Sprintf("Proxy test failed with status %d", result.Status)
		}
		p.Data["lastError"] = msg
	}
	_ = g.store.SaveProxyPool(p)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": result.OK, "status": result.Status, "statusText": nullableAny(result.StatusText),
		"error": nullableAny(result.Error), "elapsedMs": result.ElapsedMs, "testedAt": now,
	})
}

// registryModels lists one provider alias's models from the embedded
// providerModels table (getProviderModels in open-sse/config/providerModels.js).
func registryModels(alias string) []presetModel {
	models := providerdata.Get().Models[alias]
	out := make([]presetModel, 0, len(models))
	for _, m := range models {
		out = append(out, presetModel{id: m.ID, name: m.Name})
	}
	return out
}
