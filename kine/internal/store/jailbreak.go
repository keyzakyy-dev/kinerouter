package store

// Persona / jailbreak presets: prompt documents injected ahead of (or after)
// a model's system prompt, with per-model and per-API-key bindings plus
// keyword and exclude rules. Stored in the kv table under one scope, mirroring
// 9router's schema, and resolved from one cached snapshot so the request path
// does no pattern compilation.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const jailbreakScope = "jailbreak"

const (
	jailbreakDocKey     = "doc"
	jailbreakDocPrefix  = "doc:"
	jailbreakModelPfx   = "model:"
	jailbreakExcludesK  = "excludes"
	jailbreakUsagePfx   = "usage:"
	jailbreakGithubKey  = "github"
	jailbreakSourcesKey = "sync:sources"
)

// JailbreakKeyword is one include or exclude rule. Mode is "", "partial" or
// "regex"; empty is whole-token word matching.
type JailbreakKeyword struct {
	Text string `json:"text"`
	Mode string `json:"mode,omitempty"`
}

const (
	JailbreakKeywordWord    = ""
	JailbreakKeywordPartial = "partial"
	JailbreakKeywordRegex   = "regex"
)

const (
	maxJailbreakKeywords     = 20
	maxJailbreakKeywordChars = 200
	jailbreakDelimiters      = "/-_:. "
)

// JailbreakPreset is one prompt document.
type JailbreakPreset struct {
	ID           string             `json:"id,omitempty"`
	Name         string             `json:"name,omitempty"`
	Prompt       string             `json:"prompt"`
	GeminiSafety bool               `json:"geminiSafety"`
	Source       string             `json:"source,omitempty"`
	Kind         string             `json:"kind,omitempty"`
	Keywords     []JailbreakKeyword `json:"keywords,omitempty"`
	Excludes     []JailbreakKeyword `json:"excludes,omitempty"`
	Position     string             `json:"position,omitempty"`
	Mode         string             `json:"mode,omitempty"`
	Order        int                `json:"order,omitempty"`
	SyncOrigin   string             `json:"syncOrigin,omitempty"`
	SyncPath     string             `json:"syncPath,omitempty"`
	CreatedAt    string             `json:"createdAt,omitempty"`
	UpdatedAt    string             `json:"updatedAt,omitempty"`
}

// JailbreakBinding arms a preset for a model, globally or for one API key.
type JailbreakBinding struct {
	Enabled  bool   `json:"enabled"`
	PresetID string `json:"presetId,omitempty"`
}

// JailbreakGithub is the credential and mirror used by import and sync.
type JailbreakGithub struct {
	Token   string `json:"token,omitempty"`
	APIBase string `json:"apiBase,omitempty"`
}

// JailbreakSyncSource is one repository followed by the sync worker.
type JailbreakSyncSource struct {
	Repo    string `json:"repo"`
	Ref     string `json:"ref,omitempty"`
	Path    string `json:"path,omitempty"`
	Enabled bool   `json:"enabled"`
}

// NormalizeJailbreakKeywords validates and lowercases the rules that are
// matched case-insensitively.
func NormalizeJailbreakKeywords(in []JailbreakKeyword) ([]JailbreakKeyword, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxJailbreakKeywords {
		return nil, fmt.Errorf("too many keywords: %d (max %d)", len(in), maxJailbreakKeywords)
	}
	out := make([]JailbreakKeyword, 0, len(in))
	seen := map[string]bool{}
	for _, kw := range in {
		text := strings.TrimSpace(kw.Text)
		if text == "" {
			return nil, fmt.Errorf("keyword text is empty")
		}
		if len(text) > maxJailbreakKeywordChars {
			return nil, fmt.Errorf("keyword %q is too long: %d chars (max %d)", text, len(text), maxJailbreakKeywordChars)
		}
		switch kw.Mode {
		case JailbreakKeywordWord, JailbreakKeywordPartial:
			text = strings.ToLower(text)
		case JailbreakKeywordRegex:
			if _, err := regexp.Compile("(?i)" + text); err != nil {
				return nil, fmt.Errorf("invalid regex keyword %q: %w", text, err)
			}
		default:
			return nil, fmt.Errorf("unknown keyword mode %q (want word, partial or regex)", kw.Mode)
		}
		key := kw.Mode + "\x00" + text
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, JailbreakKeyword{Text: text, Mode: kw.Mode})
	}
	return out, nil
}

// ---------------------------------------------------------------- storage

func (s *Store) jbGet(key string) (string, bool) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE scope=? AND key=?`, jailbreakScope, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	return v, err == nil
}

func (s *Store) jbPut(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv(scope,key,value) VALUES(?,?,?)
		ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value`, jailbreakScope, key, value)
	if err == nil {
		s.invalidateJailbreak()
	}
	return err
}

func (s *Store) jbDelete(key string) error {
	_, err := s.db.Exec(`DELETE FROM kv WHERE scope=? AND key=?`, jailbreakScope, key)
	if err == nil {
		s.invalidateJailbreak()
	}
	return err
}

// GetJailbreakPreset returns nil when the id is unknown.
func (s *Store) GetJailbreakPreset(id string) (*JailbreakPreset, error) {
	if id == "" {
		id = "default"
	}
	key := jailbreakDocPrefix + id
	if id == "default" {
		if _, ok := s.jbGet(jailbreakDocKey); ok {
			key = jailbreakDocKey
		}
	}
	raw, ok := s.jbGet(key)
	if !ok {
		return nil, nil
	}
	var d JailbreakPreset
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("parse jailbreak preset %s: %w", id, err)
	}
	d.ID = id
	if d.Name == "" {
		d.Name = id
	}
	return &d, nil
}

// SaveJailbreakPreset creates or replaces one preset.
func (s *Store) SaveJailbreakPreset(d *JailbreakPreset) error {
	if d == nil {
		return fmt.Errorf("nil jailbreak preset")
	}
	if d.ID == "" {
		d.ID = "default"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if d.CreatedAt == "" {
		if existing, err := s.GetJailbreakPreset(d.ID); err == nil && existing != nil {
			d.CreatedAt = existing.CreatedAt
		}
	}
	if d.CreatedAt == "" {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	if d.Name == "" {
		if d.Source != "" {
			d.Name = d.Source
		} else {
			d.Name = d.ID
		}
	}
	kws, err := NormalizeJailbreakKeywords(d.Keywords)
	if err != nil {
		return err
	}
	excl, err := NormalizeJailbreakKeywords(d.Excludes)
	if err != nil {
		return err
	}
	d.Keywords, d.Excludes = kws, excl
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	key := jailbreakDocPrefix + d.ID
	if d.ID == "default" {
		key = jailbreakDocKey
	}
	return s.jbPut(key, string(raw))
}

// ListJailbreakPresets returns every preset in display order.
func (s *Store) ListJailbreakPresets() ([]*JailbreakPreset, error) {
	rows, err := s.db.Query(`SELECT key, value FROM kv WHERE scope=? AND (key LIKE ? OR key=?)`,
		jailbreakScope, jailbreakDocPrefix+"%", jailbreakDocKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*JailbreakPreset{}
	seenDefault := false
	for rows.Next() {
		var k, raw string
		if rows.Scan(&k, &raw) != nil {
			continue
		}
		var d JailbreakPreset
		if json.Unmarshal([]byte(raw), &d) != nil {
			continue
		}
		if k == jailbreakDocKey || k == jailbreakDocPrefix+"default" {
			if seenDefault {
				continue
			}
			seenDefault = true
			d.ID = "default"
		} else {
			d.ID = strings.TrimPrefix(k, jailbreakDocPrefix)
		}
		if d.Name == "" {
			d.Name = d.ID
		}
		out = append(out, &d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			if out[i].Order == 0 {
				return false
			}
			if out[j].Order == 0 {
				return true
			}
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out, rows.Err()
}

// DeleteJailbreakPreset removes a preset and unbinds every model armed to it.
func (s *Store) DeleteJailbreakPreset(id string) error {
	if id == "" {
		return nil
	}
	keys := []string{jailbreakDocPrefix + id}
	if id == "default" {
		keys = append(keys, jailbreakDocKey)
	}
	for _, k := range keys {
		if err := s.jbDelete(k); err != nil {
			return err
		}
	}
	bindings, err := s.ListJailbreakBindings()
	if err != nil {
		return err
	}
	for bindingID, cfg := range bindings {
		if cfg.PresetID == id {
			if err := s.jbDelete(jailbreakModelPfx + bindingID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReorderJailbreakPresets stores the operator's arrangement.
func (s *Store) ReorderJailbreakPresets(order []string) error {
	for i, id := range order {
		if id == "" {
			continue
		}
		d, err := s.GetJailbreakPreset(id)
		if err != nil || d == nil {
			continue
		}
		d.Order = i + 1
		if err := s.SaveJailbreakPreset(d); err != nil {
			return err
		}
	}
	return nil
}

// SetJailbreakBinding arms one model (or one key's model when bindingID is
// "<keyID>:<model>") to a preset.
func (s *Store) SetJailbreakBinding(bindingID, presetID string, enabled bool) error {
	if bindingID == "" {
		return fmt.Errorf("empty model id")
	}
	if presetID == "" || presetID == "off" || presetID == "none" {
		presetID = ""
	}
	raw, err := json.Marshal(JailbreakBinding{Enabled: enabled, PresetID: presetID})
	if err != nil {
		return err
	}
	return s.jbPut(jailbreakModelPfx+bindingID, string(raw))
}

// DeleteJailbreakBinding drops one binding row.
func (s *Store) DeleteJailbreakBinding(bindingID string) error {
	if bindingID == "" {
		return fmt.Errorf("empty model id")
	}
	return s.jbDelete(jailbreakModelPfx + bindingID)
}

// ListJailbreakBindings returns every binding, keyed by binding id.
func (s *Store) ListJailbreakBindings() (map[string]JailbreakBinding, error) {
	rows, err := s.db.Query(`SELECT key, value FROM kv WHERE scope=? AND key LIKE ?`,
		jailbreakScope, jailbreakModelPfx+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]JailbreakBinding{}
	for rows.Next() {
		var k, raw string
		if rows.Scan(&k, &raw) != nil {
			continue
		}
		var cfg JailbreakBinding
		if json.Unmarshal([]byte(raw), &cfg) != nil {
			continue
		}
		if cfg.Enabled && cfg.PresetID == "" {
			cfg.PresetID = "default"
		}
		out[strings.TrimPrefix(k, jailbreakModelPfx)] = cfg
	}
	return out, rows.Err()
}

// GetJailbreakExcludes returns the global deny list.
func (s *Store) GetJailbreakExcludes() ([]JailbreakKeyword, error) {
	raw, ok := s.jbGet(jailbreakExcludesK)
	if !ok {
		return []JailbreakKeyword{}, nil
	}
	var out []JailbreakKeyword
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []JailbreakKeyword{}
	}
	return out, nil
}

// SaveJailbreakExcludes replaces the global deny list.
func (s *Store) SaveJailbreakExcludes(in []JailbreakKeyword) error {
	norm, err := NormalizeJailbreakKeywords(in)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(norm)
	return s.jbPut(jailbreakExcludesK, string(raw))
}

// RecordJailbreakUsage bumps one preset's injection counter, best-effort.
func (s *Store) RecordJailbreakUsage(presetID string) {
	if presetID == "" {
		return
	}
	key := jailbreakUsagePfx + presetID
	var n int64
	if raw, ok := s.jbGet(key); ok {
		n, _ = strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	}
	_, _ = s.db.Exec(`INSERT INTO kv(scope,key,value) VALUES(?,?,?)
		ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value`, jailbreakScope, key, strconv.FormatInt(n+1, 10))
}

// JailbreakUsage returns the counters that exist.
func (s *Store) JailbreakUsage() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT key, value FROM kv WHERE scope=? AND key LIKE ?`,
		jailbreakScope, jailbreakUsagePfx+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k, raw string
		if rows.Scan(&k, &raw) != nil {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		out[strings.TrimPrefix(k, jailbreakUsagePfx)] = n
	}
	return out, rows.Err()
}

// GetJailbreakGithub returns the stored GitHub credential/mirror.
func (s *Store) GetJailbreakGithub() (JailbreakGithub, error) {
	var g JailbreakGithub
	raw, ok := s.jbGet(jailbreakGithubKey)
	if !ok {
		return g, nil
	}
	return g, json.Unmarshal([]byte(raw), &g)
}

// SaveJailbreakGithub replaces the credential/mirror.
func (s *Store) SaveJailbreakGithub(g JailbreakGithub) error {
	raw, _ := json.Marshal(g)
	return s.jbPut(jailbreakGithubKey, string(raw))
}

// GetJailbreakSources returns the sync subscriptions.
func (s *Store) GetJailbreakSources() ([]JailbreakSyncSource, error) {
	raw, ok := s.jbGet(jailbreakSourcesKey)
	if !ok {
		return []JailbreakSyncSource{}, nil
	}
	var out []JailbreakSyncSource
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []JailbreakSyncSource{}
	}
	return out, nil
}

// SaveJailbreakSources replaces the sync subscriptions.
func (s *Store) SaveJailbreakSources(in []JailbreakSyncSource) error {
	raw, _ := json.Marshal(in)
	return s.jbPut(jailbreakSourcesKey, string(raw))
}

// ---------------------------------------------------------------- resolver

type jailbreakRule struct {
	presetID string
	text     string
	mode     string
	re       *regexp.Regexp
}

func newJailbreakRule(presetID string, kw JailbreakKeyword) jailbreakRule {
	rule := jailbreakRule{presetID: presetID, text: kw.Text, mode: kw.Mode}
	if kw.Mode == JailbreakKeywordRegex {
		rule.re, _ = regexp.Compile("(?i)" + kw.Text)
	}
	return rule
}

func (rule jailbreakRule) matches(id string) bool {
	if id == "" || rule.text == "" {
		return false
	}
	switch rule.mode {
	case JailbreakKeywordPartial:
		return strings.Contains(strings.ToLower(id), rule.text)
	case JailbreakKeywordRegex:
		return rule.re != nil && rule.re.MatchString(id)
	default:
		return jailbreakWordMatch(id, rule.text)
	}
}

func jailbreakWordMatch(id, kw string) bool {
	lower := strings.ToLower(id)
	kw = strings.ToLower(kw)
	from := 0
	for from <= len(lower)-len(kw) {
		i := strings.Index(lower[from:], kw)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(kw)
		if jailbreakBoundary(lower, start, end) {
			return true
		}
		from = start + 1
	}
	return false
}

func jailbreakBoundary(s string, start, end int) bool {
	if start > 0 && !strings.ContainsRune(jailbreakDelimiters, rune(s[start-1])) {
		return false
	}
	if end < len(s) && !strings.ContainsRune(jailbreakDelimiters, rune(s[end])) {
		return false
	}
	return true
}

type jailbreakSnapshot struct {
	presets        map[string]*JailbreakPreset
	exact          map[string]JailbreakBinding
	keyExact       map[string]map[string]JailbreakBinding
	rules          []jailbreakRule
	excludes       []jailbreakRule
	presetExcludes map[string][]jailbreakRule
}

var (
	jbMu     sync.RWMutex
	jbCached *jailbreakSnapshot
)

func (s *Store) invalidateJailbreak() {
	jbMu.Lock()
	jbCached = nil
	jbMu.Unlock()
}

// jailbreakRules builds (once after each write) the immutable ruleset the
// request path reads.
func (s *Store) jailbreakRules() *jailbreakSnapshot {
	jbMu.RLock()
	cached := jbCached
	jbMu.RUnlock()
	if cached != nil {
		return cached
	}
	snap := &jailbreakSnapshot{
		presets:        map[string]*JailbreakPreset{},
		exact:          map[string]JailbreakBinding{},
		keyExact:       map[string]map[string]JailbreakBinding{},
		presetExcludes: map[string][]jailbreakRule{},
	}
	presets, err := s.ListJailbreakPresets()
	if err == nil {
		for _, p := range presets {
			snap.presets[p.ID] = p
			for _, kw := range p.Keywords {
				snap.rules = append(snap.rules, newJailbreakRule(p.ID, kw))
			}
			for _, kw := range p.Excludes {
				snap.presetExcludes[p.ID] = append(snap.presetExcludes[p.ID], newJailbreakRule(p.ID, kw))
			}
		}
	}
	bindings, _ := s.ListJailbreakBindings()
	for id, cfg := range bindings {
		if i := strings.IndexByte(id, ':'); i > 0 {
			keyID, model := id[:i], id[i+1:]
			if snap.keyExact[keyID] == nil {
				snap.keyExact[keyID] = map[string]JailbreakBinding{}
			}
			snap.keyExact[keyID][model] = cfg
			continue
		}
		snap.exact[id] = cfg
	}
	globalExcl, _ := s.GetJailbreakExcludes()
	for _, kw := range globalExcl {
		snap.excludes = append(snap.excludes, newJailbreakRule("", kw))
	}
	// Include rules are ordered by preset id so a length tie resolves to the
	// lowest id, never to map iteration order.
	sort.SliceStable(snap.rules, func(i, j int) bool {
		if snap.rules[i].presetID != snap.rules[j].presetID {
			return snap.rules[i].presetID < snap.rules[j].presetID
		}
		return snap.rules[i].text < snap.rules[j].text
	})

	jbMu.Lock()
	if jbCached == nil {
		jbCached = snap
	}
	cached = jbCached
	jbMu.Unlock()
	return cached
}

// JailbreakMatch explains one resolution for the dashboard.
type JailbreakMatch struct {
	Injected   bool   `json:"injected"`
	PresetID   string `json:"presetId,omitempty"`
	PresetName string `json:"presetName,omitempty"`
	Reason     string `json:"reason"`
}

// ResolveJailbreak returns the preset to inject for the candidate ids, in
// specificity order (requested id first, then forwarded leg ids).
func (s *Store) ResolveJailbreak(apiKeyID string, ids ...string) (match JailbreakMatch, preset *JailbreakPreset) {
	snap := s.jailbreakRules()

	if presetID, ok := snap.excluded("", ids); ok {
		return JailbreakMatch{Reason: fmt.Sprintf("excluded by the global rule %q", presetID)}, nil
	}

	if apiKeyID != "" {
		if cfgs := snap.keyExact[apiKeyID]; cfgs != nil {
			for _, id := range ids {
				cfg, ok := cfgs[id]
				if !ok || !cfg.Enabled {
					continue
				}
				presetID := cfg.PresetID
				if presetID == "" {
					presetID = "default"
				}
				doc, ok := snap.presets[presetID]
				if !ok {
					continue
				}
				if rule, denied := snap.excluded(presetID, ids); denied {
					return JailbreakMatch{Reason: fmt.Sprintf("preset %q is excluded by the rule %q", presetID, rule)}, nil
				}
				return JailbreakMatch{Injected: doc.Prompt != "", PresetID: doc.ID, PresetName: doc.Name,
					Reason: fmt.Sprintf("exact binding for API key %s model %q", apiKeyID, id)}, doc
			}
		}
	}

	for _, id := range ids {
		cfg, ok := snap.exact[id]
		if !ok || !cfg.Enabled {
			continue
		}
		presetID := cfg.PresetID
		if presetID == "" {
			presetID = "default"
		}
		doc, ok := snap.presets[presetID]
		if !ok {
			continue
		}
		if rule, denied := snap.excluded(presetID, ids); denied {
			return JailbreakMatch{Reason: fmt.Sprintf("preset %q is excluded by the rule %q", presetID, rule)}, nil
		}
		return JailbreakMatch{Injected: doc.Prompt != "", PresetID: doc.ID, PresetName: doc.Name,
			Reason: fmt.Sprintf("exact binding for model %q", id)}, doc
	}

	best := -1
	bestID := ""
	for _, id := range ids {
		if id == "" {
			continue
		}
		for i := range snap.rules {
			if !snap.rules[i].matches(id) {
				continue
			}
			if best < 0 || len(snap.rules[i].text) > len(snap.rules[best].text) {
				best, bestID = i, id
			}
		}
		if best >= 0 {
			break
		}
	}
	if best < 0 {
		return JailbreakMatch{Reason: "no exact binding, keyword or combo arm matched"}, nil
	}
	presetID := snap.rules[best].presetID
	if rule, denied := snap.excluded(presetID, ids); denied {
		return JailbreakMatch{Reason: fmt.Sprintf("preset %q is excluded by the rule %q", presetID, rule)}, nil
	}
	doc := snap.presets[presetID]
	if doc == nil || doc.Prompt == "" {
		return JailbreakMatch{Reason: fmt.Sprintf("keyword %q matched %q, but that preset has no prompt", snap.rules[best].text, bestID)}, nil
	}
	return JailbreakMatch{Injected: true, PresetID: doc.ID, PresetName: doc.Name,
		Reason: fmt.Sprintf("keyword %q (mode %s) matched %q", snap.rules[best].text, snap.rules[best].mode, bestID)}, doc
}

// excluded reports the deny rule that blocks this request, if any: the global
// list always, plus the winning preset's own list.
func (snap *jailbreakSnapshot) excluded(presetID string, ids []string) (string, bool) {
	for _, id := range ids {
		if id == "" {
			continue
		}
		for _, rule := range snap.excludes {
			if rule.matches(id) {
				return rule.text, true
			}
		}
		if presetID == "" {
			continue
		}
		for _, rule := range snap.presetExcludes[presetID] {
			if rule.matches(id) {
				return rule.text, true
			}
		}
	}
	return "", false
}
