package gateway

// Session identity + combo rotation. Ported from
// open-sse/utils/sessionManager.js and open-sse/services/combo.js
// (getRotatedModels). The session id keeps one conversation on one account so
// upstream prompt caches keep hitting; combo rotation gives round-robin combos
// a sticky budget before moving to the next member.
//
// In-memory only, like the JS (a restart drops the maps). A background sweeper
// drops expired entries so a long-lived process does not grow forever; lookups
// still check the TTL so a missed sweep never serves stale state.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const sessionTTL = 2 * time.Hour

var sessionHeaders = []string{"x-session-id", "session-id", "session_id", "x-amp-thread-id"}

type sessionEntry struct {
	value    string
	lastUsed time.Time
}

type sessionCache struct {
	mu sync.Mutex
	m  map[string]*sessionEntry
}

func (c *sessionCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return "", false
	}
	if time.Since(e.lastUsed) > sessionTTL {
		delete(c.m, key)
		return "", false
	}
	e.lastUsed = time.Now()
	return e.value, true
}

func (c *sessionCache) set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*sessionEntry{}
	}
	c.m[key] = &sessionEntry{value: value, lastUsed: time.Now()}
}

var sessions = &sessionCache{m: map[string]*sessionEntry{}}

func init() {
	go func() {
		t := time.NewTicker(sessionTTL / 4)
		defer t.Stop()
		for range t.C {
			sessions.sweep()
		}
	}()
}

// sweep drops entries idle past the TTL.
func (c *sessionCache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.m {
		if now.Sub(e.lastUsed) > sessionTTL {
			delete(c.m, k)
		}
	}
}

// resolveSessionID mirrors resolveSessionIdentity: client-provided first, then
// a hash of accumulated assistant text, then the connection's stable id.
func resolveSessionID(headers map[string][]string, body map[string]any, connectionID string) string {
	if s := clientSessionID(headers, body); s != "" {
		return s
	}
	if s := assistantTextSessionID(body); s != "" {
		return s
	}
	if connectionID == "" {
		return uuid.NewString() + itoa(time.Now().UnixMilli())
	}
	if v, ok := sessions.get("conn:" + connectionID); ok {
		return v
	}
	v := uuid.NewString() + itoa(time.Now().UnixMilli())
	sessions.set("conn:"+connectionID, v)
	return v
}

func clientSessionID(headers map[string][]string, body map[string]any) string {
	if md, ok := body["metadata"].(map[string]any); ok {
		if uid, _ := md["user_id"].(string); uid != "" {
			if i := strings.LastIndex(uid, "_session_"); i >= 0 {
				if v := normSessionID(uid[i+len("_session_"):]); v != "" {
					return "claude:" + v
				}
			}
			if strings.HasPrefix(uid, "{") {
				var parsed map[string]any
				if json.Unmarshal([]byte(uid), &parsed) == nil {
					if v := normSessionID(str(parsed, "session_id")); v != "" {
						return "claude:" + v
					}
				}
			}
		}
	}
	if h := headerValue(headers, "x-claude-code-session-id"); h != "" {
		return "claude:" + h
	}
	for _, k := range sessionHeaders {
		if v := headerValue(headers, k); v != "" {
			return v
		}
	}
	for _, k := range []string{"prompt_cache_key", "session_id", "conversation_id"} {
		if v := normSessionID(str(body, k)); v != "" {
			return v
		}
	}
	return ""
}

func headerValue(headers map[string][]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return normSessionID(v[0])
		}
	}
	return ""
}

func normSessionID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 256 {
		return ""
	}
	return v
}

// assistantTextSessionID keys on the first chunk of assistant text so a
// conversation is recognised even when the client sends no session header.
func assistantTextSessionID(body map[string]any) string {
	var text strings.Builder
	collect := func(items []any) {
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m == nil || str(m, "role") != "assistant" {
				continue
			}
			switch c := m["content"].(type) {
			case string:
				text.WriteString(c)
			case []any:
				for _, part := range c {
					pm, _ := part.(map[string]any)
					text.WriteString(str(pm, "text") + str(pm, "output"))
				}
			}
			if text.Len() >= 50 {
				return
			}
		}
	}
	if items, ok := body["messages"].([]any); ok {
		collect(items)
	} else if items, ok := body["input"].([]any); ok {
		collect(items)
	}
	if text.Len() < 50 {
		return ""
	}
	sum := sha256.Sum256([]byte(text.String()[:50]))
	key := "text:" + hex.EncodeToString(sum[:8])
	if v, ok := sessions.get(key); ok {
		return v
	}
	v := uuid.NewString() + itoa(time.Now().UnixMilli())
	sessions.set(key, v)
	return v
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ---------------------------------------------------------------- combo rotation

type rotationState struct {
	index    int
	useCount int
	lastUsed time.Time
}

var comboRotations = struct {
	sync.Mutex
	m map[string]*rotationState
}{m: map[string]*rotationState{}}

// rotatedMembers applies the combo's rotation strategy. Fallback (the default)
// keeps the stored order; round-robin rotates with a sticky budget.
func (g *Gateway) rotatedMembers(combo *store.Combo) []string {
	strategy, sticky := g.comboConfig(combo)
	return rotateMembers(combo.Name, combo.Models, strategy, sticky)
}

func rotateMembers(name string, models []string, strategy string, sticky int) []string {
	if strategy != "round-robin" || len(models) <= 1 {
		return models
	}
	comboRotations.Lock()
	defer comboRotations.Unlock()
	st := comboRotations.m[name]
	if st == nil {
		st = &rotationState{}
		comboRotations.m[name] = st
	}
	if time.Since(st.lastUsed) > sessionTTL {
		st.index, st.useCount = 0, 0
	}
	st.lastUsed = time.Now()
	idx := st.index % len(models)
	rotated := append(append([]string{}, models[idx:]...), models[:idx]...)
	if st.useCount+1 >= sticky {
		st.index = (idx + 1) % len(models)
		st.useCount = 0
	} else {
		st.useCount++
	}
	return rotated
}

// comboConfig resolves the per-combo override, then the global setting.
func (g *Gateway) comboConfig(combo *store.Combo) (strategy string, sticky int) {
	settings, err := g.store.GetSettings()
	if err != nil {
		return "fallback", 1
	}
	strategy, _ = settings["comboStrategy"].(string)
	sticky = int(num(settings["comboStickyRoundRobinLimit"]))
	if all, ok := settings["comboStrategies"].(map[string]any); ok {
		if c, ok := all[combo.Name].(map[string]any); ok {
			if s, ok := c["fallbackStrategy"].(string); ok && s != "" {
				strategy = s
			}
		}
	}
	if sticky <= 0 {
		sticky = 1
	}
	return strategy, sticky
}

// resetComboRotation drops rotation state when a combo changes.
func resetComboRotation(name string) {
	comboRotations.Lock()
	defer comboRotations.Unlock()
	if name == "" {
		comboRotations.m = map[string]*rotationState{}
		return
	}
	delete(comboRotations.m, name)
	clearStickyComboSession(name, "")
}
