package gateway

// CLI tool config writers. Ported from src/app/api/cli-tools/*: each tool has
// its own config file somewhere under the user's home directory, and these
// routes detect the tool and write (or reset) a KineRouter provider block in it.
//
// Security: every path below is built from os.UserHomeDir() and a fixed
// relative name — no request-supplied path ever reaches the filesystem. The
// caller-supplied "apiKey" is only ever used as the literal value written into
// a client config (resolveCliAPIKey); it never authenticates anything against
// an upstream. The placeholder "sk_kinerouter" is never written (#4399). File
// modes come from writeAtomic (0600).

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

func (g *Gateway) registerCliToolRoutes(mux *http.ServeMux) {
	r := g.requireAuth
	mux.HandleFunc("GET /api/cli-tools/all-statuses", r(g.handleCliAllStatuses))
	mux.HandleFunc("GET /api/cli-tools/claude-settings", r(g.handleCliClaudeGet))
	mux.HandleFunc("POST /api/cli-tools/claude-settings", r(g.handleCliClaudePost))
	mux.HandleFunc("DELETE /api/cli-tools/claude-settings", r(g.handleCliClaudeDelete))
	mux.HandleFunc("GET /api/cli-tools/codex-settings", r(g.handleCliCodexGet))
	mux.HandleFunc("POST /api/cli-tools/codex-settings", r(g.handleCliCodexPost))
	mux.HandleFunc("DELETE /api/cli-tools/codex-settings", r(g.handleCliCodexDelete))
	mux.HandleFunc("GET /api/cli-tools/codex-profiles", r(g.handleCliCodexProfilesGet))
	mux.HandleFunc("POST /api/cli-tools/codex-profiles", r(g.handleCliCodexProfileCreate))
	mux.HandleFunc("DELETE /api/cli-tools/codex-profiles", r(g.handleCliCodexProfileDelete))
	mux.HandleFunc("GET /api/cli-tools/opencode-settings", r(g.handleCliOpencodeGet))
	mux.HandleFunc("POST /api/cli-tools/opencode-settings", r(g.handleCliOpencodePost))
	mux.HandleFunc("PATCH /api/cli-tools/opencode-settings", r(g.handleCliOpencodePatch))
	mux.HandleFunc("DELETE /api/cli-tools/opencode-settings", r(g.handleCliOpencodeDelete))
	mux.HandleFunc("GET /api/cli-tools/droid-settings", r(g.handleCliDroidGet))
	mux.HandleFunc("POST /api/cli-tools/droid-settings", r(g.handleCliDroidPost))
	mux.HandleFunc("DELETE /api/cli-tools/droid-settings", r(g.handleCliDroidDelete))
	mux.HandleFunc("GET /api/cli-tools/openclaw-settings", r(g.handleCliOpenclawGet))
	mux.HandleFunc("POST /api/cli-tools/openclaw-settings", r(g.handleCliOpenclawPost))
	mux.HandleFunc("DELETE /api/cli-tools/openclaw-settings", r(g.handleCliOpenclawDelete))
	mux.HandleFunc("GET /api/cli-tools/hermes-settings", r(g.handleCliHermesGet))
	mux.HandleFunc("POST /api/cli-tools/hermes-settings", r(g.handleCliHermesPost))
	mux.HandleFunc("DELETE /api/cli-tools/hermes-settings", r(g.handleCliHermesDelete))
	mux.HandleFunc("GET /api/cli-tools/hermes-profiles", r(g.handleCliHermesProfiles))
	mux.HandleFunc("GET /api/cli-tools/cowork-settings", r(g.handleCliCoworkGet))
	mux.HandleFunc("POST /api/cli-tools/cowork-settings", r(g.handleCliCoworkPost))
	mux.HandleFunc("DELETE /api/cli-tools/cowork-settings", r(g.handleCliCoworkDelete))
	mux.HandleFunc("GET /api/cli-tools/cowork-mcp-registry", r(g.handleCliCoworkRegistry))
	mux.HandleFunc("POST /api/cli-tools/cowork-mcp-tools", r(g.handleCliCoworkMcpTools))
	mux.HandleFunc("GET /api/cli-tools/cline-settings", r(g.handleCliClineGet))
	mux.HandleFunc("POST /api/cli-tools/cline-settings", r(g.handleCliClinePost))
	mux.HandleFunc("DELETE /api/cli-tools/cline-settings", r(g.handleCliClineDelete))
	mux.HandleFunc("GET /api/cli-tools/kilo-settings", r(g.handleCliKiloGet))
	mux.HandleFunc("POST /api/cli-tools/kilo-settings", r(g.handleCliKiloPost))
	mux.HandleFunc("DELETE /api/cli-tools/kilo-settings", r(g.handleCliKiloDelete))
	mux.HandleFunc("GET /api/cli-tools/deepseek-tui-settings", r(g.handleCliDeepseekGet))
	mux.HandleFunc("POST /api/cli-tools/deepseek-tui-settings", r(g.handleCliDeepseekPost))
	mux.HandleFunc("DELETE /api/cli-tools/deepseek-tui-settings", r(g.handleCliDeepseekDelete))
	mux.HandleFunc("GET /api/cli-tools/jcode-settings", r(g.handleCliJcodeGet))
	mux.HandleFunc("POST /api/cli-tools/jcode-settings", r(g.handleCliJcodePost))
	mux.HandleFunc("DELETE /api/cli-tools/jcode-settings", r(g.handleCliJcodeDelete))
	mux.HandleFunc("GET /api/cli-tools/grok-build-settings", r(g.handleCliGrokGet))
	mux.HandleFunc("POST /api/cli-tools/grok-build-settings", r(g.handleCliGrokPost))
	mux.HandleFunc("DELETE /api/cli-tools/grok-build-settings", r(g.handleCliGrokDelete))
	mux.HandleFunc("GET /api/cli-tools/devin-settings", r(g.handleCliDevin))
	mux.HandleFunc("GET /api/cli-tools/copilot-settings", r(g.handleCliCopilotGet))
	mux.HandleFunc("POST /api/cli-tools/copilot-settings", r(g.handleCliCopilotPost))
	mux.HandleFunc("DELETE /api/cli-tools/copilot-settings", r(g.handleCliCopilotDelete))
	mux.HandleFunc("GET /api/cli-tools/pi-settings", r(g.handleCliPiGet))
	mux.HandleFunc("POST /api/cli-tools/pi-settings", r(g.handleCliPiPost))
	mux.HandleFunc("DELETE /api/cli-tools/pi-settings", r(g.handleCliPiDelete))
	mux.HandleFunc("GET /api/cli-tools/omp-settings", r(g.handleCliOmpGet))
	mux.HandleFunc("POST /api/cli-tools/omp-settings", r(g.handleCliOmpPost))
	mux.HandleFunc("DELETE /api/cli-tools/omp-settings", r(g.handleCliOmpDelete))
	mux.HandleFunc("GET /api/cli-tools/crush-settings", r(g.handleCliCrushGet))
	mux.HandleFunc("POST /api/cli-tools/crush-settings", r(g.handleCliCrushPost))
	mux.HandleFunc("DELETE /api/cli-tools/crush-settings", r(g.handleCliCrushDelete))
	mux.HandleFunc("GET /api/cli-tools/forge-settings", r(g.handleCliForgeGet))
	mux.HandleFunc("POST /api/cli-tools/forge-settings", r(g.handleCliForgePost))
	mux.HandleFunc("DELETE /api/cli-tools/forge-settings", r(g.handleCliForgeDelete))
	mux.HandleFunc("GET /api/cli-tools/smelt-settings", r(g.handleCliSmeltGet))
	mux.HandleFunc("POST /api/cli-tools/smelt-settings", r(g.handleCliSmeltPost))
	mux.HandleFunc("DELETE /api/cli-tools/smelt-settings", r(g.handleCliSmeltDelete))
	mux.HandleFunc("GET /api/cli-tools/codewhale-settings", r(g.handleCliCodewhaleGet))
	mux.HandleFunc("POST /api/cli-tools/codewhale-settings", r(g.handleCliCodewhalePost))
	mux.HandleFunc("DELETE /api/cli-tools/codewhale-settings", r(g.handleCliCodewhaleDelete))
}

// ---------------------------------------------------------------- helpers

var jsoncTrailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// resolveCliAPIKey mirrors src/app/api/cli-tools/resolveApiKey.js: caller key →
// first active dashboard key → "". "sk_kinerouter" is a placeholder, never a key.
func (g *Gateway) resolveCliAPIKey(caller string) string {
	if k := strings.TrimSpace(caller); k != "" && k != "sk_kinerouter" {
		return k
	}
	keys, err := g.store.ListAPIKeys()
	if err != nil {
		return ""
	}
	for _, k := range keys {
		if k.IsActive {
			return k.Key
		}
	}
	return ""
}

func normalizeV1(base string) string {
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// loopbackUnsafeTools are CLI tools whose HTTP stacks resolve "localhost" to
// ::1 first and do not retry over IPv4. The gateway listens on 0.0.0.0, which
// is IPv4 only, so a config pointing at "localhost:<port>" is refused by them
// even though the browser reaches the same URL fine. KeyRouter hit this first
// and keeps "localhost" in the UI while writing 127.0.0.1 into these five
// configs; we do the same.
var loopbackUnsafeTools = map[string]bool{
	"openclaw":     true,
	"hermes":       true,
	"grok-build":   true,
	"jcode":        true,
	"deepseek-tui": true,
}

// ipv4Loopback rewrites a "localhost" host to 127.0.0.1 while preserving
// scheme, port, path and query. Anything else (127.0.0.1, LAN IP, remote
// domain) is returned untouched so remote and tunnel setups never change.
func ipv4Loopback(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return raw
	}
	hadScheme := strings.Contains(s, "://")
	parsed := s
	if !hadScheme {
		parsed = "http://" + s
	}
	u, err := url.Parse(parsed)
	if err != nil || !strings.EqualFold(u.Hostname(), "localhost") {
		return raw
	}
	if port := u.Port(); port != "" {
		u.Host = "127.0.0.1:" + port
	} else {
		u.Host = "127.0.0.1"
	}
	out := u.String()
	if !hadScheme {
		out = strings.TrimPrefix(out, "http://")
	}
	return out
}

// cliToolBase prepares a base URL for a tool config file: fragile tools get the
// IPv4 loopback literal, and every tool gets the /v1 suffix.
func cliToolBase(toolID, baseURL string) string {
	if loopbackUnsafeTools[toolID] {
		baseURL = ipv4Loopback(baseURL)
	}
	return normalizeV1(baseURL)
}

// toolInstalled mirrors `which <cmd>` with a config-file fallback.
func toolInstalled(cmd, configPath string) bool {
	if _, err := exec.LookPath(cmd); err == nil {
		return true
	}
	if configPath == "" {
		return false
	}
	_, err := os.Stat(configPath)
	return err == nil
}

func readTextOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// readJSONMap tolerates JSONC (trailing commas); missing/unparseable → nil.
func readJSONMap(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(jsoncTrailingComma.ReplaceAllString(string(b), "$1")), &m) != nil {
		return nil
	}
	return m
}

func readJSONArray(path string) []any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var a []any
	if json.Unmarshal([]byte(jsoncTrailingComma.ReplaceAllString(string(b), "$1")), &a) != nil {
		return nil
	}
	return a
}

// writeJSONFile is JSON.stringify(v, null, 2) + fs.writeFile.
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, b)
}

// section returns m[k] as a map, creating it when absent.
func section(m map[string]any, k string) map[string]any {
	if v, ok := m[k].(map[string]any); ok {
		return v
	}
	out := map[string]any{}
	m[k] = out
	return out
}

func objMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func strArr(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// cliBody decodes a POST body; it writes the 400 and reports ok=false.
func cliBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "Invalid JSON body"}})
		return nil, false
	}
	return body, true
}

// ---------------------------------------------------------------- all-statuses

func (g *Gateway) handleCliAllStatuses(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	statuses := map[string]func() map[string]any{
		"claude":       func() map[string]any { return g.cliClaudeStatus() },
		"codex":        func() map[string]any { return g.cliCodexStatus() },
		"opencode":     func() map[string]any { return g.cliOpencodeStatus() },
		"droid":        func() map[string]any { return g.cliDroidStatus() },
		"openclaw":     func() map[string]any { return g.cliOpenclawStatus() },
		"hermes":       func() map[string]any { return g.cliHermesStatus("") },
		"cowork":       func() map[string]any { return g.cliCoworkStatus() },
		"cline":        func() map[string]any { return g.cliClineStatus() },
		"kilo":         func() map[string]any { return g.cliKiloStatus() },
		"deepseek-tui": func() map[string]any { return g.cliDeepseekStatus() },
		"jcode":        func() map[string]any { return g.cliJcodeStatus() },
		"grok-build":   func() map[string]any { return g.cliGrokStatus() },
		"devin":        func() map[string]any { return g.cliDevinStatus() },
		"pi":           func() map[string]any { return g.cliPiStatus() },
		"omp":          func() map[string]any { return g.cliOmpStatus() },
		"crush":        func() map[string]any { return g.cliCrushStatus() },
		"forge":        func() map[string]any { return g.cliForgeStatus() },
		"smelt":        func() map[string]any { return g.cliSmeltStatus() },
		"codewhale":    func() map[string]any { return g.cliCodewhaleStatus() },
	}
	for id, fn := range statuses {
		out[id] = fn()
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------- crush

func crushPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = homePath(".config")
	}
	return filepath.Join(dir, "crush", "crush.json")
}

// gatewayPort is the port this gateway serves on, so a detector or emitter
// never assumes the reference product's 20128.
func (g *Gateway) gatewayPort() int {
	if g.cfg != nil && g.cfg.Port > 0 {
		return g.cfg.Port
	}
	return config.DefaultPort
}

func (g *Gateway) crushHasKineRouter(cfg map[string]any) bool {
	providers := objMap(cfg["providers"])
	if providers == nil {
		return false
	}
	if p := objMap(providers["kinerouter"]); p != nil && str(p, "base_url") != "" {
		return true
	}
	for _, v := range providers {
		if p := objMap(v); p != nil && strings.Contains(str(p, "base_url"), itoa(int64(g.gatewayPort()))) {
			return true
		}
	}
	return false
}

func (g *Gateway) cliCrushStatus() map[string]any {
	path := crushPath()
	if !toolInstalled("crush", path) {
		return map[string]any{"installed": false, "config": nil, "message": "Crush CLI is not installed"}
	}
	cfg := readJSONMap(path)
	return map[string]any{"installed": true, "config": cfg, "hasKineRouter": g.crushHasKineRouter(cfg), "configPath": path}
}

func (g *Gateway) handleCliCrushGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliCrushStatus())
}

func (g *Gateway) handleCliCrushPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL := str(body, "baseUrl")
	if baseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "baseUrl is required"}})
		return
	}
	path := crushPath()
	existing := readJSONMap(path)
	if existing == nil {
		existing = map[string]any{}
	}
	providers := section(existing, "providers")
	modelID := firstNonEmpty(str(body, "model"), "provider/model-id")
	// A resolved window beats the 128000 guess: a wrong guess is exactly how a
	// client misses its compaction boundary (9router-go crushContextWindowFor).
	window := int64(128000)
	if lim := g.cliModelLimit(modelID); lim.Known {
		window = lim.Context
	}
	providers["kinerouter"] = map[string]any{
		"type": "openai-compat", "base_url": normalizeV1(baseURL),
		"api_key": g.resolveCliAPIKey(str(body, "apiKey")),
		"models":  []any{map[string]any{"id": modelID, "name": modelID, "context_window": window}},
	}
	if err := writeJSONFile(path, existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Crush settings applied successfully!", "configPath": path})
}

func (g *Gateway) handleCliCrushDelete(w http.ResponseWriter, r *http.Request) {
	path := crushPath()
	existing := readJSONMap(path)
	if existing == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	if providers := objMap(existing["providers"]); providers != nil {
		if _, ok := providers["kinerouter"]; ok {
			delete(providers, "kinerouter")
			if len(providers) == 0 {
				delete(existing, "providers")
			}
			writeJSONFile(path, existing)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter removed from Crush"})
}

// ---------------------------------------------------------------- smelt

func smeltPath() string { return homePath(".smelt", "config.json") }

func (g *Gateway) cliSmeltStatus() map[string]any {
	path := smeltPath()
	if !toolInstalled("smelt", path) {
		return map[string]any{"installed": false, "config": nil, "message": "Smelt CLI is not installed"}
	}
	cfg := readJSONMap(path)
	has := cfg != nil && (str(cfg, "_managedBy") == "kinerouter" || strings.Contains(str(cfg, "baseUrl"), itoa(int64(g.gatewayPort()))))
	return map[string]any{"installed": true, "config": cfg, "hasKineRouter": has, "configPath": path}
}

func (g *Gateway) handleCliSmeltGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliSmeltStatus())
}

func (g *Gateway) handleCliSmeltPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL := str(body, "baseUrl")
	if baseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "baseUrl is required"}})
		return
	}
	path := smeltPath()
	existing := readJSONMap(path)
	if existing == nil {
		existing = map[string]any{}
	}
	existing["baseUrl"] = normalizeV1(baseURL)
	existing["apiKey"] = g.resolveCliAPIKey(str(body, "apiKey"))
	existing["model"] = firstNonEmpty(str(body, "model"), str(existing, "model"), "provider/model-id")
	existing["_managedBy"] = "kinerouter"
	if err := writeJSONFile(path, existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Smelt settings applied successfully!", "configPath": path})
}

func (g *Gateway) handleCliSmeltDelete(w http.ResponseWriter, r *http.Request) {
	path := smeltPath()
	existing := readJSONMap(path)
	if existing == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	for _, k := range []string{"baseUrl", "apiKey", "model", "_managedBy"} {
		delete(existing, k)
	}
	if len(existing) == 0 {
		os.Remove(path)
	} else {
		writeJSONFile(path, existing)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Smelt KineRouter settings removed"})
}

// ---------------------------------------------------------------- droid

func droidPath() string { return homePath(".factory", "settings.json") }

func (g *Gateway) cliDroidStatus() map[string]any {
	path := droidPath()
	if !toolInstalled("droid", path) {
		return map[string]any{"installed": false, "settings": nil, "message": "Factory Droid CLI is not installed"}
	}
	settings := readJSONMap(path)
	has := false
	if arr, ok := settings["customModels"].([]any); ok {
		for _, m := range arr {
			if strings.HasPrefix(str(objMap(m), "id"), "custom:KineRouter") {
				has = true
			}
		}
	}
	return map[string]any{"installed": true, "settings": settings, "hasKineRouter": has, "settingsPath": path}
}

func (g *Gateway) handleCliDroidGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliDroidStatus())
}

func (g *Gateway) handleCliDroidPost(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update droid settings"})
		return
	}
	baseURL := str(body, "baseUrl")
	models := strArr(body["models"])
	if len(models) == 0 && str(body, "model") != "" {
		models = []string{str(body, "model")}
	}
	if baseURL == "" || len(models) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and at least one model are required"})
		return
	}
	path := droidPath()
	settings := readJSONMap(path)
	if settings == nil {
		settings = map[string]any{}
	}
	kept := []any{}
	if arr, ok := settings["customModels"].([]any); ok {
		for _, m := range arr {
			if !strings.HasPrefix(str(objMap(m), "id"), "custom:KineRouter") {
				kept = append(kept, m)
			}
		}
	}
	normalized := normalizeV1(baseURL)
	keyToUse := firstNonEmpty(str(body, "apiKey"), "your_api_key")
	defaultIndex := 0
	if v, present := body["activeModel"]; present {
		active, _ := v.(string)
		if v == "" || active == "" {
			defaultIndex = -1
		} else {
			for i, m := range models {
				if m == active {
					defaultIndex = i
					break
				}
			}
		}
	}
	ourStart := len(kept)
	for i, m := range models {
		kept = append(kept, map[string]any{
			"model": m, "id": "custom:KineRouter-" + itoa(int64(i)), "index": i,
			"baseUrl": normalized, "apiKey": keyToUse, "displayName": m,
			"maxOutputTokens": 131072, "noImageSupport": false, "provider": "openai",
		})
	}
	// defaultIndex counts our own entries, not the whole array: moving kept[0]
	// would promote the user's unrelated model instead of the active one.
	if defaultIndex >= 0 && defaultIndex < len(models) {
		first := kept[ourStart+defaultIndex]
		pick := ourStart + defaultIndex
		kept = append(kept[:pick], kept[pick+1:]...)
		kept = append([]any{first}, kept...)
	}
	// Droid addresses a custom model by its `index` field, not by its position,
	// so every entry is renumbered from zero on every write (9router-go's
	// droidRenumber): one stale index points that row at another model.
	for i, m := range kept {
		if obj := objMap(m); obj != nil {
			obj["index"] = i
		}
	}
	settings["customModels"] = kept
	if err := writeJSONFile(path, settings); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update droid settings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Factory Droid settings applied successfully!", "settingsPath": path})
}

func (g *Gateway) handleCliDroidDelete(w http.ResponseWriter, r *http.Request) {
	path := droidPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No settings file to reset"})
		return
	}
	settings := readJSONMap(path)
	if settings == nil {
		settings = map[string]any{}
	}
	if arr, ok := settings["customModels"].([]any); ok {
		kept := []any{}
		for _, m := range arr {
			if !strings.HasPrefix(str(objMap(m), "id"), "custom:KineRouter") {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			delete(settings, "customModels")
		} else {
			settings["customModels"] = kept
		}
	}
	writeJSONFile(path, settings)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter settings removed successfully"})
}

// ---------------------------------------------------------------- opencode

func opencodePath() string { return homePath(".config", "opencode", "opencode.json") }

func (g *Gateway) cliOpencodeStatus() map[string]any {
	path := opencodePath()
	if !toolInstalled("opencode", path) {
		return map[string]any{"installed": false, "config": nil, "message": "OpenCode CLI is not installed"}
	}
	cfg := readJSONMap(path)
	var provider map[string]any
	if p := objMap(cfg["provider"]); p != nil {
		provider = objMap(p["kinerouter"])
	}
	models := []string{}
	if provider != nil {
		models = sortedKeys(objMap(provider["models"]))
	}
	var active any
	if m := str(cfg, "model"); strings.HasPrefix(m, "kinerouter/") {
		active = strings.TrimPrefix(m, "kinerouter/")
	}
	var baseURL any
	if opts := objMap(provider["options"]); opts != nil {
		baseURL = opts["baseURL"]
	}
	return map[string]any{
		"installed": true, "config": cfg, "hasKineRouter": provider != nil, "configPath": path,
		"opencode": map[string]any{"models": models, "activeModel": active, "baseURL": baseURL},
	}
}

func (g *Gateway) handleCliOpencodeGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliOpencodeStatus())
}

func (g *Gateway) handleCliOpencodePost(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to apply settings"})
		return
	}
	models := strArr(body["models"])
	if len(models) == 0 && str(body, "model") != "" {
		models = []string{str(body, "model")}
	}
	baseURL := str(body, "baseUrl")
	if baseURL == "" || len(models) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and at least one model are required"})
		return
	}
	path := opencodePath()
	cfg := readJSONMap(path)
	if cfg == nil {
		cfg = map[string]any{}
	}
	providers := section(cfg, "provider")
	existing := objMap(providers["kinerouter"])
	if existing == nil {
		existing = map[string]any{"npm": "@ai-sdk/openai-compatible", "options": map[string]any{}, "models": map[string]any{}}
	}
	opts := section(existing, "options")
	opts["baseURL"] = normalizeV1(baseURL)
	opts["apiKey"] = g.resolveCliAPIKey(str(body, "apiKey"))
	modelsMap := section(existing, "models")
	for _, rawModel := range models {
		// The prefix is stripped and re-added at every write, so a caller passing
		// an id already copied out of opencode does not end up with a double one.
		m := strings.TrimPrefix(rawModel, "kinerouter/")
		entry := map[string]any{
			"name":       m,
			"modalities": map[string]any{"input": []any{"text", "image"}, "output": []any{"text"}},
		}
		// An explicit limit is what gives opencode a context percentage and an
		// auto-compact boundary; a model unknown to its own snapshot has neither.
		if lim := g.cliModelLimit(m); lim.Known {
			limit := map[string]any{"context": lim.Context}
			if lim.MaxOutput > 0 {
				limit["output"] = lim.MaxOutput
			}
			entry["limit"] = limit
		}
		// Effort variants come from the same thinking-level table the dashboard
		// picker uses; levels are per provider+model. Existing user-tuned
		// variants are preserved.
		if prev := objMap(modelsMap[m]); prev != nil {
			if v, ok := prev["variants"]; ok {
				entry["variants"] = v
			}
		} else if v := effortVariants(g, m); v != nil {
			entry["variants"] = v
		}
		modelsMap[m] = entry
	}
	providers["kinerouter"] = existing

	if v, present := body["activeModel"]; present {
		if s, _ := v.(string); s == "" && v != nil {
			cfg["model"] = ""
		} else if s != "" {
			cfg["model"] = "kinerouter/" + strings.TrimPrefix(s, "kinerouter/")
		}
	} else if len(models) > 0 {
		cfg["model"] = "kinerouter/" + strings.TrimPrefix(models[0], "kinerouter/")
	}
	subagent := strings.TrimPrefix(firstNonEmpty(str(body, "subagentModel"), models[0]), "kinerouter/")
	if len(models) > 0 {
		agent := section(cfg, "agent")
		agent["explorer"] = map[string]any{
			"description": "Fast explorer subagent for codebase exploration",
			"mode":        "subagent",
			"model":       "kinerouter/" + subagent,
		}
	}
	if err := writeJSONFile(path, cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to apply settings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "OpenCode settings applied successfully!", "configPath": path})
}

// effortVariants builds opencode's per-model "variants" map ("alias/model" →
// same level set the dashboard picker offers). nil when the model does not
// reason, so nothing is written for it.
func effortVariants(g *Gateway, fullModelID string) map[string]any {
	i := strings.IndexByte(fullModelID, '/')
	if i <= 0 {
		return nil
	}
	provider := fullModelID[:i]
	if g.reg != nil {
		if id := g.reg.ResolveAlias(provider); id != "" {
			provider = id
		}
	}
	levels := providerdata.ThinkingLevels(provider, fullModelID[i+1:])
	if len(levels) == 0 {
		return nil
	}
	out := make(map[string]any, len(levels))
	for _, l := range levels {
		out[l] = map[string]any{"reasoningEffort": l}
	}
	return out
}

func (g *Gateway) handleCliOpencodePatch(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to patch settings"})
		return
	}
	path := opencodePath()
	cfg := readJSONMap(path)
	if cfg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file found"})
		return
	}
	if body["clearActiveModel"] == true && strings.HasPrefix(str(cfg, "model"), "kinerouter/") {
		cfg["model"] = ""
	}
	writeJSONFile(path, cfg)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Settings updated"})
}

func (g *Gateway) handleCliOpencodeDelete(w http.ResponseWriter, r *http.Request) {
	path := opencodePath()
	cfg := readJSONMap(path)
	if cfg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	modelToRemove := r.URL.Query().Get("model")
	providers := objMap(cfg["provider"])
	entry := objMap(providers["kinerouter"])
	if modelToRemove != "" && entry != nil {
		if models := objMap(entry["models"]); models != nil {
			delete(models, modelToRemove)
			if len(models) == 0 {
				delete(providers, "kinerouter")
				if strings.HasPrefix(str(cfg, "model"), "kinerouter/") {
					delete(cfg, "model")
				}
			} else if str(cfg, "model") == "kinerouter/"+modelToRemove {
				cfg["model"] = "kinerouter/" + sortedKeys(models)[0]
			}
		}
	} else {
		if providers != nil {
			delete(providers, "kinerouter")
		}
		if strings.HasPrefix(str(cfg, "model"), "kinerouter/") {
			delete(cfg, "model")
		}
	}
	if agent := objMap(cfg["agent"]); agent != nil {
		if explorer := objMap(agent["explorer"]); explorer != nil && strings.HasPrefix(str(explorer, "model"), "kinerouter/") {
			delete(agent, "explorer")
			if len(agent) == 0 {
				delete(cfg, "agent")
			}
		}
	}
	writeJSONFile(path, cfg)
	msg := "KineRouter settings removed from OpenCode"
	if modelToRemove != "" {
		msg = `Model "` + modelToRemove + `" removed`
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": msg})
}

// ---------------------------------------------------------------- pi

func piAgentPath() string { return homePath(".pi", "agent", "models.json") }
func piRootPath() string  { return homePath(".pi", "models.json") }

func piResolvePath() string {
	if _, err := os.Stat(piAgentPath()); err == nil {
		return piAgentPath()
	}
	if _, err := os.Stat(piRootPath()); err == nil {
		return piRootPath()
	}
	return piAgentPath()
}

func (g *Gateway) cliPiStatus() map[string]any {
	installed := toolInstalled("pi", piAgentPath()) || func() bool {
		_, err := os.Stat(piRootPath())
		return err == nil
	}()
	if !installed {
		return map[string]any{"installed": false, "config": nil, "message": "Pi CLI is not installed"}
	}
	path := piResolvePath()
	cfg := readJSONMap(path)
	has := false
	if providers := objMap(cfg["providers"]); providers != nil {
		if p := objMap(providers["kinerouter"]); p != nil && str(p, "baseUrl") != "" {
			has = true
		}
		for _, v := range providers {
			if p := objMap(v); p != nil && strings.Contains(str(p, "baseUrl"), itoa(int64(g.gatewayPort()))) {
				has = true
			}
		}
	}
	return map[string]any{"installed": true, "config": cfg, "hasKineRouter": has, "configPath": path}
}

func (g *Gateway) handleCliPiGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliPiStatus())
}

// piInputs keeps only the modalities pi knows, de-duplicated. Empty means the
// field is dropped rather than written as a list pi would reject.
func piInputs(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	out := []any{}
	for _, x := range arr {
		s, _ := x.(string)
		if (s != "text" && s != "image") || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// piThinkingLevels keeps the recognised levels; a null is kept (it means
// "this level is unavailable"), anything else is dropped.
func piThinkingLevels(v any) map[string]any {
	m := objMap(v)
	if m == nil {
		return nil
	}
	out := map[string]any{}
	for _, k := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if s, ok := m[k].(string); ok {
			out[k] = s
		} else if val, ok := m[k]; ok && val == nil {
			out[k] = nil
		}
	}
	return out
}

func (g *Gateway) handleCliPiPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL := str(body, "baseUrl")
	if baseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "baseUrl is required"}})
		return
	}
	path := piResolvePath()
	existing := readJSONMap(path)
	if existing == nil {
		existing = map[string]any{}
	}
	providers := section(existing, "providers")
	prevProvider := objMap(providers["kinerouter"])
	prevModels := map[string]map[string]any{}
	if prevProvider != nil {
		if arr, ok := prevProvider["models"].([]any); ok {
			for _, m := range arr {
				if s, ok := m.(string); ok {
					prevModels[s] = map[string]any{}
				} else if mm := objMap(m); mm != nil && str(mm, "id") != "" {
					prevModels[str(mm, "id")] = mm
				}
			}
		}
	}
	capNum := func(v any, fallback, max int64) int64 {
		n, ok := v.(float64)
		if !ok || n <= 0 || int64(n) > max {
			return fallback
		}
		return int64(n)
	}
	withDefaults := func(id string, src map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range prevModels[id] {
			out[k] = v
		}
		for k, v := range src {
			out[k] = v
		}
		name, _ := out["name"].(string)
		if name == "" || name == id {
			if prevName, _ := prevModels[id]["name"].(string); prevName != "" {
				out["name"] = prevName
			} else {
				out["name"] = id
			}
		}
		cw, ok := src["contextWindow"]
		if !ok {
			cw = prevModels[id]["contextWindow"]
		}
		mt, ok := src["maxTokens"]
		if !ok {
			mt = prevModels[id]["maxTokens"]
		}
		// 9router's pi defaults are 200000/64000; a resolved capacity replaces
		// them field by field, because a model may publish a window without an
		// output cap.
		ctxFallback, tokFallback := int64(200000), int64(64000)
		if lim := g.cliModelLimit(id); lim.Known {
			ctxFallback = lim.Context
			if lim.MaxOutput > 0 {
				tokFallback = lim.MaxOutput
			}
		}
		out["contextWindow"] = capNum(cw, ctxFallback, 2000000)
		out["maxTokens"] = capNum(mt, tokFallback, 200000)
		// Pi carries three optional capability fields; 9router re-adds only the
		// values it recognises and drops the rest.
		inputs, reasoning, levels := out["input"], out["reasoning"], out["thinkingLevelMap"]
		delete(out, "input")
		delete(out, "reasoning")
		delete(out, "thinkingLevelMap")
		if arr := piInputs(inputs); len(arr) > 0 {
			out["input"] = arr
		}
		if b, ok := reasoning.(bool); ok {
			out["reasoning"] = b
		}
		if m := piThinkingLevels(levels); len(m) > 0 {
			out["thinkingLevelMap"] = m
		}
		return out
	}
	var modelList []any
	if arr, ok := body["models"].([]any); ok && len(arr) > 0 {
		for _, m := range arr {
			if s, ok := m.(string); ok {
				modelList = append(modelList, withDefaults(s, map[string]any{"id": s}))
			} else if mm := objMap(m); mm != nil {
				id := firstNonEmpty(str(mm, "id"), "provider/model-id")
				src := map[string]any{}
				for k, v := range mm {
					src[k] = v
				}
				src["id"] = id
				modelList = append(modelList, withDefaults(id, src))
			}
		}
	} else {
		id := firstNonEmpty(str(body, "model"), "provider/model-id")
		modelList = []any{withDefaults(id, map[string]any{"id": id})}
	}
	entry := map[string]any{}
	for k, v := range prevProvider {
		entry[k] = v
	}
	entry["baseUrl"] = normalizeV1(baseURL)
	entry["apiKey"] = firstNonEmpty(str(body, "apiKey"), str(prevProvider, "apiKey"), g.resolveCliAPIKey(""))
	entry["api"] = firstNonEmpty(str(prevProvider, "api"), "openai-completions")
	entry["models"] = modelList
	providers["kinerouter"] = entry
	if err := writeJSONFile(path, existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Pi settings applied! Use /model in Pi to select the KineRouter model.", "configPath": path})
}

func (g *Gateway) handleCliPiDelete(w http.ResponseWriter, r *http.Request) {
	path := piResolvePath()
	existing := readJSONMap(path)
	if existing == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	if providers := objMap(existing["providers"]); providers != nil {
		if _, ok := providers["kinerouter"]; ok {
			delete(providers, "kinerouter")
			if len(providers) == 0 {
				delete(existing, "providers")
			}
			writeJSONFile(path, existing)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter removed from Pi"})
}

// ---------------------------------------------------------------- cline

func clineDir() string             { return homePath(".cline", "data") }
func clineGlobalStatePath() string { return filepath.Join(clineDir(), "globalState.json") }
func clineSecretsPath() string     { return filepath.Join(clineDir(), "secrets.json") }

func clineHasKineRouter(gs map[string]any) bool {
	if gs == nil {
		return false
	}
	isOpenAI := str(gs, "actModeApiProvider") == "openai" || str(gs, "planModeApiProvider") == "openai"
	u := str(gs, "openAiBaseUrl")
	return isOpenAI && (strings.Contains(u, "localhost") || strings.Contains(u, "127.0.0.1") || strings.Contains(u, "kinerouter"))
}

func (g *Gateway) cliClineStatus() map[string]any {
	if !toolInstalled("cline", clineGlobalStatePath()) {
		return map[string]any{"installed": false, "settings": nil, "message": "Cline CLI is not installed"}
	}
	gs := readJSONMap(clineGlobalStatePath())
	var settings map[string]any
	if gs != nil {
		settings = map[string]any{
			"actModeApiProvider": gs["actModeApiProvider"], "planModeApiProvider": gs["planModeApiProvider"],
			"openAiBaseUrl": gs["openAiBaseUrl"], "openAiModelId": gs["openAiModelId"],
		}
	}
	return map[string]any{"installed": true, "settings": settings, "hasKineRouter": clineHasKineRouter(gs), "globalStatePath": clineGlobalStatePath()}
}

func (g *Gateway) handleCliClineGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliClineStatus())
}

func (g *Gateway) handleCliClinePost(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update cline settings"})
		return
	}
	baseURL, apiKey, model := str(body, "baseUrl"), str(body, "apiKey"), str(body, "model")
	if baseURL == "" || apiKey == "" || model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl, apiKey and model are required"})
		return
	}
	gs := readJSONMap(clineGlobalStatePath())
	if gs == nil {
		gs = map[string]any{}
	}
	// Cline expects the base WITHOUT /v1.
	normalized := strings.TrimSuffix(baseURL, "/v1")
	gs["actModeApiProvider"] = "openai"
	gs["planModeApiProvider"] = "openai"
	gs["openAiBaseUrl"] = normalized
	gs["openAiModelId"] = model
	gs["planModeOpenAiModelId"] = model
	writeJSONFile(clineGlobalStatePath(), gs)

	secrets := readJSONMap(clineSecretsPath())
	if secrets == nil {
		secrets = map[string]any{}
	}
	secrets["openAiApiKey"] = apiKey
	writeJSONFile(clineSecretsPath(), secrets)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Cline settings applied successfully!", "globalStatePath": clineGlobalStatePath()})
}

func (g *Gateway) handleCliClineDelete(w http.ResponseWriter, r *http.Request) {
	gs := readJSONMap(clineGlobalStatePath())
	if gs == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No settings file to reset"})
		return
	}
	if str(gs, "actModeApiProvider") == "openai" {
		delete(gs, "openAiBaseUrl")
		delete(gs, "openAiModelId")
		delete(gs, "planModeOpenAiModelId")
		gs["actModeApiProvider"] = "cline"
		gs["planModeApiProvider"] = "cline"
	}
	writeJSONFile(clineGlobalStatePath(), gs)
	secrets := readJSONMap(clineSecretsPath())
	if secrets == nil {
		secrets = map[string]any{}
	}
	delete(secrets, "openAiApiKey")
	writeJSONFile(clineSecretsPath(), secrets)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter settings removed from Cline"})
}

// ---------------------------------------------------------------- kilo

func kiloAuthPath() string           { return homePath(".local", "share", "kilo", "auth.json") }
func kiloVscodeSettingsPath() string { return homePath(".config", "Code", "User", "settings.json") }

func kiloBaseURL(entry map[string]any) string {
	return firstNonEmpty(str(entry, "baseUrl"), str(entry, "baseURL"))
}

func kiloHasKineRouter(auth map[string]any) bool {
	if auth == nil {
		return false
	}
	entry := objMap(auth["openai-compatible"])
	if entry == nil {
		entry = objMap(auth["kinerouter"])
	}
	if entry == nil {
		return false
	}
	u := kiloBaseURL(entry)
	return strings.Contains(u, "localhost") || strings.Contains(u, "127.0.0.1") || strings.Contains(u, "kinerouter")
}

func (g *Gateway) cliKiloStatus() map[string]any {
	if !toolInstalled("kilo", kiloAuthPath()) {
		return map[string]any{"installed": false, "settings": nil, "message": "Kilo Code CLI is not installed"}
	}
	auth := readJSONMap(kiloAuthPath())
	var settings map[string]any
	if auth != nil {
		settings = map[string]any{"auth": sortedKeys(auth)}
	}
	return map[string]any{"installed": true, "settings": settings, "hasKineRouter": kiloHasKineRouter(auth), "authPath": kiloAuthPath()}
}

func (g *Gateway) handleCliKiloGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliKiloStatus())
}

func (g *Gateway) handleCliKiloPost(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update kilo settings"})
		return
	}
	baseURL, apiKey, model := str(body, "baseUrl"), str(body, "apiKey"), str(body, "model")
	if baseURL == "" || apiKey == "" || model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl, apiKey and model are required"})
		return
	}
	normalized := normalizeV1(baseURL)
	auth := readJSONMap(kiloAuthPath())
	if auth == nil {
		auth = map[string]any{}
	}
	auth["openai-compatible"] = map[string]any{"type": "api-key", "apiKey": apiKey, "baseUrl": normalized, "model": model}
	if err := writeJSONFile(kiloAuthPath(), auth); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update kilo settings"})
		return
	}
	// Best effort: VS Code extension settings.
	if vs := readJSONMap(kiloVscodeSettingsPath()); vs != nil {
		vs["kilocode.customProvider"] = map[string]any{"name": "KineRouter", "baseURL": normalized, "apiKey": apiKey}
		vs["kilocode.defaultModel"] = model
		writeJSONFile(kiloVscodeSettingsPath(), vs)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Kilo Code settings applied successfully!", "authPath": kiloAuthPath()})
}

func (g *Gateway) handleCliKiloDelete(w http.ResponseWriter, r *http.Request) {
	auth := readJSONMap(kiloAuthPath())
	if auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No settings file to reset"})
		return
	}
	delete(auth, "openai-compatible")
	delete(auth, "kinerouter")
	writeJSONFile(kiloAuthPath(), auth)
	if vs := readJSONMap(kiloVscodeSettingsPath()); vs != nil {
		delete(vs, "kilocode.customProvider")
		delete(vs, "kilocode.defaultModel")
		writeJSONFile(kiloVscodeSettingsPath(), vs)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter settings removed from Kilo Code"})
}

// ---------------------------------------------------------------- copilot

func copilotPath() string {
	home := homePath()
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(firstNonEmpty(os.Getenv("APPDATA"), home), "Code", "User", "chatLanguageModels.json")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User", "chatLanguageModels.json")
	}
	return filepath.Join(home, ".config", "Code", "User", "chatLanguageModels.json")
}

func copilotEntry(config []any) map[string]any {
	for _, e := range config {
		if m := objMap(e); m != nil && str(m, "name") == "KineRouter" {
			return m
		}
	}
	return nil
}

func (g *Gateway) handleCliCopilotGet(w http.ResponseWriter, r *http.Request) {
	path := copilotPath()
	cfg := readJSONArray(path)
	entry := copilotEntry(cfg)
	var currentModel, currentURL any
	if entry != nil {
		if arr, ok := entry["models"].([]any); ok && len(arr) > 0 {
			if m := objMap(arr[0]); m != nil {
				currentModel, currentURL = m["id"], m["url"]
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": true, "config": cfg, "hasKineRouter": entry != nil,
		"configPath": path, "currentModel": currentModel, "currentUrl": currentURL,
	})
}

func (g *Gateway) handleCliCopilotPost(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update copilot settings"})
		return
	}
	baseURL := str(body, "baseUrl")
	models := strArr(body["models"])
	if baseURL == "" || len(models) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and models are required"})
		return
	}
	path := copilotPath()
	cfg := readJSONArray(path)
	if cfg == nil {
		cfg = []any{}
	}
	endpoint := baseURL + "/chat/completions#models.ai.azure.com"
	keyToUse := g.resolveCliAPIKey(str(body, "apiKey"))
	entries := []any{}
	for _, id := range models {
		entries = append(entries, map[string]any{
			"id": id, "name": id, "url": endpoint, "toolCalling": true, "vision": false,
			"maxInputTokens": 128000, "maxOutputTokens": 16000,
		})
	}
	newEntry := map[string]any{"name": "KineRouter", "vendor": "azure", "apiKey": keyToUse, "models": entries}
	replaced := false
	for i, e := range cfg {
		if m := objMap(e); m != nil && str(m, "name") == "KineRouter" {
			cfg[i] = newEntry
			replaced = true
		}
	}
	if !replaced {
		cfg = append(cfg, newEntry)
	}
	if err := writeJSONFile(path, cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update copilot settings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Copilot settings applied! Reload VS Code to take effect.", "configPath": path})
}

func (g *Gateway) handleCliCopilotDelete(w http.ResponseWriter, r *http.Request) {
	path := copilotPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	cfg := readJSONArray(path)
	kept := []any{}
	for _, e := range cfg {
		if m := objMap(e); m == nil || str(m, "name") != "KineRouter" {
			kept = append(kept, e)
		}
	}
	writeJSONFile(path, kept)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter removed from Copilot config"})
}

// ---------------------------------------------------------------- claude

func claudeSettingsPath() string { return homePath(".claude", "settings.json") }
func claudeJSONPath() string     { return homePath(".claude.json") }

var claudeExaEntry = map[string]any{"type": "http", "url": "https://mcp.exa.ai/mcp"}

// claudeTiers are the four model slots Claude Code resolves by name, in the
// order 9router-go writes them (clientconfig/claude.go).
var claudeTiers = []struct{ tier, env string }{
	{"fable", "ANTHROPIC_DEFAULT_FABLE_MODEL"},
	{"opus", "ANTHROPIC_DEFAULT_OPUS_MODEL"},
	{"sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL"},
	{"haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL"},
}

// claudeResetEnvKeys is every env key Apply manages; Reset removes exactly
// these. API_TIMEOUT_MS is ours (the TUI quick setup writes it), so a reset has
// to clear it too — 9router-go never writes it, hence it is absent upstream.
var claudeResetEnvKeys = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"API_TIMEOUT_MS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW",
}

func (g *Gateway) cliClaudeStatus() map[string]any {
	path := claudeSettingsPath()
	if !toolInstalled("claude", path) {
		return map[string]any{"installed": false, "settings": nil, "message": "Claude CLI is not installed"}
	}
	settings := readJSONMap(path)
	env := objMap(settings["env"])
	claudeJSON := readJSONMap(claudeJSONPath())
	exaOn := false
	if servers := objMap(claudeJSON["mcpServers"]); servers != nil {
		_, exaOn = servers["exa"]
	}
	// The dashboard form is driven by these fields, mirroring 9router-go's
	// adapter Current(): a tier or model the file does not carry stays absent,
	// so the UI can tell "unset" from "empty" and leave the key alone on Apply.
	tiers := map[string]any{}
	for _, t := range claudeTiers {
		if v := str(env, t.env); v != "" {
			tiers[t.tier] = v
		}
	}
	out := map[string]any{
		"installed": true, "settings": settings,
		"hasKineRouter": env != nil && str(env, "ANTHROPIC_BASE_URL") != "",
		"exaMcpEnabled": exaOn, "settingsPath": path, "tiers": tiers,
	}
	if base := str(env, "ANTHROPIC_BASE_URL"); base != "" {
		out["baseUrl"] = base
	}
	if m := str(settings, "model"); m != "" {
		out["model"] = m
	}
	if v, ok := settings["autoCompactWindow"]; ok {
		out["autoCompactWindow"] = numString(v)
	} else if v := str(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW"); v != "" {
		out["autoCompactWindow"] = v
	}
	return out
}

func (g *Gateway) handleCliClaudeGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliClaudeStatus())
}

// writeClaudeJSONMcp toggles the `exa` entry in ~/.claude.json (the file the
// Claude Code CLI actually reads mcpServers from).
func writeClaudeJSONMcp(enable bool) {
	path := claudeJSONPath()
	data := readJSONMap(path)
	if data == nil {
		data = map[string]any{}
	}
	servers := objMap(data["mcpServers"])
	if enable {
		if servers == nil {
			servers = map[string]any{}
		}
		servers["exa"] = claudeExaEntry
		data["mcpServers"] = servers
	} else if servers != nil {
		delete(servers, "exa")
		if len(servers) == 0 {
			delete(data, "mcpServers")
		}
	}
	writeJSONFile(path, data)
}

// handleCliClaudePost writes ~/.claude/settings.json the way 9router-go's
// clientconfig adapter does. Two request shapes are accepted:
//
//	{baseUrl, apiKey, model, tiers{fable,opus,sonnet,haiku}, autoCompactWindow}
//	{env: {ANTHROPIC_*}}   (older dashboard and the TUI's quick setup)
//
// The env map wins for any key it carries, so a caller that already knows the
// exact variable names is never surprised. An empty value never deletes:
// 9router-go leaves a tier the caller did not name exactly as the user had it,
// because "no value" and "the dashboard could not resolve one" look alike.
func (g *Gateway) handleCliClaudePost(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update claude settings"})
		return
	}
	env := objMap(body["env"])
	if env == nil {
		env = map[string]any{}
	}
	if body["env"] != nil && objMap(body["env"]) == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid env object"})
		return
	}

	base := strings.TrimSpace(firstNonEmpty(str(body, "baseUrl"), str(env, "ANTHROPIC_BASE_URL")))
	token := strings.TrimSpace(firstNonEmpty(str(body, "apiKey"), str(env, "ANTHROPIC_AUTH_TOKEN")))
	model := strings.TrimSpace(str(body, "model"))
	compact := numString(body["autoCompactWindow"])
	compactFromEnv := false
	if compact == "" {
		compact = numString(env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"])
		compactFromEnv = compact != ""
	}
	bodyTiers := objMap(body["tiers"])
	tierValues := map[string]string{}
	for _, t := range claudeTiers {
		v := strings.TrimSpace(str(bodyTiers, t.tier))
		if v == "" {
			v = strings.TrimSpace(str(env, t.env))
		}
		tierValues[t.env] = v
	}

	path := claudeSettingsPath()
	current := readJSONMap(path)
	if current == nil {
		current = map[string]any{}
	}
	merged := map[string]any{}
	for k, v := range current {
		merged[k] = v
	}
	merged["hasCompletedOnboarding"] = true
	mergedEnv := map[string]any{}
	for k, v := range objMap(current["env"]) {
		mergedEnv[k] = v
	}
	// The env-map shape writes exactly the keys it carries (the TUI relies on
	// API_TIMEOUT_MS landing verbatim). An empty value never clears: callers
	// send "" for "no change", not for "delete this".
	for k, v := range env {
		if sv, ok := v.(string); ok && sv == "" {
			continue
		}
		mergedEnv[k] = v
	}
	if base != "" {
		mergedEnv["ANTHROPIC_BASE_URL"] = normalizeV1(base)
	}
	// 9router never overwrites a token the file already holds: an existing
	// ANTHROPIC_AUTH_TOKEN is the user's own credential, not ours to replace.
	// Only an empty file takes the supplied one.
	if prev := str(objMap(current["env"]), "ANTHROPIC_AUTH_TOKEN"); prev != "" {
		mergedEnv["ANTHROPIC_AUTH_TOKEN"] = prev
	} else if token != "" {
		mergedEnv["ANTHROPIC_AUTH_TOKEN"] = token
	}
	for _, t := range claudeTiers {
		if v := tierValues[t.env]; v != "" {
			mergedEnv[t.env] = v
		}
	}

	// autoCompactWindow is the top-level key Claude Code reads; the env var only
	// exists for the window the resolver knows, so the CLI compacts at the real
	// boundary of a proxied model instead of its built-in name-based guess.
	derived := g.claudeCompactWindow(model, tierValues)
	if compact != "" && compact != "default" && compact != "0" {
		if n, err := strconv.Atoi(compact); err == nil {
			merged["autoCompactWindow"] = n
		} else {
			merged["autoCompactWindow"] = compact
		}
		if derived > 0 {
			mergedEnv["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.Itoa(derived)
		} else if !compactFromEnv {
			// Nothing to resolve against: honour the picked window rather than
			// leaving the CLI on its built-in assumption.
			mergedEnv["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = compact
		}
	} else {
		delete(merged, "autoCompactWindow")
		delete(mergedEnv, "CLAUDE_CODE_AUTO_COMPACT_WINDOW")
		if derived > 0 {
			mergedEnv["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.Itoa(derived)
		}
	}

	// The primary model is a top-level key. Clearing it only removes ours: a
	// "model" the user set for the official API is not ours to delete.
	if model != "" {
		merged["model"] = model
	} else if g.claudePointsAtGateway(merged, base) {
		delete(merged, "model")
	}

	merged["env"] = mergedEnv
	if err := writeJSONFile(path, merged); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update claude settings"})
		return
	}
	// 9router syncs the exa entry on every apply, so a form that omits the
	// field still removes a server it no longer wants.
	writeClaudeJSONMcp(body["exaMcpEnabled"] == true)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Settings updated successfully"})
}

func (g *Gateway) handleCliClaudeDelete(w http.ResponseWriter, r *http.Request) {
	path := claudeSettingsPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No settings file to reset"})
		return
	}
	current := readJSONMap(path)
	if current == nil {
		current = map[string]any{}
	}
	env := objMap(current["env"])
	owned := g.claudePointsAtGateway(current, "")
	if env != nil {
		for _, k := range claudeResetEnvKeys {
			delete(env, k)
		}
		if len(env) == 0 {
			delete(current, "env")
		}
	}
	delete(current, "autoCompactWindow")
	if owned {
		delete(current, "model")
	}
	writeClaudeJSONMcp(false)
	writeJSONFile(path, current)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Settings reset successfully"})
}

// claudePointsAtGateway reports whether a settings map already exposes the
// gateway. Without an explicit base URL the managed port is the only signal.
func (g *Gateway) claudePointsAtGateway(settings map[string]any, base string) bool {
	raw := strings.TrimRight(strings.TrimSpace(str(objMap(settings["env"]), "ANTHROPIC_BASE_URL")), "/")
	if raw == "" || strings.Contains(raw, "api.anthropic.com") {
		return false
	}
	norm := func(s string) string { return strings.Replace(s, "127.0.0.1", "localhost", 1) }
	if base != "" {
		target := strings.TrimRight(strings.TrimSpace(base), "/")
		want := norm(target)
		wantV1 := norm(normalizeV1(target))
		return norm(raw) == want || norm(raw) == wantV1
	}
	return strings.Contains(raw, itoa(int64(g.gatewayPort()))) || strings.Contains(raw, "kinerouter")
}

// cliModelLimit is the resolved capacity of a model id, for the client configs
// that have to state a context window themselves (codex, opencode, crush, pi,
// omp). Known is false when nothing resolved, which is what keeps a writer from
// inventing a number 9router-go would not write.
type cliModelLimit struct {
	Context   int64
	MaxOutput int64
	Known     bool
}

func (g *Gateway) cliModelLimit(model string) cliModelLimit {
	m := strings.TrimSuffix(strings.TrimSpace(model), "[1m]")
	if m == "" || g == nil {
		return cliModelLimit{}
	}
	ctx := g.contextWindowFor(m)
	if ctx <= 0 {
		return cliModelLimit{}
	}
	provider, name := splitModelRef(m)
	return cliModelLimit{
		Context:   ctx,
		MaxOutput: providerdata.CapabilitiesFor(provider, name).MaxOutput,
		Known:     true,
	}
}

// claudeCompactWindow is the narrowest context window among the models the form
// names, clamped to Claude Code's 100K–1M range. Claude Code applies one env
// value to every tier, so the narrowest is the only choice that never
// overshoots a real limit. Zero means "nothing resolved, write nothing".
func (g *Gateway) claudeCompactWindow(model string, tiers map[string]string) int {
	best := 0
	consider := func(m string) {
		m = strings.TrimSuffix(strings.TrimSpace(m), "[1m]")
		if m == "" || g == nil {
			return
		}
		w := int(g.contextWindowFor(m))
		if w <= 0 {
			return
		}
		if best == 0 || w < best {
			best = w
		}
	}
	consider(model)
	for _, t := range claudeTiers {
		consider(tiers[t.env])
	}
	if best == 0 {
		return 0
	}
	if best > 1000000 {
		return 1000000
	}
	if best < 100000 {
		return 100000
	}
	return best
}

// ---------------------------------------------------------------- devin

func devinCandidatePaths() []string {
	home := homePath()
	if runtime.GOOS == "windows" {
		localApp := firstNonEmpty(os.Getenv("LOCALAPPDATA"), filepath.Join(home, "AppData", "Local"))
		return []string{
			filepath.Join(localApp, "devin", "cli", "bin", "devin.exe"),
			filepath.Join(home, ".local", "bin", "devin.exe"),
			filepath.Join(home, "scoop", "shims", "devin.exe"),
			filepath.Join(localApp, "Programs", "devin", "devin.exe"),
		}
	}
	return []string{
		filepath.Join(home, ".local", "share", "devin", "bin", "devin"),
		filepath.Join(home, ".devin", "bin", "devin"),
		filepath.Join(home, ".local", "bin", "devin"),
		"/opt/homebrew/bin/devin",
		"/usr/local/bin/devin",
		"/usr/bin/devin",
	}
}

func (g *Gateway) cliDevinStatus() map[string]any {
	var source any
	if p, err := exec.LookPath("devin"); err == nil {
		source = "path"
		_ = p
	} else {
		for _, c := range devinCandidatePaths() {
			if _, err := os.Stat(c); err == nil {
				source = c
				break
			}
		}
	}
	if source == nil {
		return map[string]any{
			"installed":  false,
			"message":    "Devin CLI is not installed. Install it from https://cli.devin.ai and run `devin auth login`.",
			"installUrl": "https://cli.devin.ai",
		}
	}
	var version any
	if out, err := exec.Command("devin", "--version").Output(); err == nil {
		if line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]; line != "" {
			version = line
		}
	}
	return map[string]any{
		"installed": true, "source": source, "version": version,
		"message": "Devin CLI detected. Make sure `devin auth login` has been run.",
	}
}

func (g *Gateway) handleCliDevin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliDevinStatus())
}

// ---------------------------------------------------------------- codex profiles

var codexProfileNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func validCodexProfileName(name string) bool {
	return len(name) <= 64 && codexProfileNameRe.MatchString(name) && strings.ToLower(name) != "config"
}

func validCodexModel(model string) bool {
	return model != "" && len(model) <= 256 && !strings.ContainsAny(model, "\r\n\"")
}

func codexDir() string { return homePath(".codex") }

func (g *Gateway) handleCliCodexProfilesGet(w http.ResponseWriter, r *http.Request) {
	dir := codexDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"profiles": []any{}})
		return
	}
	type profile struct{ Name, Model, Command string }
	profiles := []profile{}
	// Go's RE2 has no backreferences; take the first model line and strip the
	// surrounding quote instead of matching a matched pair.
	modelRe := regexp.MustCompile(`(?m)^\s*model\s*=\s*["']([^"\n]+)["']`)
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".config.toml")
		if !strings.HasSuffix(e.Name(), ".config.toml") || e.Name() == "config.toml" {
			continue
		}
		model := ""
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			if m := modelRe.FindSubmatch(b); m != nil {
				model = string(m[1])
			}
		}
		profiles = append(profiles, profile{name, model, "codex -p " + name})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	out := make([]any, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, map[string]any{"name": p.Name, "model": p.Model, "command": p.Command})
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

func (g *Gateway) handleCliCodexProfileCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	decodeBody(r, &body)
	name := strings.ToLower(strings.TrimSpace(str(body, "name")))
	if !validCodexProfileName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Profile name can only contain alphanumeric characters, dashes, underscores and cannot be 'config'"})
		return
	}
	model := strings.TrimSpace(str(body, "model"))
	if !validCodexModel(model) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid model name"})
		return
	}
	content := "# codex -p " + name + "\nmodel = \"" + model + "\"\nmodel_provider = \"kinerouter\"\n"
	if err := writeAtomic(filepath.Join(codexDir(), name+".config.toml"), []byte(content)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to save profile"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "profile": map[string]any{
		"name": name, "model": model, "command": "codex -p " + name,
	}})
}

func (g *Gateway) handleCliCodexProfileDelete(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	decodeBody(r, &body)
	name := strings.ToLower(strings.TrimSpace(str(body, "name")))
	if !validCodexProfileName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid profile name"})
		return
	}
	err := os.Remove(filepath.Join(codexDir(), name+".config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Profile not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to delete profile"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Profile " + name + " deleted"})
}

// ---------------------------------------------------------------- openclaw

func openclawPath() string { return homePath(".openclaw", "openclaw.json") }

// resolveAgentModel flattens agents[].model, which is a string (legacy) or
// {primary, fallbacks}.
func resolveAgentModel(m any) string {
	if s, ok := m.(string); ok {
		return s
	}
	if mm := objMap(m); mm != nil {
		return str(mm, "primary")
	}
	return ""
}

func (g *Gateway) cliOpenclawStatus() map[string]any {
	path := openclawPath()
	if !toolInstalled("openclaw", path) {
		return map[string]any{"installed": false, "settings": nil, "message": "Open Claw CLI is not installed"}
	}
	settings := readJSONMap(path)
	has := false
	if models := objMap(settings["models"]); models != nil {
		if providers := objMap(models["providers"]); providers != nil {
			_, has = providers["kinerouter"]
		}
	}
	agents := []any{}
	if a := objMap(settings["agents"]); a != nil {
		if list, ok := a["list"].([]any); ok {
			for _, item := range list {
				agent := objMap(item)
				if agent == nil {
					agents = append(agents, item)
					continue
				}
				enriched := map[string]any{}
				for k, v := range agent {
					enriched[k] = v
				}
				enriched["model"] = resolveAgentModel(agent["model"])
				var current any
				if dir, ok := agent["agentDir"].(string); ok && dir != "" {
					if m := readJSONMap(filepath.Join(dir, "models.json")); m != nil {
						if p := objMap(objMap(m["providers"])["kinerouter"]); p != nil {
							if arr, ok := p["models"].([]any); ok && len(arr) > 0 {
								current = objMap(arr[0])["id"]
							}
						}
					}
				}
				enriched["currentModel"] = current
				agents = append(agents, enriched)
			}
		}
	}
	return map[string]any{"installed": true, "settings": settings, "agents": agents, "hasKineRouter": has, "settingsPath": path}
}

func (g *Gateway) handleCliOpenclawGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliOpenclawStatus())
}

func writeAgentModels(dir, model, baseURL, apiKey string) {
	path := filepath.Join(dir, "models.json")
	existing := readJSONMap(path)
	if existing == nil {
		existing = map[string]any{}
	}
	providers := section(existing, "providers")
	name := model
	if i := strings.LastIndex(model, "/"); i >= 0 {
		name = model[i+1:]
	}
	providers["kinerouter"] = map[string]any{
		"baseUrl": baseURL, "apiKey": firstNonEmpty(apiKey, "your_api_key"),
		"api":    "openai-completions",
		"models": []any{map[string]any{"id": model, "name": name}},
	}
	writeJSONFile(path, existing)
}

func (g *Gateway) handleCliOpenclawPost(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update openclaw settings"})
		return
	}
	baseURL, model := str(body, "baseUrl"), str(body, "model")
	if baseURL == "" || model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and model are required"})
		return
	}
	agentModels := objMap(body["agentModels"])
	path := openclawPath()
	settings := readJSONMap(path)
	if settings == nil {
		settings = map[string]any{}
	}
	agents := section(settings, "agents")
	defaults := section(agents, "defaults")
	defaultModel := section(defaults, "model")
	allow := section(defaults, "models")
	models := section(settings, "models")
	providers := section(models, "providers")

	normalized := cliToolBase("openclaw", baseURL)
	fullModel := "kinerouter/" + model
	for k := range allow {
		if strings.HasPrefix(k, "kinerouter/") {
			delete(allow, k)
		}
	}
	defaultModel["primary"] = fullModel
	allModels := map[string]bool{model: true}
	for _, v := range agentModels {
		if s, _ := v.(string); s != "" {
			allModels[s] = true
		}
	}
	ids := make([]string, 0, len(allModels))
	for m := range allModels {
		allow["kinerouter/"+m] = map[string]any{}
		ids = append(ids, m)
	}
	sort.Strings(ids)

	if list, ok := agents["list"].([]any); ok {
		cleaned := make([]any, 0, len(list))
		for _, item := range list {
			agent := objMap(item)
			if agent == nil {
				cleaned = append(cleaned, item)
				continue
			}
			if strings.HasPrefix(resolveAgentModel(agent["model"]), "kinerouter/") {
				copyAgent := map[string]any{}
				for k, v := range agent {
					if k != "model" {
						copyAgent[k] = v
					}
				}
				cleaned = append(cleaned, copyAgent)
			} else {
				cleaned = append(cleaned, agent)
			}
		}
		agents["list"] = cleaned
	}

	nameOf := func(m string) string {
		if i := strings.LastIndex(m, "/"); i >= 0 {
			return m[i+1:]
		}
		return m
	}
	modelEntries := make([]any, 0, len(ids))
	for _, id := range ids {
		modelEntries = append(modelEntries, map[string]any{"id": id, "name": nameOf(id)})
	}
	providers["kinerouter"] = map[string]any{
		"baseUrl": normalized, "apiKey": firstNonEmpty(str(body, "apiKey"), "your_api_key"),
		"api": "openai-completions", "models": modelEntries,
	}
	if list, ok := agents["list"].([]any); ok {
		for i, item := range list {
			agent := objMap(item)
			if agent == nil {
				continue
			}
			if m, ok := agentModels[str(agent, "id")].(string); ok && m != "" {
				agent["model"] = "kinerouter/" + m
				list[i] = agent
			}
		}
		agents["list"] = list
		for _, item := range list {
			agent := objMap(item)
			if agent == nil {
				continue
			}
			dir, _ := agent["agentDir"].(string)
			if dir == "" {
				continue
			}
			modelToWrite := model
			if m, ok := agentModels[str(agent, "id")].(string); ok && m != "" {
				modelToWrite = m
			}
			writeAgentModels(dir, modelToWrite, normalized, str(body, "apiKey"))
		}
	}
	if err := writeJSONFile(path, settings); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update openclaw settings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Open Claw settings applied successfully!", "settingsPath": path})
}

func (g *Gateway) handleCliOpenclawDelete(w http.ResponseWriter, r *http.Request) {
	path := openclawPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No settings file to reset"})
		return
	}
	settings := readJSONMap(path)
	if settings == nil {
		settings = map[string]any{}
	}
	if models := objMap(settings["models"]); models != nil {
		if providers := objMap(models["providers"]); providers != nil {
			delete(providers, "kinerouter")
			if len(providers) == 0 {
				delete(models, "providers")
			}
		}
	}
	agents := objMap(settings["agents"])
	if agents != nil {
		if defaults := objMap(agents["defaults"]); defaults != nil {
			if allow := objMap(defaults["models"]); allow != nil {
				for k := range allow {
					if strings.HasPrefix(k, "kinerouter/") {
						delete(allow, k)
					}
				}
				if len(allow) == 0 {
					delete(defaults, "models")
				}
			}
			if dm := objMap(defaults["model"]); dm != nil {
				if strings.HasPrefix(str(dm, "primary"), "kinerouter/") {
					delete(dm, "primary")
				}
			}
		}
	}
	writeJSONFile(path, settings)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter settings removed successfully"})
}

// ---------------------------------------------------------------- misc

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// numString renders a scalar the way String(v) would for the auto-compact
// window (a number or numeric string both end up as a decimal string).
func numString(v any) string {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return itoa(int64(n))
		}
		return strings.TrimRight(strings.TrimRight(formatFloat(n), "0"), ".")
	case string:
		return n
	}
	return ""
}

func formatFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
