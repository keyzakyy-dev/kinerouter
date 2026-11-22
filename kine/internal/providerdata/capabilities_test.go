package providerdata

import (
	"reflect"
	"testing"
)

// Self-check for the generated capability tables and the thinking-level
// resolver. Expected values are what open-sse/providers/thinkingLevels.js
// returns for the same provider+model pairs.
func TestThinkingLevels(t *testing.T) {
	cases := []struct {
		provider, model string
		want            []string
	}{
		{"openai", "gpt-5.5", []string{"none", "minimal", "low", "medium", "high", "xhigh"}},
		{"openai", "gpt-4o", nil},
		{"anthropic", "claude-opus-4.6", []string{"none", "low", "medium", "high", "max"}},
		{"anthropic", "claude-sonnet-4.5", []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{"codex", "gpt-6.1-sol", []string{"low", "medium", "high", "xhigh", "max"}},
		{"codex", "gpt-5.6-sol", []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}},
		{"gemini", "gemini-3-pro-preview", []string{"minimal", "low", "medium", "high"}},
		{"gemini", "gemini-4-pro", []string{"low", "medium", "high"}},
		{"kiro", "claude-sonnet-4.5", nil},
		{"kiro", "claude-opus-4.7", []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{"kimi", "kimi-k3", []string{"low", "medium", "high", "max"}},
		{"openai-compatible-x", "openai/gpt-5.6-sol", []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}},
		{"opencode", "mimo-v2.6-flash-free", []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}},
		{"minimax-code", "MiniMax-M3.1", []string{"low", "medium", "high", "xhigh", "max"}},
		{"codebuddy-cn", "deepseek-v4.1-flash", []string{"low", "high", "max"}},
		{"deepseek", "deepseek-chat", nil},
		{"commandcode", "deepseek/deepseek-v4-pro", []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{"anthropic", "claude-fable-5-2", []string{"low", "medium", "high", "xhigh", "max"}},
		{"zai", "glm-5", []string{"none", "thinking"}},
		// Suffixed preset resolves against the base id.
		{"anthropic", "claude-opus-4.6(high)", []string{"none", "low", "medium", "high", "max"}},
	}
	for _, c := range cases {
		if got := ThinkingLevels(c.provider, c.model); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ThinkingLevels(%q,%q) = %v, want %v", c.provider, c.model, got, c.want)
		}
	}
}

func TestCapabilitiesFor(t *testing.T) {
	g := CapabilitiesFor("openai", "gpt-5.5")
	if !g.Reasoning || g.ThinkingFormat != "openai" {
		t.Errorf("gpt-5.5 caps: %+v", g)
	}
	// CommandCode resolves as one family regardless of the model path.
	c := CapabilitiesFor("commandcode", "deepseek/deepseek-v4-pro")
	if !c.Reasoning || c.ThinkingFormat != "commandcode" || c.ContextWindow != 1000000 {
		t.Errorf("commandcode caps: %+v", c)
	}
	if f := CapabilitiesFor("", ""); f.ContextWindow != 200000 || f.MaxOutput != 64000 {
		t.Errorf("floor caps: %+v", f)
	}
}

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		pat, s string
		want   bool
	}{
		{"*gpt-5.5*", "OPENAI/gpt-5.5-mini", true},
		{"*gpt-5.5*", "gpt-5.6", false},
		{"*", "", true},
		{"gemini-4*", "gemini-4-pro", true},
		{"*v2.6*", "mimo-v2.6-flash", true},
	} {
		if got := Glob(c.pat, c.s); got != c.want {
			t.Errorf("Glob(%q,%q) = %v, want %v", c.pat, c.s, got, c.want)
		}
	}
}
