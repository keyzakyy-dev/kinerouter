package gateway

// TOML- and YAML-backed CLI tool config writers (codex, forge, codewhale,
// jcode, deepseek-tui, grok-build, hermes, omp). Split from clitools.go only
// because of size.
//
// Security: same posture as clitools.go — every path is home-relative and
// fixed, request input only ever lands as a config value, writes go through
// writeAtomic (0600).

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ------------------------------------------------------------- toml helpers

func parseTomlMap(content string) map[string]any {
	m := map[string]any{}
	if strings.TrimSpace(content) == "" {
		return m
	}
	if toml.Unmarshal([]byte(content), &m) != nil {
		return map[string]any{}
	}
	return m
}

func stringifyToml(m map[string]any) (string, error) {
	b, err := toml.Marshal(m)
	return string(b), err
}

func tomlSection(m map[string]any, key string) map[string]any {
	if s, ok := m[key].(map[string]any); ok {
		return s
	}
	// go-toml decodes nested tables as map[string]any, but be tolerant.
	out := map[string]any{}
	m[key] = out
	return out
}

// ---------------------------------------------------------------- codex

func codexConfigPath() string { return homePath(".codex", "config.toml") }
func codexAuthPath() string   { return homePath(".codex", "auth.json") }

func codexHasKineRouter(content string) bool {
	return strings.Contains(content, `model_provider = "kinerouter"`) ||
		strings.Contains(content, "[model_providers.kinerouter]")
}

func (g *Gateway) cliCodexStatus() map[string]any {
	raw := readTextOrEmpty(codexConfigPath())
	if !toolInstalled("codex", codexConfigPath()) {
		return map[string]any{"installed": false, "config": nil, "message": "Codex CLI is not installed"}
	}
	var cfg any
	if strings.TrimSpace(raw) != "" {
		cfg = parseTomlMap(raw)
	}
	return map[string]any{
		"installed": true, "config": cfg, "hasKineRouter": codexHasKineRouter(raw),
		"configPath": codexConfigPath(),
	}
}

func (g *Gateway) handleCliCodexGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliCodexStatus())
}

func (g *Gateway) handleCliCodexPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL, apiKey, model := str(body, "baseUrl"), str(body, "apiKey"), str(body, "model")
	if baseURL == "" || apiKey == "" || model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl, apiKey and model are required"})
		return
	}
	parsed := parseTomlMap(readTextOrEmpty(codexConfigPath()))
	parsed["model"] = model
	parsed["model_provider"] = "kinerouter"
	tomlSection(parsed, "model_providers")["kinerouter"] = map[string]any{
		"name":     "KineRouter",
		"base_url": normalizeV1(baseURL),
		"wire_api": "responses",
		// Custom providers ignore auth.json — the key must ride as a static header.
		"http_headers": map[string]any{"Authorization": "Bearer " + apiKey},
	}
	agents := tomlSection(parsed, "agents")
	delete(agents, "subagent")
	agents["default_subagent_model"] = firstNonEmpty(str(body, "subagentModel"), model)
	content, err := stringifyToml(parsed)
	if err != nil || writeAtomic(codexConfigPath(), []byte(content)) != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update codex settings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Codex settings applied successfully!", "configPath": codexConfigPath()})
}

func (g *Gateway) handleCliCodexDelete(w http.ResponseWriter, r *http.Request) {
	path := codexConfigPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	parsed := parseTomlMap(readTextOrEmpty(path))
	if str(parsed, "model_provider") == "kinerouter" {
		delete(parsed, "model")
		delete(parsed, "model_provider")
	}
	if mp := objMap(parsed["model_providers"]); mp != nil {
		delete(mp, "kinerouter")
	}
	if a := objMap(parsed["agents"]); a != nil {
		delete(a, "default_subagent_model")
		delete(a, "subagent")
	}
	content, err := stringifyToml(parsed)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to reset codex settings"})
		return
	}
	writeAtomic(path, []byte(content))

	authPath := codexAuthPath()
	if auth := readJSONMap(authPath); auth != nil {
		delete(auth, "OPENAI_API_KEY")
		delete(auth, "auth_mode")
		if len(auth) == 0 {
			os.Remove(authPath)
		} else {
			writeJSONFile(authPath, auth)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter settings removed successfully"})
}

// ---------------------------------------------------------------- forge / codewhale

// simpleOpenAIWriter builds the forge-shape tool: one `[openai]` table with a
// fixed header comment.
func (g *Gateway) simpleOpenAIWriter(w http.ResponseWriter, r *http.Request, dir, header, label string) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL := str(body, "baseUrl")
	if baseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "baseUrl is required"}})
		return
	}
	path := filepath.Join(dir, "config.toml")
	os.MkdirAll(dir, 0o700)
	existing := parseTomlMap(readTextOrEmpty(path))
	entry := map[string]any{"base_url": normalizeV1(baseURL)}
	entry["api_key"] = g.resolveCliAPIKey(str(body, "apiKey"))
	entry["model"] = firstNonEmpty(str(body, "model"), "provider/model-id")
	existing["openai"] = entry
	content, err := stringifyToml(existing)
	if err != nil || writeAtomic(path, []byte(header+content)) != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": "Failed to update settings"}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": label + " settings applied successfully!", "configPath": path})
}

func (g *Gateway) simpleOpenAIReset(w http.ResponseWriter, path, label string) {
	path = filepath.Join(path, "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	existing := parseTomlMap(string(raw))
	delete(existing, "openai")
	if len(existing) == 0 {
		os.Remove(path)
	} else if content, err := stringifyToml(existing); err == nil {
		writeAtomic(path, []byte(content))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter removed from " + label})
}

func forgeSimpleStatus(cmd, dir string, headerMarker string) map[string]any {
	path := filepath.Join(dir, "config.toml")
	raw := readTextOrEmpty(path)
	if !toolInstalled(cmd, path) {
		return map[string]any{"installed": false, "config": nil, "message": "CLI is not installed"}
	}
	var cfg any
	if strings.TrimSpace(raw) != "" {
		cfg = parseTomlMap(raw)
	}
	has := strings.Contains(raw, headerMarker) || localhostRe.MatchString(raw)
	return map[string]any{"installed": true, "config": cfg, "hasKineRouter": has, "configPath": path}
}

func (g *Gateway) cliForgeStatus() map[string]any {
	return forgeSimpleStatus("forge", homePath(".forge"), "managed by KineRouter")
}

func (g *Gateway) handleCliForgeGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliForgeStatus())
}

func (g *Gateway) handleCliForgePost(w http.ResponseWriter, r *http.Request) {
	g.simpleOpenAIWriter(w, r, homePath(".forge"), "# Forge config — managed by KineRouter\n\n", "ForgeCode")
}

func (g *Gateway) handleCliForgeDelete(w http.ResponseWriter, r *http.Request) {
	g.simpleOpenAIReset(w, homePath(".forge"), "ForgeCode")
}

func (g *Gateway) cliCodewhaleStatus() map[string]any {
	s := forgeSimpleStatus("codewhale", homePath(".codewhale"), "managed by KineRouter")
	if s["installed"] == false {
		s["message"] = "CodeWhale CLI is not installed"
	}
	return s
}

func (g *Gateway) handleCliCodewhaleGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliCodewhaleStatus())
}

func (g *Gateway) handleCliCodewhalePost(w http.ResponseWriter, r *http.Request) {
	g.simpleOpenAIWriter(w, r, homePath(".codewhale"), "# CodeWhale config — managed by KineRouter\n\n", "CodeWhale")
}

func (g *Gateway) handleCliCodewhaleDelete(w http.ResponseWriter, r *http.Request) {
	g.simpleOpenAIReset(w, homePath(".codewhale"), "CodeWhale")
}

// ---------------------------------------------------------------- deepseek-tui

func deepseekPath() string { return homePath(".deepseek", "config.toml") }

func (g *Gateway) cliDeepseekStatus() map[string]any {
	path := deepseekPath()
	if !toolInstalled("deepseek", path) {
		return map[string]any{"installed": false, "settings": nil, "message": "DeepSeek TUI is not installed"}
	}
	raw := readTextOrEmpty(path)
	settings := map[string]any{}
	if p := parseTomlMap(raw); p != nil {
		settings["provider"] = p["provider"]
		if provs := objMap(p["providers"]); provs != nil {
			settings["providers.openai"] = provs["openai"]
		}
	}
	has := strAny(settings["provider"]) == "openai"
	if open := objMap(settings["providers.openai"]); open != nil {
		has = has && localhostRe.MatchString(str(open, "base_url"))
	} else {
		has = false
	}
	return map[string]any{"installed": true, "settings": settings, "hasKineRouter": has, "configPath": path}
}

var localhostRe = regexp.MustCompile(`localhost|127\.0\.0\.1|0\.0\.0\.0`)

func (g *Gateway) handleCliDeepseekGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliDeepseekStatus())
}

func (g *Gateway) handleCliDeepseekPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL, model := str(body, "baseUrl"), str(body, "model")
	if baseURL == "" || model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and model are required"})
		return
	}
	// Literal template, exactly as the JS builds it.
	content := "provider = \"openai\"\n\n[providers.openai]\n" +
		"base_url = \"" + cliToolBase("deepseek-tui", baseURL) + "\"\n" +
		"api_key = \"" + g.resolveCliAPIKey(str(body, "apiKey")) + "\"\n" +
		"model = \"" + model + "\"\n"
	path := deepseekPath()
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := writeAtomic(path, []byte(content)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update deepseek-tui settings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "DeepSeek TUI settings applied successfully!", "configPath": path})
}

func (g *Gateway) handleCliDeepseekDelete(w http.ResponseWriter, r *http.Request) {
	path := deepseekPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	writeAtomic(path, []byte("provider = \"deepseek\"\n"))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "kinerouter config reset to DeepSeek defaults"})
}

// ---------------------------------------------------------------- jcode

func jcodeDir() string        { return homePath(".jcode") }
func jcodeConfigPath() string { return filepath.Join(jcodeDir(), "config.toml") }

func jcodeEnvPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = homePath(".config")
	}
	return filepath.Join(dir, "jcode", "provider-kinerouter.env")
}

func jcodeHasKineRouter(m map[string]any) bool {
	provs := objMap(m["providers"])
	if provs == nil {
		return false
	}
	if _, ok := provs["kinerouter"]; ok {
		return true
	}
	for _, v := range provs {
		if p := objMap(v); p != nil && localhostRe.MatchString(str(p, "base_url")) {
			return true
		}
	}
	return false
}

func (g *Gateway) cliJcodeStatus() map[string]any {
	if !toolInstalled("jcode", jcodeDir()) {
		return map[string]any{"installed": false, "message": "jcode not installed. Install via: curl -fsSL https://raw.githubusercontent.com/1jehuang/jcode/master/scripts/install.sh | bash"}
	}
	cfg := parseTomlMap(readTextOrEmpty(jcodeConfigPath()))
	return map[string]any{"installed": true, "config": cfg, "hasKineRouter": jcodeHasKineRouter(cfg), "configPath": jcodeConfigPath()}
}

func (g *Gateway) handleCliJcodeGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliJcodeStatus())
}

func readProviderEnv() map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(readTextOrEmpty(jcodeEnvPath()), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.Index(trimmed, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		value := strings.TrimSpace(trimmed[eq+1:])
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		env[key] = value
	}
	return env
}

func writeProviderEnv(env map[string]string) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("# jcode provider environment variables\n")
	for _, k := range keys {
		sb.WriteString(k + "=\"" + env[k] + "\"\n")
	}
	path := jcodeEnvPath()
	os.MkdirAll(filepath.Dir(path), 0o700)
	writeAtomic(path, []byte(sb.String()))
}

func (g *Gateway) handleCliJcodePost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL, apiKey := str(body, "baseUrl"), str(body, "apiKey")
	if baseURL == "" || apiKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and apiKey are required"})
		return
	}
	models := strArr(body["models"])
	cfg := parseTomlMap(readTextOrEmpty(jcodeConfigPath()))
	provs := tomlSection(cfg, "providers")
	provs["kinerouter"] = map[string]any{
		"type": "openai-compatible", "base_url": cliToolBase("jcode", baseURL), "auth": "bearer",
		"api_key_env": "JCODE_9ROUTER_API_KEY", "env_file": "provider-kinerouter.env",
		"default_model": firstNonEmpty(func() string {
			if len(models) > 0 {
				return models[0]
			}
			return ""
		}(), "cc/claude-opus-4-7"),
		"requires_api_key": true,
	}
	content, err := stringifyToml(cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	os.MkdirAll(jcodeDir(), 0o700)
	if writeAtomic(jcodeConfigPath(), []byte(content)) != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to write config"})
		return
	}
	env := readProviderEnv()
	env["JCODE_9ROUTER_API_KEY"] = apiKey
	writeProviderEnv(env)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "jcode configured successfully. Use: jcode --provider-profile kinerouter", "configPath": jcodeConfigPath()})
}

func (g *Gateway) handleCliJcodeDelete(w http.ResponseWriter, r *http.Request) {
	cfg := parseTomlMap(readTextOrEmpty(jcodeConfigPath()))
	if provs := objMap(cfg["providers"]); provs != nil {
		delete(provs, "kinerouter")
	}
	if content, err := stringifyToml(cfg); err == nil {
		writeAtomic(jcodeConfigPath(), []byte(content))
	}
	env := readProviderEnv()
	delete(env, "JCODE_9ROUTER_API_KEY")
	writeProviderEnv(env)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "kinerouter configuration removed from jcode"})
}

// ---------------------------------------------------------------- grok-build
//
// grokBuildConfig.js uses a section regex built on a negative lookahead, which
// RE2 cannot express. The same semantics come from a line scanner here.

const (
	grokMainSlot       = "kinerouter"
	grokBuiltinDefault = "grok-build"
	grokUnsetSentinel  = "__kinerouter_unset__"
)

var grokSubagentTypes = []string{"general-purpose", "explore", "plan"}

func grokSlot(t string) string { return grokMainSlot + "-" + t }

func grokDir() string        { return homePath(".grok") }
func grokConfigPath() string { return filepath.Join(grokDir(), "config.toml") }

var tomlSectionHeaderRe = regexp.MustCompile(`^\[([^\]]+)\][ \t]*$`)

// tomlSections splits content into ordered lines and a map of section name →
// [start, end) line range. The body excludes the header line.
func tomlSections(lines []string) map[string][2]int {
	out := map[string][2]int{}
	cur := ""
	start := 0
	for i, line := range lines {
		if m := tomlSectionHeaderRe.FindStringSubmatch(line); m != nil {
			if cur != "" {
				out[cur] = [2]int{start + 1, i}
			}
			cur = m[1]
			start = i
		}
	}
	if cur != "" {
		out[cur] = [2]int{start + 1, len(lines)}
	}
	return out
}

var grokFieldRe = regexp.MustCompile(`^[ \t]*([A-Za-z0-9_]+)[ \t]*=[ \t]*"([^"]*)"`)

func grokSectionField(content, section, key string) (string, bool) {
	lines := strings.Split(content, "\n")
	sections := tomlSections(lines)
	r, ok := sections[section]
	if !ok {
		return "", false
	}
	for i := r[0]; i < r[1]; i++ {
		if m := grokFieldRe.FindStringSubmatch(lines[i]); m != nil && m[1] == key {
			return m[2], true
		}
	}
	return "", false
}

func grokSetSectionField(content, section, key, value string) string {
	line := key + " = " + tomlQuote(value)
	lines := strings.Split(content, "\n")
	sections := tomlSections(lines)
	r, ok := sections[section]
	if !ok {
		prefix := content
		if prefix != "" && !strings.HasSuffix(prefix, "\n") {
			prefix += "\n"
		}
		return prefix + "\n[" + section + "]\n" + line + "\n"
	}
	for i := r[0]; i < r[1]; i++ {
		if m := grokFieldRe.FindStringSubmatch(lines[i]); m != nil && m[1] == key {
			lines[i] = line
			return strings.Join(lines, "\n")
		}
	}
	// Insert at the top of the section body.
	rest := append([]string{}, lines[r[0]:]...)
	head := append(append([]string{}, lines[:r[0]]...), line)
	return strings.Join(append(head, rest...), "\n")
}

var grokFieldAnyRe = regexp.MustCompile(`^[ \t]*([A-Za-z0-9_]+)[ \t]*=`)

func grokDeleteSectionField(content, section, key string) string {
	lines := strings.Split(content, "\n")
	sections := tomlSections(lines)
	r, ok := sections[section]
	if !ok {
		return content
	}
	kept := []string{}
	bodyEmpty := true
	for i := r[0]; i < r[1]; i++ {
		if m := grokFieldAnyRe.FindStringSubmatch(lines[i]); m != nil && m[1] == key {
			continue
		}
		kept = append(kept, lines[i])
		if strings.TrimSpace(lines[i]) != "" {
			bodyEmpty = false
		}
	}
	if bodyEmpty {
		out := append(append([]string{}, lines[:r[0]-1]...), lines[r[1]:]...)
		return collapseBlankRuns(strings.Join(out, "\n"))
	}
	out := append(append([]string{}, append(lines[:r[0]], kept...)...), lines[r[1]:]...)
	return strings.Join(out, "\n")
}

func collapseBlankRuns(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

func tomlQuote(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var grokSectionContentRe = func(slot string) *regexp.Regexp {
	return regexp.MustCompile(`\[model\.` + regexp.QuoteMeta(slot) + `\]\n`)
}

func grokRemoveModelSection(content, slot string) string {
	lines := strings.Split(content, "\n")
	sections := tomlSections(lines)
	r, ok := sections["model."+slot]
	if !ok {
		return content
	}
	out := append(append([]string{}, lines[:r[0]-1]...), lines[r[1]:]...)
	return collapseBlankRuns(strings.Join(out, "\n"))
}

func grokBuildModelSection(slot, model, baseURL, apiKey, name string, contextWindow int64) string {
	var sb strings.Builder
	sb.WriteString("[model." + slot + "]\n")
	sb.WriteString("model = " + tomlQuote(model) + "\n")
	sb.WriteString("base_url = " + tomlQuote(baseURL) + "\n")
	sb.WriteString("name = " + tomlQuote(name) + "\n")
	sb.WriteString("description = " + tomlQuote("Routed via KineRouter gateway") + "\n")
	sb.WriteString("api_backend = \"chat_completions\"\n")
	if apiKey != "" {
		sb.WriteString("api_key = " + tomlQuote(apiKey) + "\n")
	}
	if contextWindow > 0 {
		sb.WriteString("context_window = " + itoa(contextWindow) + "\n")
	}
	return sb.String()
}

func grokUpsertModelSection(content, slot, model, baseURL, apiKey, name string, contextWindow int64) string {
	section := grokBuildModelSection(slot, model, baseURL, apiKey, name, contextWindow)
	lines := strings.Split(content, "\n")
	sections := tomlSections(lines)
	r, ok := sections["model."+slot]
	if ok {
		out := append(append([]string{}, lines[:r[0]-1]...), strings.Split(strings.TrimSuffix(section, "\n"), "\n")...)
		out = append(out, lines[r[1]:]...)
		return strings.Join(out, "\n")
	}
	prefix := content
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + "\n" + section
}

// grokInsertMarker puts the marker line right before the main model section.
func grokInsertMarker(content, marker string) string {
	lines := strings.Split(content, "\n")
	sections := tomlSections(lines)
	if r, ok := sections["model."+grokMainSlot]; ok {
		start := r[0] - 1
		out := append(append([]string{}, lines[:start]...), marker)
		return strings.Join(append(out, lines[start:]...), "\n")
	}
	prefix := content
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + marker
}

var (
	grokPrevDefaultRe = regexp.MustCompile(`(?m)^# kinerouter-prev-default = "([^"]*)"[ \t]*\r?\n?`)
	grokPrevSubRe     = func(t string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^# kinerouter-prev-subagent-` + regexp.QuoteMeta(t) + ` = "([^"]*)"[ \t]*\r?\n?`)
	}
)

func grokRememberPreviousDefault(content string) string {
	if grokPrevDefaultRe.MatchString(content) {
		return content
	}
	if cur, ok := grokSectionField(content, "models", "default"); ok && cur != "" && cur != grokMainSlot {
		return grokInsertMarker(content, "# kinerouter-prev-default = "+tomlQuote(cur)+"\n")
	}
	return content
}

func grokRestorePreviousDefault(content string) string {
	previous := grokBuiltinDefault
	if m := grokPrevDefaultRe.FindStringSubmatch(content); m != nil {
		previous = m[1]
	}
	next := grokPrevDefaultRe.ReplaceAllString(content, "")
	if cur, ok := grokSectionField(next, "models", "default"); ok && cur == grokMainSlot {
		next = grokSetSectionField(next, "models", "default", previous)
	}
	return next
}

func grokRememberPreviousSubagent(content, t string) string {
	re := grokPrevSubRe(t)
	if re.MatchString(content) {
		return content
	}
	previous := grokUnsetSentinel
	if cur, ok := grokSectionField(content, "subagents.models", t); ok {
		previous = cur
	}
	return grokInsertMarker(content, "# kinerouter-prev-subagent-"+t+" = "+tomlQuote(previous)+"\n")
}

func grokRestorePreviousSubagent(content, t string) string {
	re := grokPrevSubRe(t)
	previous := grokUnsetSentinel
	if m := re.FindStringSubmatch(content); m != nil {
		previous = m[1]
	}
	next := re.ReplaceAllString(content, "")
	if cur, ok := grokSectionField(next, "subagents.models", t); !ok || cur != grokSlot(t) {
		return next
	}
	if previous == grokUnsetSentinel {
		return grokDeleteSectionField(next, "subagents.models", t)
	}
	return grokSetSectionField(next, "subagents.models", t, previous)
}

type grokSubagentSelection struct {
	model         string
	contextWindow int64
}

func grokApply(content, baseURL, apiKey, model string, contextWindow int64, subagents map[string]grokSubagentSelection) string {
	next := grokRememberPreviousDefault(content)
	next = grokUpsertModelSection(next, grokMainSlot, model, baseURL, apiKey, "KineRouter", contextWindow)
	next = grokSetSectionField(next, "models", "default", grokMainSlot)
	if subagents == nil {
		return next
	}
	for _, t := range grokSubagentTypes {
		sel := subagents[t]
		slot := grokSlot(t)
		if sel.model != "" {
			next = grokRememberPreviousSubagent(next, t)
			next = grokUpsertModelSection(next, slot, sel.model, baseURL, apiKey, "KineRouter "+t, sel.contextWindow)
			next = grokSetSectionField(next, "subagents.models", t, slot)
		} else {
			next = grokRestorePreviousSubagent(next, t)
			next = grokRemoveModelSection(next, slot)
		}
	}
	return next
}

func grokReset(content string) string {
	next := content
	for _, t := range grokSubagentTypes {
		next = grokRestorePreviousSubagent(next, t)
		next = grokRemoveModelSection(next, grokSlot(t))
	}
	next = grokRemoveModelSection(next, grokMainSlot)
	next = grokRestorePreviousDefault(next)
	return collapseBlankRuns(next)
}

func (g *Gateway) cliGrokStatus() map[string]any {
	if !toolInstalled("grok", grokConfigPath()) {
		if _, err := os.Stat(filepath.Join(grokDir(), "bin", "grok")); err != nil {
			return map[string]any{"installed": false, "settings": nil, "message": "Grok Build is not installed"}
		}
	}
	content := readTextOrEmpty(grokConfigPath())
	main := map[string]any{}
	if v, ok := grokSectionField(content, "model."+grokMainSlot, "model"); ok {
		main["model"] = v
	}
	if v, ok := grokSectionField(content, "model."+grokMainSlot, "base_url"); ok {
		main["base_url"] = v
	}
	if v, ok := grokSectionField(content, "model."+grokMainSlot, "name"); ok {
		main["name"] = v
	}
	if v, ok := grokSectionField(content, "model."+grokMainSlot, "api_key"); ok {
		main["api_key"] = v
	}
	if v, ok := grokSectionField(content, "model."+grokMainSlot, "api_backend"); ok {
		main["api_backend"] = v
	}
	mappings := map[string]any{}
	for _, t := range grokSubagentTypes {
		v, _ := grokSectionField(content, "subagents.models", t)
		if v == grokSlot(t) {
			mappings[t] = grokSlot(t)
		} else if v != "" {
			mappings[t] = v
		} else {
			mappings[t] = nil
		}
	}
	def, _ := grokSectionField(content, "models", "default")
	var modelOut any
	if len(main) > 0 {
		modelOut = main
	}
	return map[string]any{
		"installed": true,
		"settings": map[string]any{
			"model": modelOut, "default": def, "subagentMappings": mappings,
		},
		"hasKineRouter": main["base_url"] != nil,
		"configPath":    grokConfigPath(),
	}
}

func (g *Gateway) handleCliGrokGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliGrokStatus())
}

func (g *Gateway) handleCliGrokPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL, model := str(body, "baseUrl"), strings.TrimSpace(str(body, "model"))
	if baseURL == "" || model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and model are required"})
		return
	}
	cw := int64(0)
	if n, ok := body["contextWindow"].(float64); ok && n > 0 {
		cw = int64(n)
	} else {
		cw = g.contextWindowFor(model)
	}
	var subagents map[string]grokSubagentSelection
	if v, present := body["subagentModels"]; present {
		subagents = map[string]grokSubagentSelection{}
		if m := objMap(v); m != nil {
			for _, t := range grokSubagentTypes {
				entry := m[t]
				var selModel string
				var selCW float64
				if s, ok := entry.(string); ok {
					selModel = strings.TrimSpace(s)
				} else if em := objMap(entry); em != nil {
					selModel = strings.TrimSpace(str(em, "model"))
					if n, ok := em["contextWindow"].(float64); ok {
						selCW = n
					}
				}
				if selModel == "" {
					continue
				}
				sw := int64(0)
				if selCW > 0 {
					sw = int64(selCW)
				} else {
					sw = g.contextWindowFor(selModel)
				}
				subagents[t] = grokSubagentSelection{model: selModel, contextWindow: sw}
			}
		}
	}
	os.MkdirAll(grokDir(), 0o700)
	next := grokApply(readTextOrEmpty(grokConfigPath()), cliToolBase("grok-build", baseURL), g.resolveCliAPIKey(str(body, "apiKey")), model, cw, subagents)
	if err := writeAtomic(grokConfigPath(), []byte(next)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update grok-build settings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Grok Build settings applied successfully!", "configPath": grokConfigPath(), "modelSlot": grokMainSlot})
}

func (g *Gateway) handleCliGrokDelete(w http.ResponseWriter, r *http.Request) {
	path := grokConfigPath()
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "No config file to reset"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to reset grok-build settings"})
		return
	}
	writeAtomic(path, []byte(grokReset(string(content))))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "kinerouter model slots removed from Grok Build"})
}

// ---------------------------------------------------------------- omp

func ompDir() string        { return homePath(".omp", "agent") }
func ompModelsPath() string { return filepath.Join(ompDir(), "models.yml") }
func ompDBPath() string     { return filepath.Join(ompDir(), "agent.db") }

// ompProviderBlockRe matches a top-level `kinerouter:` block up to the next
// top-level key. The JS used a lookahead; done line-wise here.
func ompRemoveProviderBlock(content string) string {
	lines := strings.Split(content, "\n")
	start := -1
	indent := 0
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == "  "+grokMainSlot+":" || strings.TrimRight(line, " \t") == grokMainSlot+":" {
			start = i
			indent = len(line) - len(strings.TrimLeft(line, " "))
			break
		}
	}
	if start < 0 {
		return content
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		curIndent := len(line) - len(strings.TrimLeft(line, " "))
		if curIndent <= indent && strings.HasSuffix(trimmed, ":") {
			// Next sibling key at the same or shallower indent.
			if curIndent < indent || curIndent == indent {
				end = i
				break
			}
		}
	}
	return strings.Join(append(append([]string{}, lines[:start]...), lines[end:]...), "\n")
}

func ompBuildProviderYAML(baseURL, apiKey string) string {
	return "  " + grokMainSlot + ":\n" +
		"    baseUrl: " + normalizeV1(baseURL) + "\n" +
		"    apiKey: " + apiKey + "\n" +
		"    api: openai-completions\n" +
		"    authHeader: true\n" +
		"    disableStrictTools: true\n" +
		"    discovery:\n" +
		"      type: proxy"
}

func ompHasKineRouter(content string) bool {
	return strings.Contains(content, grokMainSlot+":") || localhostRe.MatchString(content)
}

func (g *Gateway) cliOmpStatus() map[string]any {
	installed := toolInstalled("omp", ompDBPath())
	if !installed {
		if _, err := os.Stat(ompModelsPath()); err == nil {
			installed = true
		}
	}
	if !installed {
		return map[string]any{"installed": false, "config": nil, "message": "Oh My Pi is not installed"}
	}
	return map[string]any{
		"installed": true, "hasKineRouter": ompHasKineRouter(readTextOrEmpty(ompModelsPath())),
		"configPath": ompModelsPath(),
	}
}

func (g *Gateway) handleCliOmpGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliOmpStatus())
}

func (g *Gateway) handleCliOmpPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL := str(body, "baseUrl")
	if baseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "baseUrl is required"}})
		return
	}
	resolvedKey := g.resolveCliAPIKey(str(body, "apiKey"))
	os.MkdirAll(ompDir(), 0o700)

	content := ompRemoveProviderBlock(readTextOrEmpty(ompModelsPath()))
	block := ompBuildProviderYAML(baseURL, resolvedKey)
	switch {
	case strings.TrimSpace(content) == "":
		content = "providers:\n" + block + "\n"
	case strings.Contains(content, "providers:"):
		content = strings.Replace(content, "providers:", "providers:\n"+block, 1)
	default:
		content = strings.TrimSpace(content) + "\n\nproviders:\n" + block + "\n"
	}
	if err := writeAtomic(ompModelsPath(), []byte(content)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	g.ompSeedAuthCredential(resolvedKey, baseURL)

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    "Oh My Pi settings applied! Run 'omp' and all KineRouter models appear under kinerouter in /model.",
		"configPath": ompModelsPath(),
	})
}

func (g *Gateway) handleCliOmpDelete(w http.ResponseWriter, r *http.Request) {
	content := ompRemoveProviderBlock(readTextOrEmpty(ompModelsPath()))
	if strings.TrimSpace(content) == "providers:" {
		os.Remove(ompModelsPath())
	} else {
		writeAtomic(ompModelsPath(), []byte(content))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "KineRouter removed from Oh My Pi"})
}

// ---------------------------------------------------------------- hermes

func hermesRoot() string { return homePath(".hermes") }

var hermesProfileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

var hermesIdentityFiles = []string{"config.yaml", ".env", "SOUL.md", "profile.yaml", "auth.json", "state.db"}

type hermesHome struct {
	name       string
	dir        string
	configPath string
	envPath    string
	isDefault  bool
}

var errHermesBadName = errors.New("Invalid Hermes profile name (letters, digits, - and _ only, max 64 chars)")

func resolveHermesHome(profile string) (hermesHome, error) {
	base := hermesRoot()
	name := strings.TrimSpace(profile)
	if name == "" || name == "default" {
		return hermesHome{name: "default", dir: base,
			configPath: filepath.Join(base, "config.yaml"), envPath: filepath.Join(base, ".env"), isDefault: true}, nil
	}
	if !hermesProfileNameRe.MatchString(name) || strings.EqualFold(name, "default") {
		return hermesHome{}, errHermesBadName
	}
	profilesRoot := filepath.Join(base, "profiles")
	dir := filepath.Join(profilesRoot, name)
	// Containment: name is already regex-bounded, but keep the explicit check.
	absDir, err := filepath.Abs(dir)
	if err != nil || !strings.HasPrefix(absDir+string(os.PathSeparator), profilesRoot+string(os.PathSeparator)) {
		return hermesHome{}, errors.New("Invalid Hermes profile path")
	}
	return hermesHome{name: name, dir: dir,
		configPath: filepath.Join(dir, "config.yaml"), envPath: filepath.Join(dir, ".env")}, nil
}

func dirExists(dir string) bool {
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

func isProfileHome(dir string) bool {
	for _, f := range hermesIdentityFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

type hermesProfile struct {
	Name          string  `json:"name"`
	Dir           string  `json:"dir"`
	IsDefault     bool    `json:"isDefault"`
	DisplayName   *string `json:"displayName"`
	Command       string  `json:"command"`
	Alias         *string `json:"alias"`
	Model         *string `json:"model"`
	BaseURL       *string `json:"baseUrl"`
	HasKineRouter bool    `json:"hasKineRouter"`
}

// Hermes YAML shapes, mirrored from hermesYaml.js. RE2 has no lookahead, but
// none of these patterns use one.
var (
	hermesModelBlockRe  = regexp.MustCompile(`(?m)^model:[ \t]*\r?\n((?:[ \t]+.*\r?\n?|[ \t]*\r?\n)*)`)
	hermesModelScalarRe = regexp.MustCompile(`(?m)^model:[ \t]*[^\r\n]*\r?\n?`)
	hermesDelegationRe  = regexp.MustCompile(`(?m)^delegation:[ \t]*\r?\n((?:[ \t]+.*\r?\n?|[ \t]*\r?\n)*)`)
	hermesAuxBlockRe    = regexp.MustCompile(`(?m)^auxiliary:[ \t]*\r?\n((?:(?:[ \t]+.*\r?\n?)|(?:[ \t]*\r?\n))*)`)
	hermesSttBlockRe    = regexp.MustCompile(`(?m)^stt:[ \t]*\r?\n((?:[ \t]+.*\r?\n?|[ \t]*\r?\n)*)`)
	hermesAuxSubRe      = regexp.MustCompile(`(?m)^  ([A-Za-z0-9_]+):[ \t]*\r?\n((?:(?:[ \t]{4,}.*\r?\n?)|(?:[ \t]*\r?\n))*)`)
	hermesDisplayNameRe = regexp.MustCompile(`(?m)^display_name:[ \t]*["']?([^"'\r\n]+)["']?`)
	hermesSttKeyRef     = "${OPENAI_API_KEY}"
)

func hermesAuxRoleRe(role string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(role) + `:[ \t]*\r?\n(?:(?:[ \t]{4,}.*\r?\n?)|(?:[ \t]*\r?\n))*`)
}

func hermesSttSubRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(name) + `:[ \t]*\r?\n((?:(?:[ \t]{4,}.*\r?\n?)|(?:[ \t]*\r?\n))*)`)
}

func hermesKVGetter(body string) func(string) *string {
	return func(key string) *string {
		re := regexp.MustCompile(`(?m)^[ \t]+` + regexp.QuoteMeta(key) + `:[ \t]*["']?([^"'\r\n]+)["']?`)
		if m := re.FindStringSubmatch(body); m != nil {
			v := strings.TrimSpace(m[1])
			return &v
		}
		return nil
	}
}

type hermesModelBlock struct {
	Default  *string `json:"default"`
	Provider *string `json:"provider"`
	BaseURL  *string `json:"base_url"`
	APIKey   *string `json:"api_key"`
}

func hermesParseModel(yaml string) *hermesModelBlock {
	m := hermesModelBlockRe.FindStringSubmatch(yaml)
	if m == nil {
		return nil
	}
	get := hermesKVGetter(m[1])
	return &hermesModelBlock{Default: get("default"), Provider: get("provider"), BaseURL: get("base_url"), APIKey: get("api_key")}
}

func hermesParseDelegation(yaml string) *hermesModelBlock {
	m := hermesDelegationRe.FindStringSubmatch(yaml)
	if m == nil {
		return nil
	}
	get := hermesKVGetter(m[1])
	return &hermesModelBlock{Default: get("model"), Provider: get("provider"), BaseURL: get("base_url")}
}

type hermesAuxRole struct {
	Model    *string `json:"model"`
	Provider *string `json:"provider"`
	BaseURL  *string `json:"base_url"`
}

func hermesParseAuxRoles(yaml string) map[string]hermesAuxRole {
	out := map[string]hermesAuxRole{}
	m := hermesAuxBlockRe.FindStringSubmatch(yaml)
	if m == nil {
		return out
	}
	for _, sm := range hermesAuxSubRe.FindAllStringSubmatch(m[1], -1) {
		get := hermesKVGetter(sm[2])
		out[sm[1]] = hermesAuxRole{Model: get("model"), Provider: get("provider"), BaseURL: get("base_url")}
	}
	return out
}

type hermesStt struct {
	Enabled  *string `json:"enabled"`
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
	BaseURL  *string `json:"base_url"`
	APIKey   *string `json:"api_key"`
}

var hermesQuoteStrip = regexp.MustCompile(`^[ \t]*["']|["'][ \t]*$`)

func hermesParseStt(yaml string) *hermesStt {
	m := hermesSttBlockRe.FindStringSubmatch(yaml)
	if m == nil {
		return nil
	}
	direct := map[string]string{}
	openai := map[string]string{}
	current := ""
	directKeyRe := regexp.MustCompile(`^  ([A-Za-z0-9_]+):[ \t]*(.*)$`)
	openaiKeyRe := regexp.MustCompile(`^ {4,}([A-Za-z0-9_]+):[ \t]*(.*)$`)
	for _, line := range strings.Split(m[1], "\n") {
		if strings.HasPrefix(line, "  ") && len(line) > 2 && line[2] != ' ' && line[2] != '\t' {
			kv := directKeyRe.FindStringSubmatch(line)
			current = ""
			if kv != nil && kv[2] != "" {
				direct[kv[1]] = hermesQuoteStrip.ReplaceAllString(kv[2], "")
			} else if kv != nil {
				current = kv[1]
			}
		} else if current == "openai" && strings.HasPrefix(line, "    ") {
			if kv := openaiKeyRe.FindStringSubmatch(line); kv != nil && kv[2] != "" {
				openai[kv[1]] = hermesQuoteStrip.ReplaceAllString(kv[2], "")
			}
		}
	}
	ptr := func(m map[string]string, k string) *string {
		if v, ok := m[k]; ok {
			return &v
		}
		return nil
	}
	return &hermesStt{Enabled: ptr(direct, "enabled"), Provider: ptr(direct, "provider"),
		Model: ptr(openai, "model"), BaseURL: ptr(openai, "base_url"), APIKey: ptr(openai, "api_key")}
}

func hermesHasKineRouter(cfg *hermesModelBlock) bool {
	return cfg != nil && cfg.BaseURL != nil && cfg.Provider != nil && *cfg.Provider == "custom" &&
		localhostRe.MatchString(*cfg.BaseURL)
}

func hermesIsCustomBlock(cfg *hermesModelBlock) bool {
	return cfg != nil && cfg.Provider != nil && *cfg.Provider == "custom"
}

func hermesIsKineRouterStt(cfg *hermesStt) bool {
	return cfg != nil && cfg.Provider != nil && *cfg.Provider == "openai" &&
		cfg.BaseURL != nil && *cfg.BaseURL != "" && cfg.APIKey != nil && *cfg.APIKey == hermesSttKeyRef
}

func hermesBuildModelBlock(model, baseURL string) string {
	return "model:\n  default: \"" + model + "\"\n  provider: \"custom\"\n  base_url: \"" + baseURL + "\"\n  api_key: ${OPENAI_API_KEY}\n"
}

func hermesBuildDelegationBlock(model, baseURL string) string {
	return "delegation:\n  model: \"" + model + "\"\n  provider: \"custom\"\n  base_url: \"" + baseURL + "\"\n  api_key: ${OPENAI_API_KEY}\n"
}

func hermesBuildAuxRoleBlock(role, model, baseURL string) string {
	return "  " + role + ":\n    provider: \"custom\"\n    model: \"" + model + "\"\n    base_url: \"" + baseURL + "\"\n    api_key: ${OPENAI_API_KEY}\n"
}

func hermesBuildSttBlock(model, baseURL string) string {
	return "stt:\n  enabled: true\n  provider: \"openai\"\n  openai:\n    model: \"" + model + "\"\n    base_url: \"" + baseURL + "\"\n    api_key: " + hermesSttKeyRef + "\n"
}

func hermesUpsertModelBlock(yaml, block string) string {
	if hermesModelBlockRe.MatchString(yaml) {
		return hermesModelBlockRe.ReplaceAllLiteralString(yaml, block)
	}
	if hermesModelScalarRe.MatchString(yaml) {
		return hermesModelScalarRe.ReplaceAllLiteralString(yaml, block)
	}
	if len(yaml) > 0 {
		return block + "\n" + yaml
	}
	return block
}

func hermesUpsertDelegationBlock(yaml, block string) string {
	if hermesDelegationRe.MatchString(yaml) {
		return hermesDelegationRe.ReplaceAllLiteralString(yaml, block)
	}
	if len(yaml) == 0 || strings.HasSuffix(yaml, "\n") {
		return yaml + block
	}
	return yaml + "\n" + block
}

func hermesUpsertAuxRole(yaml, role, roleBlock string) string {
	re := hermesAuxRoleRe(role)
	m := hermesAuxBlockRe.FindStringSubmatchIndex(yaml)
	if m == nil {
		block := "auxiliary:\n" + roleBlock
		if len(yaml) == 0 || strings.HasSuffix(yaml, "\n") {
			return yaml + block
		}
		return yaml + "\n" + block
	}
	body := yaml[m[2]:m[3]]
	var nextBody string
	if re.MatchString(body) {
		nextBody = re.ReplaceAllLiteralString(body, roleBlock)
	} else {
		nextBody = body + roleBlock
	}
	return yaml[:m[0]] + "auxiliary:\n" + nextBody + yaml[m[1]:]
}

func hermesRemoveAuxRole(yaml, role string) string {
	m := hermesAuxBlockRe.FindStringSubmatchIndex(yaml)
	if m == nil {
		return yaml
	}
	body := hermesAuxRoleRe(role).ReplaceAllLiteralString(yaml[m[2]:m[3]], "")
	if strings.TrimSpace(body) == "" {
		return yaml[:m[0]] + yaml[m[1]:]
	}
	return yaml[:m[0]] + "auxiliary:\n" + body + yaml[m[1]:]
}

func hermesRemoveModelBlock(yaml string) string {
	if hermesModelBlockRe.MatchString(yaml) {
		return strings.TrimLeft(hermesModelBlockRe.ReplaceAllLiteralString(yaml, ""), "\n")
	}
	if hermesModelScalarRe.MatchString(yaml) {
		return strings.TrimLeft(hermesModelScalarRe.ReplaceAllLiteralString(yaml, ""), "\n")
	}
	return yaml
}

func hermesRemoveDelegationBlock(yaml string) string {
	return hermesDelegationRe.ReplaceAllLiteralString(yaml, "")
}

func hermesUpsertEnvVar(envText, key, value string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=.*$`)
	line := key + "=" + value
	if re.MatchString(envText) {
		return re.ReplaceAllLiteralString(envText, line)
	}
	if len(envText) > 0 && !strings.HasSuffix(envText, "\n") {
		return envText + "\n" + line + "\n"
	}
	return envText + line + "\n"
}

// hermesUpsertSttKineRouter writes the kinerouter STT wiring, preserving sibling keys.
func hermesUpsertSttKineRouter(yaml, model, baseURL string) string {
	m := hermesSttBlockRe.FindStringSubmatchIndex(yaml)
	if m == nil {
		prefix := yaml
		if len(yaml) > 0 && !strings.HasSuffix(yaml, "\n") {
			prefix = yaml + "\n"
		}
		return prefix + hermesBuildSttBlock(model, baseURL)
	}
	body := yaml[m[2]:m[3]]
	body = setYamlDirect(body, "enabled", "true")
	body = setYamlDirect(body, "provider", `"openai"`)
	body = setYamlSub(body, "openai", "model", `"`+model+`"`)
	body = setYamlSub(body, "openai", "base_url", `"`+baseURL+`"`)
	body = setYamlSub(body, "openai", "api_key", hermesSttKeyRef)
	return yaml[:m[0]] + "stt:\n" + body + yaml[m[1]:]
}

func setYamlDirect(body, key, value string) string {
	re := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(key) + `:[^\r\n]*`)
	if re.MatchString(body) {
		return re.ReplaceAllLiteralString(body, "  "+key+": "+value)
	}
	head := "  " + key + ": " + value + "\n"
	if len(body) > 0 && !strings.HasSuffix(body, "\n") {
		return body + "\n" + head
	}
	return body + head
}

func setYamlSub(body, subName, key, value string) string {
	sub := hermesSttSubRe(subName)
	m := sub.FindStringSubmatchIndex(body)
	if m == nil {
		block := "  " + subName + ":\n    " + key + ": " + value + "\n"
		if len(body) > 0 && !strings.HasSuffix(body, "\n") {
			return body + "\n" + block
		}
		return body + block
	}
	subBody := body[m[2]:m[3]]
	re := regexp.MustCompile(`(?m)^ {4}` + regexp.QuoteMeta(key) + `:[^\r\n]*`)
	var nextSub string
	switch {
	case re.MatchString(subBody):
		nextSub = re.ReplaceAllLiteralString(subBody, "    "+key+": "+value)
	case len(subBody) == 0 || strings.HasSuffix(subBody, "\n"):
		nextSub = subBody + "    " + key + ": " + value + "\n"
	default:
		nextSub = subBody + "\n    " + key + ": " + value + "\n"
	}
	return body[:m[0]] + "  " + subName + ":\n" + nextSub + body[m[1]:]
}

// hermesRemoveSttKineRouter drops only the keys the wiring writes.
func hermesRemoveSttKineRouter(yaml string) string {
	m := hermesSttBlockRe.FindStringSubmatchIndex(yaml)
	if m == nil {
		return yaml
	}
	body := yaml[m[2]:m[3]]
	body = regexp.MustCompile(`(?m)^  enabled:[ \t]*["']?true["']?[ \t]*\r?\n?`).ReplaceAllLiteralString(body, "")
	body = regexp.MustCompile(`(?m)^  provider:[ \t]*["']?openai["']?[ \t]*\r?\n?`).ReplaceAllLiteralString(body, "")
	if sm := hermesSttSubRe("openai").FindStringSubmatchIndex(body); sm != nil {
		subBody := body[sm[2]:sm[3]]
		for _, key := range []string{"model", "base_url", "api_key"} {
			subBody = regexp.MustCompile(`(?m)^ {4}`+regexp.QuoteMeta(key)+`:[^\r\n]*\r?\n?`).ReplaceAllLiteralString(subBody, "")
		}
		if strings.TrimSpace(subBody) == "" {
			body = body[:sm[0]] + body[sm[1]:]
		} else {
			body = body[:sm[0]] + "  openai:\n" + subBody + body[sm[1]:]
		}
	}
	if strings.TrimSpace(body) == "" {
		return strings.TrimLeft(yaml[:m[0]]+yaml[m[1]:], "\n")
	}
	return yaml[:m[0]] + "stt:\n" + body + yaml[m[1]:]
}

// ---------------------------------------------------------------- hermes handlers

func (g *Gateway) hermesReadHome(profile string) (hermesHome, string, int, bool) {
	home, err := resolveHermesHome(profile)
	if err != nil {
		return hermesHome{}, err.Error(), http.StatusBadRequest, false
	}
	if !home.isDefault && !dirExists(home.dir) {
		return hermesHome{}, `Hermes profile "` + home.name + `" not found`, http.StatusNotFound, false
	}
	return home, "", 0, true
}

func hermesWriteHomeConfig(home hermesHome, yaml string) error {
	if home.isDefault {
		os.MkdirAll(home.dir, 0o700)
	}
	return writeAtomic(home.configPath, []byte(yaml))
}

func (g *Gateway) hermesWriteHomeEnv(home hermesHome, apiKey string) {
	if apiKey == "" {
		return
	}
	if home.isDefault {
		os.MkdirAll(home.dir, 0o700)
	}
	env := hermesUpsertEnvVar(readTextOrEmpty(home.envPath), "OPENAI_API_KEY", apiKey)
	writeAtomic(home.envPath, []byte(env))
}

func (g *Gateway) cliHermesStatus(profile string) map[string]any {
	if !toolInstalled("hermes", filepath.Join(hermesRoot(), "config.yaml")) {
		return map[string]any{"installed": false, "settings": nil, "message": "Hermes Agent is not installed"}
	}
	home, _, _, ok := g.hermesReadHome(profile)
	if !ok {
		return map[string]any{"installed": true, "error": "profile not found"}
	}
	yaml := readTextOrEmpty(home.configPath)
	model := hermesParseModel(yaml)
	delegation := hermesParseDelegation(yaml)
	aux := hermesParseAuxRoles(yaml)
	stt := hermesParseStt(yaml)
	has := hermesHasKineRouter(model) || hermesHasKineRouter(delegation)
	for _, r := range aux {
		if r.Provider != nil && r.BaseURL != nil && *r.Provider == "custom" && localhostRe.MatchString(*r.BaseURL) {
			has = true
		}
	}
	return map[string]any{
		"installed":     true,
		"profile":       map[string]any{"name": home.name, "dir": home.dir, "isDefault": home.isDefault},
		"settings":      map[string]any{"model": model, "delegation": delegation, "auxiliary": aux, "stt": stt},
		"hasKineRouter": has, "hasKineRouterStt": hermesIsKineRouterStt(stt),
		"configPath": home.configPath,
	}
}

func (g *Gateway) handleCliHermesGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.cliHermesStatus(r.URL.Query().Get("profile")))
}

func (g *Gateway) listHermesProfiles() []hermesProfile {
	base := hermesRoot()
	entries := []hermesProfile{}
	build := func(name, dir string, isDefault bool) hermesProfile {
		yaml := readTextOrEmpty(filepath.Join(dir, "config.yaml"))
		model := hermesParseModel(yaml)
		delegation := hermesParseDelegation(yaml)
		aux := hermesParseAuxRoles(yaml)
		e := hermesProfile{Name: name, Dir: dir, IsDefault: isDefault}
		if m := hermesDisplayNameRe.FindStringSubmatch(readTextOrEmpty(filepath.Join(dir, "profile.yaml"))); m != nil {
			v := strings.TrimSpace(m[1])
			e.DisplayName = &v
		}
		if isDefault {
			e.Command = "hermes"
		} else {
			e.Command = "hermes -p " + name
			alias := name
			e.Alias = &alias
		}
		if model != nil && model.Default != nil {
			e.Model = model.Default
		}
		if model != nil && model.BaseURL != nil {
			e.BaseURL = model.BaseURL
		}
		has := hermesHasKineRouter(model) || hermesHasKineRouter(delegation)
		for _, a := range aux {
			if a.Provider != nil && a.BaseURL != nil && *a.Provider == "custom" && localhostRe.MatchString(*a.BaseURL) {
				has = true
			}
		}
		e.HasKineRouter = has
		return e
	}
	entries = append(entries, build("default", base, true))
	names, err := readDirNames(filepath.Join(base, "profiles"))
	if err != nil {
		return entries
	}
	sort.Strings(names)
	for _, name := range names {
		if !hermesProfileNameRe.MatchString(name) || strings.EqualFold(name, "default") {
			continue
		}
		dir := filepath.Join(base, "profiles", name)
		if !dirExists(dir) || !isProfileHome(dir) {
			continue
		}
		entries = append(entries, build(name, dir, false))
	}
	return entries
}

func (g *Gateway) handleCliHermesProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profiles": g.listHermesProfiles()})
}

func (g *Gateway) handleCliHermesPost(w http.ResponseWriter, r *http.Request) {
	body, ok := cliBody(w, r)
	if !ok {
		return
	}
	baseURL := str(body, "baseUrl")
	type sel struct{ role, model string }
	var selections []sel
	if arr, ok := body["selections"].([]any); ok {
		for _, item := range arr {
			m := objMap(item)
			if m == nil {
				continue
			}
			role, model := str(m, "role"), str(m, "model")
			if role != "" && model != "" {
				selections = append(selections, sel{role, model})
			}
		}
	} else if model := str(body, "model"); model != "" {
		selections = []sel{{"default", model}}
	}
	var defaultSel *sel
	for i := range selections {
		if selections[i].role == "default" {
			defaultSel = &selections[i]
		}
	}
	audio := objMap(body["audio"])
	truthy := func(v any) bool { return v == true || v == "true" || v == "1" }
	audioEnabled := audio != nil && truthy(audio["enabled"])
	audioModel := ""
	if audio != nil {
		audioModel = strings.TrimSpace(str(audio, "model"))
	}
	if baseURL == "" || (defaultSel == nil && audio == nil) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "baseUrl and model are required"})
		return
	}
	if audioEnabled && audioModel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "audio.model is required when enabling audio transcription"})
		return
	}
	normalized := cliToolBase("hermes", baseURL)

	applyToAll := truthy(body["applyToAll"]) || truthy(r.URL.Query().Get("applyToAll"))
	if applyToAll {
		g.hermesApplyToAll(normalized, str(body, "apiKey"), func() string {
			if defaultSel != nil {
				return defaultSel.model
			}
			return ""
		}(), w)
		return
	}

	profile := str(body, "profile")
	if profile == "" {
		profile = r.URL.Query().Get("profile")
	}
	home, msg, code, ok := g.hermesReadHome(profile)
	if !ok {
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	yaml := readTextOrEmpty(home.configPath)
	for _, s := range selections {
		switch s.role {
		case "default":
			yaml = hermesUpsertModelBlock(yaml, hermesBuildModelBlock(s.model, normalized))
		case "delegation":
			yaml = hermesUpsertDelegationBlock(yaml, hermesBuildDelegationBlock(s.model, normalized))
		default:
			yaml = hermesUpsertAuxRole(yaml, s.role, hermesBuildAuxRoleBlock(s.role, s.model, normalized))
		}
	}
	if audio != nil {
		if audioEnabled {
			yaml = hermesUpsertSttKineRouter(yaml, audioModel, normalized)
		} else if hermesIsKineRouterStt(hermesParseStt(yaml)) {
			yaml = hermesRemoveSttKineRouter(yaml)
		}
	}
	if err := hermesWriteHomeConfig(home, yaml); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update hermes settings"})
		return
	}
	g.hermesWriteHomeEnv(home, str(body, "apiKey"))
	label := "Hermes settings applied successfully!"
	if !home.isDefault {
		label = `Hermes settings applied to profile "` + home.name + `"!`
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "message": label,
		"profile":       map[string]any{"name": home.name, "dir": home.dir, "isDefault": home.isDefault},
		"sttKineRouter": hermesIsKineRouterStt(hermesParseStt(yaml)),
		"configPath":    home.configPath,
	})
}

func (g *Gateway) hermesApplyToAll(normalized, apiKey, model string, w http.ResponseWriter) {
	results := []any{}
	updated := 0
	for _, p := range g.listHermesProfiles() {
		home, err := resolveHermesHome(p.Name)
		if err != nil {
			continue
		}
		fail := func(reason string) {
			results = append(results, map[string]any{"profile": p.Name, "status": "failed", "reason": reason})
		}
		yaml, err := os.ReadFile(home.configPath)
		content := string(yaml)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fail(err.Error())
			continue
		}
		main := hermesParseModel(content)
		switch {
		case main != nil && main.Default != nil && hermesIsCustomBlock(main):
			content = hermesUpsertModelBlock(content, hermesBuildModelBlock(*main.Default, normalized))
			if d := hermesParseDelegation(content); d != nil && d.Default != nil && hermesIsCustomBlock(d) {
				content = hermesUpsertDelegationBlock(content, hermesBuildDelegationBlock(*d.Default, normalized))
			}
			for role, cfg := range hermesParseAuxRoles(content) {
				if cfg.Model != nil && cfg.Provider != nil && *cfg.Provider == "custom" {
					content = hermesUpsertAuxRole(content, role, hermesBuildAuxRoleBlock(role, *cfg.Model, normalized))
				}
			}
			if hermesWriteHomeConfig(home, content) != nil {
				fail("write failed")
				continue
			}
			g.hermesWriteHomeEnv(home, apiKey)
			results = append(results, map[string]any{"profile": p.Name, "status": "updated", "model": *main.Default})
			updated++
		case main != nil && main.Default != nil:
			provider := "unknown"
			if main.Provider != nil {
				provider = *main.Provider
			}
			results = append(results, map[string]any{"profile": p.Name, "status": "skipped",
				"reason": `main model uses provider "` + provider + `" — configure this profile individually`})
		case model != "":
			content = hermesUpsertModelBlock(content, hermesBuildModelBlock(model, normalized))
			if hermesWriteHomeConfig(home, content) != nil {
				fail("write failed")
				continue
			}
			g.hermesWriteHomeEnv(home, apiKey)
			results = append(results, map[string]any{"profile": p.Name, "status": "updated", "model": model})
			updated++
		default:
			results = append(results, map[string]any{"profile": p.Name, "status": "skipped", "reason": "no main model configured"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": updated > 0, "bulk": true, "results": results,
		"updated": updated, "skipped": len(results) - updated,
		"message": "Updated endpoint on " + itoa(int64(updated)) + " profile(s)",
	})
}

func (g *Gateway) handleCliHermesDelete(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if decodeBody(r, &body) != nil {
		body = nil // an empty body is legal here: reset the default profile
	}
	profile := str(body, "profile")
	if profile == "" {
		profile = r.URL.Query().Get("profile")
	}
	home, msg, code, ok := g.hermesReadHome(profile)
	if !ok {
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	raw, err := os.ReadFile(home.configPath)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "profile": home.name, "message": "No config file to reset"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to reset hermes settings"})
		return
	}
	yaml := string(raw)
	removed := map[string]any{"model": false, "delegation": false, "auxiliary": []string{}, "stt": false}
	kept := []string{}

	if main := hermesParseModel(yaml); main != nil {
		if hermesIsCustomBlock(main) {
			yaml = hermesRemoveModelBlock(yaml)
			removed["model"] = true
		} else {
			kept = append(kept, "model")
		}
	}
	if d := hermesParseDelegation(yaml); d != nil {
		if hermesIsCustomBlock(d) {
			yaml = hermesRemoveDelegationBlock(yaml)
			removed["delegation"] = true
		} else {
			kept = append(kept, "delegation")
		}
	}
	auxRemoved := []string{}
	for role, cfg := range hermesParseAuxRoles(yaml) {
		if cfg.Provider != nil && *cfg.Provider == "custom" {
			yaml = hermesRemoveAuxRole(yaml, role)
			auxRemoved = append(auxRemoved, role)
		}
	}
	sort.Strings(auxRemoved)
	removed["auxiliary"] = auxRemoved

	if sttCfg := hermesParseStt(yaml); sttCfg != nil {
		if hermesIsKineRouterStt(sttCfg) {
			yaml = hermesRemoveSttKineRouter(yaml)
			removed["stt"] = true
			if hermesParseStt(yaml) != nil {
				kept = append(kept, "stt")
			}
		} else {
			kept = append(kept, "stt")
		}
	}
	yaml = strings.TrimLeft(yaml, "\n")
	hermesWriteHomeConfig(home, yaml)
	scope := ""
	if !home.isDefault {
		scope = ` in profile "` + home.name + `"`
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "profile": home.name, "removed": removed, "kept": kept,
		"message": "kinerouter model blocks removed" + scope,
	})
}
