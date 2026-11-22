package gateway

// Smart combo routing ("strategy: smart") and the sticky-model store it keeps
// for prompt-cache retention. Ports open-sse/services/systemoneRouter.js and
// the smart branch of open-sse/services/combo.js handleComboChat.
//
// Fail-open everywhere: a router that times out, errors, or answers unknown
// models returns nil and the combo keeps its configured order.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	smartDefaultRouterModel = "oc/jev-1.13-free"
	smartTimeout            = 8 * time.Second
	smartMaxStateChars      = 2000
	smartMaxStickySessions  = 5000
	smartStickyTTL          = 2 * time.Hour
)

type smartRoute struct {
	Choice        string
	Confidence    float64
	HasConfidence bool
	Probabilities map[string]float64
}

// ---------------------------------------------------------------- prompt state

// extractPromptState returns the latest user turn, capped at maxChars.
func extractPromptState(body map[string]any) string {
	items, _ := body["messages"].([]any)
	if items == nil {
		items, _ = body["input"].([]any)
	}
	if len(items) > 0 {
		for i := len(items) - 1; i >= 0; i-- {
			m := objMap(items[i])
			if m == nil {
				continue
			}
			role := str(m, "role")
			if role == "user" || str(m, "type") == "message" || (role == "" && m["content"] != nil) {
				if t := strings.TrimSpace(contentText(firstNonNil(m["content"], m["text"]))); t != "" {
					return capChars(t, smartMaxStateChars)
				}
			}
		}
		if t := strings.TrimSpace(contentText(firstNonNil(objMap(items[len(items)-1])["content"], objMap(items[len(items)-1])["text"]))); t != "" {
			return capChars(t, smartMaxStateChars)
		}
	}
	if p, ok := body["prompt"].(string); ok && strings.TrimSpace(p) != "" {
		return capChars(strings.TrimSpace(p), smartMaxStateChars)
	}
	if p, ok := body["input"].(string); ok && strings.TrimSpace(p) != "" {
		return capChars(strings.TrimSpace(p), smartMaxStateChars)
	}
	return ""
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func capChars(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Trim on a rune boundary so the prompt never ends mid-codepoint.
	for max > 0 && !utf8Start(s[max]) {
		max--
	}
	return s[:max]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// ---------------------------------------------------------------- answer parse

// parseSmartRoute reads `answers.route` from a System One payload.
func parseSmartRoute(jsonBody []byte) *smartRoute {
	var m map[string]any
	if json.Unmarshal(jsonBody, &m) != nil {
		return nil
	}
	route := objMap(objMap(m["answers"])["route"])
	if route == nil {
		return nil
	}
	choice := strings.TrimSpace(str(route, "choice"))
	if choice == "" {
		return nil
	}
	r := &smartRoute{Choice: choice}
	if f, ok := route["confidence"].(float64); ok {
		r.Confidence, r.HasConfidence = f, true
	}
	if pm := objMap(route["probabilities"]); pm != nil {
		r.Probabilities = map[string]float64{}
		for k, v := range pm {
			switch n := v.(type) {
			case float64:
				r.Probabilities[k] = n
			case json.Number:
				if f, err := n.Float64(); err == nil {
					r.Probabilities[k] = f
				}
			}
		}
	}
	return r
}

// orderModelsByChoice puts the chosen model first and the rest by probability.
func orderModelsByChoice(models []string, r *smartRoute) []string {
	out := append([]string{}, models...)
	if r == nil || r.Choice == "" || len(models) <= 1 {
		return out
	}
	found := false
	for _, m := range models {
		if m == r.Choice {
			found = true
			break
		}
	}
	if !found {
		return out
	}
	others := make([]string, 0, len(models)-1)
	for _, m := range models {
		if m != r.Choice {
			others = append(others, m)
		}
	}
	if len(r.Probabilities) > 0 {
		sort.SliceStable(others, func(a, b int) bool {
			return r.Probabilities[others[a]] > r.Probabilities[others[b]]
		})
	}
	return append([]string{r.Choice}, others...)
}

// ---------------------------------------------------------------- sticky store

type stickyEntry struct {
	model    string
	lastUsed time.Time
}

var stickySessions = struct {
	sync.Mutex
	m map[string]*stickyEntry
}{m: map[string]*stickyEntry{}}

func stickyKey(combo, session string) string { return combo + ":" + session }

func getStickyComboModel(combo, session string, models []string) string {
	if combo == "" || session == "" {
		return ""
	}
	stickySessions.Lock()
	defer stickySessions.Unlock()
	key := stickyKey(combo, session)
	e := stickySessions.m[key]
	if e == nil {
		return ""
	}
	if time.Since(e.lastUsed) > smartStickyTTL {
		delete(stickySessions.m, key)
		return ""
	}
	for _, m := range models {
		if m == e.model {
			e.lastUsed = time.Now()
			return e.model
		}
	}
	delete(stickySessions.m, key)
	return ""
}

func setStickyComboModel(combo, session, model string) {
	if combo == "" || session == "" || model == "" {
		return
	}
	stickySessions.Lock()
	defer stickySessions.Unlock()
	pruneStickySessions()
	stickySessions.m[stickyKey(combo, session)] = &stickyEntry{model: model, lastUsed: time.Now()}
}

func clearStickyComboSession(combo, session string) {
	stickySessions.Lock()
	defer stickySessions.Unlock()
	if combo == "" {
		stickySessions.m = map[string]*stickyEntry{}
		return
	}
	if session != "" {
		delete(stickySessions.m, stickyKey(combo, session))
		return
	}
	prefix := combo + ":"
	for k := range stickySessions.m {
		if strings.HasPrefix(k, prefix) {
			delete(stickySessions.m, k)
		}
	}
}

func resetStickyComboStore() {
	stickySessions.Lock()
	stickySessions.m = map[string]*stickyEntry{}
	stickySessions.Unlock()
}

// pruneStickySessions drops expired entries, then the oldest quarter.
func pruneStickySessions() {
	if len(stickySessions.m) < smartMaxStickySessions {
		return
	}
	now := time.Now()
	for k, v := range stickySessions.m {
		if now.Sub(v.lastUsed) > smartStickyTTL {
			delete(stickySessions.m, k)
		}
	}
	if len(stickySessions.m) < smartMaxStickySessions {
		return
	}
	type kv struct {
		k string
		t time.Time
	}
	all := make([]kv, 0, len(stickySessions.m))
	for k, v := range stickySessions.m {
		all = append(all, kv{k, v.lastUsed})
	}
	sort.Slice(all, func(a, b int) bool { return all[a].t.Before(all[b].t) })
	for i := 0; i < len(all)/4; i++ {
		delete(stickySessions.m, all[i].k)
	}
}

// ---------------------------------------------------------------- resolver

// resolveSmartChoice asks the System One router which combo member fits the
// request. Never returns an error: nil means "keep the configured order".
func (g *Gateway) resolveSmartChoice(ctx context.Context, body map[string]any, models []string, routerModel string) *smartRoute {
	if len(models) <= 1 {
		return nil
	}
	target := strings.TrimSpace(routerModel)
	if target == "" {
		target = smartDefaultRouterModel
	}
	state := extractPromptState(body)
	if state == "" {
		return nil
	}
	criteria := map[string]any{}
	for _, m := range models {
		if strings.TrimSpace(m) != "" {
			criteria[strings.TrimSpace(m)] = nil
		}
	}
	if len(criteria) == 0 {
		return nil
	}
	targets, err := g.resolveTargets(target)
	if err != nil || len(targets) == 0 {
		return nil
	}
	t := targets[0]
	cfg := g.mediaConfig(t.Provider, "systemoneConfig")
	if cfg == nil {
		return nil
	}
	conns, err := g.store.ListConnections(t.Provider, true)
	if err != nil {
		return nil
	}
	tried := map[string]bool{}
	conn := g.pickConnection(conns, t.Model, tried)
	if conn == nil {
		return nil
	}
	if _, err := g.oauth.RefreshOne(ctx, g.store, conn, 2*time.Minute); err == nil {
		if fresh, err := g.store.GetConnection(conn.ID); err == nil {
			conn = fresh
		}
	}

	sysBody := map[string]any{
		"model": g.reg.UpstreamModelID(t.Provider, t.Model),
		"state": state,
		"questions": map[string]any{"route": map[string]any{
			"type":         "choice",
			"instructions": "Which of these configured models is the most appropriate for this request?",
			"criteria":     criteria,
		}},
	}
	rctx, cancel := context.WithTimeout(ctx, smartTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, "/", nil)
	if err != nil {
		return nil
	}
	status, out, _, err := g.systemoneOnce(req, conn, t, cfg, sysBody)
	if err != nil || status != http.StatusOK {
		return nil
	}
	return parseSmartRoute(out)
}

// ---------------------------------------------------------------- combo hook

// smartComboOrder applies the smart strategy to the member list.
func (g *Gateway) smartComboOrder(ctx context.Context, combo *store.Combo, cfg map[string]any, body map[string]any, sessionID string) []string {
	models := combo.Models
	policy := strings.ToLower(strAny(cfg["smartPolicy"]))
	if policy == "" {
		policy = "smart"
	}
	sessionSticky := true
	if v, ok := cfg["sessionSticky"].(bool); ok {
		sessionSticky = v
	}
	perTurn := policy == "per-turn" || !sessionSticky
	strict := policy == "strict"

	threshold := 0.8
	switch {
	case policy == "custom":
		if f, ok := cfg["confidenceThreshold"].(float64); ok && f > 0 {
			threshold = f
		}
	case policy == "smart-switch-70":
		threshold = 0.7
	case policy == "smart-switch-90":
		threshold = 0.9
	case strings.HasPrefix(policy, "smart-switch") || policy == "smart":
		if f, ok := cfg["confidenceThreshold"].(float64); ok && f > 0 {
			threshold = f
		}
	}

	withSticky := func(model string) []string {
		out := []string{model}
		for _, m := range models {
			if m != model {
				out = append(out, m)
			}
		}
		return out
	}

	active := ""
	if !perTurn && sessionID != "" {
		active = getStickyComboModel(combo.Name, sessionID, models)
	}
	routerModel := str(cfg, "routerModel")

	if active != "" {
		if strict {
			return withSticky(active)
		}
		// Smart switch: only leave the sticky model when the task clearly changed.
		choice := g.resolveSmartChoice(ctx, body, models, routerModel)
		if choice != nil && choice.Choice != active && choice.HasConfidence && choice.Confidence >= threshold && containsStr(models, choice.Choice) {
			setStickyComboModel(combo.Name, sessionID, choice.Choice)
			return orderModelsByChoice(models, choice)
		}
		return withSticky(active)
	}

	choice := g.resolveSmartChoice(ctx, body, models, routerModel)
	if choice == nil || !containsStr(models, choice.Choice) {
		return models
	}
	if choice.HasConfidence && choice.Confidence < threshold {
		return models
	}
	if !perTurn && sessionID != "" {
		setStickyComboModel(combo.Name, sessionID, choice.Choice)
	}
	return orderModelsByChoice(models, choice)
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// comboConfigMap returns a combo's per-combo strategy block from settings.
func (g *Gateway) comboConfigMap(name string) map[string]any {
	settings, err := g.store.GetSettings()
	if err != nil {
		return map[string]any{}
	}
	all, _ := settings["comboStrategies"].(map[string]any)
	if c := objMap(all[name]); c != nil {
		return c
	}
	return map[string]any{}
}
