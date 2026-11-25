package executor

import (
	"strings"
	"testing"
)

// Self-check for the per-provider quirk ports: URL/header rewrites (azure,
// xiaomi-tokenplan, iflow signature, copilot headers) and body rewrites
// (kimchi artifact strip, codebuddy neutral prompt, github max_tokens).
func TestQuirks(t *testing.T) {
	// azure: deployment URL + api-key, no Bearer.
	req := Request{Provider: "azure", Model: "gpt-4o",
		Creds: Credentials{APIKey: "k", Extra: map[string]any{"azureEndpoint": "https://x.openai.azure.com/", "apiVersion": "2024-06-01"}}}
	h := map[string]string{"Authorization": "Bearer k"}
	u, _ := ApplyQuirksTransport(req, h)
	if u != "https://x.openai.azure.com/openai/deployments/gpt-4o/chat/completions?api-version=2024-06-01" {
		t.Fatalf("azure url %q", u)
	}
	if h["api-key"] != "k" || h["Authorization"] != "" {
		t.Fatalf("azure headers %v", h)
	}

	// xiaomi-tokenplan: claude goes to /anthropic/v1/messages.
	req = Request{Provider: "xiaomi-tokenplan", Format: "claude",
		Creds: Credentials{BaseURL: "https://tp.xiaomimimo.com/v1/chat/completions"}}
	if u, _ := ApplyQuirksTransport(req, map[string]string{}); u != "https://tp.xiaomimimo.com/anthropic/v1/messages" {
		t.Fatalf("xiaomi url %q", u)
	}

	// iflow: HMAC-SHA256 over "ua:session:ts" with the api key.
	req = Request{Provider: "iflow", Creds: Credentials{APIKey: "secret"}}
	h = map[string]string{"User-Agent": "iFlow-Cli"}
	ApplyQuirksTransport(req, h)
	if h["session-id"] == "" || h["x-iflow-timestamp"] == "" || len(h["x-iflow-signature"]) != 64 {
		t.Fatalf("iflow headers %v", h)
	}
	if h["Authorization"] != "Bearer secret" {
		t.Fatalf("iflow auth %q", h["Authorization"])
	}

	// github: copilot identity headers.
	req = Request{Provider: "github", Creds: Credentials{APIKey: "tok"}}
	h = map[string]string{}
	ApplyQuirksTransport(req, h)
	if h["copilot-integration-id"] != "vscode-chat" || h["Authorization"] != "Bearer tok" || h["x-github-api-version"] != "2025-04-01" {
		t.Fatalf("github headers %v", h)
	}

	// github body: gpt-5 gets max_completion_tokens, o3 gets reasoning_effort stripped.
	body := map[string]any{"max_tokens": 100.0, "reasoning_effort": "none"}
	ApplyQuirksBody("github", "gpt-5-mini", body, true)
	if body["max_completion_tokens"] != 100.0 || body["max_tokens"] != nil || body["reasoning_effort"] != nil {
		t.Fatalf("github body %v", body)
	}
	body = map[string]any{"max_tokens": 1.0}
	ApplyQuirksBody("github", "claude-sonnet-4.5", body, true)
	if body["max_completion_tokens"] != nil || body["max_tokens"] != 1.0 {
		t.Fatalf("non-o-series must keep max_tokens: %v", body)
	}

	// kimchi: system merged into messages, anthropic artifacts dropped.
	body = map[string]any{
		"system":            "SYS",
		"anthropic_version": "x",
		"thinking":          map[string]any{"type": "enabled"},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "hi", "cache_control": map[string]any{}}}},
		},
	}
	ApplyQuirksBody("kimchi", "claude-x", body, true)
	if _, ok := body["system"]; ok || body["anthropic_version"] != nil || body["thinking"] != nil {
		t.Fatalf("kimchi drops %v", body)
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("kimchi system merge %v", msgs)
	}
	parts := msgs[1].(map[string]any)["content"].([]any)
	if parts[0].(map[string]any)["cache_control"] != nil {
		t.Fatal("kimchi cache_control not stripped")
	}

	// codebuddy: stream forced, agent system prompt neutralized.
	long := strings.Repeat("You are Claude Code. ", 200)
	body = map[string]any{"messages": []any{map[string]any{"role": "system", "content": long}}}
	ApplyQuirksBody("codebuddy-cn", "m", body, false)
	if body["stream"] != true {
		t.Fatal("codebuddy must force stream")
	}
	if got := body["messages"].([]any)[0].(map[string]any)["content"]; got != codebuddyNeutralPrompt {
		t.Fatalf("codebuddy prompt %q", got)
	}
	// Typed-block shape is preserved.
	body = map[string]any{"messages": []any{map[string]any{"role": "system",
		"content": []any{map[string]any{"type": "text", "text": "you are claude code"}}}}}
	ApplyQuirksBody("codebuddy-intl", "m", body, false)
	blk := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if blk[0].(map[string]any)["text"] != codebuddyNeutralPrompt {
		t.Fatalf("typed block %v", blk)
	}

	// minimax headers.
	req = Request{Provider: "minimax-code", Creds: Credentials{Hooks: []string{"minimaxHeaders"}}}
	h = map[string]string{}
	ApplyQuirksTransport(req, h)
	if h["x-api-key"] != "sk-xxx" || h["X-Mavis-Session-Id"] == "" {
		t.Fatalf("minimax headers %v", h)
	}
}

func TestHeaderHooks(t *testing.T) {
	// kimiHeaders: stable device id from providerSpecificData, host fingerprint.
	req := Request{Provider: "kimi", Creds: Credentials{Extra: map[string]any{"deviceId": "dev-1"}, Hooks: []string{"kimiHeaders"}}}
	h := map[string]string{}
	applyHeaderHooks(req, h)
	if h["X-Msh-Device-Id"] != "dev-1" || h["X-Msh-Platform"] != "kinerouter" || h["X-Msh-Device-Model"] == "" {
		t.Fatalf("kimi headers %v", h)
	}
	// A missing device id falls back to a fresh kimi-<ts> id.
	h = map[string]string{}
	req.Creds.Extra = map[string]any{}
	applyHeaderHooks(req, h)
	if !strings.HasPrefix(h["X-Msh-Device-Id"], "kimi-") {
		t.Fatalf("kimi fallback id %q", h["X-Msh-Device-Id"])
	}

	// clineHeaders: WorkOS JWT gets the prefix, clp_ key is verbatim.
	h = map[string]string{}
	applyHeaderHooks(Request{Provider: "cline", Creds: Credentials{AccessToken: "eyJhbGciOi.payload.sig", Hooks: []string{"clineHeaders"}}}, h)
	if h["Authorization"] != "Bearer workos:eyJhbGciOi.payload.sig" || h["X-IS-MULTIROOT"] != "false" {
		t.Fatalf("cline jwt %v", h)
	}
	h = map[string]string{}
	applyHeaderHooks(Request{Provider: "clinepass", Creds: Credentials{APIKey: "clp_abc", Hooks: []string{"clineHeaders"}}}, h)
	if h["Authorization"] != "Bearer clp_abc" {
		t.Fatalf("clinepass key %v", h)
	}

	// museHeaders: only on subscription (access token, no plain key).
	h = map[string]string{}
	applyHeaderHooks(Request{Provider: "muse", Creds: Credentials{AccessToken: "a", Hooks: []string{"museHeaders"}}}, h)
	if h["x-api-version"] != "1.0.0" {
		t.Fatalf("muse %v", h)
	}
	h = map[string]string{}
	applyHeaderHooks(Request{Provider: "muse", Creds: Credentials{APIKey: "k", Hooks: []string{"museHeaders"}}}, h)
	if h["x-api-version"] != "" {
		t.Fatalf("muse api key must not set x-api-version: %v", h)
	}

	// kilocodeOrg: org id from providerSpecificData.
	h = map[string]string{}
	applyHeaderHooks(Request{Provider: "kilocode", Creds: Credentials{Extra: map[string]any{"orgId": "org-9"}, Hooks: []string{"kilocodeOrg"}}}, h)
	if h["X-Kilocode-OrganizationID"] != "org-9" {
		t.Fatalf("kilocode %v", h)
	}
}

func TestCodexQuirks(t *testing.T) {
	body := map[string]any{
		"input":       "hello",
		"input_items": 1,
	}
	ApplyQuirksBody("codex", "gpt-5.3-codex", body, true)
	if body["stream"] != true {
		t.Fatal("codex must stream")
	}
	if _, ok := body["input"].(string); ok {
		t.Fatal("string input must become an array")
	}
	if body["instructions"] != codexDefaultInstructions {
		t.Fatalf("instructions %v", body["instructions"])
	}
	// Stored ids (rs_/fc_/msg_) are stripped; client ids survive.
	body = map[string]any{"input": []any{
		map[string]any{"type": "message", "id": "msg_1", "role": "user"},
		map[string]any{"type": "message", "id": "keep-me", "role": "user"},
	}}
	ApplyQuirksBody("codex", "m", body, true)
	items := body["input"].([]any)
	if items[0].(map[string]any)["id"] != nil || items[1].(map[string]any)["id"] != "keep-me" {
		t.Fatalf("codex id strip %v", items)
	}
	// Empty input gets a placeholder turn.
	body = map[string]any{"input": []any{}}
	ApplyQuirksBody("codex", "m", body, true)
	if len(body["input"].([]any)) != 1 {
		t.Fatalf("empty input %v", body["input"])
	}

	// codexHeaders binds the account id (workspace > chatgpt account > account).
	h := map[string]string{}
	ApplyQuirksTransport(Request{Provider: "codex", Creds: Credentials{
		SessionID: "sess-1", Extra: map[string]any{"chatgptAccountId": "acct-7"}}}, h)
	if h["session_id"] != "sess-1" || h["ChatGPT-Account-ID"] != "acct-7" || h["originator"] != "codex_cli_rs" {
		t.Fatalf("codex headers %v", h)
	}
}

// The ChatGPT Codex backend rejects any parameter outside its allowlist with
// 400 "Unsupported parameter", so a client that sends temperature or an output
// budget would never get an answer.
func TestCodexUnsupportedParamsDropped(t *testing.T) {
	body := map[string]any{
		"model":                 "gpt-5.3-codex",
		"input":                 []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
		"temperature":           0.7,
		"top_p":                 0.9,
		"max_output_tokens":     4096,
		"max_tokens":            4096,
		"max_completion_tokens": 4096,
		"metadata":              map[string]any{"k": "v"},
		"stream_options":        map[string]any{"include_usage": true},
		"previous_response_id":  "resp_1",
		"user":                  "someone",
		"seed":                  7.0,
		"n":                     2.0,
		"prompt_cache_key":      "cache-1",
		"parallel_tool_calls":   true,
		"text":                  map[string]any{"format": map[string]any{"type": "text"}},
	}
	ApplyQuirksBody("codex", "gpt-5.3-codex", body, true)
	for _, k := range []string{"temperature", "top_p", "max_output_tokens", "max_tokens",
		"max_completion_tokens", "metadata", "stream_options", "previous_response_id",
		"user", "seed", "n"} {
		if _, ok := body[k]; ok {
			t.Fatalf("%s must be dropped for codex: %#v", k, body)
		}
	}
	for _, k := range []string{"model", "input", "stream", "instructions", "prompt_cache_key",
		"parallel_tool_calls", "text"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("%s must survive: %#v", k, body)
		}
	}
}

func TestCodexServiceTierAndReasoningInclude(t *testing.T) {
	fast := map[string]any{"input": "hi", "service_tier": "fast", "speed": true}
	ApplyQuirksBody("codex", "m", fast, true)
	if fast["service_tier"] != "priority" {
		t.Fatalf("fast tier: %v", fast["service_tier"])
	}
	if _, ok := fast["speed"]; ok {
		t.Fatal("the client-only speed hint must be dropped")
	}
	flex := map[string]any{"input": "hi", "service_tier": "flex"}
	ApplyQuirksBody("codex", "m", flex, true)
	if _, ok := flex["service_tier"]; ok {
		t.Fatalf("a tier the backend does not accept must be dropped: %v", flex["service_tier"])
	}
	// Reasoning effort asks for encrypted content so a store=false session can
	// carry the reasoning forward.
	r := map[string]any{"input": "hi", "reasoning": map[string]any{"effort": "high"}}
	ApplyQuirksBody("codex", "m", r, true)
	inc, _ := r["include"].([]any)
	if len(inc) != 1 || inc[0] != "reasoning.encrypted_content" {
		t.Fatalf("include: %v", r["include"])
	}
	// A client-supplied include list is preserved.
	own := map[string]any{"input": "hi", "reasoning": map[string]any{"effort": "high"},
		"include": []any{"message.output_text.logprobs"}}
	ApplyQuirksBody("codex", "m", own, true)
	if got := own["include"].([]any); len(got) != 1 || got[0] != "message.output_text.logprobs" {
		t.Fatalf("client include overwritten: %v", got)
	}
	// effort "none" does not ask for reasoning content.
	none := map[string]any{"input": "hi", "reasoning": map[string]any{"effort": "none"}}
	ApplyQuirksBody("codex", "m", none, true)
	if none["include"] != nil {
		t.Fatalf("effort none must not request reasoning: %v", none["include"])
	}
}
