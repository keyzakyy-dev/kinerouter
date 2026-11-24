package executor

// Freebuff (codebuff.com) wire. Its CLI-shaped protocol needs three things the
// plain openai passthrough cannot do: a per-model session admission, an agent
// run id bound to every turn, and the CLI fingerprint headers. The whole
// exchange therefore lives in the executor.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	freebuffSessionPath   = "/api/v1/freebuff/session"
	freebuffAdmissionPath = "/api/v1/freebuff/session/admission"
	freebuffRunsPath      = "/api/v1/agent-runs"
	freebuffCLIUserAgent  = "Freebuff-CLI/0.2.36"
	freebuffCLIVersion    = "0.2.36"
	freebuffChatUA        = "ai-sdk/openai-compatible/1.0/codebuff"
	freebuffMarker        = "You are Buffy, the strategic coding assistant."
	freebuffSessionTTL    = time.Hour
)

var freebuffRootAgent = map[string]string{
	"deepseek/deepseek-v4-pro":        "base3-free-deepseek",
	"deepseek/deepseek-v4-flash":      "base3-free-deepseek-flash",
	"deepseek/deepseek-v4-flash-fast": "base3-fast",
	"mimo/mimo-v2.5":                  "base3-free-mimo",
	"mimo/mimo-v2.6-pro":              "base3-free-mimo-2-6-pro",
	"minimax/minimax-m3":              "base3-free-minimax-m3",
	"openai/gpt-5.6-luna":             "base3-free-luna",
	"openai/gpt-6-luna":               "base3-free-luna-6",
	"openai/gpt-6.1-sol":              "base3-free-gpt-6-1-sol",
	"openai/gpt-6.1-terra":            "base3-free-gpt-6-1-terra",
	"google/gemini-4-argon":           "base3-free-gemini-4-argon",
	"google/gemini-3.8-flash":         "base3-free-gemini-3-8-flash",
	"z-ai/glm-5.2":                    "base3-free-glm",
	"z-ai/glm-5.3-flash":              "base3-free-glm-5-3-flash",
	"upstage/solar-pro4":              "base3-free-solar-pro4",
	"upstage/solar-mini4":             "base3-free-solar-mini4",
	"meta/muse-spark-1.2-contributor": "base3-free-muse-spark",
	"meta/muse-spark-1.3-contributor": "base3-free-muse-spark-1-3",
	"anthropic/claude-fable-5":        "base2-free-fable",
	"anthropic/claude-fable-5.1":      "base2-free-fable",
	"space-bunny/space-bunny-alpha":   "base3-free-space-bunny-alpha",
	"ox/ox-alpha":                     "base3-free-ox-alpha",
}

type freebuffSession struct {
	InstanceID string
	ExpiresAt  time.Time
}

var (
	freebuffMu      sync.Mutex
	freebuffCache   = map[string]freebuffSession{} // token::model
	freebuffClaimMu sync.Mutex
)

// freebuffHeaders is the registry auth hook: the stream recovery flag the chat
// lane demands (a bare request answers 410 freebuff_update_required).
func freebuffHeaders(_ Request, headers map[string]string) {
	headers["x-freebuff-sse-gzip-recovery"] = "1"
}

func freebuffOrigin(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return "https://www.codebuff.com"
}

func freebuffCached(token, model string) (freebuffSession, bool) {
	freebuffMu.Lock()
	defer freebuffMu.Unlock()
	s, ok := freebuffCache[token+"::"+model]
	if ok && time.Now().After(s.ExpiresAt) {
		delete(freebuffCache, token+"::"+model)
		return freebuffSession{}, false
	}
	return s, ok
}

func freebuffAdmit(ctx context.Context, client *http.Client, origin, token, model string) (freebuffSession, error) {
	freebuffClaimMu.Lock()
	defer freebuffClaimMu.Unlock()
	if s, ok := freebuffCached(token, model); ok {
		return s, nil
	}
	instanceID := "cli:" + uuid.NewString()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+freebuffAdmissionPath, nil)
	if err != nil {
		return freebuffSession{}, err
	}
	for k, v := range map[string]string{
		"Authorization":                  "Bearer " + token,
		"User-Agent":                     freebuffCLIUserAgent,
		"x-freebuff-client-version":      freebuffCLIVersion,
		"x-freebuff-instance-id":         instanceID,
		"x-freebuff-desktop-attempt-id":  strings.TrimPrefix(instanceID, "cli:"),
		"x-freebuff-model":               model,
		"x-freebuff-wallet-spend-limit":  "session",
		"x-freebuff-multi-session":       "1",
		"x-freebuff-purchase-continuity": "1",
	} {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return freebuffSession{}, fmt.Errorf("freebuff admission: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var data struct {
		Status       string `json:"status"`
		InstanceID   string `json:"instanceId"`
		ExpiresAt    string `json:"expiresAt"`
		CurrentModel string `json:"currentModel"`
		Error        string `json:"error"`
		Message      string `json:"message"`
	}
	_ = json.Unmarshal(raw, &data)
	if data.Status == "model_locked" || data.Error == "model_locked" {
		return freebuffSession{}, fmt.Errorf("freebuff session is locked to %q: %s", data.CurrentModel, data.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return freebuffSession{}, fmt.Errorf("freebuff admission %d: %.300s", resp.StatusCode, raw)
	}
	if data.InstanceID == "" {
		return freebuffSession{}, fmt.Errorf("freebuff admission returned no instanceId")
	}
	s := freebuffSession{InstanceID: data.InstanceID, ExpiresAt: time.Now().Add(freebuffSessionTTL)}
	if t, err := time.Parse(time.RFC3339, data.ExpiresAt); err == nil {
		s.ExpiresAt = t
	}
	freebuffMu.Lock()
	freebuffCache[token+"::"+model] = s
	freebuffMu.Unlock()
	return s, nil
}

func freebuffStartRun(ctx context.Context, client *http.Client, origin, token, model string) (string, error) {
	agent := freebuffRootAgent[model]
	if agent == "" {
		agent = "base3-free-deepseek-flash"
	}
	payload, _ := json.Marshal(map[string]any{"action": "START", "agentId": agent, "ancestorRunIds": []string{}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+freebuffRunsPath, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", freebuffCLIUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("freebuff run start: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("freebuff run start %d: %.300s", resp.StatusCode, raw)
	}
	var out struct {
		RunID string `json:"runId"`
	}
	if json.Unmarshal(raw, &out) != nil || out.RunID == "" {
		return "", fmt.Errorf("freebuff run start returned no runId")
	}
	return out.RunID, nil
}

func freebuffFinishRun(client *http.Client, origin, token, runID, status string) {
	if runID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"action": "FINISH", "runId": runID, "status": status})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+freebuffRunsPath, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", freebuffCLIUserAgent)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// freebuffShape rewrites an openai body into the CLI's turn shape.
func freebuffShape(body map[string]any, runID string, sess freebuffSession, fingerprintID string) error {
	delete(body, "reasoning_effort")
	delete(body, "reasoning")

	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		body["messages"] = []any{map[string]any{"role": "system", "content": freebuffMarker}}
	} else if first, ok := msgs[0].(map[string]any); ok && first["role"] == "system" {
		content, _ := first["content"].(string)
		if !strings.Contains(content, freebuffMarker) {
			first["content"] = freebuffMarker + "\n\n" + content
		}
	} else {
		body["messages"] = append([]any{map[string]any{"role": "system", "content": freebuffMarker}}, msgs...)
	}

	if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
		found := false
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if fn, ok := tm["function"].(map[string]any); ok && fn["name"] == "end_turn" {
					found = true
				}
			}
		}
		if !found {
			body["tools"] = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": "end_turn", "description": "Signal the end of the current task.",
					"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
				},
			})
		}
	}

	body["provider"] = map[string]any{"allow_fallbacks": false}
	if fingerprintID == "" {
		fingerprintID = "codebuff-cli-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	}
	body["codebuff_metadata"] = map[string]any{
		"client_id":              fingerprintID,
		"cost_mode":              "free",
		"run_id":                 runID,
		"trace_session_id":       uuid.NewString(),
		"surface":                "cli",
		"freebuff_multi_session": "1",
		"freebuff_instance_id":   sess.InstanceID,
	}
	return nil
}

// executeFreebuff drives one turn: admit a session, start a run, shape and send
// the chat body, re-admit once on a stale-seat status, then finish the run.
func (e *Executor) executeFreebuff(ctx context.Context, req Request, endpoint string, headers map[string]string, client *http.Client) (*Response, error) {
	token := firstNonEmpty(req.Creds.APIKey, req.Creds.AccessToken)
	if token == "" {
		return nil, fmt.Errorf("freebuff: access token is required")
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, fmt.Errorf("freebuff: parse body: %w", err)
	}
	model, _ := body["model"].(string)
	origin := freebuffOrigin(endpoint)
	fingerprintID, _ := req.Creds.Extra["fingerprintId"].(string)

	sess, err := freebuffAdmit(ctx, client, origin, token, model)
	if err != nil {
		return nil, err
	}
	runID, err := freebuffStartRun(ctx, client, origin, token, model)
	if err != nil {
		return nil, err
	}
	runStatus := "failed"
	defer func() { freebuffFinishRun(client, origin, token, runID, runStatus) }()

	if err := freebuffShape(body, runID, sess, fingerprintID); err != nil {
		return nil, err
	}
	send := func() (*http.Response, error) {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			hreq.Header.Set(k, v)
		}
		hreq.Header.Set("User-Agent", freebuffChatUA)
		return client.Do(hreq)
	}

	resp, err := send()
	if err != nil {
		return nil, fmt.Errorf("freebuff upstream: %w", err)
	}
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusPreconditionRequired {
		stale, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if strings.Contains(string(stale), "model_locked") {
			return &Response{Status: resp.StatusCode, Header: resp.Header, Body: io.NopCloser(bytes.NewReader(stale))}, nil
		}
		freebuffMu.Lock()
		delete(freebuffCache, token+"::"+model)
		freebuffMu.Unlock()
		retrySess, sessErr := freebuffAdmit(ctx, client, origin, token, model)
		if sessErr != nil {
			return &Response{Status: resp.StatusCode, Header: resp.Header, Body: io.NopCloser(bytes.NewReader(stale))}, nil
		}
		retryRun, runErr := freebuffStartRun(ctx, client, origin, token, model)
		if runErr != nil {
			return &Response{Status: resp.StatusCode, Header: resp.Header, Body: io.NopCloser(bytes.NewReader(stale))}, nil
		}
		freebuffFinishRun(client, origin, token, runID, "failed")
		runID, runStatus = retryRun, "failed"
		if err := freebuffShape(body, retryRun, retrySess, fingerprintID); err != nil {
			return nil, err
		}
		resp, err = send()
		if err != nil {
			return nil, fmt.Errorf("freebuff upstream retry: %w", err)
		}
	}
	if resp.StatusCode == http.StatusOK {
		runStatus = "completed"
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
}
