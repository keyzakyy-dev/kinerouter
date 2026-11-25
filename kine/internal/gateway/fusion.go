package gateway

// Fusion combos ("strategy: fusion"): fan the prompt out to every panel model
// in parallel, then a judge model synthesizes one final answer.
// Ports open-sse/services/combo.js handleFusionChat.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

type fusionTuning struct {
	minPanel    int
	grace       time.Duration
	hardTimeout time.Duration
}

var fusionDefaults = fusionTuning{minPanel: 2, grace: 8 * time.Second, hardTimeout: 90 * time.Second}

// captureWriter collects a member's response instead of sending it.
type captureWriter struct {
	header http.Header
	status int
	body   []byte
}

func (c *captureWriter) Header() http.Header {
	if c.header == nil {
		c.header = http.Header{}
	}
	return c.header
}
func (c *captureWriter) WriteHeader(code int) { c.status = code }
func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.body = append(c.body, b...)
	return len(b), nil
}
func (c *captureWriter) Flush() {}

// collectMember runs one combo member non-streaming and returns its body.
func (g *Gateway) collectMember(ctx context.Context, r *http.Request, member string, body map[string]any, apiKey, clientFormat string) (int, []byte) {
	targets, err := g.resolveTargets(member)
	if err != nil {
		return http.StatusBadRequest, nil
	}
	raw := cloneMap(body)
	cw := &captureWriter{}
	res := g.dispatch(cw, r.WithContext(ctx), targets, raw, apiKey, clientFormat, false, "")
	if !res.ok {
		return res.status, res.body
	}
	return cw.status, cw.body
}

// extractPanelText pulls assistant prose from any client-format response.
func extractPanelText(jsonBody []byte) string {
	var m map[string]any
	if len(jsonBody) == 0 {
		return ""
	}
	if err := json.Unmarshal(jsonBody, &m); err != nil {
		return ""
	}
	if choices, _ := m["choices"].([]any); len(choices) > 0 {
		ch := objMap(choices[0])
		msg := objMap(ch["message"])
		if msg == nil {
			msg = objMap(ch["delta"])
		}
		if t := strings.TrimSpace(contentText(msg["content"])); t != "" {
			return t
		}
		if t := strings.TrimSpace(strAny(ch["text"])); t != "" {
			return t
		}
	}
	if t := strings.TrimSpace(contentText(m["content"])); t != "" {
		return t
	}
	if cands, _ := m["candidates"].([]any); len(cands) > 0 {
		parts, _ := objMap(objMap(cands[0])["content"])["parts"].([]any)
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(strAny(objMap(p)["text"]))
		}
		if t := strings.TrimSpace(b.String()); t != "" {
			return t
		}
	}
	if out, _ := m["output"].([]any); len(out) > 0 {
		var b strings.Builder
		for _, o := range out {
			for _, c := range listOf(objMap(o)["content"]) {
				b.WriteString(strAny(objMap(c)["text"]))
			}
		}
		if t := strings.TrimSpace(b.String()); t != "" {
			return t
		}
	}
	return ""
}

const (
	toolCallPrefix   = "[Called tools: "
	toolResultPrefix = "[Tool result: "
)

// flattenToolHistory turns tool turns into prose so panel models keep context
// but cannot emit tool calls. Shared with the non-tool-model lane so both send
// the same shape.
func flattenToolHistory(items []any) []any {
	flat, _ := flattenToolTurns(items)
	return flat
}

// appendUserTurn adds a synthesized user turn in whichever array the body uses.
func appendUserTurn(body map[string]any, text string) map[string]any {
	next := cloneMap(body)
	userMsg := map[string]any{"role": "user", "content": text}
	switch {
	case body["messages"] != nil:
		next["messages"] = append(append([]any{}, listOf(body["messages"])...), userMsg)
	case body["input"] != nil:
		next["input"] = append(append([]any{}, listOf(body["input"])...), userMsg)
	case body["contents"] != nil:
		next["contents"] = append(append([]any{}, listOf(body["contents"])...),
			map[string]any{"role": "user", "parts": []any{map[string]any{"text": text}}})
	default:
		next["messages"] = []any{userMsg}
	}
	return next
}

// buildJudgePrompt is the judge directive: analyze the panel, then write one
// answer. Sources are anonymized so brand reputation does not bias the judge.
func buildJudgePrompt(answers []fusionAnswer) string {
	var b strings.Builder
	b.WriteString("You are the JUDGE in a model-fusion panel. ")
	b.WriteString(itoa(int64(len(answers))))
	b.WriteString(" expert models independently answered the user's most recent request. Their responses are below, anonymized by source.\n\n")
	b.WriteString("Do NOT mention that multiple models were used, and do NOT refer to the sources. Produce ONE authoritative final answer addressed directly to the user.\n\n")
	b.WriteString("First, internally analyze the panel along these dimensions: consensus (points most sources agree on — treat as higher-confidence), contradictions (where they disagree — resolve with your own judgment), partial coverage, unique insights only one source surfaced, and blind spots every source missed. Then write the best possible final answer grounded in that analysis — more complete and correct than any single response, with no filler.\n\n")
	b.WriteString("=== PANEL RESPONSES ===\n")
	for i, a := range answers {
		b.WriteString("[Source " + itoa(int64(i+1)) + "]\n" + a.text + "\n\n")
	}
	b.WriteString("=== END PANEL RESPONSES ===\n\n")
	b.WriteString("Now write the final answer to the user's original request.")
	return b.String()
}

type fusionAnswer struct {
	model string
	text  string
}

type panelResult struct {
	status int
	body   []byte
	count  int
}

// handleFusion runs a fusion combo: parallel panel, then judge.
func (g *Gateway) handleFusion(w http.ResponseWriter, r *http.Request, combo *store.Combo, raw map[string]any, apiKey, clientFormat string, stream bool) {
	cfgBlock := g.comboConfigMap(combo.Name)
	tuning := fusionDefaults
	if ft := objMap(cfgBlock["fusionTuning"]); ft != nil {
		if v := int(num(ft["minPanel"])); v > 0 {
			tuning.minPanel = v
		}
		if v := int(num(ft["stragglerGraceMs"])); v > 0 {
			tuning.grace = time.Duration(v) * time.Millisecond
		}
		if v := int(num(ft["panelHardTimeoutMs"])); v > 0 {
			tuning.hardTimeout = time.Duration(v) * time.Millisecond
		}
	}
	panel := make([]string, 0, len(combo.Models))
	for _, m := range combo.Models {
		if strings.TrimSpace(m) != "" {
			panel = append(panel, m)
		}
	}
	if len(panel) == 0 {
		writeErr(w, http.StatusBadRequest, "Fusion combo has no models")
		return
	}
	if len(panel) == 1 {
		g.dispatch(w, r, resolveOrNil(g, panel[0]), raw, apiKey, clientFormat, stream, resolveSessionID(r.Header, raw, ""))
		return
	}
	minPanel := tuning.minPanel
	if minPanel < 2 {
		minPanel = 2
	}
	if minPanel > len(panel) {
		minPanel = len(panel)
	}
	judge := firstNonEmpty(str(cfgBlock, "judgeModel"), panel[0])

	// Panel body: non-streaming, tools stripped, tool history flattened.
	panelBody := cloneMap(raw)
	for _, k := range []string{"tools", "tool_choice", "stream_options"} {
		delete(panelBody, k)
	}
	panelBody["stream"] = false
	if panelBody["messages"] != nil {
		panelBody["messages"] = flattenToolHistory(listOf(panelBody["messages"]))
	} else if panelBody["input"] != nil {
		panelBody["input"] = flattenToolHistory(listOf(panelBody["input"]))
	}

	results := g.runPanel(r, panel, panelBody, apiKey, clientFormat, minPanel, tuning)

	answers := make([]fusionAnswer, 0, len(results))
	for _, res := range results {
		if res == nil || res.status < 200 || res.status >= 300 {
			continue
		}
		if text := extractPanelText(res.body); text != "" {
			answers = append(answers, fusionAnswer{model: panel[res.count], text: text})
		}
	}
	if len(answers) == 0 {
		writeErr(w, http.StatusServiceUnavailable, "All fusion panel models failed")
		return
	}
	if len(answers) == 1 {
		g.dispatch(w, r, resolveOrNil(g, answers[0].model), raw, apiKey, clientFormat, stream, resolveSessionID(r.Header, raw, ""))
		return
	}
	judgeBody := appendUserTurn(raw, buildJudgePrompt(answers))
	g.dispatch(w, r, resolveOrNil(g, judge), judgeBody, apiKey, clientFormat, stream, resolveSessionID(r.Header, raw, ""))
}

func resolveOrNil(g *Gateway, model string) []target {
	targets, err := g.resolveTargets(model)
	if err != nil {
		return nil
	}
	return targets
}

// runPanel executes the panel with quorum-grace collection: once minPanel
// calls succeed it gives the stragglers a grace window, then returns whatever
// landed. The slice is indexed like panel (nil = dropped).
func (g *Gateway) runPanel(r *http.Request, panel []string, body map[string]any, apiKey, clientFormat string, minPanel int, tuning fusionTuning) []*panelResult {
	out := make([]*panelResult, len(panel))
	done := make(chan int, len(panel))
	// One context for the whole panel: when the grace window or the hard
	// timeout ends the wait, the stragglers' upstream calls are cancelled
	// instead of holding their sockets until their own timeout expires.
	panelCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var mu sync.Mutex
	go func() {
		var wg sync.WaitGroup
		for i, member := range panel {
			i, member := i, member
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, memberCancel := context.WithTimeout(panelCtx, tuning.hardTimeout)
				defer memberCancel()
				status, respBody := g.collectMember(ctx, r, member, body, apiKey, clientFormat)
				mu.Lock()
				out[i] = &panelResult{status: status, body: respBody, count: i}
				mu.Unlock()
				select {
				case done <- i:
				case <-panelCtx.Done():
				}
			}()
		}
		wg.Wait()
		close(done)
	}()

	deadline := time.NewTimer(tuning.hardTimeout)
	defer deadline.Stop()
	var graceTimer *time.Timer
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()
	var graceC <-chan time.Time
	okCount, settled := 0, 0
	for settled < len(panel) {
		select {
		case i, open := <-done:
			if !open {
				return out
			}
			settled++
			mu.Lock()
			if res := out[i]; res != nil && res.status >= 200 && res.status < 300 {
				okCount++
			}
			mu.Unlock()
			if settled == len(panel) {
				return out
			}
			if okCount >= minPanel && graceTimer == nil {
				graceTimer = time.NewTimer(tuning.grace)
				graceC = graceTimer.C
			}
		case <-graceC:
			return out
		case <-deadline.C:
			return out
		}
	}
	return out
}
