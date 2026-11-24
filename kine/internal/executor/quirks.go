package executor

// Per-provider quirks that the JS expresses as DefaultExecutor subclasses, kept
// here as flat functions: applyQuirksBody runs in the gateway before the body is
// marshalled, applyQuirksTransport rewrites the URL/headers the default builder
// produced. Providers that need a full custom transport (bedrock SigV4, vertex
// service account, qoder COSY, zed/windsurf/trae/dev-ci sidecars) are not here.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ApplyQuirksBody rewrites a provider-shaped request body in place before it is
// sent. model is the resolved upstream model id, stream the resolved upstream
// streaming flag.
func ApplyQuirksBody(provider, model string, body map[string]any, stream bool) {
	switch provider {
	case "kimchi":
		kimchiBody(body, model)
	case "codebuddy-cn", "codebuddy-intl":
		codebuddyBody(body)
	case "github":
		githubBody(model, body)
	case "codex":
		codexBody(model, body)
	case "iflow":
		if stream {
			if msgs, ok := body["messages"].([]any); ok && len(msgs) > 0 {
				if _, exists := body["stream_options"]; !exists {
					body["stream_options"] = map[string]any{"include_usage": true}
				}
			}
		}
	}
}

// ApplyQuirksTransport rewrites the URL (returned) and headers (in place) built
// by Executor.build. Returning "" leaves the URL untouched.
func ApplyQuirksTransport(req Request, headers map[string]string) (string, string) {
	applyHeaderHooks(req, headers)
	switch req.Provider {
	case "azure":
		// Azure authenticates with api-key, not Bearer.
		delete(headers, "Authorization")
		if k := firstNonEmpty(req.Creds.APIKey, req.Creds.AccessToken); k != "" {
			headers["api-key"] = k
		}
		return azureURL(req), ""
	case "xiaomi-tokenplan":
		return xiaomiTokenplanURL(req), ""
	case "codex":
		codexHeaders(req, headers)
	case "iflow":
		iflowHeaders(req, headers)
	case "github":
		githubHeaders(req, headers)
	}
	return "", ""
}

// applyHeaderHooks runs the registry-declared auth.hooks (default.js
// HEADER_HOOKS): per-request client fingerprint headers several gateways
// reject requests without.
func applyHeaderHooks(req Request, headers map[string]string) {
	hooks := req.Creds.Hooks
	for _, h := range hooks {
		switch h {
		case "codebuddyHeaders":
			codebuddyHeaders(headers)
		case "kimiHeaders":
			kimiHeaders(req, headers)
		case "clineHeaders":
			clineHeaders(req, headers)
		case "kilocodeOrg":
			if v, ok := req.Creds.Extra["orgId"].(string); ok && v != "" {
				headers["X-Kilocode-OrganizationID"] = v
			}
		case "minimaxHeaders":
			headers["x-api-key"] = "sk-xxx"
			_, off := time.Now().Zone()
			headers["X-Mavis-Timezone-Offset"] = fmt.Sprintf("%d", -off/60) // JS getTimezoneOffset()
			if headers["X-Mavis-Session-Id"] == "" {
				headers["X-Mavis-Session-Id"] = uuid.NewString()
			}
		case "museHeaders":
			if req.Creds.AccessToken != "" && req.Creds.APIKey == "" {
				headers["x-api-version"] = "1.0.0"
			}
		case "zcodeHeaders":
			zcodeHeaders(req, headers)
		case "freePiHeaders":
			freePiHeaders(req, headers)
		}
	}
}

// kimiHeaders is the CLIProxyAPI commonHeaders parity block: a stable per
// connection device id plus the host fingerprint.
func kimiHeaders(req Request, headers map[string]string) {
	model := hostPlatform() + " " + hostArch()
	devName := hostName()
	if devName == "" {
		devName = "unknown"
	}
	id, _ := req.Creds.Extra["deviceId"].(string)
	if strings.TrimSpace(id) == "" {
		id = "kimi-" + fmt.Sprint(time.Now().UnixMilli())
	}
	for k, v := range map[string]string{
		"X-Msh-Platform":     "kinerouter",
		"X-Msh-Version":      appVersion(),
		"X-Msh-Device-Name":  devName,
		"X-Msh-Device-Model": model,
		"X-Msh-Device-Id":    strings.TrimSpace(id),
	} {
		headers[k] = v
	}
}

// clineHeaders fingerprints the Cline/ClinePass client; the OAuth token is a
// WorkOS JWT and must carry the "workos:" prefix, but a clp_ API key must not.
func clineHeaders(req Request, headers map[string]string) {
	tok := firstNonEmpty(req.Creds.APIKey, req.Creds.AccessToken)
	if strings.HasPrefix(tok, "eyJ") && strings.Count(tok, ".") >= 2 {
		tok = "workos:" + tok
	}
	if tok != "" {
		headers["Authorization"] = "Bearer " + tok
	}
	v := appVersion()
	for k, val := range map[string]string{
		"HTTP-Referer":       "https://cline.bot",
		"X-Title":            "Cline",
		"User-Agent":         "KineRouter/" + v,
		"X-PLATFORM":         hostPlatform(),
		"X-PLATFORM-VERSION": "go" + strings.TrimPrefix(runtimeVersion(), "go"),
		"X-CLIENT-TYPE":      "kinerouter",
		"X-CLIENT-VERSION":   v,
		"X-CORE-VERSION":     v,
		"X-IS-MULTIROOT":     "false",
	} {
		headers[k] = val
	}
}

// ---------------------------------------------------------------- azure

func azureURL(req Request) string {
	endpoint := strFromExtra(req.Creds.Extra, "azureEndpoint")
	if endpoint == "" {
		endpoint = "https://api.openai.com"
	}
	apiVersion := strFromExtra(req.Creds.Extra, "apiVersion")
	if apiVersion == "" {
		apiVersion = "2024-10-01-preview"
	}
	deployment := strFromExtra(req.Creds.Extra, "deployment")
	if deployment == "" {
		deployment = req.Model
	}
	if deployment == "" {
		deployment = "gpt-4"
	}
	return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		strings.TrimSuffix(endpoint, "/"), deployment, apiVersion)
}

// ---------------------------------------------------------------- xiaomi-tokenplan

func xiaomiTokenplanURL(req Request) string {
	base := req.Creds.BaseURL
	if base == "" {
		base = "https://token-plan-sgp.xiaomimimo.com/v1/chat/completions"
	}
	root := strings.TrimSuffix(strings.TrimSuffix(base, "/chat/completions"), "/v1")
	if req.Format == "claude" {
		return root + "/anthropic/v1/messages"
	}
	return root + "/v1/chat/completions"
}

// ---------------------------------------------------------------- codex

// codexHeaders binds the request to the Codex client and, crucially, to the
// right ChatGPT account: with several Codex logins configured a missing
// ChatGPT-Account-ID cross-binds and surfaces as token_invalid.
func codexHeaders(req Request, headers map[string]string) {
	if headers["originator"] == "" {
		headers["originator"] = "codex_cli_rs"
	}
	sid := req.Creds.SessionID
	if sid == "" {
		if v, ok := req.Creds.Extra["connectionId"].(string); ok {
			sid = v
		}
	}
	if sid == "" {
		sid = "default"
	}
	headers["session_id"] = sid
	for _, key := range []string{"workspaceId", "chatgptAccountId", "accountId"} {
		if v, ok := req.Creds.Extra[key].(string); ok && v != "" {
			if headers["ChatGPT-Account-ID"] == "" {
				headers["ChatGPT-Account-ID"] = v
			}
			break
		}
	}
}

// ---------------------------------------------------------------- iflow

func iflowHeaders(req Request, headers map[string]string) {
	apiKey := req.Creds.APIKey
	if apiKey == "" {
		apiKey = req.Creds.AccessToken
	}
	ua := headers["User-Agent"]
	if ua == "" {
		ua = "iFlow-Cli"
	}
	sessionID := "session-" + uuid.NewString()
	ts := time.Now().UnixMilli()
	payload := fmt.Sprintf("%s:%s:%d", ua, sessionID, ts)
	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte(payload))
	headers["session-id"] = sessionID
	headers["x-iflow-timestamp"] = fmt.Sprintf("%d", ts)
	headers["x-iflow-signature"] = hex.EncodeToString(mac.Sum(nil))
	if req.Creds.APIKey != "" {
		headers["Authorization"] = "Bearer " + req.Creds.APIKey
	}
}

// ---------------------------------------------------------------- github copilot

const (
	copilotVSCodeVersion       = "1.104.1"
	copilotChatVersion         = "0.26.7"
	copilotUserAgent           = "GitHubCopilotChat/0.26.7"
	copilotAPIVersion          = "2025-04-01"
	copilotAnthropicAPIVersion = "2023-06-01"
)

func githubHeaders(req Request, headers map[string]string) {
	token := req.Creds.APIKey
	if token == "" {
		token = req.Creds.AccessToken
	}
	hdrs := map[string]string{
		"Authorization":                       "Bearer " + token,
		"copilot-integration-id":              "vscode-chat",
		"editor-version":                      "vscode/" + copilotVSCodeVersion,
		"editor-plugin-version":               "copilot-chat/" + copilotChatVersion,
		"user-agent":                          copilotUserAgent,
		"openai-intent":                       "conversation-panel",
		"x-github-api-version":                copilotAPIVersion,
		"x-request-id":                        uuid.NewString(),
		"x-vscode-user-agent-library-version": "electron-fetch",
		"X-Initiator":                         "user",
		"anthropic-version":                   copilotAnthropicAPIVersion,
	}
	for k, v := range hdrs {
		headers[k] = v
	}
}

// githubBody mirrors the Copilot executor's transformRequest: gpt-5/o-series
// reject max_tokens, and "none" is not a value every model accepts.
func githubBody(model string, body map[string]any) {
	low := strings.ToLower(model)
	if strings.Contains(low, "gpt-5") || strings.Contains(low, "o1-") ||
		strings.Contains(low, "o3-") || strings.Contains(low, "o4-") {
		if v, ok := body["max_tokens"]; ok {
			body["max_completion_tokens"] = v
			delete(body, "max_tokens")
		}
	}
	if body["reasoning_effort"] == "none" {
		delete(body, "reasoning_effort")
	}
}

// codexBody mirrors the parts of CodexExecutor.transformRequest that do not
// need model-catalog knowledge: the Responses body must be an array, streamed,
// carry no server-generated item ids (store=false cannot resolve them) and
// default instructions when none were sent.
const codexDefaultInstructions = "You are a coding agent running in the Codex CLI."

func codexBody(model string, body map[string]any) {
	delete(body, "_compact")
	if in, ok := body["input"].(string); ok {
		body["input"] = []any{map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": in}}}}
	}
	if items, ok := body["input"].([]any); ok && len(items) == 0 {
		body["input"] = []any{map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "..."}}}}
	}
	body["stream"] = true
	// Codex /responses runs with store=false, so rs_/fc_/resp_/msg_ ids the
	// client echoes back cannot be resolved and the request is rejected.
	if items, ok := body["input"].([]any); ok {
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				if id, ok := m["id"].(string); ok && codexStoredID(id) {
					delete(m, "id")
				}
			}
		}
	}
	if s, _ := body["instructions"].(string); strings.TrimSpace(s) == "" {
		body["instructions"] = codexDefaultInstructions
	}
}

func codexStoredID(id string) bool {
	for _, p := range []string{"rs_", "fc_", "resp_", "msg_"} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- codebuddy

func codebuddyHeaders(headers map[string]string) {
	requestID := uuid.NewString()
	headers["X-Request-ID"] = requestID
	headers["X-Conversation-ID"] = uuid.NewString()
	headers["X-Conversation-Request-ID"] = requestID
	headers["X-Conversation-Message-ID"] = requestID
}

const codebuddyNeutralPrompt = "You are a helpful AI assistant that helps with software engineering tasks."

// ---------------------------------------------------------------- kimchi body

var kimchiDrops = []string{
	"anthropic_version", "anthropic_beta", "client_metadata", "mcp_servers",
	"stop_sequences", "thinking", "top_k", "system",
}

func kimchiBody(body map[string]any, model string) {
	// Merge a top-level system prompt into messages[] as an OpenAI system turn.
	if sys, ok := body["system"]; ok {
		text := systemText(sys)
		if msgs, ok := body["messages"].([]any); ok && strings.TrimSpace(text) != "" {
			merged := false
			for _, m := range msgs {
				mm, ok := m.(map[string]any)
				if !ok || mm["role"] != "system" {
					continue
				}
				if s, ok := mm["content"].(string); ok {
					mm["content"] = text + "\n\n" + s
				} else if parts, ok := mm["content"].([]any); ok {
					mm["content"] = append([]any{map[string]any{"type": "text", "text": text}}, parts...)
				}
				merged = true
				break
			}
			if !merged {
				body["messages"] = append([]any{map[string]any{"role": "system", "content": text}}, msgs...)
			}
		}
	}
	for _, k := range kimchiDrops {
		delete(body, k)
	}
	// Anthropic-backed Kimchi models reject OpenAI reasoning knobs.
	if strings.Contains(strings.ToLower(model), "claude") || strings.Contains(strings.ToLower(model), "anthropic") {
		delete(body, "reasoning_effort")
		delete(body, "reasoning")
	}
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			delete(mm, "cache_control")
			if parts, ok := mm["content"].([]any); ok {
				for _, p := range parts {
					if pm, ok := p.(map[string]any); ok {
						delete(pm, "cache_control")
						delete(pm, "signature")
					}
				}
			}
			if mm["role"] == "assistant" {
				if rc, ok := mm["reasoning_content"].(string); ok && len(rc) > 8 {
					delete(mm, "reasoning_content")
				}
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				delete(tm, "cache_control")
			}
		}
	}
}

func systemText(sys any) string {
	switch s := sys.(type) {
	case string:
		return s
	case []any:
		var parts []string
		for _, p := range s {
			switch v := p.(type) {
			case string:
				parts = append(parts, v)
			case map[string]any:
				if t, ok := v["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// ---------------------------------------------------------------- codebuddy body

func codebuddyBody(body map[string]any) {
	// CodeBuddy rejects non-stream chat requests.
	body["stream"] = true
	if msgs, ok := body["messages"].([]any); ok {
		for i, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok || mm["role"] != "system" {
				continue
			}
			text := contentText(mm["content"])
			if text == "" {
				continue
			}
			if len(text) > 2000 || isAgentPrompt(text) {
				if _, ok := mm["content"].(string); ok {
					mm["content"] = codebuddyNeutralPrompt
				} else {
					mm["content"] = []any{map[string]any{"type": "text", "text": codebuddyNeutralPrompt}}
				}
				msgs[i] = mm
			}
		}
	}
	eff, _ := body["reasoning_effort"].(string)
	if eff == "none" || eff == "off" {
		delete(body, "reasoning_effort")
	} else if eff != "" {
		body["reasoning_summary"] = "auto"
	}
}

func contentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var out []string
		for _, p := range c {
			if pm, ok := p.(map[string]any); ok {
				if t, ok := pm["text"].(string); ok {
					out = append(out, t)
				}
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

var codebuddyAgentMarkers = []string{
	"you are claude code", "claude code", "anthropic's official cli",
	"anthropic official cli", "anxthxropic", "cursor", "windsurf", "cline",
	"aider", "continue", "copilot", "cody", "ai agent", "coding agent",
	"code agent", "cc_entrypoint", "orchestration capabilities",
	"OhMyOpenCode", "<agent-identity>", "<Role>", "<Behavior_Instructions>",
}

func isAgentPrompt(text string) bool {
	low := strings.ToLower(text)
	for _, m := range codebuddyAgentMarkers {
		if strings.Contains(low, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- helpers

func strFromExtra(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// appVersion is the client version the CLI-parity headers report. Upstreams
// fingerprint the reference client, so this tracks the Node app's package
// version, not the KineRouter binary version. Override at link time when the
// tracked KineRouter release moves:
//
//	go build -ldflags "-X github.com/keyzakyy-dev/kinerouter/internal/executor.kineAppVersion=0.6.0"
var kineAppVersion = "0.5.119"

func appVersion() string { return kineAppVersion }

func hostPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	}
	return "Linux"
}

func hostArch() string       { return runtime.GOARCH }
func runtimeVersion() string { return runtime.Version() }

func hostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}
