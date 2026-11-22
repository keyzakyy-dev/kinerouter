package mitm

// Per-tool intercepts shared by the MITM server. Each one translates the
// tool's own wire shape to the local router's, then translates the answer back.
// Mirrors src/mitm/handlers/{antigravity,copilot,kiro}.js.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// ---------------------------------------------------------------- shared

// routerPost sends body to the local router and returns its response.
// Client headers are dropped except content-type (which we set) — no tool
// credential is ever forwarded to the router.
func (s *server) routerPost(ctx context.Context, path string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.routerBase+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	return (&http.Client{Timeout: 10 * time.Minute}).Do(req)
}

// pipeSSE copies the router response to the client as-is.
func pipeSSE(w http.ResponseWriter, resp *http.Response) {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	if strings.Contains(ct, "text/event-stream") {
		w.Header().Set("X-Accel-Buffering", "no")
	}
	w.WriteHeader(resp.StatusCode)
	if resp.Body == nil {
		return
	}
	flushCopy(w, resp.Body)
}

// eachSSEChunk parses `data:` lines from an SSE body.
func eachSSEChunk(body io.Reader, fn func(map[string]any)) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(data), &m) == nil {
			fn(m)
		}
	}
}

func flushCopy(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// ---------------------------------------------------------------- antigravity

// interceptAntigravity forwards the Antigravity body to /v1/chat/completions
// and rewrites OpenAI SSE back into the Antigravity envelope. The Go gateway
// has no antigravity client format, so the body is flattened here first.
func (s *server) interceptAntigravity(w http.ResponseWriter, r *http.Request, body []byte, mapped string) {
	isStream := strings.Contains(r.URL.RequestURI(), ":streamGenerateContent")
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		s.antigravityError(w, isStream, err.Error())
		return
	}
	chat := translate.GeminiRequestToChat(antigravityInner(parsed), mapped, isStream)
	resp, err := s.routerPost(r.Context(), "/v1/chat/completions", chat)
	if err != nil {
		s.antigravityError(w, isStream, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 || !isStream {
		var out map[string]any
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if json.Unmarshal(raw, &out) != nil {
			writeJSONRaw(w, resp.StatusCode, map[string]any{"error": map[string]any{
				"message": string(raw), "type": "mitm_error"}})
			return
		}
		if resp.StatusCode >= 400 {
			writeJSONRaw(w, resp.StatusCode, map[string]any{"error": rawError(out)})
			return
		}
		writeJSONRaw(w, http.StatusOK, map[string]any{"response": translate.OpenAIToGeminiResponse(out)})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	conv := &antigravityConv{}
	eachSSEChunk(resp.Body, func(chunk map[string]any) {
		for _, ev := range conv.transform(chunk) {
			w.Write([]byte("data: " + string(mustJSON(ev)) + "\r\n\r\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})
}

// antigravityInner unwraps {request:{contents,...}} to the Gemini top level.
func antigravityInner(body map[string]any) map[string]any {
	inner, ok := body["request"].(map[string]any)
	if !ok {
		return body
	}
	out := make(map[string]any, len(inner))
	for k, v := range inner {
		out[k] = v
	}
	return out
}

func (s *server) antigravityError(w http.ResponseWriter, isStream bool, msg string) {
	if isStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: " + string(mustJSON(map[string]any{"error": map[string]any{"message": msg}})) + "\r\n\r\n"))
		return
	}
	writeJSONRaw(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": msg, "type": "mitm_error"}})
}

// antigravityConv rewrites OpenAI SSE chunks into Antigravity frames, emitting
// tool calls once (at finish) as complete functionCall parts.
type antigravityConv struct {
	respID  string
	model   string
	usage   map[string]any
	tools   map[int]*pendingTool
	toolKey int
}

type pendingTool struct{ id, name, args string }

func (c *antigravityConv) transform(chunk map[string]any) []map[string]any {
	choice := firstChoice(chunk)
	delta, _ := choice["delta"].(map[string]any)
	if c.respID == "" {
		c.respID, _ = chunk["id"].(string)
		if c.respID == "" {
			c.respID = "resp_mitm"
		}
	}
	if m, _ := chunk["model"].(string); m != "" {
		c.model = m
	}
	if u, ok := chunk["usage"].(map[string]any); ok {
		c.usage = u
	}

	var parts []any
	if rc, _ := delta["reasoning_content"].(string); rc != "" {
		parts = append(parts, map[string]any{"thought": true, "text": rc})
	}
	if txt, _ := delta["content"].(string); txt != "" {
		parts = append(parts, map[string]any{"text": txt})
	}
	if tcs, ok := delta["tool_calls"].([]any); ok {
		if c.tools == nil {
			c.tools = map[int]*pendingTool{}
		}
		for _, t := range tcs {
			tm, _ := t.(map[string]any)
			if tm == nil {
				continue
			}
			idx := intOf(tm["index"])
			pt := c.tools[idx]
			if pt == nil {
				pt = &pendingTool{}
				c.tools[idx] = pt
			}
			if id, _ := tm["id"].(string); id != "" {
				pt.id = id
			}
			fn, _ := tm["function"].(map[string]any)
			if n, _ := fn["name"].(string); n != "" {
				pt.name += n
			}
			if a, _ := fn["arguments"].(string); a != "" {
				pt.args += a
			}
		}
	}

	finish, _ := choice["finish_reason"].(string)
	if finish != "" {
		for i := 0; i < len(c.tools); i++ {
			pt := c.tools[i]
			if pt == nil {
				continue
			}
			args := map[string]any{}
			json.Unmarshal([]byte(pt.args), &args)
			parts = append(parts, map[string]any{"functionCall": map[string]any{"name": pt.name, "args": args}})
		}
	}
	if len(parts) == 0 && finish == "" {
		return nil
	}
	if len(parts) == 0 {
		parts = []any{map[string]any{"text": ""}}
	}
	cand := map[string]any{"content": map[string]any{"role": "model", "parts": parts}}
	resp := map[string]any{"candidates": []any{cand}, "modelVersion": c.model, "responseId": c.respID}
	if finish != "" {
		cand["finishReason"] = geminiFinish(finish)
		if c.usage != nil {
			resp["usageMetadata"] = geminiUsageFromOpenAI(c.usage)
		}
	}
	return []map[string]any{{"response": resp}}
}

func geminiFinish(reason string) string {
	switch reason {
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	}
	return "STOP"
}

func geminiUsageFromOpenAI(u map[string]any) map[string]any {
	out := map[string]any{
		"promptTokenCount":     intOf(u["prompt_tokens"]),
		"candidatesTokenCount": intOf(u["completion_tokens"]),
		"totalTokenCount":      intOf(u["total_tokens"]),
	}
	if d, ok := u["completion_tokens_details"].(map[string]any); ok {
		if r := intOf(d["reasoning_tokens"]); r > 0 {
			out["thoughtsTokenCount"] = r
		}
	}
	if d, ok := u["prompt_tokens_details"].(map[string]any); ok {
		if c := intOf(d["cached_tokens"]); c > 0 {
			out["cachedContentTokenCount"] = c
		}
	}
	return out
}

// ---------------------------------------------------------------- copilot

var copilotURLMap = []struct{ sub, path string }{
	{"/chat/completions", "/v1/chat/completions"},
	{"/v1/messages", "/v1/messages"},
	{"/responses", "/v1/responses"},
}

func (s *server) interceptCopilot(w http.ResponseWriter, r *http.Request, body []byte, mapped string) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeJSONRaw(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": err.Error(), "type": "mitm_error"}})
		return
	}
	parsed["model"] = mapped
	path := "/v1/chat/completions"
	for _, m := range copilotURLMap {
		if strings.Contains(r.URL.RequestURI(), m.sub) {
			path = m.path
			break
		}
	}
	resp, err := s.routerPost(r.Context(), path, parsed)
	if err != nil {
		writeJSONRaw(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": err.Error(), "type": "mitm_error"}})
		return
	}
	defer resp.Body.Close()
	pipeSSE(w, resp)
}

// ---------------------------------------------------------------- kiro

func (s *server) interceptKiro(w http.ResponseWriter, r *http.Request, body []byte, mapped string) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeJSONRaw(w, http.StatusInternalServerError, kiroError(err.Error()))
		return
	}
	messages := codeWhispererToMessages(parsed)
	if len(messages) == 0 {
		writeJSONRaw(w, http.StatusInternalServerError, kiroError("codeWhispererToMessages produced 0 messages"))
		return
	}
	chat := map[string]any{"model": mapped, "messages": messages, "stream": true}
	if tools := extractTools(parsed); len(tools) > 0 {
		chat["tools"] = tools
		chat["tool_choice"] = "auto"
	}
	resp, err := s.routerPost(r.Context(), "/v1/chat/completions", chat)
	if err != nil {
		writeJSONRaw(w, http.StatusInternalServerError, kiroError(err.Error()))
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	state := newKiroState(mapped)
	flush := func(frames [][]byte) {
		for _, f := range frames {
			w.Write(f)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	eachSSEChunk(resp.Body, func(chunk map[string]any) {
		flush(convertOpenAIToKiro(chunk, state))
	})
	flush(convertOpenAIToKiro(nil, state))
}

func kiroError(msg string) map[string]any {
	return map[string]any{"error": map[string]any{"message": msg, "type": "mitm_error", "handler": "kiro"}}
}

// ---------------------------------------------------------------- helpers

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func writeJSONRaw(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(mustJSON(v))
}

func rawError(m map[string]any) any {
	if e, ok := m["error"]; ok {
		return e
	}
	return m
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// rootCertDER returns the Root CA DER for inclusion in leaf chain.
func rootCertDER(p Paths) ([]byte, error) {
	data, err := os.ReadFile(p.RootCert)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, x509.ErrUnsupportedAlgorithm
	}
	return block.Bytes, nil
}

// ---------------------------------------------------------------- upstream DNS

var upstreamResolvers = []string{"8.8.8.8:53", "1.1.1.1:53"}

type ipCacheEntry struct {
	ip string
	ts time.Time
}

var (
	ipCacheMu sync.Mutex
	ipCache   = map[string]ipCacheEntry{}
)

const ipCacheTTL = 5 * time.Minute

// resolvePublic resolves host through a public resolver so the local
// /etc/hosts redirect (which points at this server) is bypassed.
func resolvePublic(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	ipCacheMu.Lock()
	c, ok := ipCache[host]
	ipCacheMu.Unlock()
	if ok && time.Since(c.ts) < ipCacheTTL {
		return c.ip, nil
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			for _, srv := range upstreamResolvers {
				if conn, err := d.DialContext(ctx, "udp", srv); err == nil {
					return conn, nil
				}
			}
			return nil, errors.New("no upstream resolver reachable")
		},
	}
	addrs, err := r.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		if err == nil {
			err = errors.New("no address")
		}
		return "", err
	}
	ipCacheMu.Lock()
	ipCache[host] = ipCacheEntry{ip: addrs[0], ts: time.Now()}
	ipCacheMu.Unlock()
	return addrs[0], nil
}

// ---------------------------------------------------------------- identity rewrite

const antigravityVersion = "2.24.0"

// applyAntigravityVersionOverride rewrites the IDE version markers Antigravity
// 2.x sends on generation requests. Mirrors antigravityIdeVersion.js.
func applyAntigravityVersionOverride(body []byte, headers http.Header, requestURL string) ([]byte, http.Header) {
	if !strings.Contains(requestURL, ":generateContent") && !strings.Contains(requestURL, ":streamGenerateContent") {
		return body, headers
	}
	out := headers.Clone()
	if ua := out.Get("User-Agent"); strings.Contains(ua, "antigravity/") {
		out.Set("User-Agent", rewriteAntigravityUA(ua, antigravityVersion))
	}
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil {
		return body, out
	}
	metadata, _ := parsed["metadata"].(map[string]any)
	if metadata == nil {
		return body, out
	}
	name, _ := metadata["ideName"].(string)
	typ, _ := metadata["ideType"].(string)
	_, hasVersion := metadata["ideVersion"]
	if !(strings.EqualFold(name, "antigravity") || strings.ToUpper(typ) == "ANTIGRAVITY" || hasVersion) {
		return body, out
	}
	metadata["ideVersion"] = antigravityVersion
	parsed["metadata"] = metadata
	next, err := json.Marshal(parsed)
	if err != nil {
		return body, out
	}
	return next, out
}

func rewriteAntigravityUA(ua, version string) string {
	parts := strings.Fields(ua)
	for i, p := range parts {
		if strings.HasPrefix(p, "antigravity/") {
			parts[i] = "antigravity/" + version
		}
	}
	return strings.Join(parts, " ")
}
