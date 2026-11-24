package executor

// agy-acp drives the official Antigravity CLI (`agy acp`) over Agent Client
// Protocol. It is a separate provider from antigravity: no HTTP transport and
// no shared token file, only a credential snapshot written into an isolated
// HOME for the turn. Ported from 9router-go internal/proxy/executor/agy_acp.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/agy"
	"github.com/keyzakyy-dev/kinerouter/internal/config"
)

func (e *Executor) executeAGYACP(ctx context.Context, req Request) (*Response, error) {
	var chat agy.ChatRequest
	if err := json.Unmarshal(req.Body, &chat); err != nil {
		return agyError(fmt.Errorf("invalid request body: %w", err))
	}
	model := chat.Model
	if model == "" {
		model = req.Model
	}
	opts := agy.TurnOptions{
		AccountRoot:    agyAccountRoot(req),
		Credentials:    agy.Credentials{AccessToken: req.Creds.AccessToken, RefreshToken: req.Creds.RefreshToken, ExpiresAt: req.Creds.ExpiresAt},
		PermissionMode: "accept-edits",
		MCPTools:       agy.ToolsFromOpenAI(chat.Tools),
		MCPSeeded:      seedAGYToolResults(chat.Messages),
	}
	if v, ok := req.Creds.Extra["permissionMode"].(string); ok && v != "" {
		opts.PermissionMode = v
	}
	if v, ok := req.Creds.Extra["agyPath"].(string); ok && v != "" {
		opts.Binary = v
	}
	release, err := agy.DefaultManager().Acquire(ctx)
	if err != nil {
		return agyError(err)
	}
	prompt := agy.BuildPrompt(&chat)
	responseID := fmt.Sprintf("chatcmpl-agyacp-%d", time.Now().UnixMilli())
	created := time.Now().Unix()

	if !req.Stream {
		defer release()
		var text, thinking strings.Builder
		var toolCalls []map[string]any
		err := agy.RunTurn(ctx, opts, model, prompt, func(ev agy.TurnEvent) {
			switch ev.Kind {
			case agy.UpdateText:
				text.WriteString(ev.Text)
			case agy.UpdateThinking:
				thinking.WriteString(ev.Text)
			case agy.UpdateToolCall:
				if ev.ToolCall != nil {
					toolCalls = append(toolCalls, agyToolCall(ev.ToolCall))
				}
			}
		})
		if err != nil {
			return agyError(err)
		}
		message := map[string]any{"role": "assistant", "content": text.String()}
		if thinking.Len() > 0 {
			message["reasoning_content"] = thinking.String()
		}
		finish := "stop"
		if len(toolCalls) > 0 {
			message["tool_calls"] = toolCalls
			finish = "tool_calls"
		}
		out, _ := json.Marshal(map[string]any{
			"id": responseID, "object": "chat.completion", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
		})
		h := http.Header{}
		h.Set("Content-Type", "application/json")
		return &Response{Status: http.StatusOK, Header: h, Body: io.NopCloser(bytes.NewReader(out))}, nil
	}

	events := make(chan agy.TurnEvent, 256)
	errCh := make(chan error, 1)
	go func() {
		defer release()
		errCh <- agy.RunTurn(ctx, opts, model, prompt, func(ev agy.TurnEvent) {
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		})
	}()

	// Hold the response until the turn produces something, so a spawn/auth
	// failure is still a status the router can fail over on.
	var first agy.TurnEvent
	select {
	case first = <-events:
	case err := <-errCh:
		if err != nil {
			return agyError(err)
		}
		return agyError(agy.ErrStall)
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	pr, pw := io.Pipe()
	go streamAGYACP(pw, first, events, errCh, responseID, model, created)
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	return &Response{Status: http.StatusOK, Header: h, Body: pr}, nil
}

func streamAGYACP(w io.WriteCloser, first agy.TurnEvent, events <-chan agy.TurnEvent, errCh <-chan error, id, model string, created int64) {
	defer w.Close()
	base := func(choices []map[string]any) map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": choices}
	}
	emit := func(chunk map[string]any) error {
		b, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "data: %s\n\n", b)
		return err
	}
	delta := func(d map[string]any) error {
		return emit(base([]map[string]any{{"index": 0, "delta": d, "finish_reason": nil}}))
	}
	if err := delta(map[string]any{"role": "assistant"}); err != nil {
		return
	}
	var toolCalls []map[string]any
	handle := func(ev agy.TurnEvent) error {
		switch ev.Kind {
		case agy.UpdateThinking:
			if ev.Text != "" {
				return delta(map[string]any{"reasoning_content": ev.Text})
			}
		case agy.UpdateText:
			if ev.Text != "" {
				return delta(map[string]any{"content": ev.Text})
			}
		case agy.UpdateToolCall:
			if ev.ToolCall != nil {
				toolCalls = append(toolCalls, agyToolCall(ev.ToolCall))
			}
		}
		return nil
	}
	if err := handle(first); err != nil {
		return
	}
	for {
		select {
		case ev := <-events:
			if err := handle(ev); err != nil {
				return
			}
		case err := <-errCh:
			if err != nil {
				msg, _ := json.Marshal(map[string]any{"error": map[string]any{"message": "agy-acp: " + err.Error(), "type": "api_error"}})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			finish := "stop"
			if len(toolCalls) > 0 {
				finish = "tool_calls"
				for i, tc := range toolCalls {
					item := map[string]any{"index": i, "id": tc["id"], "type": "function", "function": tc["function"]}
					if err := delta(map[string]any{"tool_calls": []map[string]any{item}}); err != nil {
						return
					}
				}
			}
			_ = emit(base([]map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}))
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
	}
}

func agyToolCall(tc *agy.ToolCall) map[string]any {
	return map[string]any{
		"id":   tc.ID,
		"type": "function",
		"function": map[string]any{
			"name":      tc.Name,
			"arguments": string(tc.Input),
		},
	}
}

func agyAccountRoot(req Request) string {
	base := ""
	if v, ok := req.Creds.Extra["accountRoot"].(string); ok && v != "" {
		base = v
	}
	if base == "" {
		root := os.Getenv("AGY_ACP_DATA_DIR")
		if root == "" {
			root = filepath.Join(config.DataDir(), "agy-acp")
		}
		base = filepath.Join(root, sanitizeAGYID(req.ConnectionID))
	}
	return base
}

func sanitizeAGYID(id string) string {
	out := make([]rune, 0, len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}

// agyError maps an ACP failure onto an HTTP response the router understands.
func agyError(err error) (*Response, error) {
	if err == nil {
		return nil, nil
	}
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, agy.ErrAuth), errors.Is(err, agy.ErrNoCred):
		status = http.StatusUnauthorized
	case errors.Is(err, agy.ErrQuota):
		status = http.StatusTooManyRequests
	case errors.Is(err, agy.ErrPolicy):
		status = http.StatusForbidden
	case errors.Is(err, agy.ErrBinary):
		status = http.StatusServiceUnavailable
	case errors.Is(err, agy.ErrNoModel):
		status = http.StatusBadRequest
	case errors.Is(err, agy.ErrStall):
		status = http.StatusBadGateway
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": "agy-acp: " + err.Error(), "type": "api_error"}})
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return &Response{Status: status, Header: h, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

// seedAGYToolResults maps tool names to their prior results, so a multi-turn
// tool call gets the real data instead of an "awaiting result" placeholder.
func seedAGYToolResults(messages []agy.ChatMessage) map[string]string {
	byID := map[string]string{}
	byName := map[string]string{}
	for _, m := range messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var calls []struct {
				ID       string `json:"id"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(m.ToolCalls, &calls) == nil {
				for _, c := range calls {
					byID[c.ID] = c.Function.Name
				}
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			if name := byID[m.ToolCallID]; name != "" {
				byName[name] = chatContentText(m.Content)
			}
		}
	}
	return byName
}

func chatContentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var out strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				out.WriteString(b.Text)
			}
		}
		return out.String()
	}
	return ""
}
