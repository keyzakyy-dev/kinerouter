package translate

// Kiro / AWS CodeWhisperer wire format. Ported from
// open-sse/translator/request/{openai-to-kiro,claude-to-kiro}.js and
// concerns/kiroConversation.js.
//
// The converter reads both OpenAI- and Claude-shaped messages, so the direct
// claude→kiro route needs no OpenAI pivot (the one lossy hop JS avoids).
//
// ponytail: session replay (applyKiroSessionReplay) is skipped — it re-sends a
// cached content prefix so Kiro's prompt cache hits on resumed conversations.
// Add when cache-read accounting matters more than the extra state.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	kiroAgenticSuffix  = "-agentic"
	kiroThinkingSuffix = "-thinking"
	kiroToolNameMax    = 64
	kiroToolDescMax    = 10237
	kiroToolIDMax      = 64
	// Kiro rejects empty user content; placeholders mirror kiroConversation.js.
	kiroToolResultsPlaceholder = "Tool results provided."
	kiroEmptyUserPlaceholder   = "continue"
)

// Default CodeWhisperer profile ARNs (us-east-1), keyed by auth method.
var kiroDefaultProfileARNs = map[string]string{
	"builder-id": "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX",
	"social":     "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK",
}

const kiroAgenticSystemPrompt = `# CRITICAL: CHUNKED WRITE PROTOCOL (MANDATORY)

You MUST follow these rules for ALL file operations. Violation causes server timeouts and task failure.

## ABSOLUTE LIMITS
- **MAXIMUM 350 LINES** per single write/edit operation - NO EXCEPTIONS
- **RECOMMENDED 300 LINES** or less for optimal performance
- **NEVER** write entire files in one operation if >300 lines

## MANDATORY CHUNKED WRITE STRATEGY

### For NEW FILES (>300 lines total):
1. FIRST: Write initial chunk (first 250-300 lines)
2. THEN: Append remaining content in 250-300 line chunks
3. REPEAT: Continue appending until complete

### For EDITING EXISTING FILES:
1. Use surgical edits - change ONLY what's needed
2. NEVER rewrite entire files
3. Split large refactors into multiple small, focused edits

REMEMBER: When in doubt, write LESS per operation.`

// KiroProfileARN resolves the profile ARN for a connection. Account-bound auth
// methods (api_key / idc / external_idp) must never fall back to the shared
// default: it belongs to another account and Kiro answers 403.
func KiroProfileARN(authMethod, connectionARN string) string {
	if connectionARN != "" {
		return connectionARN
	}
	switch authMethod {
	case "api_key", "idc", "external_idp":
		return ""
	case "google", "github":
		return kiroDefaultProfileARNs["social"]
	}
	return kiroDefaultProfileARNs["builder-id"]
}

// normalizeKiroModelID folds Anthropic spellings onto Kiro's dotted aliases.
func normalizeKiroModelID(model string) string {
	if model == "" {
		return model
	}
	id := strings.ToLower(model)
	if len(id) > 9 && id[len(id)-9] == '-' && allDigits(id[len(id)-8:]) {
		id = id[:len(id)-9]
	}
	has := func(parts ...string) bool {
		for _, p := range parts {
			if strings.Contains(id, p) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.Contains(id, "sonnet"):
		switch {
		case has("5.5", "5-5"):
			return "claude-sonnet-5.5"
		case has("4.6", "4-6"):
			return "claude-sonnet-4.6"
		case has("sonnet-5", "sonnet.5"):
			return "claude-sonnet-5"
		case has("4.5", "4-5"):
			return "claude-sonnet-4.5"
		case has("sonnet-4", "sonnet.4"):
			return "claude-sonnet-4"
		}
		return "claude-sonnet-4.5"
	case strings.Contains(id, "fable"):
		return "claude-fable-5"
	case strings.Contains(id, "opus"):
		switch {
		case has("5.5", "5-5"):
			return "claude-opus-5.5"
		case has("opus-5", "opus.5", "opus 5"):
			return "claude-opus-5"
		case has("4.8", "4-8"):
			return "claude-opus-4.8"
		case has("4.7", "4-7"):
			return "claude-opus-4.7"
		case has("4.5", "4-5"):
			return "claude-opus-4.5"
		}
		return "claude-opus-4.6"
	case strings.Contains(id, "haiku"):
		if has("5.5", "5-5") {
			return "claude-haiku-5.5"
		}
		return "claude-haiku-4.5"
	}
	return model
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// resolveKiroModel strips the synthetic -agentic / -thinking suffixes.
func resolveKiroModel(model string) (upstream string, agentic bool) {
	upstream = model
	if strings.HasSuffix(upstream, kiroThinkingSuffix) {
		upstream = strings.TrimSuffix(upstream, kiroThinkingSuffix)
	}
	if strings.HasSuffix(upstream, kiroAgenticSuffix) {
		agentic = true
		upstream = strings.TrimSuffix(upstream, kiroAgenticSuffix)
	}
	return normalizeKiroModelID(upstream), agentic
}

// kiroThinkingBudget maps the request's effort knobs onto a token budget, or 0
// when Thinking is off. Mirrors resolveKiroThinkingBudget without the
// thinkingUnified parser: budget_tokens and effort levels are what clients send.
func kiroThinkingBudget(raw map[string]any) int64 {
	if th, ok := raw["thinking"].(map[string]any); ok {
		if th["type"] == "enabled" {
			if b := num(th["budget_tokens"]); b > 0 {
				return clampBudget(b)
			}
			return 16000
		}
		if th["type"] == "disabled" {
			return 0
		}
	}
	effort := str(raw, "reasoning_effort")
	if oc, ok := raw["output_config"].(map[string]any); ok {
		if s, _ := oc["effort"].(string); s != "" {
			effort = s
		}
	}
	var budget int64
	switch strings.ToLower(effort) {
	case "minimal":
		budget = 2000
	case "low":
		budget = 4000
	case "medium":
		budget = 10000
	case "high", "xhigh", "max":
		budget = 16000
	default:
		return 0
	}
	return clampBudget(budget)
}

func clampBudget(b int64) int64 {
	if b < 1 {
		b = 1
	}
	if b > 32000 {
		b = 32000
	}
	return b
}

// ------------------------------------------------------- tool specs

type kiroToolSpecs struct {
	specs      []any // []{toolSpecification:{name,description,inputSchema:{json}}}
	specNames  map[string]bool
	restoreMap map[string]string // sanitized → original, for the response side
}

func normalizeKiroToolSpecs(tools any) kiroToolSpecs {
	out := kiroToolSpecs{specNames: map[string]bool{}, restoreMap: map[string]string{}}
	arr, _ := tools.([]any)
	used := map[string]bool{}
	seen := map[string]bool{}
	for i, t := range arr {
		tool, ok := t.(map[string]any)
		if !ok {
			continue
		}
		fn, _ := tool["function"].(map[string]any)
		name := str(tool, "name")
		schema, desc := tool["input_schema"], str(tool, "description")
		if fn != nil {
			if n := str(fn, "name"); n != "" {
				name = n
			}
			if d := str(fn, "description"); d != "" {
				desc = d
			}
			if p, ok := fn["parameters"]; ok {
				schema = p
			}
		}
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		safe := uniqueKiroToolName(name, i, used)
		if desc == "" {
			desc = "Tool: " + name
		}
		desc = trimRunes(desc, kiroToolDescMax)
		out.specs = append(out.specs, map[string]any{"toolSpecification": map[string]any{
			"name":        safe,
			"description": desc,
			"inputSchema": map[string]any{"json": normalizeRootSchema(schema)},
		}})
		out.specNames[safe] = true
		if safe != name {
			out.restoreMap[safe] = name
		}
	}
	return out
}

func uniqueKiroToolName(raw string, index int, used map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		if r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	base := trimRunes(strings.Trim(b.String(), "_"), kiroToolNameMax)
	if base == "" {
		base = "tool_" + strconv.Itoa(index+1)
	}
	cand := base
	for suffix := 2; used[cand]; suffix++ {
		tail := "_" + strconv.Itoa(suffix)
		cand = trimRunes(base, kiroToolNameMax-len(tail)) + tail
	}
	used[cand] = true
	return cand
}

func trimRunes(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit])
}

// normalizeRootSchema drops empty required/additionalProperties and pins type.
func normalizeRootSchema(schema any) map[string]any {
	m, ok := cleanSchemaValue(schema).(map[string]any)
	if !ok {
		m = map[string]any{}
	}
	m["type"] = "object"
	props, ok := m["properties"].(map[string]any)
	if !ok {
		props = map[string]any{}
		m["properties"] = props
	}
	if req, ok := m["required"].([]any); ok {
		var kept []any
		seen := map[string]bool{}
		for _, r := range req {
			s, _ := r.(string)
			if _, has := props[s]; has && !seen[s] {
				seen[s] = true
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			delete(m, "required")
		} else {
			m["required"] = kept
		}
	}
	return m
}

func cleanSchemaValue(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cleanSchemaValue(t[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			if k == "additionalProperties" {
				continue
			}
			if k == "required" {
				if arr, ok := child.([]any); ok && len(arr) == 0 {
					continue
				}
			}
			out[k] = cleanSchemaValue(child)
		}
		return out
	}
	return v
}

// ------------------------------------------------------- conversation

type kiroTurn = map[string]any

// convertKiroMessages turns a message list (OpenAI or Claude shape) into
// alternating Kiro turns, with the last user turn popping out as the current
// message.
func convertKiroMessages(messages []any, model string) (history []any, current map[string]any) {
	var pendingUser, pendingAssistant []string
	var pendingResults, pendingImages []any
	role := ""

	flush := func() {
		switch role {
		case "user":
			content := strings.TrimSpace(strings.Join(pendingUser, "\n\n"))
			if content == "" {
				if len(pendingResults) > 0 {
					content = kiroToolResultsPlaceholder
				} else {
					content = kiroEmptyUserPlaceholder
				}
			}
			uim := map[string]any{"content": content, "modelId": model}
			if len(pendingImages) > 0 {
				uim["images"] = pendingImages
			}
			if len(pendingResults) > 0 {
				uim["userInputMessageContext"] = map[string]any{"toolResults": pendingResults}
			}
			msg := kiroTurn{"userInputMessage": uim}
			history = append(history, msg)
			current = msg
			pendingUser, pendingResults, pendingImages = nil, nil, nil
		case "assistant":
			content := strings.TrimSpace(strings.Join(pendingAssistant, "\n\n"))
			if content == "" {
				content = "..."
			}
			history = append(history, kiroTurn{"assistantResponseMessage": map[string]any{"content": content}})
			pendingAssistant = nil
		}
	}

	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		mrole := str(msg, "role")
		wasSystem := mrole == "system" || mrole == "developer"
		switch mrole {
		case "system", "developer", "tool", "user":
			mrole = "user"
		case "assistant":
		default:
			continue
		}
		if mrole != role && role != "" {
			flush()
		}
		role = mrole

		if mrole == "user" {
			texts := kiroUserBlocks(msg, &pendingResults, &pendingImages)
			if orig := str(msg, "role"); orig == "tool" {
				pendingResults = append(pendingResults, map[string]any{
					"toolUseId": str(msg, "tool_call_id"),
					"status":    kiroResultStatus(msg),
					"content":   []any{map[string]any{"text": contentText(msg["content"])}},
				})
			} else if len(texts) > 0 {
				text := strings.Join(texts, "\n")
				if wasSystem {
					text = "<instructions>\n" + text + "\n</instructions>"
				}
				pendingUser = append(pendingUser, text)
			}
		} else {
			text, toolUses := kiroAssistantBlocks(msg)
			if text != "" {
				pendingAssistant = append(pendingAssistant, text)
			}
			if len(toolUses) > 0 {
				flush()
				if len(history) > 0 {
					last, _ := history[len(history)-1].(map[string]any)
					if arm, ok := last["assistantResponseMessage"].(map[string]any); ok {
						arm["toolUses"] = toolUses
					}
				}
				role = ""
			}
		}
	}
	if role != "" {
		flush()
	}
	// Pop the last user turn (skipping a trailing assistant turn) as current.
	for i := len(history) - 1; i >= 0; i-- {
		if t, ok := history[i].(map[string]any); ok && t["userInputMessage"] != nil {
			current, _ = t, ok
			history = append(history[:i], history[i+1:]...)
			break
		}
	}
	if current == nil {
		current = kiroTurn{"userInputMessage": map[string]any{"content": "", "modelId": model}}
	}
	return history, current
}

func kiroResultStatus(msg map[string]any) string {
	if msg["is_error"] == true || msg["status"] == "error" {
		return "error"
	}
	return "success"
}

// kiroUserBlocks pulls text, images and tool results out of one user message.
func kiroUserBlocks(msg map[string]any, results, images *[]any) []string {
	switch c := msg["content"].(type) {
	case string:
		if strings.TrimSpace(c) == "" {
			return nil
		}
		return []string{c}
	case []any:
		var texts []string
		for _, b := range c {
			blk, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch str(blk, "type") {
			case "text":
				if t := str(blk, "text"); t != "" {
					texts = append(texts, t)
				}
			case "image_url":
				url := ""
				if iu, ok := blk["image_url"].(map[string]any); ok {
					url = str(iu, "url")
				}
				if mime, data, ok := parseDataURI(url); ok {
					*images = append(*images, kiroImage(mime, data))
				} else if strings.HasPrefix(url, "http") {
					texts = append(texts, "[Image: "+url+"]")
				}
			case "image":
				if src, ok := blk["source"].(map[string]any); ok && str(src, "type") == "base64" {
					mime := str(src, "media_type")
					if mime == "" {
						mime = "image/png"
					}
					*images = append(*images, kiroImage(mime, str(src, "data")))
				}
			case "tool_result":
				text := contentText(blk["content"])
				var innerTexts []string
				if arr, ok := blk["content"].([]any); ok {
					hasImage := false
					for _, ib := range arr {
						iblk, ok := ib.(map[string]any)
						if !ok {
							continue
						}
						if str(iblk, "type") == "image" {
							if src, ok := iblk["source"].(map[string]any); ok && str(src, "type") == "base64" {
								hasImage = true
								mime := str(src, "media_type")
								if mime == "" {
									mime = "image/png"
								}
								*images = append(*images, kiroImage(mime, str(src, "data")))
							}
						} else if t := str(iblk, "text"); t != "" {
							innerTexts = append(innerTexts, t)
						}
					}
					if len(innerTexts) > 0 {
						text = strings.Join(innerTexts, "\n")
					} else if hasImage {
						text = "(image attached)"
					}
				}
				status := "success"
				if blk["is_error"] == true {
					status = "error"
				}
				*results = append(*results, map[string]any{
					"toolUseId": str(blk, "tool_use_id"),
					"status":    status,
					"content":   []any{map[string]any{"text": text}},
				})
			}
		}
		return texts
	}
	return nil
}

func kiroAssistantBlocks(msg map[string]any) (text string, toolUses []any) {
	switch c := msg["content"].(type) {
	case string:
		text = strings.TrimSpace(c)
	case []any:
		var texts []string
		for _, b := range c {
			blk, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch str(blk, "type") {
			case "text":
				if t := str(blk, "text"); t != "" {
					texts = append(texts, t)
				}
			case "tool_use":
				input, _ := blk["input"].(map[string]any)
				if input == nil {
					input = map[string]any{}
				}
				toolUses = append(toolUses, map[string]any{
					"toolUseId": str(blk, "id"),
					"name":      str(blk, "name"),
					"input":     input,
				})
			}
		}
		text = strings.TrimSpace(strings.Join(texts, "\n"))
	}
	if tcs, ok := msg["tool_calls"].([]any); ok && len(tcs) > 0 {
		toolUses = nil
		for _, tc := range tcs {
			call, ok := tc.(map[string]any)
			if !ok {
				continue
			}
			id := str(call, "id")
			if id == "" {
				id = newKiroCallID()
			}
			if fn, ok := call["function"].(map[string]any); ok {
				toolUses = append(toolUses, map[string]any{
					"toolUseId": id,
					"name":      str(fn, "name"),
					"input":     parseToolArgs(fn["arguments"]),
				})
			} else {
				input, _ := call["input"].(map[string]any)
				if input == nil {
					input = map[string]any{}
				}
				toolUses = append(toolUses, map[string]any{
					"toolUseId": id,
					"name":      str(call, "name"),
					"input":     input,
				})
			}
		}
	}
	return text, toolUses
}

func parseToolArgs(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil && m != nil {
			return m
		}
	}
	return map[string]any{}
}

func kiroImage(mime, data string) map[string]any {
	format := mime
	if i := strings.IndexByte(mime, '/'); i >= 0 {
		format = mime[i+1:]
	}
	return map[string]any{"format": format, "source": map[string]any{"bytes": data}}
}

func parseDataURI(url string) (mime, data string, ok bool) {
	if !strings.HasPrefix(url, "data:") {
		return "", "", false
	}
	rest := url[len("data:"):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", "", false
	}
	meta := rest[:comma]
	if !strings.Contains(meta, "base64") {
		return "", "", false
	}
	return strings.TrimSuffix(meta, ";base64"), rest[comma+1:], true
}

func contentText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, b := range t {
			if blk, ok := b.(map[string]any); ok {
				if s := str(blk, "text"); s != "" {
					parts = append(parts, s)
				}
			} else if s, ok := b.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func newKiroCallID() string {
	var b [16]byte
	rand.Read(b[:])
	return "call_" + hex.EncodeToString(b[:8])
}

// ------------------------------------------------------- canonicalize

// canonicalizeKiroConversation enforces alternating turns, one-to-one adjacent
// tool use/result pairs, and tool specs only on the current message. A pair
// that cannot be repaired is flattened to text, which Kiro always accepts.
func canonicalizeKiroConversation(history []any, current map[string]any, model string, specs kiroToolSpecs) (outHistory []map[string]any, outCurrent map[string]any, valid bool) {
	turns := make([]map[string]any, 0, len(history)+1)
	for _, h := range history {
		t, ok := h.(map[string]any)
		if !ok {
			continue
		}
		isUser := t["userInputMessage"] != nil
		if isUser == (t["assistantResponseMessage"] != nil) {
			continue
		}
		prev := lastTurn(turns)
		if isUser && prev != nil && prev["userInputMessage"] != nil {
			mergeKiroUser(prev["userInputMessage"].(map[string]any), t["userInputMessage"].(map[string]any))
		} else if !isUser && prev != nil && prev["assistantResponseMessage"] != nil {
			mergeKiroAssistant(prev["assistantResponseMessage"].(map[string]any), t["assistantResponseMessage"].(map[string]any))
		} else {
			turns = append(turns, cloneTurn(t))
		}
	}
	isUser := current["userInputMessage"] != nil
	if prev := lastTurn(turns); prev != nil {
		if isUser && prev["userInputMessage"] != nil {
			mergeKiroUser(prev["userInputMessage"].(map[string]any), current["userInputMessage"].(map[string]any))
			current = prev
		} else if !isUser && prev["assistantResponseMessage"] != nil {
			mergeKiroAssistant(prev["assistantResponseMessage"].(map[string]any), current["assistantResponseMessage"].(map[string]any))
			current = prev
		} else {
			turns = append(turns, cloneTurn(current))
		}
	} else {
		turns = append(turns, cloneTurn(current))
	}
	// The current message is always the trailing user turn.
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i]["userInputMessage"] != nil {
			current = turns[i]
			turns = append(turns[:i], turns[i+1:]...)
			break
		}
	}
	if len(turns) > 0 && turns[0]["assistantResponseMessage"] != nil {
		turns = append([]map[string]any{{"userInputMessage": map[string]any{"content": "continue", "modelId": model}}}, turns...)
	}

	usedIDs := map[string]bool{}
	for i := 1; i+1 < len(turns); i += 2 {
		assistant, _ := turns[i]["assistantResponseMessage"].(map[string]any)
		nextUser, _ := turns[i+1]["userInputMessage"].(map[string]any)
		if assistant == nil || nextUser == nil {
			continue
		}
		reconcileKiroToolPair(assistant, nextUser, i, specs, usedIDs)
	}

	uim := current["userInputMessage"].(map[string]any)
	ctx, _ := uim["userInputMessageContext"].(map[string]any)
	if len(specs.specs) > 0 {
		if ctx == nil {
			ctx = map[string]any{}
		}
		ctx["tools"] = specs.specs
		uim["userInputMessageContext"] = ctx
	}
	cleanKiroContext(uim)
	stripKiroTools(turns)

	errors := validateKiroConversation(turns, current, specs.specNames)
	if len(errors) > 0 {
		// Second chance: flatten every structured tool turn to text.
		for _, t := range append(turns, current) {
			flattenTurnTools(t)
		}
		errors = validateKiroConversation(turns, current, specs.specNames)
	}
	return turns, current, len(errors) == 0
}

func lastTurn(turns []map[string]any) map[string]any {
	if len(turns) == 0 {
		return nil
	}
	return turns[len(turns)-1]
}

func cloneTurn(t map[string]any) map[string]any {
	b, _ := json.Marshal(t)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

func mergeKiroUser(dst, src map[string]any) {
	if extra := str(src, "content"); extra != "" {
		if base := str(dst, "content"); base != "" {
			dst["content"] = base + "\n\n" + extra
		} else {
			dst["content"] = extra
		}
	}
	if imgs, ok := src["images"].([]any); ok && len(imgs) > 0 {
		existing, _ := dst["images"].([]any)
		dst["images"] = append(existing, imgs...)
	}
	srcCtx, _ := src["userInputMessageContext"].(map[string]any)
	if srcCtx == nil {
		return
	}
	dstCtx, _ := dst["userInputMessageContext"].(map[string]any)
	if dstCtx == nil {
		dstCtx = map[string]any{}
		dst["userInputMessageContext"] = dstCtx
	}
	for _, key := range []string{"toolResults", "tools"} {
		if add, ok := srcCtx[key].([]any); ok && len(add) > 0 {
			existing, _ := dstCtx[key].([]any)
			dstCtx[key] = append(existing, add...)
		}
	}
}

func mergeKiroAssistant(dst, src map[string]any) {
	if extra := str(src, "content"); extra != "" {
		if base := str(dst, "content"); base != "" {
			dst["content"] = base + "\n\n" + extra
		} else {
			dst["content"] = extra
		}
	}
	if calls, ok := src["toolUses"].([]any); ok && len(calls) > 0 {
		existing, _ := dst["toolUses"].([]any)
		dst["toolUses"] = append(existing, calls...)
	}
}

// reconcileKiroToolPair keeps only calls that have a matching result and a
// declared spec; everything else is flattened to text so the turn stays honest.
func reconcileKiroToolPair(assistant, user map[string]any, turnIndex int, specs kiroToolSpecs, usedIDs map[string]bool) {
	calls, _ := assistant["toolUses"].([]any)
	ctx, _ := user["userInputMessageContext"].(map[string]any)
	rawResults, _ := ctx["toolResults"].([]any)
	if len(calls) == 0 {
		for _, r := range rawResults {
			appendKiroText(user, toolResultText(r))
		}
		if ctx != nil {
			delete(ctx, "toolResults")
		}
		cleanKiroContext(user)
		return
	}

	type record struct {
		call     map[string]any
		index    int
		key      string
		name     string
		input    map[string]any
		result   map[string]any
		hasInput bool
	}
	queues := map[string][]*record{}
	var records []*record
	for i, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := str(call, "name")
		if mapped, ok := specs.restoreMap[name]; ok {
			name = mapped
		}
		input, hasInput := kiroToolInput(call["input"])
		rec := &record{call: call, index: i, key: str(call, "toolUseId"), name: name, input: input, hasInput: hasInput}
		queues[rec.key] = append(queues[rec.key], rec)
		records = append(records, rec)
	}
	var orphans []any
	for _, raw := range rawResults {
		res, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		matched := false
		for _, rec := range queues[str(res, "toolUseId")] {
			if rec.result == nil {
				rec.result = res
				matched = true
				break
			}
		}
		if !matched {
			orphans = append(orphans, res)
		}
	}

	var keptCalls, keptResults []any
	for _, rec := range records {
		safe := sanitizedKiroToolName(rec.name, specs)
		valid := rec.result != nil && rec.hasInput && specs.specNames[safe]
		if !valid {
			appendKiroText(assistant, toolCallText(rec.call))
			if rec.result != nil {
				appendKiroText(user, toolResultText(rec.result))
			}
			continue
		}
		id := reserveKiroToolID(rec.key, turnIndex, rec.index, safe, usedIDs)
		keptCalls = append(keptCalls, map[string]any{"toolUseId": id, "name": safe, "input": rec.input})
		keptResults = append(keptResults, map[string]any{
			"toolUseId": id,
			"status":    kiroStatus(rec.result),
			"content":   kiroResultContent(rec.result),
		})
	}
	for _, o := range orphans {
		appendKiroText(user, toolResultText(o))
	}

	if len(keptCalls) > 0 {
		assistant["toolUses"] = keptCalls
	} else {
		delete(assistant, "toolUses")
	}
	if ctx == nil {
		ctx = map[string]any{}
		user["userInputMessageContext"] = ctx
	}
	if len(keptResults) > 0 {
		ctx["toolResults"] = keptResults
	} else {
		delete(ctx, "toolResults")
	}
	cleanKiroContext(user)
}

// sanitizedKiroToolName maps an original tool name onto the spec's safe name.
func sanitizedKiroToolName(name string, specs kiroToolSpecs) string {
	if specs.specNames[name] {
		return name
	}
	for safe, orig := range specs.restoreMap {
		if orig == name {
			return safe
		}
	}
	return name
}

func kiroToolInput(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case nil:
		return map[string]any{}, true
	case map[string]any:
		return t, true
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil && m != nil {
			return m, true
		}
		return nil, false
	}
	return nil, false
}

func reserveKiroToolID(raw string, turnIndex, callIndex int, name string, used map[string]bool) string {
	var b strings.Builder
	for _, r := range raw {
		if r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	base := b.String()
	if base == "" {
		base = "call_msg" + strconv.Itoa(turnIndex) + "_tc" + strconv.Itoa(callIndex) + "_" + name
	}
	base = trimRunes(base, kiroToolIDMax)
	cand := base
	for suffix := 2; used[cand]; suffix++ {
		tail := "_" + strconv.Itoa(suffix)
		cand = trimRunes(base, kiroToolIDMax-len(tail)) + tail
	}
	used[cand] = true
	return cand
}

func kiroStatus(result map[string]any) string {
	if str(result, "status") == "error" {
		return "error"
	}
	return "success"
}

func kiroResultContent(result map[string]any) []any {
	switch c := result["content"].(type) {
	case []any:
		out := make([]any, 0, len(c))
		for _, p := range c {
			if blk, ok := p.(map[string]any); ok {
				out = append(out, map[string]any{"text": contentText(blk["text"])})
			} else {
				out = append(out, map[string]any{"text": contentText(p)})
			}
		}
		if len(out) > 0 {
			return out
		}
	case string:
		return []any{map[string]any{"text": c}}
	}
	return []any{map[string]any{"text": ""}}
}

func toolCallText(call map[string]any) string {
	input, _ := json.Marshal(call["input"])
	return "[Tool call: " + str(call, "name") + "(" + string(input) + ")]"
}

func toolResultText(r any) string {
	res, _ := r.(map[string]any)
	label := "Tool result"
	if res != nil && str(res, "status") == "error" {
		label = "Tool result (error)"
	}
	var text string
	if res != nil {
		text = contentText(res["content"])
	}
	return "[" + label + ": " + text + "]"
}

func appendKiroText(msg map[string]any, extra string) {
	if extra == "" {
		return
	}
	if base := str(msg, "content"); base != "" {
		msg["content"] = base + "\n\n" + extra
	} else {
		msg["content"] = extra
	}
}

func cleanKiroContext(user map[string]any) {
	ctx, _ := user["userInputMessageContext"].(map[string]any)
	if ctx == nil {
		return
	}
	if arr, ok := ctx["toolResults"].([]any); !ok || len(arr) == 0 {
		delete(ctx, "toolResults")
	}
	if arr, ok := ctx["tools"].([]any); !ok || len(arr) == 0 {
		delete(ctx, "tools")
	}
	if len(ctx) == 0 {
		delete(user, "userInputMessageContext")
	}
}

func stripKiroTools(turns []map[string]any) {
	for _, t := range turns {
		if uim, ok := t["userInputMessage"].(map[string]any); ok {
			if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
				delete(ctx, "tools")
				cleanKiroContext(uim)
			}
		}
	}
}

func flattenTurnTools(turn map[string]any) {
	if arm, ok := turn["assistantResponseMessage"].(map[string]any); ok {
		if calls, ok := arm["toolUses"].([]any); ok && len(calls) > 0 {
			for _, c := range calls {
				if call, ok := c.(map[string]any); ok {
					appendKiroText(arm, toolCallText(call))
				}
			}
			delete(arm, "toolUses")
		}
	}
	if uim, ok := turn["userInputMessage"].(map[string]any); ok {
		if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
			if results, ok := ctx["toolResults"].([]any); ok && len(results) > 0 {
				for _, r := range results {
					appendKiroText(uim, toolResultText(r))
				}
				delete(ctx, "toolResults")
			}
			cleanKiroContext(uim)
		}
	}
}

func validateKiroConversation(history []map[string]any, current map[string]any, specNames map[string]bool) []string {
	var errs []string
	turns := append(append([]map[string]any{}, history...), current)
	usedIDs := map[string]bool{}
	for i, t := range turns {
		if t == nil {
			continue
		}
		uim, _ := t["userInputMessage"].(map[string]any)
		if expectUser := i%2 == 0; (uim != nil) != expectUser {
			errs = append(errs, "role:"+strconv.Itoa(i))
		}
		if uim != nil {
			if i == 0 {
				if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
					if arr, ok := ctx["toolResults"].([]any); ok && len(arr) > 0 {
						errs = append(errs, "orphan:0")
					}
				}
			}
			continue
		}
		arm, _ := t["assistantResponseMessage"].(map[string]any)
		if arm == nil {
			continue
		}
		calls, _ := arm["toolUses"].([]any)
		var results []any
		if i+1 < len(turns) {
			if next := turns[i+1]; next != nil {
				if nu, ok := next["userInputMessage"].(map[string]any); ok {
					if ctx, ok := nu["userInputMessageContext"].(map[string]any); ok {
						results, _ = ctx["toolResults"].([]any)
					}
				}
			}
		}
		resultIDs := map[string]bool{}
		for _, r := range results {
			if rm, ok := r.(map[string]any); ok {
				resultIDs[str(rm, "toolUseId")] = true
			}
		}
		if len(calls) != len(results) {
			errs = append(errs, "pair:"+strconv.Itoa(i))
		}
		for _, c := range calls {
			call, _ := c.(map[string]any)
			if call == nil {
				continue
			}
			id := str(call, "toolUseId")
			if id == "" || usedIDs[id] {
				errs = append(errs, "id:"+strconv.Itoa(i))
			}
			usedIDs[id] = true
			if !resultIDs[id] {
				errs = append(errs, "pair:"+strconv.Itoa(i))
			}
			if !specNames[str(call, "name")] {
				errs = append(errs, "spec:"+strconv.Itoa(i))
			}
		}
	}
	if cu, _ := current["userInputMessage"].(map[string]any); cu == nil || str(cu, "content") == "" {
		errs = append(errs, "current")
	}
	return errs
}

// ------------------------------------------------------- payload

// KiroOptions carries the connection facts the payload needs beyond the body.
type KiroOptions struct {
	ProfileARN string
	SessionID  string // stable conversation id; empty generates one
}

// ToKiro builds the CodeWhisperer GenerateAssistantResponse payload. It accepts
// both OpenAI- and Claude-shaped bodies. Returns false when the conversation
// cannot be made shippable — the caller answers 400 without an upstream call.
func ToKiro(raw map[string]any, model string, opts KiroOptions) (payload map[string]any, restore map[string]string, ok bool) {
	upstream, agentic := resolveKiroModel(model)
	messages, _ := raw["messages"].([]any)
	history, current := convertKiroMessages(messages, upstream)
	specs := normalizeKiroToolSpecs(raw["tools"])

	// The system prompt travels in the first user turn: CodeWhisperer rejects a
	// top-level systemPrompt with 400 REQUEST_BODY_INVALID.
	var promptParts []string
	if budget := kiroThinkingBudget(raw); budget > 0 {
		promptParts = append(promptParts, "<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>"+strconv.FormatInt(budget, 10)+"</max_thinking_length>")
	}
	if agentic {
		promptParts = append(promptParts, kiroAgenticSystemPrompt)
	}
	if sys := claudeSystemText(raw["system"]); sys != "" {
		promptParts = append(promptParts, sys)
	}
	promptParts = append(promptParts, "[Context: Current time is "+time.Now().UTC().Format(time.RFC3339)+"]")
	prefix := strings.Join(promptParts, "\n\n")
	if first := firstKiroUserTurn(history, current); first != nil {
		appendKiroText(first, prefix)
	}

	canonHistory, canonCurrent, valid := canonicalizeKiroConversation(history, current, upstream, specs)
	if !valid {
		return nil, nil, false
	}
	uim := canonCurrent["userInputMessage"].(map[string]any)
	cur := map[string]any{"content": str(uim, "content"), "modelId": upstream, "origin": "AI_EDITOR"}
	if ctx, ok := uim["userInputMessageContext"]; ok {
		cur["userInputMessageContext"] = ctx
	}
	if imgs, ok := uim["images"]; ok {
		cur["images"] = imgs
	}
	conversationID := opts.SessionID
	if conversationID == "" {
		conversationID = newKiroCallID()
	}
	payload = map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType": "MANUAL",
			"conversationId":  conversationID,
			"currentMessage":  map[string]any{"userInputMessage": cur},
			"history":         canonHistory,
		},
	}
	if opts.ProfileARN != "" {
		payload["profileArn"] = opts.ProfileARN
	}
	if fields := kiroEffortFields(raw, upstream); fields != nil {
		payload["additionalModelRequestFields"] = fields
	}
	if maxTokens := num(raw["max_tokens"]); maxTokens > 0 || raw["temperature"] != nil || raw["top_p"] != nil {
		inf := map[string]any{"maxTokens": 32000}
		if maxTokens > 0 {
			inf["maxTokens"] = maxTokens
		}
		if t, ok := raw["temperature"].(float64); ok {
			inf["temperature"] = t
		}
		if p, ok := raw["top_p"].(float64); ok {
			inf["topP"] = p
		}
		payload["inferenceConfig"] = inf
	}
	return payload, specs.restoreMap, true
}

func firstKiroUserTurn(history []any, current map[string]any) map[string]any {
	for _, h := range history {
		if t, ok := h.(map[string]any); ok {
			if uim, ok := t["userInputMessage"].(map[string]any); ok {
				return uim
			}
		}
	}
	if uim, ok := current["userInputMessage"].(map[string]any); ok {
		return uim
	}
	return nil
}

func claudeSystemText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, b := range t {
			switch blk := b.(type) {
			case string:
				parts = append(parts, blk)
			case map[string]any:
				if s := str(blk, "text"); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// kiroEffortFields maps effort onto Kiro's native schema. Claude 4.6+ takes
// output_config; GPT-5.6 takes reasoning. Older models reject the field, so
// they get none.
func kiroEffortFields(raw map[string]any, model string) map[string]any {
	effort := strings.ToLower(str(raw, "reasoning_effort"))
	if oc, ok := raw["output_config"].(map[string]any); ok {
		if s, _ := oc["effort"].(string); s != "" {
			effort = strings.ToLower(s)
		}
	}
	if r, ok := raw["reasoning"].(map[string]any); ok {
		if s, _ := r["effort"].(string); s != "" && effort == "" {
			effort = strings.ToLower(s)
		}
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return nil
	}
	id := strings.ReplaceAll(strings.ToLower(model), "-", ".")
	switch {
	case strings.Contains(id, "gpt.5.6"):
		if effort == "max" {
			effort = "xhigh"
		}
		return map[string]any{"reasoning": map[string]any{"effort": effort}}
	case strings.Contains(id, "claude"):
		if effort == "xhigh" && kiroLacksXhigh(model) {
			effort = "high"
		}
		return map[string]any{
			"thinking":      map[string]any{"type": "adaptive", "display": "summarized"},
			"output_config": map[string]any{"effort": effort},
		}
	}
	return nil
}

// kiroLacksXhigh: Claude <= 4.6 has no xhigh tier.
func kiroLacksXhigh(model string) bool {
	id := strings.ReplaceAll(strings.ToLower(model), "-", ".")
	i := strings.Index(id, "claude.")
	if i < 0 {
		return true
	}
	for _, p := range strings.Split(id[i+len("claude."):], ".") {
		if n, ok := atoi(p); ok {
			return n <= 4 && !strings.Contains(id, "claude.4.7") && !strings.Contains(id, "claude.4.8")
		}
	}
	return true
}

func atoi(s string) (int, bool) {
	if !allDigits(s) {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}
