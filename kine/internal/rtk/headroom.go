package rtk

// Headroom proxy compression — ported from open-sse/rtk/headroom.js. POSTs the
// conversation to an external Headroom proxy at <url>/v1/compress and writes the
// compressed text back into the *provider-shaped* body, never round-tripping the
// native blocks (a Claude↔OpenAI hop would drop documents, signed thinking and
// tool-result metadata).
//
// Fail-open, like the JS: any error returns nil and leaves the body untouched.
//
// ponytail: the proxy only answers OpenAI-shaped messages[], so this mirrors the
// JS projection strategy per format (OpenAI/Responses tool output, Claude, Kiro,
// CommandCode) instead of translating whole requests. Add a translator-based
// round-trip only if the proxy gains native format support.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

const (
	headroomDefaultTimeout = 3 * time.Second
	headroomMaxDepth       = 8
	headroomMaxResponse    = 16 << 20
)

// HeadroomOptions configures one compression attempt.
type HeadroomOptions struct {
	Enabled              bool
	URL                  string
	Model                string
	Format               string
	CompressUserMessages bool
	Timeout              time.Duration
}

// HeadroomStats is the proxy's reported token delta.
type HeadroomStats struct {
	TokensBefore int64
	TokensAfter  int64
	TokensSaved  int64
}

// HeadroomDiag explains what happened; Reason is set on every early return.
type HeadroomDiag struct {
	Reason   string
	Endpoint string
}

type leafTarget struct {
	m   map[string]any
	key string
	arr []any
	idx int
}

func (t leafTarget) set(s string) {
	if t.m != nil {
		t.m[t.key] = s
	} else {
		t.arr[t.idx] = s
	}
}

// collectLeaf walks the text leaves the proxy may rewrite: plain strings that
// are not JSON payloads, inside result/output/content/text fields.
func collectLeaf(v any, tgt leafTarget, depth int, add func(leafTarget, string)) {
	if depth > headroomMaxDepth {
		return
	}
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) == "" {
			return
		}
		var probe any
		if json.Unmarshal([]byte(x), &probe) == nil {
			return // structured payload: leave in its native form
		}
		add(tgt, x)
	case []any:
		for i := range x {
			collectLeaf(x[i], leafTarget{arr: x, idx: i}, depth+1, add)
		}
	case map[string]any:
		if t, _ := x["type"].(string); t != "" && t != "text" && t != "input_text" {
			return
		}
		for _, f := range []string{"result", "output", "content", "text"} {
			if v, ok := x[f]; ok {
				collectLeaf(v, leafTarget{m: x, key: f}, depth+1, add)
			}
		}
	}
}

func headroomToolID(n int) string { return fmt.Sprintf("headroom-tool-%d", n) }

// hasUnsafeResponsesInput reports whether body.input holds protocol items that
// cannot survive a plain messages[] round-trip.
func hasUnsafeResponsesInput(body map[string]any) bool {
	items, ok := body["input"].([]any)
	if !ok {
		return false
	}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if t, _ := m["type"].(string); t != "" && t != "message" {
				return true
			}
		}
	}
	return false
}

type projection struct {
	messages []map[string]any
	targets  []*leafTarget // nil = metadata-only entry
}

// collectToolOutputs handles the two formats whose tool results live inside
// native structures (Responses items, Gemini functionResponse parts).
func collectToolOutputs(body map[string]any, format string) *projection {
	p := &projection{}
	add := func(t leafTarget, text string) {
		p.messages = append(p.messages, map[string]any{
			"role": "tool", "tool_call_id": headroomToolID(len(p.messages)), "content": text,
		})
		p.targets = append(p.targets, &t)
	}
	switch {
	case format == translate.FormatResponses:
		if !hasUnsafeResponsesInput(body) {
			return nil
		}
		items, _ := body["input"].([]any)
		for _, it := range items {
			if m, ok := it.(map[string]any); ok && m["type"] == "function_call_output" {
				collectLeaf(m["output"], leafTarget{m: m, key: "output"}, 0, add)
			}
		}
	case format == translate.FormatGemini || format == "gemini-cli" || format == "antigravity" || format == "vertex":
		var contents []any
		if req, ok := body["request"].(map[string]any); ok {
			contents, _ = req["contents"].([]any)
		} else {
			contents, _ = body["contents"].([]any)
		}
		if contents == nil {
			return nil
		}
		for _, c := range contents {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			parts, _ := cm["parts"].([]any)
			for _, pt := range parts {
				pm, ok := pt.(map[string]any)
				if !ok {
					continue
				}
				if fr, ok := pm["functionResponse"].(map[string]any); ok {
					collectLeaf(fr["response"], leafTarget{m: fr, key: "response"}, 0, add)
				}
			}
		}
	default:
		return nil
	}
	return p
}

// applyToolOutputs copies compressed text back only when the proxy preserved
// count, roles, ids and produced shorter text.
func applyToolOutputs(p *projection, out []any, diag *HeadroomDiag) bool {
	if len(out) != len(p.messages) {
		diag.Reason = "proxy response did not match tool-output count"
		return false
	}
	type update struct {
		tgt  leafTarget
		text string
	}
	var updates []update
	for i, raw := range out {
		m, ok := raw.(map[string]any)
		if !ok || m["role"] != "tool" || m["tool_call_id"] != p.messages[i]["tool_call_id"] {
			diag.Reason = "proxy response did not preserve tool-output IDs, order, roles or text"
			return false
		}
		text, ok := m["content"].(string)
		if !ok {
			diag.Reason = "proxy response did not preserve tool-output IDs, order, roles or text"
			return false
		}
		// The original leaf text is recoverable from the projection itself.
		orig, _ := p.messages[i]["content"].(string)
		if strings.TrimSpace(text) != "" && len(text) < len(orig) {
			updates = append(updates, update{*p.targets[i], text})
		}
	}
	for _, u := range updates {
		u.tgt.set(u.text)
	}
	return true
}

func collectClaude(body map[string]any) *projection {
	msgs, ok := body["messages"].([]any)
	if !ok {
		return nil
	}
	p := &projection{}
	addText := func(role string, m map[string]any, key string, extra map[string]any) {
		if s, ok := m[key].(string); ok {
			msg := map[string]any{"role": role, "content": s}
			for k, v := range extra {
				msg[k] = v
			}
			p.messages = append(p.messages, msg)
			p.targets = append(p.targets, &leafTarget{m: m, key: key})
		}
	}
	addContent := func(role string, m map[string]any, key string, extra map[string]any) {
		if _, ok := m[key].(string); ok {
			addText(role, m, key, extra)
			return
		}
		blocks, _ := m[key].([]any)
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if ok && bm["type"] == "text" {
				addText(role, bm, "text", extra)
			}
		}
	}
	addContent("system", body, "system", nil)
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		blocks, _ := m["content"].([]any)
		var toolUses []any
		for _, b := range blocks {
			if bm, ok := b.(map[string]any); ok && bm["type"] == "tool_use" {
				toolUses = append(toolUses, bm)
			}
		}
		if role == "assistant" && len(toolUses) > 0 {
			calls := make([]any, 0, len(toolUses))
			for _, tu := range toolUses {
				tm := tu.(map[string]any)
				args, _ := json.Marshal(tm["input"])
				calls = append(calls, map[string]any{
					"id": tm["id"], "type": "function",
					"function": map[string]any{"name": tm["name"], "arguments": string(args)},
				})
			}
			p.messages = append(p.messages, map[string]any{"role": "assistant", "content": "", "tool_calls": calls})
			p.targets = append(p.targets, nil)
		}
		addContent(role, m, "content", nil)
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok || bm["type"] != "tool_result" {
				continue
			}
			addContent("tool", bm, "content", map[string]any{"tool_call_id": bm["tool_use_id"]})
		}
	}
	for _, t := range p.targets {
		if t != nil {
			return p
		}
	}
	return nil
}

func collectKiro(body map[string]any) *projection {
	state, ok := body["conversationState"].(map[string]any)
	if !ok {
		return nil
	}
	p := &projection{}
	addText := func(role string, m map[string]any, key string, extra map[string]any) {
		s, ok := m[key].(string)
		if !ok {
			return
		}
		msg := map[string]any{"role": role, "content": s}
		for k, v := range extra {
			msg[k] = v
		}
		p.messages = append(p.messages, msg)
		p.targets = append(p.targets, &leafTarget{m: m, key: key})
	}
	visit := func(raw any) {
		item, ok := raw.(map[string]any)
		if !ok {
			return
		}
		if user, ok := item["userInputMessage"].(map[string]any); ok {
			addText("system", user, "systemInstruction", nil)
			addText("user", user, "content", nil)
			if ctx, ok := user["userInputMessageContext"].(map[string]any); ok {
				if results, ok := ctx["toolResults"].([]any); ok {
					for _, r := range results {
						rm, ok := r.(map[string]any)
						if !ok {
							continue
						}
						content, _ := rm["content"].([]any)
						extra := map[string]any{}
						if id, ok := rm["toolUseId"]; ok && id != nil && id != "" {
							extra["tool_call_id"] = id
						}
						for _, part := range content {
							if pm, ok := part.(map[string]any); ok {
								addText("tool", pm, "text", extra)
							}
						}
					}
				}
			}
			return
		}
		assistant, ok := item["assistantResponseMessage"].(map[string]any)
		if !ok {
			return
		}
		var extra map[string]any
		if uses, ok := assistant["toolUses"].([]any); ok && len(uses) > 0 {
			var calls []any
			for _, u := range uses {
				um, ok := u.(map[string]any)
				if !ok {
					continue
				}
				args, _ := json.Marshal(um["input"])
				call := map[string]any{
					"id": um["toolUseId"], "type": "function",
					"function": map[string]any{"name": um["name"], "arguments": string(args)},
				}
				if call["id"] != nil || call["function"].(map[string]any)["name"] != "" {
					calls = append(calls, call)
				}
			}
			if len(calls) > 0 {
				extra = map[string]any{"tool_calls": calls}
			}
		}
		addText("assistant", assistant, "content", extra)
	}
	if hist, ok := state["history"].([]any); ok {
		for _, h := range hist {
			visit(h)
		}
	}
	if cur, ok := state["currentMessage"]; ok {
		visit(cur)
	}
	if len(p.messages) == 0 {
		return nil
	}
	return p
}

func collectCommandCode(body map[string]any) *projection {
	params, ok := body["params"].(map[string]any)
	if !ok {
		return nil
	}
	msgs, ok := params["messages"].([]any)
	if !ok {
		return nil
	}
	p := &projection{}
	addText := func(role string, m map[string]any, key string, extra map[string]any) {
		s, ok := m[key].(string)
		if !ok {
			return
		}
		msg := map[string]any{"role": role, "content": s}
		for k, v := range extra {
			msg[k] = v
		}
		p.messages = append(p.messages, msg)
		p.targets = append(p.targets, &leafTarget{m: m, key: key})
	}
	addText("system", params, "system", nil)
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		parts, _ := m["content"].([]any)
		role, _ := m["role"].(string)
		var calls []any
		for _, pt := range parts {
			if pm, ok := pt.(map[string]any); ok && pm["type"] == "tool_call" {
				args, _ := json.Marshal(pm["input"])
				calls = append(calls, map[string]any{
					"id": pm["toolCallId"], "type": "function",
					"function": map[string]any{"name": pm["toolName"], "arguments": string(args)},
				})
			}
		}
		if role == "assistant" && len(calls) > 0 {
			p.messages = append(p.messages, map[string]any{"role": "assistant", "content": "", "tool_calls": calls})
			p.targets = append(p.targets, nil)
		}
		for _, pt := range parts {
			pm, ok := pt.(map[string]any)
			if !ok {
				continue
			}
			switch pm["type"] {
			case "text":
				addText(role, pm, "text", nil)
			case "tool_result":
				if out, ok := pm["output"].(map[string]any); ok && out["type"] == "text" {
					addText("tool", out, "value", map[string]any{"tool_call_id": pm["toolCallId"], "name": pm["toolName"]})
				}
			}
		}
	}
	if len(p.messages) == 0 {
		return nil
	}
	return p
}

// textFromMessage flattens an OpenAI-shaped message's content to text.
func textFromMessage(m map[string]any) (string, bool) {
	if s, ok := m["content"].(string); ok {
		return s, true
	}
	parts, ok := m["content"].([]any)
	if !ok {
		return "", false
	}
	var out []string
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if s, ok := v["text"].(string); ok {
				out = append(out, s)
			}
		}
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, "\n"), true
}

func applyProjection(p *projection, out []any, diag *HeadroomDiag, label string) bool {
	if len(out) != len(p.messages) {
		diag.Reason = "proxy response did not match " + label + " message count"
		return false
	}
	type update struct {
		tgt  leafTarget
		text string
	}
	var updates []update
	for i := range p.messages {
		m, ok := out[i].(map[string]any)
		if !ok || m["role"] != p.messages[i]["role"] || m["tool_call_id"] != p.messages[i]["tool_call_id"] {
			diag.Reason = "proxy response did not preserve " + label + " message order"
			return false
		}
		text, ok := textFromMessage(m)
		if !ok {
			diag.Reason = "proxy response missing " + label + " text content"
			return false
		}
		if p.targets[i] != nil {
			updates = append(updates, update{*p.targets[i], text})
		}
	}
	for _, u := range updates {
		u.tgt.set(u.text)
	}
	return true
}

func compressEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		// Mirror the JS fallback: string-join on the raw value.
		base := strings.TrimSuffix(strings.SplitN(raw, "#", 2)[0], "/")
		if base == "" {
			return "", fmt.Errorf("invalid proxy URL")
		}
		return base + "/v1/compress", nil
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/compress"
	u.Fragment = ""
	return u.String(), nil
}

// callCompress POSTs messages to the proxy. Returns the compressed messages.
func callCompress(endpoint, model string, messages []map[string]any, o HeadroomOptions, diag *HeadroomDiag) ([]any, *HeadroomStats) {
	payload := map[string]any{"messages": messages, "model": model}
	if o.CompressUserMessages {
		payload["config"] = map[string]any{"compress_user_messages": true}
	}
	raw, _ := json.Marshal(payload)
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = headroomDefaultTimeout
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		diag.Reason = "request failed: " + err.Error()
		return nil, nil
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		diag.Reason = "request failed: " + err.Error()
		return nil, nil
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		diag.Reason = fmt.Sprintf("proxy returned HTTP %d", res.StatusCode)
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, headroomMaxResponse))
	if err != nil {
		diag.Reason = "request failed: " + err.Error()
		return nil, nil
	}
	var data map[string]any
	if json.Unmarshal(body, &data) != nil {
		diag.Reason = "proxy response is not JSON"
		return nil, nil
	}
	out, ok := data["messages"].([]any)
	if !ok {
		diag.Reason = "proxy response missing messages[]"
		return nil, nil
	}
	stats := &HeadroomStats{
		TokensBefore: int64(number(data["tokens_before"])),
		TokensAfter:  int64(number(data["tokens_after"])),
		TokensSaved:  int64(number(data["tokens_saved"])),
	}
	return out, stats
}

func number(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// CompressWithHeadroom rewrites body in place. Returns nil stats when nothing
// changed; diag.Reason explains every early return.
func CompressWithHeadroom(body map[string]any, o HeadroomOptions) (*HeadroomStats, HeadroomDiag) {
	var diag HeadroomDiag
	if !o.Enabled {
		diag.Reason = "disabled"
		return nil, diag
	}
	if o.URL == "" {
		diag.Reason = "missing proxy URL"
		return nil, diag
	}
	if body == nil {
		diag.Reason = "missing request body"
		return nil, diag
	}
	endpoint, err := compressEndpoint(o.URL)
	if err != nil {
		diag.Reason = err.Error()
		return nil, diag
	}
	diag.Endpoint = maskEndpoint(endpoint)

	if p := collectToolOutputs(body, o.Format); p != nil {
		if len(p.messages) == 0 {
			diag.Reason = "no eligible plain-text tool output"
			return nil, diag
		}
		out, stats := callCompress(endpoint, o.Model, p.messages, o, &diag)
		if out == nil {
			return nil, diag
		}
		if !applyToolOutputs(p, out, &diag) {
			return nil, diag
		}
		return stats, diag
	}

	if o.Format == translate.FormatClaude {
		p := collectClaude(body)
		if p == nil {
			diag.Reason = "Claude request has no eligible text content"
			return nil, diag
		}
		out, stats := callCompress(endpoint, o.Model, p.messages, o, &diag)
		if out == nil {
			return nil, diag
		}
		if !applyProjection(p, out, &diag, "Claude") {
			return nil, diag
		}
		return stats, diag
	}

	if o.Format == "kiro" {
		p := collectKiro(body)
		if p == nil {
			diag.Reason = "Kiro request did not project to messages[]"
			return nil, diag
		}
		out, stats := callCompress(endpoint, o.Model, p.messages, o, &diag)
		if out == nil {
			return nil, diag
		}
		if !applyProjection(p, out, &diag, "Kiro") {
			return nil, diag
		}
		return stats, diag
	}

	if o.Format == "commandcode" {
		p := collectCommandCode(body)
		if p == nil {
			diag.Reason = "CommandCode request did not project to messages[]"
			return nil, diag
		}
		out, stats := callCompress(endpoint, o.Model, p.messages, o, &diag)
		if out == nil {
			return nil, diag
		}
		if !applyProjection(p, out, &diag, "CommandCode") {
			return nil, diag
		}
		return stats, diag
	}

	// Responses shape (Codex): body.input holds Responses items. Translate to
	// OpenAI messages for the proxy, then rebuild input from the compressed
	// messages so body.input keeps the Responses contract.
	if o.Format == translate.FormatResponses {
		oai := translate.ResponsesRequestToChat(body)
		rawMsgs, ok := oai["messages"].([]any)
		if !ok {
			diag.Reason = "openai-responses request did not translate to messages[]"
			return nil, diag
		}
		msgs := make([]map[string]any, 0, len(rawMsgs))
		for _, it := range rawMsgs {
			m, ok := it.(map[string]any)
			if !ok {
				diag.Reason = "request has non-object messages"
				return nil, diag
			}
			msgs = append(msgs, m)
		}
		out, stats := callCompress(endpoint, o.Model, msgs, o, &diag)
		if out == nil {
			return nil, diag
		}
		rebuilt := translate.ChatRequestToResponses(map[string]any{"messages": out}, o.Model, true)
		if items, ok := rebuilt["input"].([]any); ok {
			body["input"] = items
		}
		return stats, diag
	}

	key := ""
	if _, ok := body["messages"].([]any); ok {
		key = "messages"
	} else if _, ok := body["input"].([]any); ok {
		key = "input"
	}
	if key == "" {
		diag.Reason = "unsupported " + o.Format + " request shape"
		return nil, diag
	}
	var msgs []map[string]any
	for _, it := range body[key].([]any) {
		m, ok := it.(map[string]any)
		if !ok {
			diag.Reason = "request has non-object messages"
			return nil, diag
		}
		msgs = append(msgs, m)
	}
	out, stats := callCompress(endpoint, o.Model, msgs, o, &diag)
	if out == nil {
		return nil, diag
	}
	body[key] = out
	return stats, diag
}

// maskEndpoint strips credentials and query from a URL for diagnostics.
func maskEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
