package main

// Terminal UI: a port of 9Router's cli/src/cli/terminalUI.js and cli/menus/*.
// The tree, labels, colours and prompts follow the reference; only the backend
// calls differ, because each feature already exists as a KineRouter API route.

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	tuiReset   = "\x1b[0m"
	tuiBold    = "\x1b[1m"
	tuiDim     = "\x1b[2m"
	tuiGreen   = "\x1b[32m"
	tuiRed     = "\x1b[31m"
	tuiYellow  = "\x1b[33m"
	tuiCyan    = "\x1b[36m"
	tuiDefault = "\x1b[38;2;217;119;87m"
)

type tuiItem struct {
	label  func(any) string
	action func(any) bool
}

func fixedLabel(s string) func(any) string { return func(any) string { return s } }

func runTUI(base string, port int, dataDir string, in *bufio.Reader) {
	t := newTUI(base, port, dataDir)
	t.in = in
	t.mainMenu()
}

// ---------------------------------------------------------------- chrome

func (t *tui) prompt(question string) string {
	fmt.Print(question)
	line, _ := t.in.ReadString('\n')
	return strings.TrimSpace(line)
}

func (t *tui) confirm(question string) bool {
	for {
		switch strings.ToLower(t.prompt(question + " (y/n): ")) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Println("Please answer 'y' or 'n'")
	}
}

func (t *tui) pause() {
	fmt.Print("Press Enter to continue...")
	_, _ = t.in.ReadString('\n')
}

func (t *tui) status(kind, message string) {
	symbols := map[string]string{"success": "✓", "error": "✗", "warning": "⚠", "info": "ℹ"}
	colors := map[string]string{"success": tuiGreen, "error": tuiRed, "warning": tuiYellow, "info": tuiCyan}
	fmt.Printf("%s%s %s%s\n", colors[kind], symbols[kind], message, tuiReset)
}

func clearScreen() { fmt.Print("\x1b[2J\x1b[H") }

func copyToClipboard(s string) bool {
	fmt.Printf("\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(s)))
	return true
}

// menu reproduces showMenuWithBack: a Back row first, rows from build(data),
// a header from header(data) and refresh() before every render.
func (t *tui) menu(title string, breadcrumb []string, header func(any) string, refresh func() any, build func(any) []tuiItem, backLabel string) {
	if backLabel == "" {
		backLabel = "← Back"
	}
	for {
		var data any
		if refresh != nil {
			data = refresh()
			if data == nil {
				return
			}
		}
		items := build(data)
		rows := make([]string, 0, len(items)+1)
		rows = append(rows, backLabel)
		for _, it := range items {
			rows = append(rows, it.label(data))
		}
		h := ""
		if header != nil {
			h = header(data)
		}
		sel, err := selectMenu(title, "", breadcrumb, h, rows, 0)
		if err != nil || sel <= 0 {
			return
		}
		if !items[sel-1].action(data) {
			return
		}
	}
}

// ---------------------------------------------------------------- header

func (t *tui) localEndpoint() string { return fmt.Sprintf("http://localhost:%d/v1", t.port) }

func (t *tui) endpoint() string {
	res, err := t.do("GET", "/api/tunnel/status", nil)
	if err != nil {
		return t.localEndpoint()
	}
	tun := asMap(res["tunnel"])
	if asBool(tun["enabled"]) && asStr(tun["publicUrl"]) != "" {
		return asStr(tun["publicUrl"]) + "/v1"
	}
	return t.localEndpoint()
}

func (t *tui) header() string {
	res, _ := t.do("GET", "/api/tunnel/status", nil)
	tun := asMap(res["tunnel"])
	lines := []string{}
	if asBool(tun["enabled"]) && asStr(tun["publicUrl"]) != "" {
		lines = append(lines, "Endpoint: "+tuiGreen+asStr(tun["publicUrl"])+"/v1"+tuiReset)
		lines = append(lines, "Tunnel:   "+tuiGreen+"ON"+tuiReset+" "+tuiDim+"("+asStr(tun["shortId"])+")"+tuiReset)
	} else {
		lines = append(lines, "Endpoint: "+t.localEndpoint())
		lines = append(lines, "Tunnel:   "+tuiRed+"OFF"+tuiReset+" "+tuiDim+"(local only)"+tuiReset)
	}
	keys, _ := t.keys()
	if len(keys) == 0 {
		lines = append(lines, "Key:      "+tuiDim+"No API keys yet"+tuiReset)
	} else {
		lines = append(lines, "Key:      "+tuiCyan+asStr(keys[0]["key"])+tuiReset)
		for _, k := range keys[1:] {
			lines = append(lines, "          "+tuiCyan+asStr(k["key"])+tuiReset)
		}
	}
	return strings.Join(lines, "\n")
}

func (t *tui) keys() ([]map[string]any, error) {
	res, err := t.do("GET", "/api/keys", nil)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, k := range asList(res["keys"]) {
		if m := asMap(k); m != nil {
			out = append(out, m)
		}
	}
	return out, nil
}

func (t *tui) firstKey() string {
	keys, _ := t.keys()
	if len(keys) == 0 {
		return ""
	}
	return asStr(keys[0]["key"])
}

// ---------------------------------------------------------------- main

func (t *tui) mainMenu() {
	items := []tuiItem{
		{fixedLabel("Providers"), func(any) bool { t.providersMenu([]string{"KineRouter", "Providers"}); return true }},
		{fixedLabel("API Keys"), func(any) bool { t.apiKeysMenu([]string{"KineRouter", "API Keys"}); return true }},
		{fixedLabel("Combos"), func(any) bool { t.combosMenu([]string{"KineRouter", "Combos"}); return true }},
		{fixedLabel("CLI Tools"), func(any) bool { t.cliToolsMenu([]string{"KineRouter", "CLI Tools"}); return true }},
		{fixedLabel("Settings"), func(any) bool { t.settingsMenu([]string{"KineRouter", "Settings"}); return true }},
	}
	t.menu("📡 KineRouter Terminal UI", []string{"KineRouter"}, func(any) string { return t.header() },
		nil, func(any) []tuiItem { return items }, "← Back to Interface Menu")
}

// ---------------------------------------------------------------- providers

type providerData struct {
	providers []any
	nodes     []any
}

// providerMenuEntry is one row of 9Router's OAUTH_PROVIDERS/APIKEY_PROVIDERS
// tables: the id the API takes, the display name it shows and the group label.
type providerMenuEntry struct {
	id, name, group string
}

var providerMenuEntries = []providerMenuEntry{
	{"claude", "Claude Code", "oauth"},
	{"codex", "OpenAI Codex", "oauth"},
	{"gemini-cli", "Gemini CLI", "oauth"},
	{"github", "GitHub Copilot", "oauth"},
	{"antigravity", "Antigravity", "oauth"},
	{"iflow", "iFlow AI", "oauth"},
	{"qwen", "Qwen Code", "oauth"},
	{"kiro", "Kiro AI", "oauth"},
	{"glm", "Zai GLM Coding", "oauth"},
	{"openrouter", "OpenRouter", "api"},
	{"glm", "Zai GLM Coding", "api"},
	{"minimax", "Minimax Coding", "api"},
	{"kimi", "Kimi", "api"},
	{"openai", "OpenAI", "api"},
	{"anthropic", "Anthropic", "api"},
	{"gemini", "Gemini", "api"},
}

func (t *tui) providersMenu(breadcrumb []string) {
	refresh := func() any {
		res, err := t.do("GET", "/api/providers", nil)
		if err != nil {
			clearScreen()
			t.status("error", "Failed to fetch providers: "+err.Error())
			t.pause()
			return nil
		}
		nodes, _ := t.do("GET", "/api/provider-nodes", nil)
		return providerData{asList(res["providers"]), asList(nodes["nodes"])}
	}
	build := func(raw any) []tuiItem {
		d := raw.(providerData)
		byID := map[string]map[string]any{}
		for _, pv := range d.providers {
			if p := asMap(pv); p != nil {
				byID[asStr(p["id"])] = p
			}
		}
		var items []tuiItem
		for _, def := range providerMenuEntries {
			entry := def
			prov := byID[entry.id]
			if prov == nil {
				continue
			}
			label := fmt.Sprintf("%s (%s) - %d Connected", entry.name, groupName(entry.group), asInt(prov["connections"]))
			items = append(items, tuiItem{
				label: fixedLabel(label),
				action: func(any) bool {
					t.providerDetail(prov, entry.group, append(breadcrumb, entry.name))
					return true
				},
			})
		}
		items = append(items, tuiItem{label: fixedLabel(tuiDim + "── Custom Providers ──" + tuiReset), action: func(any) bool { return true }})
		items = append(items, tuiItem{
			label: fixedLabel(fmt.Sprintf("Custom Providers - %d Configured", len(d.nodes))),
			action: func(any) bool {
				t.customProvidersMenu(append(breadcrumb, "Custom Providers"))
				return true
			},
		})
		return items
	}
	t.menu("🔌 Providers Management", breadcrumb, nil, refresh, build, "")
}

func providerName(p map[string]any) string {
	if n := asStr(pick(p, "display", "name")); n != "" {
		return n
	}
	if n := asStr(p["name"]); n != "" {
		return n
	}
	return asStr(p["id"])
}

func groupName(group string) string {
	if group == "oauth" {
		return "OAuth"
	}
	return "API"
}

func providerGroup(p map[string]any) string {
	if strings.EqualFold(asStr(p["authType"]), "oauth") {
		return "oauth"
	}
	return "api"
}

func (t *tui) providerDetail(prov map[string]any, group string, breadcrumb []string) {
	pid := asStr(prov["id"])
	alias := asStr(prov["alias"])
	if alias == "" {
		alias = pid
	}

	header := func(any) string {
		lines := []string{"Alias: " + tuiCyan + alias + tuiReset}
		var names []string
		for _, m := range asList(prov["models"]) {
			if id := asStr(asMap(m)["id"]); id != "" {
				names = append(names, alias+"/"+id)
			}
		}
		shown := names
		if len(shown) > 5 {
			shown = shown[:5]
		}
		if len(names) == 0 {
			lines = append(lines, "Models: "+tuiDim+"No models configured"+tuiReset)
		} else {
			more := ""
			if len(names) > 5 {
				more = fmt.Sprintf(" (+%d more)", len(names)-5)
			}
			lines = append(lines, "Models: "+tuiDim+strings.Join(shown, ", ")+more+tuiReset)
		}
		return strings.Join(lines, "\n")
	}

	conns := func() []map[string]any {
		res, _ := t.do("GET", "/api/providers", nil)
		var out []map[string]any
		for _, cv := range asList(res["connections"]) {
			c := asMap(cv)
			if asStr(c["provider"]) == pid {
				out = append(out, c)
			}
		}
		return out
	}

	build := func(any) []tuiItem {
		items := []tuiItem{{
			label: fixedLabel("Add New Connection"),
			action: func(any) bool {
				t.addConnection(prov)
				return true
			},
		}}
		for _, c := range conns() {
			conn := c
			items = append(items, tuiItem{
				label: fixedLabel(connName(conn) + " (" + connStatus(conn) + ")"),
				action: func(any) bool {
					t.connectionActions(conn, prov, breadcrumb)
					return true
				},
			})
		}
		return items
	}
	titleGroup := "OAUTH"
	if group == "api" {
		titleGroup = "APIKEY"
	}
	title := fmt.Sprintf("🔌 %s (%s)", providerName(prov), titleGroup)
	t.menu(title, breadcrumb, header, nil, build, "← Back to Providers")
}

func connName(c map[string]any) string {
	for _, k := range []string{"name", "email", "displayName"} {
		if s := asStr(c[k]); s != "" {
			return s
		}
	}
	return "Unnamed"
}

func connStatus(c map[string]any) string {
	switch asStr(c["testStatus"]) {
	case "active":
		return "✓"
	case "error":
		return "✗"
	}
	return "?"
}

func (t *tui) connectionActions(conn, prov map[string]any, breadcrumb []string) {
	cid := asStr(conn["id"])
	name := connName(conn)
	status := "? Unknown"
	switch asStr(conn["testStatus"]) {
	case "active":
		status = "✓ Active"
	case "error":
		status = "✗ Error"
	}
	header := func(any) string {
		return "Connection: " + name + "\nStatus: " + status
	}
	items := []tuiItem{
		{fixedLabel("Rename Connection"), func(any) bool {
			newName := t.prompt(fmt.Sprintf("New name (current: %s): ", name))
			if newName == "" {
				return true
			}
			t.status("info", "Renaming connection...")
			if _, err := t.do("PATCH", "/api/connections/"+cid, map[string]any{"name": newName}); err != nil {
				t.status("error", "Rename failed: "+err.Error())
			} else {
				t.status("success", "Connection renamed!")
			}
			t.pause()
			return true
		}},
		{fixedLabel("Test Connection"), func(any) bool {
			t.status("info", "Testing connection...")
			res, err := t.do("POST", "/api/providers/"+cid+"/test", nil)
			switch {
			case err != nil:
				t.status("error", "Test failed: "+err.Error())
			case asBool(res["valid"]):
				t.status("success", "Connection is working!")
			default:
				t.status("error", "Test failed: "+asStr(res["error"]))
			}
			t.pause()
			return true
		}},
		{fixedLabel("Delete Connection"), func(any) bool {
			if !t.confirm(fmt.Sprintf("Delete connection %q?", name)) {
				return true
			}
			if _, err := t.do("DELETE", "/api/connections/"+cid, nil); err != nil {
				t.status("error", "Delete failed: "+err.Error())
			} else {
				t.status("success", "Connection deleted!")
			}
			t.pause()
			return false
		}},
	}
	t.menu(fmt.Sprintf("🔌 %s", name), append(breadcrumb, name), header, nil,
		func(any) []tuiItem { return items }, "")
}

func (t *tui) addConnection(prov map[string]any) {
	if providerGroup(prov) == "api" {
		t.addAPIKeyConnection(prov)
		return
	}
	pid := asStr(prov["id"])
	res, _ := t.do("GET", "/api/oauth/providers", nil)
	device := false
	for _, p := range asList(res["device"]) {
		if asStr(p) == pid {
			device = true
		}
	}
	if device {
		t.addDeviceConnection(prov)
	} else {
		t.addOAuthConnection(prov)
	}
}

func (t *tui) addAPIKeyConnection(prov map[string]any) {
	clearScreen()
	fmt.Printf("\n➕ Add %s API Key Connection\n\n", providerName(prov))
	name := t.prompt("Connection Name: ")
	if name == "" {
		t.status("warning", "Cancelled")
		t.pause()
		return
	}
	key := t.prompt("API Key: ")
	if key == "" {
		t.status("warning", "Cancelled")
		t.pause()
		return
	}
	t.status("info", "Creating connection...")
	body := map[string]any{"provider": asStr(prov["id"]), "name": name, "apiKey": key}
	if _, err := t.do("POST", "/api/connections", body); err != nil {
		t.status("error", "✗ Failed: "+err.Error())
	} else {
		t.status("success", "✓ Connection created successfully!")
	}
	t.pause()
}

func (t *tui) addOAuthConnection(prov map[string]any) {
	pid := asStr(prov["id"])
	clearScreen()
	t.status("info", "Requesting authorization URL...")
	res, err := t.do("GET", "/api/oauth/"+pid+"/authorize", nil)
	if err != nil {
		t.status("error", "Failed: "+err.Error())
		t.pause()
		return
	}
	authURL := asStr(res["authorizeUrl"])
	state := asStr(res["state"])
	if authURL == "" {
		t.status("error", "Failed: No auth URL received")
		t.pause()
		return
	}
	clearScreen()
	fmt.Printf("\n%s=== 🔐 OAuth Login ===%s\n  Providers > %s > Add Connection\n\n", tuiCyan, tuiReset, providerName(prov))
	fmt.Printf("  %s1.%s Open this URL in your browser:\n     %s%s%s\n", tuiBold, tuiReset, tuiDim, authURL, tuiReset)
	if copyToClipboard(authURL) {
		fmt.Printf("     %s✓ Link copied to clipboard!%s\n", tuiGreen, tuiReset)
	}
	fmt.Printf("\n  %s2.%s Complete authorization in browser\n\n", tuiBold, tuiReset)
	fmt.Printf("  %s3.%s Copy the callback URL from address bar\n\n", tuiBold, tuiReset)
	callback := t.prompt("  Paste callback URL: ")
	if callback == "" {
		t.status("warning", "Cancelled")
		t.pause()
		return
	}
	code, urlState := parseCallback(callback)
	if code == "" {
		t.status("error", "No authorization code found in URL")
		t.pause()
		return
	}
	if urlState == "" {
		urlState = state
	}
	fmt.Println()
	t.status("info", "Exchanging code for tokens...")
	if _, err := t.do("POST", "/api/oauth/"+pid+"/exchange", map[string]any{"code": code, "state": urlState}); err != nil {
		t.status("error", "Failed: "+err.Error())
	} else {
		t.status("success", "Connection created successfully!")
	}
	t.pause()
}

// parseCallback pulls code/state out of a pasted callback URL without pulling
// in the URL package's error handling for the common "pasted only the code" case.
func parseCallback(raw string) (code, state string) {
	raw = strings.TrimSpace(raw)
	i := strings.IndexByte(raw, '?')
	if i < 0 {
		return raw, ""
	}
	for _, part := range strings.Split(raw[i+1:], "&") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		val := kv[1]
		if j := strings.IndexByte(val, '#'); j >= 0 {
			val = val[:j]
		}
		switch kv[0] {
		case "code":
			code = val
		case "state":
			state = val
		}
	}
	return code, state
}

func (t *tui) addDeviceConnection(prov map[string]any) {
	pid := asStr(prov["id"])
	clearScreen()
	t.status("info", "Requesting device code...")
	res, err := t.do("POST", "/api/oauth/"+pid+"/device", nil)
	if err != nil {
		t.status("error", "Failed: "+err.Error())
		t.pause()
		return
	}
	state := asStr(res["state"])
	userCode := asStr(res["userCode"])
	url := asStr(res["verificationUriComplete"])
	if url == "" {
		url = asStr(res["verificationUri"])
	}
	if state == "" {
		t.status("error", "Failed: No device code received")
		t.pause()
		return
	}
	clearScreen()
	fmt.Printf("\n%s=== 📱 Device Login ===%s\n  Providers > %s > Add Connection\n\n", tuiCyan, tuiReset, providerName(prov))
	fmt.Printf("  %s1.%s Open: %s%s%s\n", tuiBold, tuiReset, tuiDim, url, tuiReset)
	if copyToClipboard(url) {
		fmt.Printf("     %s✓ Link copied to clipboard!%s\n", tuiGreen, tuiReset)
	}
	if asStr(res["verificationUriComplete"]) == "" && userCode != "" {
		fmt.Printf("\n  %s2.%s Enter code: %s%s%s\n", tuiBold, tuiReset, tuiBold, userCode, tuiReset)
	}
	fmt.Printf("\n  %sWaiting for authorization...%s\n\n", tuiDim, tuiReset)

	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Second)
		out, err := t.do("GET", "/api/oauth/"+pid+"/poll?state="+state, nil)
		if err == nil && asStr(out["status"]) == "connected" {
			t.status("success", "\nConnection created successfully!")
			t.pause()
			return
		}
		if err != nil {
			t.status("error", "\nFailed: "+err.Error())
			t.pause()
			return
		}
		fmt.Print(".")
	}
	t.status("error", "\nTimeout waiting for authorization")
	t.pause()
}

// ---------------------------------------------------------------- custom nodes

func (t *tui) customProvidersMenu(breadcrumb []string) {
	refresh := func() any {
		res, err := t.do("GET", "/api/provider-nodes", nil)
		if err != nil {
			return []any{}
		}
		return asList(res["nodes"])
	}
	build := func(raw any) []tuiItem {
		items := []tuiItem{{
			label:  fixedLabel("➕ Add Custom Provider"),
			action: func(any) bool { t.addCustomNode(); return true },
		}}
		for _, nv := range raw.([]any) {
			node := asMap(nv)
			label := fmt.Sprintf("[%s] %s (%s)", asStr(node["prefix"]), asStr(node["name"]), asStr(node["type"]))
			items = append(items, tuiItem{
				label: fixedLabel(label),
				action: func(any) bool {
					t.customNodeDetail(node, append(breadcrumb, asStr(node["name"])))
					return true
				},
			})
		}
		return items
	}
	t.menu("🔧 Custom Providers", breadcrumb, nil, refresh, build, "← Back to Providers")
}

func (t *tui) customNodeDetail(node map[string]any, breadcrumb []string) {
	header := func(any) string {
		return "Type: " + asStr(node["type"]) + "\nPrefix: " + tuiCyan + asStr(node["prefix"]) + tuiReset +
			"\nBase URL: " + tuiDim + asStr(node["baseUrl"]) + tuiReset
	}
	items := []tuiItem{
		{fixedLabel("Connections"), func(any) bool {
			t.customNodeConnections(node, breadcrumb)
			return true
		}},
		{fixedLabel("Edit Node"), func(any) bool {
			t.editCustomNode(node)
			return true
		}},
		{fixedLabel("Delete Node"), func(any) bool {
			if !t.confirm(fmt.Sprintf("Delete %q and all its connections?", asStr(node["name"]))) {
				return true
			}
			if _, err := t.do("DELETE", "/api/provider-nodes/"+asStr(node["id"]), nil); err != nil {
				t.status("error", "Delete failed: "+err.Error())
			} else {
				t.status("success", "Node deleted!")
			}
			t.pause()
			return false
		}},
	}
	t.menu("🔧 "+asStr(node["name"]), breadcrumb, header, nil, func(any) []tuiItem { return items }, "")
}

func (t *tui) customNodeConnections(node map[string]any, breadcrumb []string) {
	refresh := func() any {
		res, _ := t.do("GET", "/api/providers", nil)
		var out []any
		for _, cv := range asList(res["connections"]) {
			if c := asMap(cv); asStr(c["provider"]) == asStr(node["id"]) {
				out = append(out, c)
			}
		}
		return out
	}
	build := func(raw any) []tuiItem {
		items := []tuiItem{{
			label: fixedLabel("Add API Key Connection"),
			action: func(any) bool {
				t.addAPIKeyConnection(map[string]any{"id": node["id"], "display": map[string]any{"name": node["name"]}})
				return true
			},
		}}
		for _, cv := range raw.([]any) {
			conn := asMap(cv)
			items = append(items, tuiItem{
				label: fixedLabel(connName(conn) + " (" + connStatus(conn) + ")"),
				action: func(any) bool {
					t.connectionActions(conn, node, breadcrumb)
					return true
				},
			})
		}
		return items
	}
	t.menu("🔌 "+asStr(node["name"])+" – Connections", breadcrumb, nil, refresh, build, "← Back")
}

func (t *tui) addCustomNode() {
	clearScreen()
	fmt.Print("\n➕ Add Custom Provider\n\n")
	fmt.Println("Select type:\n  1. openai-compatible\n  2. anthropic-compatible")
	kind := ""
	switch t.prompt("Type (1/2): ") {
	case "1":
		kind = "openai-compatible"
	case "2":
		kind = "anthropic-compatible"
	default:
		t.status("warning", "Cancelled")
		t.pause()
		return
	}
	name := t.prompt("Name: ")
	if name == "" {
		t.status("warning", "Cancelled")
		t.pause()
		return
	}
	prefix := t.prompt("Prefix (used in model IDs, e.g. myapi): ")
	if prefix == "" {
		t.status("warning", "Cancelled")
		t.pause()
		return
	}
	baseURL := t.prompt("Base URL (e.g. https://api.example.com/v1): ")
	if baseURL == "" {
		t.status("warning", "Cancelled")
		t.pause()
		return
	}
	body := map[string]any{"name": name, "prefix": prefix, "baseUrl": baseURL, "type": kind}
	if kind == "openai-compatible" {
		apiType := "chat"
		fmt.Println("\nAPI Type:\n  1. chat\n  2. responses")
		if t.prompt("API Type (1/2, default 1): ") == "2" {
			apiType = "responses"
		}
		body["apiType"] = apiType
	}
	t.status("info", "Creating provider node...")
	if _, err := t.do("POST", "/api/provider-nodes", body); err != nil {
		t.status("error", "✗ Failed: "+err.Error())
	} else {
		t.status("success", "✓ Provider created!")
	}
	t.pause()
}

func (t *tui) editCustomNode(node map[string]any) {
	clearScreen()
	fmt.Printf("\n✏️  Edit %s\n\n%sLeave blank to keep current value%s\n\n", asStr(node["name"]), tuiDim, tuiReset)
	updates := map[string]any{}
	if v := t.prompt(fmt.Sprintf("Name (%s): ", asStr(node["name"]))); v != "" {
		updates["name"] = v
	}
	if v := t.prompt(fmt.Sprintf("Base URL (%s): ", asStr(node["baseUrl"]))); v != "" {
		updates["baseUrl"] = v
	}
	if v := t.prompt(fmt.Sprintf("Prefix (%s): ", asStr(node["prefix"]))); v != "" {
		updates["prefix"] = v
	}
	if len(updates) == 0 {
		t.status("warning", "No changes")
		t.pause()
		return
	}
	t.status("info", "Updating...")
	if _, err := t.do("PUT", "/api/provider-nodes/"+asStr(node["id"]), updates); err != nil {
		t.status("error", "✗ Failed: "+err.Error())
	} else {
		for k, v := range updates {
			node[k] = v
		}
		t.status("success", "✓ Updated!")
	}
	t.pause()
}

// ---------------------------------------------------------------- api keys

func (t *tui) apiKeysMenu(breadcrumb []string) {
	endpoint := t.endpoint()
	refresh := func() any {
		keys, err := t.keys()
		if err != nil {
			clearScreen()
			t.status("error", "Failed to fetch API keys: "+err.Error())
			t.pause()
			return nil
		}
		return keys
	}
	build := func(raw any) []tuiItem {
		items := []tuiItem{{
			label:  fixedLabel("Create New API Key"),
			action: func(any) bool { t.createAPIKey(); return true },
		}}
		for _, key := range raw.([]map[string]any) {
			k := key
			items = append(items, tuiItem{
				label: fixedLabel(fmt.Sprintf("%s (%s)", asStr(k["name"]), tuiMaskKey(asStr(k["key"])))),
				action: func(any) bool {
					t.apiKeyActions(k, endpoint, breadcrumb)
					return true
				},
			})
		}
		return items
	}
	header := func(any) string { return "Endpoint: " + endpoint }
	t.menu("🔑 API Keys Management", breadcrumb, header, refresh, build, "")
}

func (t *tui) createAPIKey() {
	fmt.Println("\n📝 Create New API Key")
	fmt.Println(strings.Repeat("─", 30))
	name := t.prompt("Enter key name: ")
	if name == "" {
		t.status("error", "Key name cannot be empty")
		t.pause()
		return
	}
	res, err := t.do("POST", "/api/keys", map[string]any{"name": name})
	if err != nil {
		t.status("error", "Failed to create key: "+err.Error())
		t.pause()
		return
	}
	fmt.Println("\n✅ API Key created successfully!")
	fmt.Println("\n⚠️  IMPORTANT: Save this key now. You won't be able to see it again!")
	fmt.Printf("\nKey: %s\nName: %s\nID: %s\n", asStr(res["key"]), asStr(res["name"]), asStr(res["id"]))
	if t.confirm("\nCopy key to clipboard?") {
		if copyToClipboard(asStr(res["key"])) {
			t.status("success", "Key copied to clipboard!")
		} else {
			t.status("error", "Failed to copy to clipboard")
		}
	}
	t.pause()
}

func (t *tui) apiKeyActions(key map[string]any, endpoint string, breadcrumb []string) {
	name := asStr(key["name"])
	header := func(any) string {
		return "Name: " + name + "\nKey: " + asStr(key["key"]) + "\nEndpoint: " + endpoint
	}
	items := []tuiItem{
		{fixedLabel("Copy to Clipboard"), func(any) bool {
			if copyToClipboard(asStr(key["key"])) {
				t.status("success", fmt.Sprintf("Key %q copied to clipboard!", name))
			} else {
				t.status("error", "Failed to copy to clipboard")
			}
			t.pause()
			return true
		}},
		{fixedLabel("Delete Key"), func(any) bool {
			if !t.confirm("\nAre you sure you want to delete this key?") {
				t.status("info", "Deletion cancelled")
				t.pause()
				return true
			}
			if _, err := t.do("DELETE", "/api/keys/"+asStr(key["id"]), nil); err != nil {
				t.status("error", "Failed to delete key: "+err.Error())
			} else {
				t.status("success", "API key deleted successfully")
			}
			t.pause()
			return false
		}},
	}
	t.menu("🔑 "+name, append(breadcrumb, name), header, nil, func(any) []tuiItem { return items }, "")
}

// ---------------------------------------------------------------- combos

func (t *tui) combosMenu(breadcrumb []string) {
	refresh := func() any {
		res, err := t.do("GET", "/api/combos", nil)
		if err != nil {
			clearScreen()
			t.status("error", "Failed to load combos: "+err.Error())
			t.pause()
			return nil
		}
		return asList(res["combos"])
	}
	build := func(raw any) []tuiItem {
		items := []tuiItem{{
			label:  fixedLabel("Create New Combo"),
			action: func(any) bool { t.createCombo(); return true },
		}}
		for _, cv := range raw.([]any) {
			combo := asMap(cv)
			chain := comboChain(combo)
			items = append(items, tuiItem{
				label: fixedLabel(fmt.Sprintf("%s: %s", asStr(combo["name"]), truncate(chain, 35))),
				action: func(any) bool {
					t.comboActions(combo, breadcrumb)
					return true
				},
			})
		}
		return items
	}
	t.menu("🔀 Combos Management", breadcrumb, nil, refresh, build, "")
}

func comboChain(combo map[string]any) string {
	var parts []string
	for _, m := range asList(combo["models"]) {
		parts = append(parts, asStr(m))
	}
	return strings.Join(parts, " → ")
}

func (t *tui) comboActions(combo map[string]any, breadcrumb []string) {
	header := func(any) string {
		return "Name: " + asStr(combo["name"]) + "\nModels: " + comboChain(combo)
	}
	items := []tuiItem{
		{fixedLabel("Edit Combo"), func(any) bool { t.editCombo(combo); return true }},
		{fixedLabel("Delete Combo"), func(any) bool {
			if !t.confirm(fmt.Sprintf("Delete combo %q?", asStr(combo["name"]))) {
				return true
			}
			if _, err := t.do("DELETE", "/api/combos/"+asStr(combo["id"]), nil); err != nil {
				t.status("error", "Delete failed: "+err.Error())
			} else {
				t.status("success", "Combo deleted!")
			}
			t.pause()
			return false
		}},
	}
	t.menu("🔀 "+asStr(combo["name"]), append(breadcrumb, asStr(combo["name"])), header, nil,
		func(any) []tuiItem { return items }, "")
}

// pickModels is the "add one by one" loop the reference uses for combo chains.
func (t *tui) pickModels(start int) []string {
	var models []string
	for {
		chain := "None"
		if len(models) > 0 {
			chain = strings.Join(models, " → ")
		}
		model := t.selectModel(fmt.Sprintf("Add Model #%d", len(models)+1), chain, true)
		if model == "" {
			return models
		}
		models = append(models, model)
		fmt.Printf("\n✓ Added: %s\nCurrent chain: %s\n\n", model, strings.Join(models, " → "))
		if !t.confirm("Add another model?") {
			return models
		}
	}
}

func (t *tui) availableModelIDs() []string {
	res, _ := t.do("GET", "/api/models", nil)
	var out []string
	for _, item := range asList(res["data"]) {
		if id := asStr(asMap(item)["id"]); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func (t *tui) createCombo() {
	clearScreen()
	t.status("info", "Create New Combo")
	fmt.Println()
	name := t.prompt("Combo name: ")
	if name == "" {
		t.status("error", "Combo name is required")
		t.pause()
		return
	}
	t.status("info", "Loading available models...")
	available := t.availableModelIDs()
	if len(available) == 0 {
		t.status("warning", "No models available. Please add providers first.")
		t.pause()
		return
	}
	fmt.Println()
	t.status("info", "Select models for the chain (minimum 2)")

	var selected []string
	for {
		clearScreen()
		fmt.Printf("Creating combo: %s\nSelected models (%d):\n", name, len(selected))
		if len(selected) == 0 {
			fmt.Println("  (none)")
		} else {
			for i, m := range selected {
				fmt.Printf("  %d. %s\n", i+1, m)
			}
		}
		fmt.Println("\nAvailable models:")
		for i, m := range available {
			fmt.Printf("  %d. %s\n", i+1, m)
		}
		fmt.Print("\nActions:\n  - Enter number to add model\n  - Type 'done' to finish (min 2 models)\n  - Type 'cancel' to abort\n\n")
		input := strings.ToLower(t.prompt("Action: "))
		switch input {
		case "cancel":
			t.status("warning", "Cancelled")
			t.pause()
			return
		case "done":
			if len(selected) < 2 {
				t.status("error", "Please select at least 2 models")
				t.pause()
				continue
			}
		default:
			n := atoiSafe(input)
			if n < 1 || n > len(available) {
				t.status("error", "Invalid model number")
				t.pause()
				continue
			}
			selected = append(selected, available[n-1])
			continue
		}
		break
	}
	t.status("info", "Creating combo...")
	if _, err := t.do("POST", "/api/combos", map[string]any{"name": name, "models": selected}); err != nil {
		t.status("error", "Failed to create combo: "+err.Error())
		t.pause()
		return
	}
	t.status("success", fmt.Sprintf("Combo %q created successfully!", name))
	t.pause()
}

func (t *tui) editCombo(combo map[string]any) {
	clearScreen()
	fmt.Printf("\n✏️  Edit Combo: %s\n\n", asStr(combo["name"]))
	name := asStr(combo["name"])
	if v := t.prompt(fmt.Sprintf("New name (Enter to keep %q): ", name)); v != "" {
		name = v
	}
	fmt.Println("\nCurrent models: " + comboChain(combo))
	body := map[string]any{"name": name}
	if t.confirm("Edit model chain?") {
		fmt.Println("\nSelect models for this combo (add one by one):")
		if models := t.pickModels(2); len(models) > 0 {
			body["models"] = models
		}
	}
	if _, err := t.do("PUT", "/api/combos/"+asStr(combo["id"]), body); err != nil {
		t.status("error", "Update failed: "+err.Error())
	} else {
		t.status("success", "Combo updated!")
	}
	t.pause()
}

// ---------------------------------------------------------------- models

// selectModel is the grouped, searchable picker from utils/modelSelector.js.
func (t *tui) selectModel(title, current string, excludeCombos bool) string {
	res, err := t.do("GET", "/api/models", nil)
	if err != nil {
		t.status("error", "Failed to load models: "+err.Error())
		t.pause()
		return ""
	}
	groups := map[string][]string{}
	var order []string
	var combos []string
	for _, item := range asList(res["data"]) {
		m := asMap(item)
		id, owner := asStr(m["id"]), asStr(m["owned_by"])
		if id == "" {
			continue
		}
		if owner == "combo" {
			combos = append(combos, id)
			continue
		}
		if _, ok := groups[owner]; !ok {
			order = append(order, owner)
		}
		groups[owner] = append(groups[owner], id)
	}
	sort.Strings(order)

	var all []string
	if !excludeCombos {
		all = append(all, combos...)
	}
	for _, g := range order {
		all = append(all, groups[g]...)
	}
	if len(all) == 0 {
		clearScreen()
		fmt.Printf("\n🎯 %s\n%s\n\n  No connected providers found.\n  Please connect a provider in Providers menu first.\n\n  m. ✍️  Enter custom model ID\n  0. Cancel\n\n", title, strings.Repeat("=", 50))
		switch strings.ToLower(t.prompt("Select option (m/0): ")) {
		case "m":
			return t.prompt("Enter custom model ID: ")
		}
		return ""
	}

	categories := []string{}
	models := map[string][]string{}
	if len(combos) > 0 && !excludeCombos {
		categories = append(categories, "[Combos]")
		models["[Combos]"] = combos
	}
	for _, g := range order {
		categories = append(categories, g)
		models[g] = groups[g]
	}

	for {
		clearScreen()
		fmt.Printf("\n🎯 %s\n%s\n", title, strings.Repeat("=", 50))
		if current != "" {
			fmt.Printf("Current: %s\n", current)
		}
		fmt.Println()

		if len(categories) == 1 {
			return t.pickFromList(title, current, models[categories[0]], all)
		}

		fmt.Println("[Providers & Groups]")
		for i, cat := range categories {
			fmt.Printf("  %d. %s (%d models)\n", i+1, cat, len(models[cat]))
		}
		fmt.Print("\n  s. 🔍 Search models\n  m. ✍️  Enter custom model ID\n  0. Cancel\n\n")
		choice := strings.ToLower(t.prompt("Enter choice (number / keyword / s / m): "))
		switch choice {
		case "", "0":
			return ""
		case "s":
			if q := t.prompt("Enter search keyword: "); q != "" {
				if picked := t.pickFromList(title, current, filterModels(all, q), all); picked != "" {
					return picked
				}
			}
			continue
		case "m":
			if custom := t.prompt("Enter custom model ID: "); custom != "" {
				return custom
			}
			continue
		}
		if n := atoiSafe(choice); n >= 1 && n <= len(categories) {
			if picked := t.pickFromList(title+" > "+categories[n-1], current, models[categories[n-1]], all); picked != "" {
				return picked
			}
			continue
		}
		if picked := t.pickFromList(title, current, filterModels(all, choice), all); picked != "" {
			return picked
		}
	}
}

func (t *tui) pickFromList(title, current string, list, all []string) string {
	for {
		clearScreen()
		fmt.Printf("\n🎯 %s\n%s\n", title, strings.Repeat("=", 50))
		if current != "" {
			fmt.Printf("Current: %s\n", current)
		}
		fmt.Println()
		for i, m := range list {
			fmt.Printf("  %d. %s\n", i+1, m)
		}
		fmt.Print("\n  s. 🔍 Search models\n  m. ✍️  Enter custom model ID\n  0. Cancel\n\n")
		switch choice := strings.ToLower(t.prompt("Enter choice (number / s / m / 0): ")); choice {
		case "", "0":
			return ""
		case "s":
			if q := t.prompt("Enter search keyword: "); q != "" {
				list = filterModels(all, q)
			}
			continue
		case "m":
			if custom := t.prompt("Enter custom model ID: "); custom != "" {
				return custom
			}
			continue
		default:
			if n := atoiSafe(choice); n >= 1 && n <= len(list) {
				return list[n-1]
			}
		}
	}
}

func filterModels(list []string, q string) []string {
	q = strings.ToLower(q)
	var out []string
	for _, m := range list {
		if strings.Contains(strings.ToLower(m), q) {
			out = append(out, m)
		}
	}
	return out
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ---------------------------------------------------------------- cli tools

type cliToolDef struct {
	id, menu, name, title, endpoint string
}

var cliToolDefs = []cliToolDef{
	{"claude", "Claude Code", "Claude Code", "🔧 Claude Code Settings", "claude-settings"},
	{"codex", "Codex CLI", "Codex CLI", "🤖 Codex CLI Settings", "codex-settings"},
	{"droid", "Factory Droid", "Factory Droid", "🤖 Factory Droid Settings", "droid-settings"},
	{"openclaw", "Open Claw", "Open Claw", "🦞 Open Claw Settings", "openclaw-settings"},
	{"opencode", "OpenCode", "OpenCode CLI", "💻 OpenCode CLI Settings", "opencode-settings"},
	{"hermes", "Hermes", "Hermes Agent", "⚡ Hermes Agent Settings", "hermes-settings"},
}

func (t *tui) cliToolsMenu(breadcrumb []string) {
	var items []tuiItem
	for _, def := range cliToolDefs {
		d := def
		items = append(items, tuiItem{
			label: fixedLabel(d.menu),
			action: func(any) bool {
				switch d.id {
				case "claude":
					t.claudeMenu([]string{"KineRouter", "CLI Tools", d.menu})
				case "hermes":
					t.hermesMenu([]string{"KineRouter", "CLI Tools", d.menu})
				default:
					t.genericCLIMenu(d, []string{"KineRouter", "CLI Tools", d.menu})
				}
				return true
			},
		})
	}
	header := func(any) string {
		return "Configure CLI tools to use KineRouter\nEndpoint: " + t.endpoint()
	}
	t.menu("🔧 CLI Tools", breadcrumb, header, nil, func(any) []tuiItem { return items }, "")
}

func (t *tui) cliStatus(endpoint, query string) map[string]any {
	path := "/api/cli-tools/" + endpoint + query
	res, err := t.do("GET", path, nil)
	if err != nil {
		return map[string]any{}
	}
	return res
}

func cliNotConfigured(installed bool, tool string) string {
	if !installed {
		return "Status:   " + tuiRed + "✗ " + tool + " not installed" + tuiReset
	}
	return "Status:   " + tuiRed + "✗ Not configured" + tuiReset + "\n" +
		tuiDim + "Run \"Quick Setup\" to configure" + tuiReset
}

const claudeDefaultSonnet = "cc/claude-sonnet-4-5-20250929"
const claudeDefaultOpus = "cc/claude-opus-4-5-20251101"
const claudeDefaultHaiku = "cc/claude-haiku-4-5-20251001"

func (t *tui) claudeMenu(breadcrumb []string) {
	models := map[string]string{"sonnet": "Not set", "opus": "Not set", "haiku": "Not set"}
	header := func(any) string {
		st := t.cliStatus("claude-settings", "")
		if asBool(st["installed"]) {
			env := asMap(asMap(st["settings"])["env"])
			if base := asStr(env["ANTHROPIC_BASE_URL"]); base != "" {
				lines := []string{
					"Status:   " + tuiGreen + "✓ Configured" + tuiReset,
					"Endpoint: " + tuiCyan + base + tuiReset,
				}
				if key := asStr(env["ANTHROPIC_AUTH_TOKEN"]); key != "" {
					lines = append(lines, "API Key:  "+tuiDim+truncate(key, 10)+"..."+tuiReset)
				}
				return strings.Join(lines, "\n")
			}
		}
		return cliNotConfigured(asBool(st["installed"]), "Claude CLI")
	}
	refresh := func() any {
		st := t.cliStatus("claude-settings", "")
		env := asMap(asMap(st["settings"])["env"])
		for _, pair := range [][2]string{{"sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL"}, {"opus", "ANTHROPIC_DEFAULT_OPUS_MODEL"}, {"haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}} {
			models[pair[0]] = asStr(env[pair[1]])
			if models[pair[0]] == "" {
				models[pair[0]] = "Not set"
			}
		}
		return st
	}
	items := func(any) []tuiItem {
		return []tuiItem{
			{fixedLabel("⚡ Quick Setup (recommended)"), func(any) bool { t.claudeQuickSetup(); return true }},
			{label: func(any) string { return "Sonnet → " + models["sonnet"] }, action: func(any) bool {
				t.claudeSelectModel("Sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL")
				return true
			}},
			{label: func(any) string { return "Opus → " + models["opus"] }, action: func(any) bool {
				t.claudeSelectModel("Opus", "ANTHROPIC_DEFAULT_OPUS_MODEL")
				return true
			}},
			{label: func(any) string { return "Haiku → " + models["haiku"] }, action: func(any) bool {
				t.claudeSelectModel("Haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL")
				return true
			}},
			{fixedLabel("Reset to Default"), func(any) bool { t.cliReset("claude-settings", "Settings reset successfully!"); return true }},
		}
	}
	t.menu("🔧 Claude Code Settings", breadcrumb, header, refresh, items, "")
}

func (t *tui) claudeQuickSetup() {
	key := t.firstKey()
	if key == "" {
		t.status("error", "No API keys found. Create one in API Keys menu first.")
		t.pause()
		return
	}
	env := map[string]any{
		"ANTHROPIC_BASE_URL":             t.endpoint(),
		"ANTHROPIC_AUTH_TOKEN":           key,
		"API_TIMEOUT_MS":                 "600000",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": claudeDefaultSonnet,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   claudeDefaultOpus,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  claudeDefaultHaiku,
	}
	_, err := t.do("POST", "/api/cli-tools/claude-settings", map[string]any{"env": env})
	if err != nil {
		t.status("error", "Failed: "+err.Error())
	} else {
		t.status("success", "Quick Setup completed!")
	}
	t.pause()
}

func (t *tui) claudeSelectModel(name, envKey string) {
	current := asStr(asMap(asMap(t.cliStatus("claude-settings", "")["settings"])["env"])[envKey])
	selected := t.selectModel("Select "+name+" Model", current, true)
	if selected == "" {
		return
	}
	env := map[string]any{envKey: selected}
	if asStr(asMap(asMap(t.cliStatus("claude-settings", "")["settings"])["env"])["ANTHROPIC_BASE_URL"]) == "" {
		env["ANTHROPIC_BASE_URL"] = t.endpoint()
		env["API_TIMEOUT_MS"] = "600000"
		if key := t.firstKey(); key != "" {
			env["ANTHROPIC_AUTH_TOKEN"] = key
		}
	}
	if _, err := t.do("POST", "/api/cli-tools/claude-settings", map[string]any{"env": env}); err != nil {
		t.status("error", "Failed: "+err.Error())
	} else {
		t.status("success", fmt.Sprintf("%s → %s saved!", name, selected))
	}
	t.pause()
}

func (t *tui) cliReset(endpoint, message string) {
	if _, err := t.do("DELETE", "/api/cli-tools/"+endpoint, nil); err != nil {
		t.status("error", "Failed: "+err.Error())
	} else {
		t.status("success", message)
	}
	t.pause()
}

func (t *tui) genericCLIMenu(def cliToolDef, breadcrumb []string) {
	header := func(any) string {
		st := t.cliStatus(def.endpoint, "")
		installed := asBool(st["installed"])
		if !installed {
			return cliNotConfigured(false, def.name)
		}
		if !asBool(st["hasKineRouter"]) {
			return cliNotConfigured(true, def.name)
		}
		lines := []string{"Status:   " + tuiGreen + "✓ Configured" + tuiReset}
		switch def.id {
		case "opencode":
			oc := asMap(st["opencode"])
			if v := asStr(oc["baseURL"]); v != "" {
				lines = append(lines, "Endpoint: "+tuiCyan+v+tuiReset)
			}
			if v := asStr(oc["activeModel"]); v != "" {
				lines = append(lines, "Active:   "+tuiDim+v+tuiReset)
			}
			if models := asList(oc["models"]); len(models) > 0 {
				var names []string
				for _, m := range models {
					names = append(names, asStr(m))
				}
				lines = append(lines, "Models:   "+tuiDim+strings.Join(names, ", ")+tuiReset)
			}
		case "openclaw":
			prov := asMap(pick(st, "settings", "models", "providers", "kinerouter"))
			if v := asStr(prov["baseUrl"]); v != "" {
				lines = append(lines, "Endpoint: "+tuiCyan+v+tuiReset)
			}
			model := ""
			if primary := asStr(pick(st, "settings", "agents", "defaults", "model", "primary")); strings.HasPrefix(primary, "kinerouter/") {
				model = strings.TrimPrefix(primary, "kinerouter/")
			} else if models := asList(prov["models"]); len(models) > 0 {
				model = asStr(asMap(models[0])["id"])
			}
			if model != "" {
				lines = append(lines, "Model:    "+tuiDim+model+tuiReset)
			}
		case "droid":
			for _, cv := range asList(pick(st, "settings", "customModels")) {
				c := asMap(cv)
				if strings.HasPrefix(asStr(c["id"]), "custom:KineRouter") {
					if v := asStr(c["baseUrl"]); v != "" {
						lines = append(lines, "Endpoint: "+tuiCyan+v+tuiReset)
					}
					if v := asStr(c["model"]); v != "" {
						lines = append(lines, "Model:    "+tuiDim+v+tuiReset)
					}
				}
			}
		case "codex":
			cfg := asMap(st["config"])
			prov := asMap(pick(cfg, "model_providers", "kinerouter"))
			if v := asStr(prov["base_url"]); v != "" {
				lines = append(lines, "Endpoint: "+tuiCyan+v+tuiReset)
			}
			if v := asStr(cfg["model"]); v != "" {
				lines = append(lines, "Model:    "+tuiDim+v+tuiReset)
			}
		}
		return strings.Join(lines, "\n")
	}
	items := func(any) []tuiItem {
		return []tuiItem{
			{fixedLabel("⚡ Quick Setup"), func(any) bool { t.genericQuickSetup(def); return true }},
			{fixedLabel("Reset to Default"), func(any) bool { t.cliReset(def.endpoint, def.name+" settings reset!"); return true }},
		}
	}
	t.menu(def.title, breadcrumb, header, func() any { return t.cliStatus(def.endpoint, "") }, items, "")
}

func (t *tui) genericQuickSetup(def cliToolDef) {
	key := t.firstKey()
	if key == "" {
		t.status("error", "No API keys found. Create one in API Keys menu first.")
		t.pause()
		return
	}
	base := t.endpoint()
	switch def.id {
	case "codex":
		model := t.selectModel("Select Codex Model", claudeDefaultSonnet, true)
		if model == "" {
			return
		}
		t.applyCLI(def, map[string]any{"baseUrl": base, "apiKey": key, "model": model})
	case "droid", "openclaw":
		model := t.selectModel("Select "+def.name+" Model", claudeDefaultSonnet, true)
		if model == "" {
			return
		}
		t.applyCLI(def, map[string]any{"baseUrl": base, "apiKey": key, "model": model})
	case "opencode":
		first := t.selectModel("Select Active Model (OpenCode)", "", true)
		if first == "" {
			return
		}
		models := []string{first}
		for t.confirm(fmt.Sprintf("Add another model? (current: %d)", len(models))) {
			next := t.selectModel(fmt.Sprintf("Add Model #%d", len(models)+1), strings.Join(models, ", "), true)
			if next == "" || contains(models, next) {
				break
			}
			models = append(models, next)
		}
		subagent := first
		if t.confirm("Set a different subagent model? (default: " + first + ")") {
			if picked := t.selectModel("Select Subagent Model", first, true); picked != "" {
				subagent = picked
			}
		}
		t.applyCLI(def, map[string]any{
			"baseUrl": base, "apiKey": key, "models": models,
			"activeModel": first, "subagentModel": subagent,
		})
	}
}

func (t *tui) applyCLI(def cliToolDef, body map[string]any) {
	if _, err := t.do("POST", "/api/cli-tools/"+def.endpoint, body); err != nil {
		t.status("error", "Failed: "+err.Error())
	} else {
		t.status("success", def.name+" setup completed!")
	}
	t.pause()
}

// ---------------------------------------------------------------- hermes

func (t *tui) hermesMenu(breadcrumb []string) {
	profile := "default"
	fetchProfiles := func() []any {
		res, _ := t.do("GET", "/api/cli-tools/hermes-profiles", nil)
		return asList(res["profiles"])
	}
	header := func(any) string {
		st := t.cliStatus("hermes-settings", "?profile="+profile)
		if !asBool(st["installed"]) {
			return cliNotConfigured(false, "Hermes Agent")
		}
		name := asStr(pick(st, "profile", "name"))
		if name == "" {
			name = profile
		}
		lines := []string{}
		if asBool(st["hasKineRouter"]) {
			lines = append(lines, "Status:   "+tuiGreen+"✓ Configured"+tuiReset+" ("+name+")")
		} else {
			lines = append(lines, "Status:   "+tuiRed+"✗ Not configured"+tuiReset+" ("+name+")")
			lines = append(lines, tuiDim+"Run \"Quick Setup\" to configure"+tuiReset)
		}
		model := asMap(pick(st, "settings", "model"))
		if v := asStr(model["base_url"]); v != "" {
			lines = append(lines, "Endpoint: "+tuiCyan+v+tuiReset)
		}
		if v := asStr(model["default"]); v != "" {
			lines = append(lines, "Model:    "+tuiDim+v+tuiReset)
		}
		profiles := fetchProfiles()
		if len(profiles) > 1 {
			var parts []string
			for _, p := range profiles {
				pv := asMap(p)
				mark := tuiRed + "✗"
				if asBool(pv["hasKineRouter"]) {
					mark = tuiGreen + "✓"
				}
				parts = append(parts, asStr(pv["name"])+mark)
			}
			lines = append(lines, "Profiles:  "+strings.Join(parts, tuiReset+" ")+tuiReset)
		}
		return strings.Join(lines, "\n")
	}
	items := func(any) []tuiItem {
		return []tuiItem{
			{label: func(any) string { return "🎯 Profile: " + profile }, action: func(any) bool {
				profiles := fetchProfiles()
				if len(profiles) == 0 {
					t.status("error", "No Hermes profiles found.")
					t.pause()
					return true
				}
				fmt.Println("Select Hermes profile:")
				for i, p := range profiles {
					fmt.Printf("  %d. %s\n", i+1, asStr(asMap(p)["name"]))
				}
				if n := atoiSafe(t.prompt("\nSelect option (number): ")); n >= 1 && n <= len(profiles) {
					profile = asStr(asMap(profiles[n-1])["name"])
					t.status("success", fmt.Sprintf("Active profile: %q", profile))
				}
				t.pause()
				return true
			}},
			{fixedLabel("⚡ Quick Setup"), func(any) bool { t.hermesQuickSetup(profile); return true }},
			{fixedLabel("🔁 Apply to All Profiles"), func(any) bool { t.hermesApplyAll(); return true }},
			{label: func(any) string { return "↺ Reset Profile: " + profile }, action: func(any) bool {
				t.cliReset("hermes-settings?profile="+profile, fmt.Sprintf("Hermes settings reset for profile %q!", profile))
				return true
			}},
		}
	}
	t.menu("⚡ Hermes Agent Settings", breadcrumb, header, func() any { return fetchProfiles() }, items, "")
}

func (t *tui) hermesQuickSetup(profile string) {
	key := t.firstKey()
	if key == "" {
		t.status("error", "No API keys found. Create one in API Keys menu first.")
		t.pause()
		return
	}
	model := t.selectModel("Select Hermes Model ("+profile+")", "", true)
	if model == "" {
		return
	}
	body := map[string]any{"profile": profile, "baseUrl": t.endpoint(), "apiKey": key, "model": model}
	if _, err := t.do("POST", "/api/cli-tools/hermes-settings", body); err != nil {
		t.status("error", "Failed: "+err.Error())
	} else {
		t.status("success", fmt.Sprintf("Hermes setup completed for profile %q!", profile))
	}
	t.pause()
}

func (t *tui) hermesApplyAll() {
	key := t.firstKey()
	if key == "" {
		t.status("error", "No API keys found. Create one in API Keys menu first.")
		t.pause()
		return
	}
	model := t.selectModel("Select Model (used only for profiles with no model yet)", "", true)
	if model == "" {
		return
	}
	body := map[string]any{"applyToAll": true, "baseUrl": t.endpoint(), "apiKey": key, "model": model}
	res, err := t.do("POST", "/api/cli-tools/hermes-settings", body)
	if err != nil {
		t.status("error", "Failed: "+err.Error())
		t.pause()
		return
	}
	updated := asInt(res["updated"])
	var skipped []string
	for _, r := range asList(res["results"]) {
		item := asMap(r)
		if asStr(item["status"]) != "updated" {
			reason := asStr(item["reason"])
			if reason == "" {
				reason = asStr(item["status"])
			}
			skipped = append(skipped, "  "+asStr(item["profile"])+": "+reason)
		}
	}
	if len(skipped) == 0 {
		t.status("success", fmt.Sprintf("Endpoint applied to %d profile(s).", updated))
	} else {
		kind := "error"
		if updated > 0 {
			kind = "success"
		}
		t.status(kind, fmt.Sprintf("Updated %d profile(s), skipped %d:\n%s", updated, len(skipped), strings.Join(skipped, "\n")))
	}
	t.pause()
}

// ---------------------------------------------------------------- settings

func (t *tui) settingsMenu(breadcrumb []string) {
	refresh := func() any {
		tun, _ := t.do("GET", "/api/tunnel/status", nil)
		set, _ := t.do("GET", "/api/settings", nil)
		return map[string]any{"tunnel": asMap(tun["tunnel"]), "settings": set}
	}
	header := func(raw any) string {
		d := raw.(map[string]any)
		tun := asMap(d["tunnel"])
		set := asMap(d["settings"])
		lines := []string{}
		if asBool(tun["enabled"]) && asStr(tun["publicUrl"]) != "" {
			lines = append(lines, "  Endpoint: "+tuiGreen+asStr(tun["publicUrl"])+"/v1"+tuiReset)
			lines = append(lines, "  Tunnel:   "+tuiGreen+"ON"+tuiReset+" "+tuiDim+"("+asStr(tun["shortId"])+")"+tuiReset)
		} else {
			lines = append(lines, "  Endpoint: "+t.localEndpoint())
			lines = append(lines, "  Tunnel:   "+tuiRed+"OFF"+tuiReset+" "+tuiDim+"(local only)"+tuiReset)
		}
		rtk := asBool(set["rtkEnabled"])
		lines = append(lines, "  RTK:      "+onOff(rtk)+" "+tuiDim+"(Token Saver)"+tuiReset)
		headroom := asBool(set["headroomEnabled"])
		roomURL := asStr(set["headroomUrl"])
		if roomURL == "" {
			roomURL = "http://localhost:8787"
		}
		lines = append(lines, "  Headroom: "+onOff(headroom)+" "+tuiDim+"("+roomURL+")"+tuiReset)
		mode := asStr(set["authMode"])
		if mode == "" {
			mode = "password"
		}
		color := tuiYellow
		if mode == "password" {
			color = tuiGreen
		}
		lines = append(lines, "  Auth:     "+color+strings.ToUpper(mode)+tuiReset+" "+tuiDim+"(login mode)"+tuiReset)
		return strings.Join(lines, "\n")
	}
	items := func(raw any) []tuiItem {
		d := raw.(map[string]any)
		set := asMap(d["settings"])
		return []tuiItem{
			{fixedLabel("Tunnel ON"), func(any) bool { t.tunnelAction(true); return true }},
			{fixedLabel("Tunnel OFF"), func(any) bool { t.tunnelAction(false); return true }},
			{label: func(any) string {
				return fmt.Sprintf("Token Saver (RTK): %s → toggle", onOffText(asBool(set["rtkEnabled"])))
			}, action: func(any) bool { t.toggleSetting("rtkEnabled", asBool(set["rtkEnabled"]), "Token Saver"); return true }},
			{label: func(any) string {
				return fmt.Sprintf("Token Saver (Headroom): %s → toggle", onOffText(asBool(set["headroomEnabled"])))
			}, action: func(any) bool {
				t.toggleSetting("headroomEnabled", asBool(set["headroomEnabled"]), "Headroom")
				return true
			}},
			{fixedLabel("🔑 Reset Password to Default"), func(any) bool { t.resetPassword(); return true }},
			{label: func(any) string {
				mode := asStr(set["authMode"])
				if mode == "" || mode == "password" {
					return "🔓 Reset Auth Mode (already password)"
				}
				return "🔓 Reset Auth Mode to Password (current: " + mode + ")"
			}, action: func(any) bool { t.resetAuthMode(); return true }},
		}
	}
	t.menu("⚙️  Settings", breadcrumb, header, refresh, items, "")
}

func onOff(on bool) string {
	if on {
		return tuiGreen + "ON" + tuiReset
	}
	return tuiRed + "OFF" + tuiReset
}

func onOffText(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

func (t *tui) tunnelAction(enable bool) {
	if enable {
		t.status("info", "Creating tunnel...")
		res, err := t.do("POST", "/api/tunnel/enable", nil)
		if err != nil {
			t.status("error", "Failed: "+err.Error())
		} else if asBool(res["alreadyRunning"]) {
			t.status("success", "Tunnel already running: "+asStr(res["publicUrl"]))
		} else {
			t.status("success", fmt.Sprintf("Tunnel enabled: %s (%s)", asStr(res["publicUrl"]), asStr(res["shortId"])))
		}
	} else {
		if _, err := t.do("POST", "/api/tunnel/disable", nil); err != nil {
			t.status("error", "Failed: "+err.Error())
		} else {
			t.status("success", "Tunnel disabled")
		}
	}
	t.pause()
}

func (t *tui) toggleSetting(key string, current bool, label string) {
	next := !current
	if _, err := t.do("PATCH", "/api/settings", map[string]any{key: next}); err != nil {
		t.status("error", "Failed: "+err.Error())
	} else {
		t.status("success", label+" "+onOffText(next)+"d")
	}
	t.pause()
}

func (t *tui) resetPassword() {
	if !t.confirm(fmt.Sprintf("Reset dashboard password to default %q?", "123456")) {
		t.status("info", "Cancelled")
		t.pause()
		return
	}
	if _, err := t.do("POST", "/api/auth/reset-password", nil); err != nil {
		t.status("error", "Failed to reset password: "+err.Error())
	} else {
		t.status("success", "Password reset. Default: 123456")
	}
	t.pause()
}

func (t *tui) resetAuthMode() {
	if !t.confirm("Reset auth mode to PASSWORD (disable OIDC)?") {
		t.status("info", "Cancelled")
		t.pause()
		return
	}
	if _, err := t.do("PATCH", "/api/settings", map[string]any{"authMode": "password"}); err != nil {
		t.status("error", "Failed: "+err.Error())
	} else {
		t.status("success", "Auth mode reset to password. OIDC disabled.")
	}
	t.pause()
}
