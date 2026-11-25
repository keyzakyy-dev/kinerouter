package gateway

// Claude Desktop "Cowork" (3p) provisioning plus the two MCP probes the
// dashboard calls while building that config.
//
// Security:
//   - Every filesystem path is home-derived and fixed; no request input names
//     a path. Managed config files are written through writeAtomic (0600).
//   - The inference gateway API key travels into the 3p config because Claude
//     Desktop itself must present it — same as the reference client.
//   - The local MCP bridge entries carry x-kine-cli-token so the desktop app can
//     reach this gateway's /api/mcp bridge under requireAuth.
//   - cowork-mcp-tools is an SSRF sink: a caller-supplied URL gets POSTed to.
//     Loopback callers keep their self-hosted servers; anything else must pass
//     netguard.CheckURL first.

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/keyzakyy-dev/kinerouter/internal/netguard"
)

// ---------------------------------------------------------------- plugins

type coworkPlugin struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url"`
	Transport   string   `json:"transport,omitempty"`
	OAuth       bool     `json:"oauth,omitempty"`
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	ToolNames   []string `json:"toolNames,omitempty"`
	Extension   string   `json:"extensionUrl,omitempty"`
}

var coworkDefaultPlugins = []coworkPlugin{
	{Name: "exa", Title: "Exa", Description: "Real-time web search and code documentation",
		URL: "https://mcp.exa.ai/mcp", Transport: "http",
		ToolNames: []string{"web_search_exa", "web_fetch_exa"}},
	{Name: "tavily", Title: "Tavily", Description: "Real-time web search optimized for LLM agents",
		URL: "https://mcp.tavily.com/mcp", Transport: "http", OAuth: true,
		ToolNames: []string{"tavily_search", "tavily_extract", "tavily_crawl", "tavily_map"}},
}

var coworkLocalStdioPlugins = []coworkPlugin{
	{Name: "browsermcp", Title: "Browser MCP",
		Description: "Control your running Chrome (requires Chrome extension)",
		Extension:   "https://chromewebstore.google.com/detail/browser-mcp-automate-your/bjfgambnhccakkhmkepdoekmckoijdlc",
		Command:     "npx", Args: []string{"-y", "@browsermcp/mcp@latest"},
		ToolNames: []string{"browser_navigate", "browser_snapshot", "browser_click", "browser_type",
			"browser_screenshot", "browser_get_console_logs", "browser_wait", "browser_press_key",
			"browser_go_back", "browser_go_forward"}},
}

var coworkSseURLRe = regexp.MustCompile(`(?i)/sse(\b|/)`)

// coworkManagedServers mirrors buildManagedMcpServers.
func coworkManagedServers(plugins []coworkPlugin) []map[string]any {
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, p := range plugins {
		if p.Name == "" || p.URL == "" || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		transport := p.Transport
		if transport == "" {
			if coworkSseURLRe.MatchString(p.URL) {
				transport = "sse"
			} else {
				transport = "http"
			}
		}
		entry := map[string]any{"name": p.Name, "url": p.URL, "transport": transport}
		if p.OAuth {
			entry["oauth"] = true
		}
		if len(p.ToolNames) > 0 {
			prefix := p.Name + "-"
			bare := map[string]bool{}
			for _, raw := range p.ToolNames {
				if raw == "" {
					continue
				}
				t := raw
				for strings.HasPrefix(t, prefix) {
					t = strings.TrimPrefix(t, prefix)
				}
				bare[t] = true
			}
			keys := make([]string, 0, len(bare))
			for t := range bare {
				keys = append(keys, t)
			}
			sort.Strings(keys)
			policy := map[string]any{}
			for _, t := range keys {
				policy[t] = "allow"
				policy[prefix+t] = "allow"
			}
			entry["toolPolicy"] = policy
		}
		out = append(out, entry)
	}
	return out
}

// ---------------------------------------------------------------- paths

func coworkCandidateRoots() []string {
	home := homePath()
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		return []string{filepath.Join(base, "Claude-3p"), filepath.Join(base, "Claude")}
	case "windows":
		localApp := firstNonEmpty(os.Getenv("LOCALAPPDATA"), filepath.Join(home, "AppData", "Local"))
		roaming := firstNonEmpty(os.Getenv("APPDATA"), filepath.Join(home, "AppData", "Roaming"))
		return []string{
			filepath.Join(localApp, "Claude-3p"), filepath.Join(roaming, "Claude-3p"),
			filepath.Join(localApp, "Claude"), filepath.Join(roaming, "Claude"),
		}
	}
	return []string{filepath.Join(home, ".config", "Claude-3p"), filepath.Join(home, ".config", "Claude")}
}

func coworkAppInstallPaths() []string {
	home := homePath()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Applications/Claude.app", filepath.Join(home, "Applications", "Claude.app")}
	case "windows":
		localApp := firstNonEmpty(os.Getenv("LOCALAPPDATA"), filepath.Join(home, "AppData", "Local"))
		programFiles := firstNonEmpty(os.Getenv("ProgramFiles"), `C:\Program Files`)
		return []string{filepath.Join(localApp, "AnthropicClaude"), filepath.Join(programFiles, "Claude"),
			filepath.Join(programFiles, "AnthropicClaude")}
	}
	return nil
}

func coworkWriteRoot() string { return coworkCandidateRoots()[0] }

func coworkReadRoot() string {
	for _, dir := range coworkCandidateRoots() {
		if _, err := os.Stat(filepath.Join(dir, "configLibrary")); err == nil {
			return dir
		}
	}
	return coworkCandidateRoots()[0]
}

func coworkConfigDir() string      { return filepath.Join(coworkReadRoot(), "configLibrary") }
func coworkWriteConfigDir() string { return filepath.Join(coworkWriteRoot(), "configLibrary") }

func cowork1pRoot() string {
	home := homePath()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude")
	case "windows":
		return filepath.Join(firstNonEmpty(os.Getenv("APPDATA"), filepath.Join(home, "AppData", "Roaming")), "Claude")
	}
	return filepath.Join(home, ".config", "Claude")
}

func cowork1pConfigPath() string { return filepath.Join(cowork1pRoot(), "claude_desktop_config.json") }

var coworkSecurityRelax = map[string]any{
	"coworkEgressAllowedHosts":            []any{"*"},
	"disabledBuiltinTools":                []any{},
	"isLocalDevMcpEnabled":                true,
	"isDesktopExtensionEnabled":           true,
	"isDesktopExtensionDirectoryEnabled":  true,
	"isDesktopExtensionSignatureRequired": false,
	"isClaudeCodeForDesktopEnabled":       true,
	"disableEssentialTelemetry":           true,
	"disableNonessentialTelemetry":        true,
	"disableNonessentialServices":         true,
}

const coworkInferenceProvider = "gateway"

// ---------------------------------------------------------------- 1p helpers

func coworkRead1p() map[string]any {
	m := readJSONMap(cowork1pConfigPath())
	if m == nil {
		return map[string]any{}
	}
	return m
}

func coworkWrite1p(cfg map[string]any) {
	os.MkdirAll(cowork1pRoot(), 0o700)
	writeJSONFile(cowork1pConfigPath(), cfg)
}

// coworkBootstrapDeploymentMode flips the 1p config into 3p mode.
func coworkBootstrapDeploymentMode() bool {
	cfg := coworkRead1p()
	if str(cfg, "deploymentMode") == "3p" {
		return false
	}
	cfg["deploymentMode"] = "3p"
	coworkWrite1p(cfg)
	return true
}

// coworkCleanup1pLegacy drops the stdio entries earlier versions wrote.
func coworkCleanup1pLegacy() {
	cfg := coworkRead1p()
	servers := objMap(cfg["mcpServers"])
	if servers == nil {
		return
	}
	for _, p := range coworkLocalStdioPlugins {
		delete(servers, p.Name)
	}
	if len(servers) == 0 {
		delete(cfg, "mcpServers")
	}
	coworkWrite1p(cfg)
}

// ---------------------------------------------------------------- 3p helpers

// coworkEnsureMeta returns the applied config id, creating _meta.json if needed.
func coworkEnsureMeta() map[string]any {
	writePath := filepath.Join(coworkWriteConfigDir(), "_meta.json")
	if meta := readJSONMap(writePath); meta != nil && str(meta, "appliedId") != "" {
		return meta
	}
	if existing := readJSONMap(filepath.Join(coworkConfigDir(), "_meta.json")); existing != nil && str(existing, "appliedId") != "" {
		os.MkdirAll(coworkWriteConfigDir(), 0o700)
		writeJSONFile(writePath, existing)
		return existing
	}
	id := randomUUID()
	meta := map[string]any{"appliedId": id, "entries": []any{map[string]any{"id": id, "name": "Default"}}}
	os.MkdirAll(coworkWriteConfigDir(), 0o700)
	writeJSONFile(writePath, meta)
	return meta
}

// coworkWriteSkipApprovals sets operonSkipMcpApprovals for every managed server.
func coworkWriteSkipApprovals(servers []map[string]any) any {
	path := filepath.Join(coworkWriteRoot(), "config.json")
	cfg := readJSONMap(path)
	if cfg == nil {
		cfg = map[string]any{}
	}
	skip := map[string]any{}
	for _, s := range servers {
		if name, _ := s["name"].(string); name != "" {
			skip[name] = true
		}
	}
	cfg["operonSkipMcpApprovals"] = skip
	os.MkdirAll(coworkWriteRoot(), 0o700)
	if err := writeJSONFile(path, cfg); err != nil {
		return map[string]any{"error": "write failed"}
	}
	return map[string]any{"written": len(skip)}
}

func (g *Gateway) coworkLocalBridgeEntries(names []string) []map[string]any {
	out := []map[string]any{}
	port := itoa(int64(g.gatewayPort()))
	for _, n := range names {
		var def *coworkPlugin
		for i := range coworkLocalStdioPlugins {
			if coworkLocalStdioPlugins[i].Name == n {
				def = &coworkLocalStdioPlugins[i]
				break
			}
		}
		if def == nil {
			continue
		}
		entry := map[string]any{
			"name":      def.Name,
			"url":       "http://localhost:" + port + "/api/mcp/" + def.Name + "/sse",
			"transport": "sse",
		}
		if len(def.ToolNames) > 0 {
			prefix := def.Name + "-"
			policy := map[string]any{}
			for _, t := range def.ToolNames {
				policy[t] = "allow"
				policy[prefix+t] = "allow"
			}
			entry["toolPolicy"] = policy
		}
		out = append(out, entry)
	}
	return out
}

func coworkInjectAuthHeaders(entries []map[string]any, token string) []map[string]any {
	for _, e := range entries {
		u, _ := e["url"].(string)
		if strings.HasPrefix(u, "http://localhost:") && strings.Contains(u, "/api/mcp/") {
			e["headers"] = map[string]any{"x-kine-cli-token": token}
		}
	}
	return entries
}

// ---------------------------------------------------------------- status

func (g *Gateway) coworkInstalled() bool {
	for _, dir := range append(coworkCandidateRoots(), coworkAppInstallPaths()...) {
		if _, err := os.Stat(dir); err == nil {
			return true
		}
	}
	return false
}

func (g *Gateway) cliCoworkStatus() map[string]any {
	if !g.coworkInstalled() {
		return map[string]any{"installed": false, "config": nil, "message": "Claude Desktop (Cowork mode) not detected"}
	}
	meta := readJSONMap(filepath.Join(coworkConfigDir(), "_meta.json"))
	appliedID := ""
	if meta != nil {
		appliedID = str(meta, "appliedId")
	}
	var config map[string]any
	configPath := ""
	if appliedID != "" {
		configPath = filepath.Join(coworkConfigDir(), appliedID+".json")
		config = readJSONMap(configPath)
	}
	baseURL := str(config, "inferenceGatewayBaseUrl")
	models := []any{}
	if arr, ok := config["inferenceModels"].([]any); ok {
		for _, m := range arr {
			if s, ok := m.(string); ok && s != "" {
				models = append(models, s)
			} else if mm := objMap(m); mm != nil && str(mm, "name") != "" {
				models = append(models, str(mm, "name"))
			}
		}
	}
	managed := []map[string]any{}
	if arr, ok := config["managedMcpServers"].([]any); ok {
		for _, m := range arr {
			if mm := objMap(m); mm != nil {
				managed = append(managed, mm)
			}
		}
	}
	isLocalURL := func(u string) bool { return strings.Contains(u, "/api/mcp/") }
	localNames := map[string]bool{}
	for _, p := range coworkLocalStdioPlugins {
		localNames[p.Name] = true
	}
	activeLocal := []any{}
	custom := []any{}
	plugins := []any{}
	for _, m := range managed {
		name := str(m, "name")
		u := str(m, "url")
		switch {
		case localNames[name] && isLocalURL(u):
			activeLocal = append(activeLocal, name)
		case m["custom"] == true || (!localNames[name] && isLocalURL(u)):
			custom = append(custom, map[string]any{"name": name, "url": u, "transport": m["transport"], "custom": true})
		default:
			keys := []string{}
			if tp, ok := m["toolPolicy"].(map[string]any); ok {
				prefix := name + "-"
				bare := map[string]bool{}
				for k := range tp {
					t := k
					for strings.HasPrefix(t, prefix) {
						t = strings.TrimPrefix(t, prefix)
					}
					bare[t] = true
				}
				for t := range bare {
					keys = append(keys, t)
				}
				sort.Strings(keys)
			}
			for _, d := range coworkDefaultPlugins {
				if d.Name == name {
					keys = d.ToolNames
					break
				}
			}
			plugins = append(plugins, map[string]any{
				"name": name, "url": u, "transport": m["transport"],
				"oauth": m["oauth"] != nil && m["oauth"] != false, "toolNames": keys,
			})
		}
	}
	return map[string]any{
		"installed": true, "config": config,
		"hasKineRouter": str(config, "inferenceProvider") == coworkInferenceProvider && baseURL != "",
		"configPath":    configPath,
		"cowork": map[string]any{
			"appliedId": appliedID, "baseUrl": baseURL, "models": models,
			"provider":      config["inferenceProvider"],
			"plugins":       plugins,
			"localPlugins":  activeLocal,
			"customPlugins": custom,
		},
		"defaultPlugins":    coworkDefaultPlugins,
		"localStdioPlugins": coworkLocalStdioPlugins,
	}
}

func (g *Gateway) handleCliCoworkGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliCoworkStatus())
}

// coworkGatewayBaseURL keeps the gateway origin bare. Claude Desktop builds
// its requests as <base>/v1/messages, so a stored "/v1" suffix turns every
// call into /v1/v1/messages and the app drops to config_model_rejected.
func coworkGatewayBaseURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if strings.HasSuffix(strings.ToLower(u), "/v1") {
		u = strings.TrimRight(u[:len(u)-3], "/")
	}
	return u
}

func (g *Gateway) handleCliCoworkPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL, apiKey := coworkGatewayBaseURL(str(body, "baseUrl")), str(body, "apiKey")
	if baseURL == "" || apiKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and apiKey are required"})
		return
	}
	models := []any{}
	if arr, ok := body["models"].([]any); ok {
		for _, m := range arr {
			if s, ok := m.(string); ok && strings.TrimSpace(s) != "" {
				models = append(models, map[string]any{"name": s})
			}
		}
	}
	if len(models) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "At least one model is required"})
		return
	}
	plugins := coworkDefaultPlugins
	if arr, present := body["plugins"].([]any); present {
		plugins = []coworkPlugin{}
		for _, item := range arr {
			if m := objMap(item); m != nil {
				plugins = append(plugins, coworkPlugin{
					Name: str(m, "name"), URL: str(m, "url"), Transport: str(m, "transport"),
					OAuth: m["oauth"] == true, ToolNames: strArr(m["toolNames"]),
				})
			}
		}
	}
	localNames := strArr(body["localPlugins"])
	customIn := []map[string]any{}
	if arr, ok := body["customPlugins"].([]any); ok {
		for _, item := range arr {
			m := objMap(item)
			if m == nil || str(m, "url") == "" {
				continue
			}
			customIn = append(customIn, m)
		}
	}

	managed := coworkManagedServers(plugins)
	managed = append(managed, g.coworkLocalBridgeEntries(localNames)...)
	for _, p := range customIn {
		transport := firstNonEmpty(str(p, "transport"), "sse")
		managed = append(managed, map[string]any{
			"name": str(p, "name"), "url": str(p, "url"), "transport": transport, "custom": true,
		})
	}
	managed = coworkInjectAuthHeaders(managed, g.cliMachineID)

	bootstrapped := coworkBootstrapDeploymentMode()
	meta := coworkEnsureMeta()
	configPath := filepath.Join(coworkWriteConfigDir(), str(meta, "appliedId")+".json")

	newConfig := map[string]any{}
	for k, v := range coworkSecurityRelax {
		newConfig[k] = v
	}
	newConfig["inferenceProvider"] = coworkInferenceProvider
	newConfig["inferenceGatewayBaseUrl"] = baseURL
	newConfig["inferenceGatewayApiKey"] = apiKey
	newConfig["inferenceModels"] = models
	if len(managed) > 0 {
		newConfig["managedMcpServers"] = managed
	}
	if err := writeJSONFile(configPath, newConfig); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to apply cowork settings"})
		return
	}
	skip := coworkWriteSkipApprovals(managed)
	coworkCleanup1pLegacy()

	msg := "Cowork settings applied. Quit & reopen Claude Desktop."
	if bootstrapped {
		msg = "Cowork enabled (3p mode set). Quit & reopen Claude Desktop."
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "bootstrapped": bootstrapped, "message": msg,
		"configPath": configPath, "skipApprovals": skip,
		"localMcp": map[string]any{"applied": localNames, "via": "3p-sse-bridge"},
	})
}

func (g *Gateway) handleCliCoworkDelete(w http.ResponseWriter, r *http.Request) {
	meta := readJSONMap(filepath.Join(coworkConfigDir(), "_meta.json"))
	if meta == nil || str(meta, "appliedId") == "" {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No active config to reset"})
		return
	}
	configPath := filepath.Join(coworkConfigDir(), str(meta, "appliedId")+".json")
	if _, err := os.Stat(configPath); err == nil {
		writeJSONFile(configPath, map[string]any{})
	}
	coworkWriteSkipApprovals(nil)
	coworkCleanup1pLegacy()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Cowork config reset"})
}

// ---------------------------------------------------------------- MCP registry

type coworkRegistryCache struct {
	ts   time.Time
	data map[string]any
}

var coworkRegistry struct {
	cached coworkRegistryCache
}

var coworkRegistryURL = "https://api.anthropic.com/mcp-registry/v0/servers"

var (
	coworkMcpClaudeRe    = regexp.MustCompile(`(?i)^https?://[^/]*\bmcp\.claude\.com\b`)
	coworkApiAnthropicRe = regexp.MustCompile(`(?i)^https?://api\.anthropic\.com/mcp\b`)
	coworkTemplateRe     = regexp.MustCompile(`[<{]`)
)

func coworkIsDirectConnect(u string) bool {
	if u == "" {
		return false
	}
	if coworkMcpClaudeRe.MatchString(u) || coworkApiAnthropicRe.MatchString(u) || coworkTemplateRe.MatchString(u) {
		return false
	}
	return strings.HasPrefix(strings.ToLower(u), "https://")
}

func coworkFetchRegistry(ctx context.Context) ([]any, error) {
	out := []any{}
	seen := map[string]bool{}
	cursor := ""
	client := &http.Client{Timeout: 20 * time.Second}
	for i := 0; i < 20; i++ {
		url := coworkRegistryURL + "?limit=500&visibility=commercial,gsuite,gsuite-google"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			break
		}
		req.Header.Set("accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			break
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			break
		}
		for _, item := range listOf(payload["servers"]) {
			im := objMap(item)
			if im == nil {
				continue
			}
			s := objMap(im["server"])
			meta := map[string]any{}
			if m := objMap(im["_meta"]); m != nil {
				meta = objMap(m["com.anthropic.api/mcp-registry"])
			}
			remotes := listOf(s["remotes"])
			if len(remotes) == 0 {
				continue
			}
			remote := objMap(remotes[0])
			u := str(remote, "url")
			if u == "" || !coworkIsDirectConnect(u) || seen[u] {
				continue
			}
			if len(listOf(meta["requiredFields"])) > 0 {
				continue
			}
			seen[u] = true
			transport := "http"
			if str(remote, "type") == "sse" {
				transport = "sse"
			}
			toolNames := strArr(meta["toolNames"])
			out = append(out, map[string]any{
				"name": str(s, "name"), "slug": firstNonEmpty(str(meta, "slug"), str(s, "name")),
				"title":       firstNonEmpty(str(s, "title"), str(meta, "displayName"), str(s, "name")),
				"description": firstNonEmpty(str(s, "description"), str(meta, "oneLiner")),
				"url":         u, "transport": transport,
				"oauth":     meta["isAuthless"] != true,
				"toolNames": toolNames, "toolCount": len(toolNames),
				"iconUrl": meta["iconUrl"],
			})
		}
		cursor, _ = objMap(payload["metadata"])["nextCursor"].(string)
		if cursor == "" {
			break
		}
	}
	return out, nil
}

func (g *Gateway) handleCliCoworkRegistry(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("refresh") == "1"
	if !force && coworkRegistry.cached.data != nil && time.Since(coworkRegistry.cached.ts) < time.Hour {
		out := map[string]any{"cached": true}
		for k, v := range coworkRegistry.cached.data {
			out[k] = v
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	servers, err := coworkFetchRegistry(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "servers": []any{}, "total": 0})
		return
	}
	data := map[string]any{"servers": servers, "total": len(servers)}
	coworkRegistry.cached = coworkRegistryCache{ts: time.Now(), data: data}
	out := map[string]any{"cached": false}
	for k, v := range data {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------- MCP probe

type coworkProbe struct {
	RequiresAuth bool             `json:"requiresAuth,omitempty"`
	Tools        []map[string]any `json:"tools"`
	Error        string           `json:"error,omitempty"`
}

func coworkProbeMCP(ctx context.Context, url string) coworkProbe {
	client := netguard.Client(8 * time.Second)
	headers := map[string]string{
		"Content-Type":         "application/json",
		"Accept":               "application/json, text/event-stream",
		"MCP-Protocol-Version": "2025-06-18",
	}
	post := func(body string, sessionID string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if sessionID != "" {
			req.Header.Set("mcp-session-id", sessionID)
		}
		return client.Do(req)
	}

	initRes, err := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"kinerouter","version":"1"}}}`, "")
	if err != nil {
		return coworkProbe{Tools: []map[string]any{}, Error: err.Error()}
	}
	if initRes.StatusCode == http.StatusUnauthorized || initRes.StatusCode == http.StatusForbidden {
		initRes.Body.Close()
		return coworkProbe{RequiresAuth: true, Tools: []map[string]any{}}
	}
	if initRes.StatusCode != http.StatusOK {
		initRes.Body.Close()
		return coworkProbe{Tools: []map[string]any{}, Error: "init " + itoa(int64(initRes.StatusCode))}
	}
	sessionID := initRes.Header.Get("mcp-session-id")
	io.Copy(io.Discard, io.LimitReader(initRes.Body, 1<<20))
	initRes.Body.Close()

	if res, err := post(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`, sessionID); err == nil {
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
	}

	listRes, err := post(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, sessionID)
	if err != nil {
		return coworkProbe{Tools: []map[string]any{}, Error: err.Error()}
	}
	defer listRes.Body.Close()
	if listRes.StatusCode == http.StatusUnauthorized || listRes.StatusCode == http.StatusForbidden {
		return coworkProbe{RequiresAuth: true, Tools: []map[string]any{}}
	}
	raw, _ := io.ReadAll(io.LimitReader(listRes.Body, 8<<20))
	var parsed map[string]any
	if strings.Contains(listRes.Header.Get("content-type"), "text/event-stream") {
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var obj map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &obj) != nil {
				continue
			}
			if id, _ := obj["id"].(float64); int(id) == 2 && obj["result"] != nil {
				parsed = obj
				break
			}
		}
	} else {
		json.Unmarshal(raw, &parsed)
	}
	tools := []map[string]any{}
	if result := objMap(parsed["result"]); result != nil {
		for _, t := range listOf(result["tools"]) {
			tm := objMap(t)
			if tm == nil {
				continue
			}
			tools = append(tools, map[string]any{"name": str(tm, "name"), "description": str(tm, "description")})
		}
	}
	return coworkProbe{Tools: tools}
}

func (g *Gateway) handleCliCoworkMcpTools(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	target := str(body, "url")
	if target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "url required"})
		return
	}
	// SSRF guard: a remote caller may not point this at an internal address.
	if !isLoopback(clientHost(r)) {
		if err := netguard.CheckURL(r.Context(), target); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "URL not allowed"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, coworkProbeMCP(ctx, target))
}

// ---------------------------------------------------------------- omp agent.db

// ompSeedAuthCredential is a best-effort mirror of the JS route's agent.db
// insert: models.yml stays the source of truth, a locked or missing DB is
// silently skipped.
func (g *Gateway) ompSeedAuthCredential(apiKey, baseURL string) {
	db, err := sql.Open("sqlite", "file:"+ompDBPath()+"?_pragma=busy_timeout(2000)")
	if err != nil {
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if db.PingContext(ctx) != nil {
		return
	}
	data, _ := json.Marshal(map[string]any{"apiKey": apiKey, "baseUrl": baseURL})
	now := time.Now().Unix()
	if _, err := db.ExecContext(ctx, "DELETE FROM auth_credentials WHERE provider = ?", grokMainSlot); err != nil {
		return
	}
	db.ExecContext(ctx,
		"INSERT INTO auth_credentials (provider, credential_type, data, disabled_cause, identity_key, created_at, updated_at) VALUES (?, ?, ?, NULL, NULL, ?, ?)",
		grokMainSlot, "api_key", string(data), now, now)
}
