package providerdata

// Model capabilities — mirrors open-sse/providers/capabilities.js. Resolution
// order (first match wins, merged over the default floor):
//   1. providers[provider][model]   provider-specific override
//   2. models[model]                canonical exact id
//   3. patterns[]                   glob, ordered specific → generic
//   4. default                      safe floor
// The synced models.dev catalog overlay (modalities on, limits replaced) is
// applied by the caller; this package stays file-free past its embed.
//
// capabilities.json beside this file is hand-maintained; edit and rebuild.

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed capabilities.json
var capsRaw []byte

// Capabilities is one resolved model capability set.
type Capabilities struct {
	Vision             bool
	PDF                bool
	AudioInput         bool
	VideoInput         bool
	Reasoning          bool
	ThinkingFormat     string // "" when the model has none
	ThinkingCanDisable bool
	ContextWindow      int64
	MaxOutput          int64
}

type capsPatch struct {
	Vision             *bool   `json:"vision"`
	PDF                *bool   `json:"pdf"`
	AudioInput         *bool   `json:"audioInput"`
	VideoInput         *bool   `json:"videoInput"`
	Reasoning          *bool   `json:"reasoning"`
	ThinkingFormat     *string `json:"thinkingFormat"`
	ThinkingCanDisable *bool   `json:"thinkingCanDisable"`
	ContextWindow      *int64  `json:"contextWindow"`
	MaxOutput          *int64  `json:"maxOutput"`
}

func (c *Capabilities) apply(p capsPatch) {
	if p.Vision != nil {
		c.Vision = *p.Vision
	}
	if p.PDF != nil {
		c.PDF = *p.PDF
	}
	if p.AudioInput != nil {
		c.AudioInput = *p.AudioInput
	}
	if p.VideoInput != nil {
		c.VideoInput = *p.VideoInput
	}
	if p.Reasoning != nil {
		c.Reasoning = *p.Reasoning
	}
	if p.ThinkingFormat != nil {
		c.ThinkingFormat = *p.ThinkingFormat
	}
	if p.ThinkingCanDisable != nil {
		c.ThinkingCanDisable = *p.ThinkingCanDisable
	}
	if p.ContextWindow != nil {
		c.ContextWindow = *p.ContextWindow
	}
	if p.MaxOutput != nil {
		c.MaxOutput = *p.MaxOutput
	}
}

var (
	capsOnce sync.Once
	capsData struct {
		Default   capsPatch                       `json:"default"`
		Models    map[string]capsPatch            `json:"models"`
		Providers map[string]map[string]capsPatch `json:"providers"`
		Patterns  []struct {
			Pattern string    `json:"pattern"`
			Caps    capsPatch `json:"caps"`
		} `json:"patterns"`
	}
)

func loadCapsData() {
	capsOnce.Do(func() {
		if err := json.Unmarshal(capsRaw, &capsData); err != nil {
			panic("providerdata: capabilities: " + err.Error())
		}
	})
}

func baseCapabilities() Capabilities {
	loadCapsData()
	c := Capabilities{ContextWindow: 200000, MaxOutput: 64000, ThinkingCanDisable: true}
	c.apply(capsData.Default)
	return c
}

// CapabilitiesFor resolves what a model can do. An empty model returns the floor.
func CapabilitiesFor(provider, model string) Capabilities {
	loadCapsData()
	if model == "" {
		return baseCapabilities()
	}
	base := model
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	// The CommandCode wire is one endpoint for every model; family patterns
	// would mis-describe it, so it resolves as a single family.
	if provider == "commandcode" || provider == "cmc" {
		if p, ok := capsData.Providers["commandcode"]; ok {
			if e, ok := p[model]; ok {
				c := baseCapabilities()
				c.apply(e)
				return c
			}
			if e, ok := p[base]; ok {
				c := baseCapabilities()
				c.apply(e)
				return c
			}
		}
		c := baseCapabilities()
		c.Reasoning = true
		c.ThinkingFormat = "commandcode"
		c.ContextWindow = 1000000
		c.MaxOutput = 384000
		return c
	}
	if provider != "" {
		if p, ok := capsData.Providers[provider]; ok {
			if e, ok := p[model]; ok {
				c := baseCapabilities()
				c.apply(e)
				return c
			}
			if e, ok := p[base]; ok {
				c := baseCapabilities()
				c.apply(e)
				return c
			}
		}
	}
	if e, ok := capsData.Models[base]; ok {
		c := baseCapabilities()
		c.apply(e)
		return c
	}
	if e, ok := capsData.Models[model]; ok {
		c := baseCapabilities()
		c.apply(e)
		return c
	}
	for _, p := range capsData.Patterns {
		if Glob(p.Pattern, base) || Glob(p.Pattern, model) {
			c := baseCapabilities()
			c.apply(p.Caps)
			return c
		}
	}
	return baseCapabilities()
}

// Glob matches a `*`-only wildcard pattern case-insensitively against s
// (mirrors matchPattern in open-sse/providers/pricing.js).
func Glob(pattern, s string) bool {
	pat := strings.ToLower(pattern)
	str := strings.ToLower(s)
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(str) {
		switch {
		case pi < len(pat) && (pat[pi] == str[si]):
			pi++
			si++
		case pi < len(pat) && pat[pi] == '*':
			star = pi
			mark = si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}
