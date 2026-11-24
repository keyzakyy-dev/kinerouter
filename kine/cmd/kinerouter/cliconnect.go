package main

// `kinerouter connect <server-url>` and `kinerouter show` — the CLI launcher's
// remote-config commands (KineRouter `cli/src/cli/commands/{connect,show}.js`).
//
// connect logs in to a REMOTE server with the dashboard password, reuses or
// creates one API key for this machine, then writes each local CLI tool's own
// config file so those tools talk to that server. Nothing is started locally.
//
// Security: the password and API key are never printed (the key is masked);
// files that hold a key are written 0600; a saved password is bound to the
// server it was saved for so it can never be replayed to another host.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/config"

	toml "github.com/pelletier/go-toml/v2"
	"golang.org/x/term"
)

const cliHelp = `
Usage:
  kinerouter [options]
  kinerouter connect <server-url> [options]
  kinerouter show [tool...] [--json]
  kinerouter mitm-server        (internal: privileged MITM proxy)
  kinerouter tray               Start the desktop tray icon (needs kinerouter-tray)
  kinerouter version            Show this version and the newest release
  kinerouter update             Install the newest release for this channel

options (the login entry runs --tray --skip-update):
  -p, --port <port>  Port to serve on (default: 20128, or $PORT)
  -H, --host <host>  Address to bind (default: 0.0.0.0, or $HOST)
  -t, --tray         Serve headless and show the tray icon, no menu
  --skip-update      Skip the release check; used at login so startup never
                     waits on the network

connect points THIS machine's CLI tools at a remote KineRouter server.
show prints the router settings each local tool currently holds (read-only).

  kinerouter connect http://10.0.0.5:20128 --tools claude,codex
  kinerouter show claude

connect options:
  --tools <list>     Comma-separated: claude, codex, opencode, droid, crush,
                     kilo, cline, pi, omp, all            (default: claude)
  --password <pw>    Dashboard password (or KINEROUTER_PASSWORD; prompted)
  --save             Save the password to ~/.kinerouter/connect.env (0600)
  --key-name <name>  API key name to reuse/create (default: cli-<hostname>)
  --api-key <key>    Use this key, skip login and key lookup
  --model <model>    Model for non-Claude tools (default: the server's own)
  --fable/--opus/--sonnet/--haiku <model>   Override a Claude Code tier
  --print-env        Print OpenAI-compatible env vars too
  --reset            Remove the router settings from the selected tools
  -h, --help         Show this help

show options:
  --tools <list>     Same list as above
  --json             Machine-readable output (key still masked)
`

var cliToolIDs = []string{"claude", "codex", "opencode", "droid", "crush", "kilo", "cline", "pi", "omp"}

var cliToolAliases = map[string]string{
	"claude-code": "claude", "claudecode": "claude", "factory": "droid",
	"kilocode": "kilo", "pi-coding-agent": "pi", "oh-my-pi": "omp",
}

// Claude Code tier env keys, in flag order.
var claudeTiers = []struct{ flag, envKey string }{
	{"fable", "ANTHROPIC_DEFAULT_FABLE_MODEL"},
	{"opus", "ANTHROPIC_DEFAULT_OPUS_MODEL"},
	{"sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL"},
	{"haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL"},
}

// runCLI dispatches `connect` / `show`; it reports whether args named one.
func runCLI(args []string) (handled bool, code int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "connect":
		return true, runConnect(args[1:])
	case "show":
		return true, runShow(args[1:])
	case "mitm-server":
		return true, runMitmServer()
	case "tray":
		// Hands off to the companion binary: a GUI toolkit cannot be linked into
		// this one without breaking the CGO_ENABLED=0 cross-build.
		return true, runTray(args[1:])
	case "version", "-v", "--version":
		return true, runVersion()
	case "update":
		return true, runUpdate()
	case "help", "-h", "--help":
		fmt.Print(cliHelp)
		return true, 0
	}
	if strings.HasPrefix(args[0], "-") {
		// Everything else is still an env var, so an unknown flag is a typo and
		// not a silently ignored option: starting the server anyway would swallow
		// `kinerouter --porrt 3000` and serve on 20128 regardless.
		fmt.Fprintf(os.Stderr, "unknown flag %q\n\n%s", args[0], cliHelp)
		return true, 2
	}
	return false, 0
}

// ---------------------------------------------------------------- connect

type connectOpts struct {
	url      string
	tools    []string
	password *string
	keyName  string
	apiKey   string
	model    string
	models   map[string]string // claude tier flag → model
	save     bool
	printEnv bool
	reset    bool
	help     bool
}

func parseConnectArgs(argv []string) (connectOpts, error) {
	o := connectOpts{models: map[string]string{}}
	host, _ := os.Hostname()
	o.keyName = truncate("cli-"+host, 64)
	if p := os.Getenv("KINEROUTER_PASSWORD"); p != "" {
		o.password = &p
	}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		next := func() string {
			i++
			if i >= len(argv) {
				return ""
			}
			return argv[i]
		}
		switch {
		case a == "--password":
			v := next()
			o.password = &v
		case a == "--key-name":
			o.keyName = next()
		case a == "--api-key":
			o.apiKey = next()
		case a == "--tools":
			o.tools = splitCSV(next())
		case a == "--model":
			o.model = next()
		case a == "--save":
			o.save = true
		case a == "--print-env":
			o.printEnv = true
		case a == "--reset":
			o.reset = true
		case a == "-h" || a == "--help":
			o.help = true
		case strings.HasPrefix(a, "--"):
			tier := strings.TrimPrefix(a, "--")
			if !isClaudeTier(tier) {
				return o, fmt.Errorf("unknown option: %s", a)
			}
			o.models[tier] = next()
		case o.url == "":
			o.url = a
		default:
			return o, fmt.Errorf("unknown option: %s", a)
		}
	}
	return o, nil
}

func isClaudeTier(flag string) bool {
	for _, t := range claudeTiers {
		if t.flag == flag {
			return true
		}
	}
	return false
}

func runConnect(argv []string) int {
	o, err := parseConnectArgs(argv)
	if err != nil {
		fmt.Println("❌", err)
		return 2
	}
	if o.help {
		fmt.Print(cliHelp)
		return 0
	}
	if !o.reset && o.url == "" {
		fmt.Print(cliHelp)
		return 1
	}
	tools, err := resolveTools(o.tools, "claude")
	if err != nil {
		fmt.Println("❌", err)
		return 2
	}
	if o.reset {
		failed := 0
		for _, id := range tools {
			files, err := resetTool(id)
			if err != nil {
				failed++
				fmt.Printf("❌ %s: %v\n", id, err)
				continue
			}
			if len(files) == 0 {
				fmt.Printf("• %s: nothing to reset\n", id)
			} else {
				fmt.Printf("✅ %s: removed router settings (%s)\n", id, strings.Join(files, ", "))
			}
		}
		if failed > 0 {
			return 1
		}
		return 0
	}

	server, err := normalizeServerURL(o.url)
	if err != nil {
		fmt.Println("❌", err)
		return 2
	}
	if strings.HasPrefix(server, "http://") && !isLocalOrigin(server) {
		fmt.Println("⚠ Plain HTTP: password and API key travel unencrypted. Use only on a trusted LAN/VPN.")
	}

	apiKey, cookie, err := authenticate(server, o)
	if err != nil {
		fmt.Println("❌", err)
		return 1
	}

	// Models come from the server's own cli-tools config; flags override.
	srvClaude, srvByTool, srvShared := fetchServerModels(server, cookie, tools)
	if cookie == "" {
		fmt.Println("• --api-key given: cannot read the server's models — pass model flags")
	}
	available := listServerModels(server, apiKey)

	claudeModels := map[string]string{}
	var claudeUnset []string
	if hasTool(tools, "claude") {
		for _, t := range claudeTiers {
			m := firstNonEmpty(o.models[t.flag], srvClaude[t.flag])
			if m != "" {
				claudeModels[t.envKey] = m
				warnMissing(available, "claude "+t.flag, m, "--"+t.flag)
			} else {
				claudeUnset = append(claudeUnset, t.flag)
			}
		}
	}
	toolModels := map[string]string{}
	var fromOpencode []string
	for _, id := range tools {
		if id == "claude" || id == "omp" {
			continue
		}
		m := firstNonEmpty(o.model, srvByTool[id], srvShared)
		toolModels[id] = m
		if m != "" {
			warnMissing(available, "model", m, "--model")
		}
		if o.model == "" && srvByTool[id] == "" && srvShared != "" {
			fromOpencode = append(fromOpencode, id)
		}
	}
	if len(fromOpencode) > 0 {
		fmt.Printf("• %s: no own model on the server — using its OpenCode model\n", strings.Join(fromOpencode, ", "))
	}

	failed, skipped := 0, 0
	for _, id := range tools {
		if id != "claude" && id != "omp" && toolModels[id] == "" {
			skipped++
			fmt.Printf("⏭  %s: no model configured on the server — skipped (pass --model <model>)\n", id)
			continue
		}
		files, err := applyTool(id, server, apiKey, toolModels[id], claudeModels)
		if err != nil {
			failed++
			fmt.Printf("❌ %s: %v\n", id, err)
			continue
		}
		fmt.Printf("✅ %s → %s\n", id, strings.Join(files, ", "))
	}

	fmt.Printf("   Base URL: %s/v1\n", server)
	for _, t := range claudeTiers {
		if m := claudeModels[t.envKey]; m != "" {
			fmt.Printf("   %s=%s\n", t.envKey, m)
		}
	}
	if len(claudeUnset) > 0 && hasTool(tools, "claude") {
		fmt.Printf("   Claude %s: not set on the server — Claude Code's own default applies (or pass --%s <model>)\n",
			strings.Join(claudeUnset, ", "), claudeUnset[0])
	}
	for _, id := range tools {
		if m := toolModels[id]; m != "" {
			fmt.Printf("   %s model: %s\n", id, m)
		}
	}
	if hasTool(tools, "omp") {
		fmt.Println("   omp models: every server model, listed under kinerouter in /model")
	}
	fmt.Printf("   Restart the tools to apply. Undo: kinerouter connect --reset --tools %s\n", strings.Join(tools, ","))
	if o.printEnv {
		fmt.Println("\nOpenAI-compatible CLIs (codex, opencode, aider, …):")
		fmt.Printf("   OPENAI_BASE_URL=%s/v1\n   OPENAI_API_KEY=%s\n", server, apiKey)
	}
	if failed > 0 || skipped > 0 {
		return 1
	}
	return 0
}

// authenticate returns (apiKey, sessionCookie). The cookie is "" when the key
// was supplied directly, which only costs the server-side model lookup.
func authenticate(server string, o connectOpts) (string, string, error) {
	if o.apiKey != "" {
		fmt.Println("• Using provided API key")
		return o.apiKey, "", nil
	}
	var saved *string
	if o.password == nil {
		if p, ok := loadSavedPassword(server); ok {
			saved = &p
			fmt.Printf("• Using saved password (%s)\n", savedEnvPath())
		}
	}
	password := ""
	if o.password != nil {
		password = *o.password
	} else if saved != nil {
		password = *saved
	} else {
		p, err := promptPassword()
		if err != nil {
			return "", "", err
		}
		password = p
	}
	fmt.Printf("• Logging in to %s\n", server)
	cookie, err := loginServer(server, password)
	if err != nil {
		if saved != nil {
			return "", "", fmt.Errorf("%w — saved password may be stale; delete %s or pass --password", err, savedEnvPath())
		}
		return "", "", err
	}
	if o.save && (saved == nil || *saved != password) {
		if err := savePassword(server, password); err != nil {
			fmt.Printf("⚠ could not save password: %v\n", err)
		} else {
			fmt.Printf("• Saved password to %s (plain text, mode 600)\n", savedEnvPath())
		}
	}
	key, created, err := getOrCreateKey(server, cookie, o.keyName)
	if err != nil {
		return "", "", err
	}
	verb := "Reusing"
	if created {
		verb = "Created"
	}
	fmt.Printf("• %s API key %q (%s)\n", verb, o.keyName, maskKey(key))
	return key, cookie, nil
}

// ---------------------------------------------------------------- HTTP

var client = &http.Client{Timeout: 20 * time.Second}

var ctrlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// apiCall performs one request; server-supplied error strings are stripped of
// control bytes before they reach the terminal (escape injection).
func apiCall(rawURL, method string, body any, cookie, apiKey string) (int, []byte, http.Header, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, rawURL, rdr)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("cannot reach %s: %w", rawURL, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return res.StatusCode, data, res.Header, nil
}

func serverError(data []byte) string {
	var v map[string]any
	if json.Unmarshal(data, &v) == nil {
		switch e := v["error"].(type) {
		case string:
			return ctrlChars.ReplaceAllString(e, "")
		case map[string]any:
			if s, ok := e["message"].(string); ok {
				return ctrlChars.ReplaceAllString(s, "")
			}
		}
	}
	return ""
}

func loginServer(server, password string) (string, error) {
	status, data, header, err := apiCall(server+"/api/auth/login", http.MethodPost, map[string]any{"password": password}, "", "")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("login failed (%d): %s", status, fallback(serverError(data), "unknown error"))
	}
	for _, c := range header.Values("Set-Cookie") {
		if m := regexp.MustCompile(`(?:^|\s)auth_token=([^;]+)`).FindStringSubmatch(c); m != nil {
			return "auth_token=" + m[1], nil
		}
	}
	return "", errors.New("login succeeded but server returned no session cookie")
}

func getOrCreateKey(server, cookie, name string) (key string, created bool, err error) {
	status, data, _, err := apiCall(server+"/api/keys", http.MethodGet, nil, cookie, "")
	if err != nil {
		return "", false, err
	}
	if status == http.StatusUnauthorized {
		return "", false, errors.New("unauthorized listing API keys — wrong password or session rejected")
	}
	if status != http.StatusOK {
		return "", false, fmt.Errorf("failed to list API keys (%d): %s", status, serverError(data))
	}
	var list struct {
		Keys []struct {
			Name     string `json:"name"`
			Key      string `json:"key"`
			IsActive *bool  `json:"isActive"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return "", false, fmt.Errorf("cannot parse key list: %w", err)
	}
	for _, k := range list.Keys {
		if k.IsActive != nil && !*k.IsActive {
			continue
		}
		if k.Name == name && k.Key != "" {
			return k.Key, false, nil
		}
	}
	status, data, _, err = apiCall(server+"/api/keys", http.MethodPost, map[string]any{"name": name}, cookie, "")
	if err != nil {
		return "", false, err
	}
	var createdKey struct {
		Key string `json:"key"`
	}
	if status != http.StatusCreated || json.Unmarshal(data, &createdKey) != nil || createdKey.Key == "" {
		return "", false, fmt.Errorf("failed to create API key (%d): %s", status, serverError(data))
	}
	return createdKey.Key, true, nil
}

func listServerModels(server, apiKey string) map[string]bool {
	status, data, _, err := apiCall(server+"/v1/models", http.MethodGet, nil, "", apiKey)
	if err != nil || status != http.StatusOK {
		return nil
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	set := map[string]bool{}
	for _, m := range out.Data {
		set[m.ID] = true
	}
	return set
}

func warnMissing(available map[string]bool, label, model, flag string) {
	if available == nil {
		return
	}
	// Claude Code's "[1m]" context marker is part of the env value, not the id.
	if !available[strings.TrimSuffix(model, "[1m]")] {
		fmt.Printf("⚠ %s: %q not listed by server — override with %s <model>\n", label, model, flag)
	}
}

// fetchServerModels reads the model ids the server operator already chose. Only
// ids are taken — never that host's baseUrl or apiKey.
func fetchServerModels(server, cookie string, tools []string) (claude map[string]string, byTool map[string]string, shared string) {
	claude, byTool = map[string]string{}, map[string]string{}
	if cookie == "" {
		return claude, byTool, ""
	}
	get := func(route string) map[string]any {
		status, data, _, err := apiCall(server+"/api/cli-tools/"+route, http.MethodGet, nil, cookie, "")
		if err != nil || status != http.StatusOK {
			return nil
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		return m
	}
	if hasTool(tools, "claude") {
		if env := obj(get("claude-settings"), "settings", "env"); env != nil {
			for _, t := range claudeTiers {
				if s, _ := env[t.envKey].(string); s != "" {
					claude[t.flag] = s
				}
			}
		}
	}
	for _, id := range tools {
		if id == "claude" || id == "omp" || byTool[id] != "" {
			continue
		}
		if m := toolServerModel(id, get(toolRoute(id))); m != "" {
			byTool[id] = m
		}
	}
	// OpenCode doubles as the shared fallback, so read it even when unselected.
	if _, ok := byTool["opencode"]; !ok {
		if m := toolServerModel("opencode", get("opencode-settings")); m != "" {
			byTool["opencode"] = m
		}
	}
	return claude, byTool, byTool["opencode"]
}

var toolRoutes = map[string]string{
	"codex": "codex-settings", "opencode": "opencode-settings", "droid": "droid-settings",
	"crush": "crush-settings", "cline": "cline-settings", "pi": "pi-settings",
}

func toolRoute(id string) string { return toolRoutes[id] }

// toolServerModel extracts that tool's configured model from its status body.
func toolServerModel(id string, data map[string]any) string {
	if data == nil {
		return ""
	}
	switch id {
	case "codex":
		var cfg map[string]any
		if s, _ := data["config"].(string); s != "" && toml.Unmarshal([]byte(s), &cfg) == nil {
			if cfg["model_provider"] == "kinerouter" {
				m, _ := cfg["model"].(string)
				return m
			}
		}
	case "opencode":
		if m, _ := obj(data, "config")["model"].(string); strings.HasPrefix(m, "kinerouter/") {
			return strings.TrimPrefix(m, "kinerouter/")
		}
	case "droid":
		for _, m := range arr(obj(obj(data, "settings"), "customModels")) {
			mm, _ := m.(map[string]any)
			if idStr, _ := mm["id"].(string); strings.HasPrefix(idStr, "custom:KineRouter") {
				s, _ := mm["model"].(string)
				return s
			}
		}
	case "crush", "pi":
		for _, m := range arr(obj(obj(obj(data, "config"), "providers"), "kinerouter", "models")) {
			switch v := m.(type) {
			case string:
				if v != "" {
					return v
				}
			case map[string]any:
				if s, _ := v["id"].(string); s != "" {
					return s
				}
			}
		}
	case "cline":
		st := obj(data, "settings")
		if st["actModeApiProvider"] == "openai" {
			s, _ := st["openAiModelId"].(string)
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- saved password

func savedEnvPath() string {
	return filepath.Join(config.DataDir(), "connect.env")
}

func loadSavedPassword(server string) (string, bool) {
	data, err := os.ReadFile(savedEnvPath())
	if err != nil {
		return "", false
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal([]byte(v), &s) != nil {
			s = strings.Trim(v, `"'`)
		}
		env[k] = s
	}
	if env["KINEROUTER_SERVER"] != server || env["KINEROUTER_PASSWORD"] == "" {
		return "", false
	}
	return env["KINEROUTER_PASSWORD"], true
}

// savePassword binds the password to one server so it is never replayed to a
// different host. Plain text, owner-only.
func savePassword(server, password string) error {
	path := savedEnvPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	srv, _ := json.Marshal(server)
	pw, _ := json.Marshal(password)
	body := []byte("KINEROUTER_SERVER=" + string(srv) + "\nKINEROUTER_PASSWORD=" + string(pw) + "\n")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func promptPassword() (string, error) {
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return "", errors.New("password required: pass --password or set KINEROUTER_PASSWORD")
	}
	fmt.Print("Router dashboard password: ")
	// Echo off while typing; fall back to visible input if the terminal is
	// not a tty x/term understands (pipes already bailed out above).
	if pw, err := term.ReadPassword(int(os.Stdin.Fd())); err == nil {
		fmt.Println()
		return string(pw), nil
	}
	fmt.Println("\n(echo on: terminal did not accept silent input)")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", errors.New("cancelled")
	}
	fmt.Println()
	return strings.TrimRight(string(line), "\r\n"), nil
}

// ---------------------------------------------------------------- tool writers

func applyTool(id, baseURL, apiKey, model string, claudeModels map[string]string) ([]string, error) {
	v1 := strings.TrimSuffix(baseURL, "/") + "/v1"
	switch id {
	case "claude":
		path := filepath.Join(homeDir(), ".claude", "settings.json")
		cfg := readJSONOr(path, map[string]any{})
		cfg["hasCompletedOnboarding"] = true
		env := objMap(cfg["env"])
		if env == nil {
			env = map[string]any{}
		}
		env["ANTHROPIC_BASE_URL"] = v1
		env["ANTHROPIC_AUTH_TOKEN"] = apiKey
		for _, t := range claudeTiers {
			if m := claudeModels[t.envKey]; m != "" {
				env[t.envKey] = m
			} else {
				delete(env, t.envKey) // keep Claude Code's own default
			}
		}
		cfg["env"] = env
		return []string{path}, writeSecretJSON(path, cfg)

	case "codex":
		path := filepath.Join(homeDir(), ".codex", "config.toml")
		cfg := readTOMLOr(path)
		cfg["model"] = model
		cfg["model_provider"] = "kinerouter"
		provs := objMap(cfg["model_providers"])
		if provs == nil {
			provs = map[string]any{}
		}
		// Custom providers ignore auth.json — the key travels as a header.
		provs["kinerouter"] = map[string]any{
			"name": "KineRouter", "base_url": v1, "wire_api": "responses",
			"http_headers": map[string]any{"Authorization": "Bearer " + apiKey},
		}
		cfg["model_providers"] = provs
		agents := objMap(cfg["agents"])
		if agents == nil {
			agents = map[string]any{}
		}
		delete(agents, "subagent")
		agents["default_subagent_model"] = model
		cfg["agents"] = agents
		out, err := toml.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		return []string{path}, writeSecret(path, out)

	case "opencode":
		path := filepath.Join(homeDir(), ".config", "opencode", "opencode.json")
		cfg := readJSONOr(path, map[string]any{})
		provs := objMap(cfg["provider"])
		if provs == nil {
			provs = map[string]any{}
		}
		p := objMap(provs["kinerouter"])
		if p == nil {
			p = map[string]any{"npm": "@ai-sdk/openai-compatible"}
		}
		p["options"] = map[string]any{"baseURL": v1, "apiKey": apiKey}
		p["models"] = map[string]any{model: map[string]any{
			"name": model, "modalities": map[string]any{"input": []any{"text", "image"}, "output": []any{"text"}},
		}}
		provs["kinerouter"] = p
		cfg["provider"] = provs
		cfg["model"] = "kinerouter/" + model
		agents := objMap(cfg["agent"])
		if agents == nil {
			agents = map[string]any{}
		}
		agents["explorer"] = map[string]any{
			"description": "Fast explorer subagent for codebase exploration",
			"mode":        "subagent", "model": "kinerouter/" + model,
		}
		cfg["agent"] = agents
		return []string{path}, writeSecretJSON(path, cfg)

	case "droid":
		path := filepath.Join(homeDir(), ".factory", "settings.json")
		cfg := readJSONOr(path, map[string]any{})
		ours := []any{map[string]any{
			"model": model, "id": "custom:KineRouter-0", "index": 0, "baseUrl": v1, "apiKey": apiKey,
			"displayName": model, "maxOutputTokens": 131072, "noImageSupport": false, "provider": "openai",
		}}
		for _, m := range arr(cfg["customModels"]) {
			mm, _ := m.(map[string]any)
			if s, _ := mm["id"].(string); !strings.HasPrefix(s, "custom:KineRouter") {
				ours = append(ours, m)
			}
		}
		for i, m := range ours {
			if mm, ok := m.(map[string]any); ok {
				mm["index"] = i
			}
		}
		cfg["customModels"] = ours
		return []string{path}, writeSecretJSON(path, cfg)

	case "crush":
		path := filepath.Join(configHome(), "crush", "crush.json")
		cfg := readJSONOr(path, map[string]any{})
		provs := objMap(cfg["providers"])
		if provs == nil {
			provs = map[string]any{}
		}
		provs["kinerouter"] = map[string]any{
			"type": "openai-compat", "base_url": v1, "api_key": apiKey,
			"models": []any{map[string]any{"id": model, "name": model, "context_window": 128000}},
		}
		cfg["providers"] = provs
		return []string{path}, writeSecretJSON(path, cfg)

	case "kilo":
		path := filepath.Join(homeDir(), ".local", "share", "kilo", "auth.json")
		cfg := readJSONOr(path, map[string]any{})
		cfg["openai-compatible"] = map[string]any{"type": "api-key", "apiKey": apiKey, "baseUrl": v1, "model": model}
		return []string{path}, writeSecretJSON(path, cfg)

	case "cline":
		dir := filepath.Join(homeDir(), ".cline", "data")
		statePath := filepath.Join(dir, "globalState.json")
		secretPath := filepath.Join(dir, "secrets.json")
		state := readJSONOr(statePath, map[string]any{})
		state["actModeApiProvider"] = "openai"
		state["planModeApiProvider"] = "openai"
		state["openAiBaseUrl"] = strings.TrimSuffix(baseURL, "/") // Cline wants no /v1
		state["openAiModelId"] = model
		state["planModeOpenAiModelId"] = model
		if err := writeSecretJSON(statePath, state); err != nil {
			return nil, err
		}
		secrets := readJSONOr(secretPath, map[string]any{})
		secrets["openAiApiKey"] = apiKey
		return []string{statePath, secretPath}, writeSecretJSON(secretPath, secrets)

	case "pi":
		agentPath := filepath.Join(homeDir(), ".pi", "agent", "models.json")
		rootPath := filepath.Join(homeDir(), ".pi", "models.json")
		path := agentPath
		if !fileExists(agentPath) && fileExists(rootPath) {
			path = rootPath
		}
		cfg := readJSONOr(path, map[string]any{})
		provs := objMap(cfg["providers"])
		if provs == nil {
			provs = map[string]any{}
		}
		provs["kinerouter"] = map[string]any{
			"baseUrl": v1, "apiKey": apiKey, "api": "openai-completions",
			"models": []any{map[string]any{"id": model, "name": model, "contextWindow": 128000, "maxTokens": 16384}},
		}
		cfg["providers"] = provs
		return []string{path}, writeSecretJSON(path, cfg)

	case "omp":
		path := filepath.Join(homeDir(), ".omp", "agent", "models.yml")
		content := ompStripBlock(readText(path))
		block := "  kinerouter:\n" +
			"    baseUrl: " + v1 + "\n" +
			"    apiKey: " + apiKey + "\n" +
			"    api: openai-completions\n" +
			"    authHeader: true\n" +
			"    disableStrictTools: true\n" +
			"    discovery:\n" +
			"      type: proxy"
		switch {
		case strings.TrimSpace(content) == "":
			content = "providers:\n" + block + "\n"
		case strings.Contains(content, "providers:"):
			content = strings.Replace(content, "providers:", "providers:\n"+block, 1)
		default:
			content = strings.TrimSpace(content) + "\n\nproviders:\n" + block + "\n"
		}
		return []string{path}, writeSecret(path, []byte(content))
	}
	return nil, fmt.Errorf("unknown tool %q", id)
}

func resetTool(id string) ([]string, error) {
	home := homeDir()
	switch id {
	case "claude":
		path := filepath.Join(home, ".claude", "settings.json")
		cfg := readJSONOrNil(path)
		if cfg == nil {
			return nil, nil
		}
		if env := objMap(cfg["env"]); env != nil {
			delete(env, "ANTHROPIC_BASE_URL")
			delete(env, "ANTHROPIC_AUTH_TOKEN")
			for _, t := range claudeTiers {
				delete(env, t.envKey)
			}
			if len(env) == 0 {
				delete(cfg, "env")
			}
		}
		return []string{path}, writeSecretJSON(path, cfg)

	case "codex":
		path := filepath.Join(home, ".codex", "config.toml")
		cfg := readTOMLOr(path)
		if len(cfg) == 0 {
			return nil, nil
		}
		if cfg["model_provider"] == "kinerouter" {
			delete(cfg, "model")
			delete(cfg, "model_provider")
		}
		if provs := objMap(cfg["model_providers"]); provs != nil {
			delete(provs, "kinerouter")
			if len(provs) == 0 {
				delete(cfg, "model_providers")
			}
		}
		if agents := objMap(cfg["agents"]); agents != nil {
			delete(agents, "default_subagent_model")
			delete(agents, "subagent")
			if len(agents) == 0 {
				delete(cfg, "agents")
			}
		}
		out, err := toml.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		return []string{path}, writeSecret(path, out)

	case "opencode":
		path := filepath.Join(home, ".config", "opencode", "opencode.json")
		cfg := readJSONOrNil(path)
		if cfg == nil {
			return nil, nil
		}
		if provs := objMap(cfg["provider"]); provs != nil {
			delete(provs, "kinerouter")
		}
		if m, _ := cfg["model"].(string); strings.HasPrefix(m, "kinerouter/") {
			delete(cfg, "model")
		}
		if agents := objMap(cfg["agent"]); agents != nil {
			if ex := objMap(agents["explorer"]); ex != nil {
				if m, _ := ex["model"].(string); strings.HasPrefix(m, "kinerouter/") {
					delete(agents, "explorer")
				}
			}
			if len(agents) == 0 {
				delete(cfg, "agent")
			}
		}
		return []string{path}, writeSecretJSON(path, cfg)

	case "droid":
		path := filepath.Join(home, ".factory", "settings.json")
		cfg := readJSONOrNil(path)
		if cfg == nil {
			return nil, nil
		}
		var keep []any
		for _, m := range arr(cfg["customModels"]) {
			mm, _ := m.(map[string]any)
			if s, _ := mm["id"].(string); !strings.HasPrefix(s, "custom:KineRouter") {
				keep = append(keep, m)
			}
		}
		if keep == nil {
			delete(cfg, "customModels")
		} else {
			cfg["customModels"] = keep
		}
		return []string{path}, writeSecretJSON(path, cfg)

	case "crush":
		path := filepath.Join(configHome(), "crush", "crush.json")
		cfg := readJSONOrNil(path)
		if cfg == nil {
			return nil, nil
		}
		provs := objMap(cfg["providers"])
		if provs == nil || provs["kinerouter"] == nil {
			return nil, nil
		}
		delete(provs, "kinerouter")
		if len(provs) == 0 {
			delete(cfg, "providers")
		}
		return []string{path}, writeSecretJSON(path, cfg)

	case "kilo":
		path := filepath.Join(home, ".local", "share", "kilo", "auth.json")
		cfg := readJSONOrNil(path)
		if cfg == nil {
			return nil, nil
		}
		delete(cfg, "openai-compatible")
		delete(cfg, "kinerouter")
		return []string{path}, writeSecretJSON(path, cfg)

	case "cline":
		dir := filepath.Join(home, ".cline", "data")
		statePath := filepath.Join(dir, "globalState.json")
		secretPath := filepath.Join(dir, "secrets.json")
		state := readJSONOrNil(statePath)
		if state == nil {
			return nil, nil
		}
		var touched []string
		if state["actModeApiProvider"] == "openai" {
			delete(state, "openAiBaseUrl")
			delete(state, "openAiModelId")
			delete(state, "planModeOpenAiModelId")
			state["actModeApiProvider"] = "cline"
			state["planModeApiProvider"] = "cline"
			if err := writeSecretJSON(statePath, state); err != nil {
				return nil, err
			}
			touched = append(touched, statePath)
		}
		if secrets := readJSONOrNil(secretPath); secrets != nil {
			delete(secrets, "openAiApiKey")
			if err := writeSecretJSON(secretPath, secrets); err != nil {
				return nil, err
			}
			touched = append(touched, secretPath)
		}
		return touched, nil

	case "pi":
		for _, p := range []string{
			filepath.Join(home, ".pi", "agent", "models.json"), filepath.Join(home, ".pi", "models.json"),
		} {
			cfg := readJSONOrNil(p)
			if cfg == nil {
				continue
			}
			provs := objMap(cfg["providers"])
			if provs == nil || provs["kinerouter"] == nil {
				continue
			}
			delete(provs, "kinerouter")
			if len(provs) == 0 {
				delete(cfg, "providers")
			}
			return []string{p}, writeSecretJSON(p, cfg)
		}
		return nil, nil

	case "omp":
		path := filepath.Join(home, ".omp", "agent", "models.yml")
		content := ompStripBlock(readText(path))
		if strings.TrimSpace(content) == "providers:" || strings.TrimSpace(content) == "" {
			if !fileExists(path) {
				return nil, nil
			}
			return []string{path}, os.Remove(path)
		}
		return []string{path}, writeSecret(path, []byte(content))
	}
	return nil, fmt.Errorf("unknown tool %q", id)
}

// ---------------------------------------------------------------- show

type showRecord struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Files      []string          `json:"files"`
	Configured bool              `json:"configured"`
	BaseURL    string            `json:"baseUrl,omitempty"`
	APIKey     string            `json:"apiKey,omitempty"`
	Models     map[string]string `json:"models,omitempty"`
	Active     *bool             `json:"active,omitempty"`
	Error      string            `json:"error,omitempty"`
}

func runShow(argv []string) int {
	var ids []string
	asJSON := false
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help":
			fmt.Print(cliHelp)
			return 0
		case a == "--tools":
			i++
			if i < len(argv) {
				ids = append(ids, splitCSV(argv[i])...)
			}
		case strings.HasPrefix(a, "--tools="):
			ids = append(ids, splitCSV(strings.TrimPrefix(a, "--tools="))...)
		case strings.HasPrefix(a, "-"):
			fmt.Printf("❌ unknown option: %s\n", a)
			return 2
		default:
			ids = append(ids, splitCSV(a)...)
		}
	}
	tools, err := resolveTools(ids, "")
	if err != nil {
		fmt.Println("❌", err)
		return 2
	}
	records := make([]showRecord, 0, len(tools))
	bad := false
	for _, id := range tools {
		r := inspectTool(id)
		if r.Error != "" {
			bad = true
		}
		records = append(records, r)
	}
	if asJSON {
		out, _ := json.MarshalIndent(records, "", "  ")
		fmt.Println(string(out))
	} else {
		for i, r := range records {
			if i > 0 {
				fmt.Println()
			}
			printRecord(r)
		}
	}
	if bad {
		return 1
	}
	return 0
}

func printRecord(r showRecord) {
	if r.Error != "" {
		fmt.Printf("❌ %s: %s\n", r.Name, r.Error)
		return
	}
	if !r.Configured {
		fmt.Printf("• %s: not configured for the router (%s)\n", r.Name, r.Files[0])
		return
	}
	inactive := ""
	if r.Active != nil && !*r.Active {
		inactive = "  (router present but not the active provider)"
	}
	fmt.Printf("✅ %s%s\n", r.Name, inactive)
	fmt.Printf("   File:     %s\n", strings.Join(r.Files, ", "))
	fmt.Printf("   Base URL: %s\n", fallback(r.BaseURL, "(none)"))
	fmt.Printf("   API key:  %s\n", fallback(r.APIKey, "(none)"))
	if len(r.Models) == 0 {
		fmt.Println("   Models:   (none set — the tool's own default applies)")
		return
	}
	width := 0
	for k := range r.Models {
		width = max(width, len(k))
	}
	fmt.Println("   Models:")
	for _, k := range sortedKeys(r.Models) {
		fmt.Printf("     %-*s  %s\n", width, k, r.Models[k])
	}
}

func inspectTool(id string) showRecord {
	home := homeDir()
	r := showRecord{ID: id, Name: toolName(id), Models: map[string]string{}}
	boolp := func(b bool) *bool { return &b }
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }

	switch id {
	case "claude":
		r.Files = []string{filepath.Join(home, ".claude", "settings.json")}
		env := objMap(readJSONOrNil(r.Files[0])["env"])
		r.BaseURL, r.APIKey = str(env, "ANTHROPIC_BASE_URL"), maskKey(str(env, "ANTHROPIC_AUTH_TOKEN"))
		if m := str(env, "ANTHROPIC_MODEL"); m != "" {
			r.Models["main"] = m
		}
		for _, t := range claudeTiers {
			if m := str(env, t.envKey); m != "" {
				r.Models[t.flag] = m
			}
		}

	case "codex":
		r.Files = []string{filepath.Join(home, ".codex", "config.toml")}
		cfg := readTOMLOr(r.Files[0])
		p := objMap(objMap(cfg["model_providers"])["kinerouter"])
		r.BaseURL = str(p, "base_url")
		r.APIKey = maskKey(strings.TrimPrefix(str(objMap(p["http_headers"]), "Authorization"), "Bearer "))
		if m := str(cfg, "model"); m != "" {
			r.Models["main"] = m
		}
		if m := str(objMap(cfg["agents"]), "default_subagent_model"); m != "" {
			r.Models["subagent"] = m
		}
		if p != nil {
			r.Active = boolp(cfg["model_provider"] == "kinerouter")
		}

	case "opencode":
		r.Files = []string{filepath.Join(home, ".config", "opencode", "opencode.json")}
		cfg := readJSONOrNil(r.Files[0])
		p := objMap(objMap(cfg["provider"])["kinerouter"])
		opts := objMap(p["options"])
		r.BaseURL, r.APIKey = str(opts, "baseURL"), maskKey(str(opts, "apiKey"))
		if m := str(cfg, "model"); m != "" {
			r.Models["main"] = m
		}
		if m := str(objMap(objMap(cfg["agent"])["explorer"]), "model"); m != "" {
			r.Models["explorer"] = m
		}
		if p != nil {
			r.Active = boolp(strings.HasPrefix(str(cfg, "model"), "kinerouter/"))
		}

	case "droid":
		r.Files = []string{filepath.Join(home, ".factory", "settings.json")}
		cfg := readJSONOrNil(r.Files[0])
		var ours []string
		for _, m := range arr(cfg["customModels"]) {
			mm, _ := m.(map[string]any)
			if s, _ := mm["id"].(string); strings.HasPrefix(s, "custom:KineRouter") {
				if r.BaseURL == "" {
					r.BaseURL, r.APIKey = str(mm, "baseUrl"), maskKey(str(mm, "apiKey"))
				}
				ours = append(ours, str(mm, "model"))
			}
		}
		if len(ours) > 0 {
			r.Models["main"] = ours[0]
			if len(ours) > 1 {
				r.Models["available"] = strings.Join(ours, ", ")
			}
		}

	case "crush":
		r.Files = []string{filepath.Join(configHome(), "crush", "crush.json")}
		p := objMap(objMap(readJSONOrNil(r.Files[0])["providers"])["kinerouter"])
		r.BaseURL, r.APIKey = str(p, "base_url"), maskKey(str(p, "api_key"))
		for _, m := range arr(p["models"]) {
			if mm, ok := m.(map[string]any); ok {
				if s := str(mm, "id"); s != "" && r.Models["main"] == "" {
					r.Models["main"] = s
				}
			}
		}

	case "kilo":
		r.Files = []string{filepath.Join(home, ".local", "share", "kilo", "auth.json")}
		a := objMap(readJSONOrNil(r.Files[0])["openai-compatible"])
		r.BaseURL, r.APIKey = str(a, "baseUrl"), maskKey(str(a, "apiKey"))
		if m := str(a, "model"); m != "" {
			r.Models["main"] = m
		}

	case "cline":
		dir := filepath.Join(home, ".cline", "data")
		r.Files = []string{filepath.Join(dir, "globalState.json"), filepath.Join(dir, "secrets.json")}
		st := readJSONOrNil(r.Files[0])
		r.BaseURL = str(st, "openAiBaseUrl")
		r.APIKey = maskKey(str(readJSONOr(r.Files[1], nil), "openAiApiKey"))
		if m := str(st, "openAiModelId"); m != "" {
			r.Models["act"] = m
		}
		if m := str(st, "planModeOpenAiModelId"); m != "" {
			r.Models["plan"] = m
		}
		if st != nil {
			r.Active = boolp(st["actModeApiProvider"] == "openai")
		}

	case "pi":
		agentPath := filepath.Join(home, ".pi", "agent", "models.json")
		rootPath := filepath.Join(home, ".pi", "models.json")
		r.Files = []string{agentPath}
		if !fileExists(agentPath) && fileExists(rootPath) {
			r.Files[0] = rootPath
		}
		p := objMap(objMap(readJSONOrNil(r.Files[0])["providers"])["kinerouter"])
		r.BaseURL, r.APIKey = str(p, "baseUrl"), maskKey(str(p, "apiKey"))
		for _, m := range arr(p["models"]) {
			switch v := m.(type) {
			case string:
				if v != "" && r.Models["main"] == "" {
					r.Models["main"] = v
				}
			case map[string]any:
				if s := str(v, "id"); s != "" && r.Models["main"] == "" {
					r.Models["main"] = s
				}
			}
		}

	case "omp":
		r.Files = []string{filepath.Join(home, ".omp", "agent", "models.yml")}
		text := readText(r.Files[0])
		r.BaseURL = yamlValue(text, "baseUrl")
		r.APIKey = maskKey(yamlValue(text, "apiKey"))
		if strings.Contains(text, "type: proxy") {
			r.Models["main"] = "(all server models, via discovery)"
		}
	}
	r.Configured = r.BaseURL != "" || r.APIKey != "" || len(r.Models) > 0
	return r
}

var yamlKV = regexp.MustCompile(`(?m)^\s+(baseUrl|apiKey):\s*["']?([^"'\r\n]+)["']?`)

func yamlValue(text, key string) string {
	for _, m := range yamlKV.FindAllStringSubmatch(text, -1) {
		if m[1] == key {
			return strings.TrimSpace(m[2])
		}
	}
	return ""
}

// ompStripBlock removes the kinerouter provider block from models.yml by
// indentation, the same way the dashboard route does.
func ompStripBlock(content string) string {
	lines := strings.Split(content, "\n")
	start, indent := -1, 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "kinerouter:" {
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
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		cur := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		if cur <= indent && strings.HasSuffix(trimmed, ":") {
			end = i
			break
		}
	}
	return strings.Join(append(append([]string{}, lines[:start]...), lines[end:]...), "\n")
}

func toolName(id string) string {
	switch id {
	case "claude":
		return "Claude Code"
	case "codex":
		return "OpenAI Codex CLI"
	case "opencode":
		return "OpenCode"
	case "droid":
		return "Factory Droid"
	case "kilo":
		return "Kilo Code CLI"
	case "cline":
		return "Cline CLI"
	case "pi":
		return "Pi"
	case "omp":
		return "Oh My Pi"
	}
	if id == "" {
		return id
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// ---------------------------------------------------------------- helpers

func resolveTools(ids []string, dflt string) ([]string, error) {
	if len(ids) == 0 {
		if dflt == "" {
			return append([]string{}, cliToolIDs...), nil
		}
		ids = []string{dflt}
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range ids {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		if id == "all" {
			for _, t := range cliToolIDs {
				if !seen[t] {
					seen[t] = true
					out = append(out, t)
				}
			}
			continue
		}
		if real, ok := cliToolAliases[id]; ok {
			id = real
		}
		if !contains(cliToolIDs, id) {
			return nil, fmt.Errorf("unknown tool %q. Supported: %s, all", raw, strings.Join(cliToolIDs, ", "))
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func normalizeServerURL(input string) (string, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", errors.New("server URL required")
	}
	if !regexp.MustCompile(`(?i)^https?://`).MatchString(raw) {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid server URL: %s", input)
	}
	return u.Scheme + "://" + u.Host, nil
}

func isLocalOrigin(server string) bool {
	u, err := url.Parse(server)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func maskKey(key string) string {
	if len(key) < 12 {
		return ""
	}
	return key[:6] + "…" + key[len(key)-4:]
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hasTool(list []string, id string) bool { return contains(list, id) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func fallback(v, dflt string) string {
	if v == "" {
		return dflt
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

func configHome() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), ".config")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func readText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readJSONOr(path string, dflt map[string]any) map[string]any {
	text := readText(path)
	if text == "" {
		return dflt
	}
	var m map[string]any
	// JSONC (trailing commas) is tolerated, like the dashboard writers.
	if json.Unmarshal(stripTrailingCommas(text), &m) != nil {
		return dflt
	}
	return m
}

// readJSONOrNil reads a JSON/JSONC file, returning nil when it is absent or
// unparsable, so callers can tell "no file" from "empty object".
func readJSONOrNil(path string) map[string]any {
	if readText(path) == "" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(stripTrailingCommas(readText(path)), &m) != nil {
		return nil
	}
	return m
}

func readTOMLOr(path string) map[string]any {
	m := map[string]any{}
	if text := readText(path); text != "" {
		_ = toml.Unmarshal([]byte(text), &m)
	}
	return m
}

var jsoncTrailingComma = regexp.MustCompile(`("(?:[^"\\]|\\.)*")|,(\s*[}\]])`)

func stripTrailingCommas(text string) []byte {
	return jsoncTrailingComma.ReplaceAllFunc([]byte(text), func(m []byte) []byte {
		if m[0] == '"' {
			return m
		}
		return m[1:] // drop the comma, keep the brace
	})
}

// writeSecretJSON / writeSecret write a file that holds credentials 0600.
func writeSecretJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeSecret(path, append(b, '\n'))
}

func writeSecret(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600) // mode above only applies on create
}

func objMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// obj walks nested maps without nil panics.
func obj(v any, keys ...string) map[string]any {
	cur := objMap(v)
	for _, k := range keys {
		if cur == nil {
			return nil
		}
		nxt, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = nxt
	}
	return cur
}

func arr(v any) []any {
	a, _ := v.([]any)
	return a
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// runTray execs the companion `kinerouter-tray` binary that sits next to this
// one. It is a separate program because linking a GUI toolkit here would force
// cgo into every release target; see cmd/kinerouter-tray/main.go.
func runTray(args []string) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tray: %v\n", err)
		return 1
	}
	name := "kinerouter-tray"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(filepath.Dir(self), name)
	if _, err := os.Stat(bin); err != nil {
		fmt.Fprintf(os.Stderr,
			"tray: %s not found.\n"+
				"The tray ships with Windows releases only; elsewhere build it beside this binary:\n"+
				"  go build -tags tray -o kinerouter-tray ./cmd/kinerouter-tray\n", bin)
		return 1
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "tray: %v\n", err)
		return 1
	}
	return 0
}
