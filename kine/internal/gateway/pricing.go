package gateway

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

//go:embed pricingdefaults.json
var pricingDefaultsJSON []byte

// defaultPricing decodes the JS table once at first use.
var defaultPricing = sync.OnceValue(func() map[string]map[string]priceEntry {
	m := map[string]map[string]priceEntry{}
	if json.Unmarshal(pricingDefaultsJSON, &m) != nil {
		return map[string]map[string]priceEntry{}
	}
	return m
})

// pricingTable resolves per-model $/1M-token prices: built-in defaults from
// open-sse/providers/pricing.js, overridden field-by-field by the kv scope
// "pricing" (written by the dashboard).
//
// ponytail: no 5s cache TTL like pricingRepo.js; loadPricing runs per gateway
// start and after a PATCH, add caching if kv reads ever show up hot.
type pricingTable struct {
	entries map[string]priceEntry                    // "provider/model" plus bare "model" aliases
	user    map[string]map[string]map[string]float64 // kv key → model → only fields the user set
}

type priceEntry struct {
	Input         float64 `json:"input"`
	Output        float64 `json:"output"`
	Cached        float64 `json:"cached"`
	Reasoning     float64 `json:"reasoning"`
	CacheCreation float64 `json:"cache_creation"`
}

func loadPricing(st *store.Store) *pricingTable {
	pt := &pricingTable{user: map[string]map[string]map[string]float64{}}
	if raw, err := st.GetKV(store.ScopePricing); err == nil {
		for key, val := range raw {
			var models map[string]map[string]float64
			if json.Unmarshal([]byte(val), &models) == nil {
				pt.user[key] = models
			}
		}
	}
	pt.entries = map[string]priceEntry{}
	for provider, models := range pt.merged() {
		for model, e := range models {
			pt.entries[provider+"/"+model] = e
			if _, ok := pt.entries[model]; !ok {
				pt.entries[model] = e
			}
		}
	}
	return pt
}

// merged returns defaults deep-merged with user overrides — the shape
// GET /api/pricing returns. Only fields present in a user entry replace the
// default ones, matching the JS spread in pricingRepo.getPricing.
func (p *pricingTable) merged() map[string]map[string]priceEntry {
	def := defaultPricing()
	out := make(map[string]map[string]priceEntry, len(def)+len(p.user))
	for provider, models := range def {
		m := make(map[string]priceEntry, len(models))
		for model, e := range models {
			m[model] = e
		}
		out[provider] = m
	}
	for provider, models := range p.user {
		if out[provider] == nil {
			out[provider] = map[string]priceEntry{}
		}
		for model, fields := range models {
			e := out[provider][model]
			applyFields(&e, fields)
			out[provider][model] = e
		}
	}
	return out
}

func applyFields(e *priceEntry, fields map[string]float64) {
	if v, ok := fields["input"]; ok {
		e.Input = v
	}
	if v, ok := fields["output"]; ok {
		e.Output = v
	}
	if v, ok := fields["cached"]; ok {
		e.Cached = v
	}
	if v, ok := fields["reasoning"]; ok {
		e.Reasoning = v
	}
	if v, ok := fields["cache_creation"]; ok {
		e.CacheCreation = v
	}
}

// Cost returns dollars for the given token counts. Unknown models cost 0.
func (p *pricingTable) Cost(provider, model string, promptTokens, completionTokens int64) float64 {
	e, ok := p.entries[provider+"/"+model]
	if !ok {
		e, ok = p.entries[model]
	}
	if !ok {
		return 0
	}
	return float64(promptTokens)/1e6*e.Input + float64(completionTokens)/1e6*e.Output
}

// userOnly returns just the kv overrides (getUserPricing, which POST/PATCH and
// DELETE /api/pricing return).
func (p *pricingTable) userOnly() map[string]map[string]priceEntry {
	out := make(map[string]map[string]priceEntry, len(p.user))
	for provider, models := range p.user {
		m := make(map[string]priceEntry, len(models))
		for model, fields := range models {
			var e priceEntry
			applyFields(&e, fields)
			m[model] = e
		}
		out[provider] = m
	}
	return out
}

// jsonUnmarshalStr decodes a stored kv blob.
func jsonUnmarshalStr(raw string, v any) error { return json.Unmarshal([]byte(raw), v) }

// stringifyAny encodes a value for kv storage.
func stringifyAny(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
