package providerdata

// Thinking levels per model — the valid `model(level)` suffixes the dashboard
// picker and the opencode writer offer. Mirrors
// open-sse/providers/thinkingLevels.js; capability data (reasoning flag +
// thinkingFormat + thinkingCanDisable) comes from capabilities.go.

import (
	"regexp"
	"strings"
)

// Shared level sets, deduped against provider docs.
var (
	lvBase     = []string{"none", "low", "medium", "high"}
	lvOnOff    = []string{"none", "thinking"}
	lvOpenAI   = []string{"none", "minimal", "low", "medium", "high", "xhigh"}
	lvLevelMax = []string{"none", "low", "medium", "high", "max"}
	lvBudgetX  = []string{"none", "low", "medium", "high", "xhigh", "max"}
	lvGemini   = []string{"minimal", "low", "medium", "high"}
	lvHiMax    = []string{"none", "high", "max"}
)

// formatLevels maps a thinkingFormat to its selectable levels.
var formatLevels = map[string][]string{
	"openai":          lvOpenAI,
	"claude-adaptive": lvBudgetX,
	"claude-budget":   lvBudgetX,
	"gemini-level":    lvGemini,
	"gemini-budget":   lvBase,
	"zai":             lvOnOff,
	"qwen":            lvBase,
	"kimi":            lvLevelMax,
	"deepseek":        lvHiMax,
	"commandcode":     {"none", "low", "medium", "high", "xhigh", "max"},
	"minimax":         lvOnOff,
	"hunyuan":         lvBase,
	"step":            lvBase,
	"nous":            lvBase,
}

var (
	codexGPT56Levels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	// Opus/Sonnet 4.6 lack xhigh (Anthropic + Kiro docs).
	claudeNoXhigh = []string{"none", "low", "medium", "high", "max"}
)

type thinkingPattern struct {
	provider string // "" = any provider
	pattern  string
	levels   []string
}

// patternThinking is glob, first match wins — more precise than the format default.
var patternThinking = []thinkingPattern{
	// Gemini 4 (Argon) rejects "minimal" with 400; LOW is the floor.
	{pattern: "*gemini-4*", levels: []string{"low", "medium", "high"}},
	{pattern: "*claude*4.6*", levels: claudeNoXhigh},
	{pattern: "*claude*4-6*", levels: claudeNoXhigh},
	{provider: "codex", pattern: "*gpt-6*", levels: codexGPT56Levels},
	{provider: "codex", pattern: "*gpt-5.6-sol*", levels: append(append([]string{}, codexGPT56Levels...), "ultra")},
	{provider: "codex", pattern: "*gpt-5.6-terra*", levels: append(append([]string{}, codexGPT56Levels...), "ultra")},
	{provider: "codex", pattern: "*gpt-5.6-luna*", levels: codexGPT56Levels},
	{pattern: "*codex*", levels: []string{"low", "medium", "high", "xhigh"}}, // cannot disable
	// OpenCode Free MiMo takes the full OpenAI effort enum; keep ahead of the
	// unqualified *mimo*v2.6* row (which describes Xiaomi's own API).
	{provider: "opencode", pattern: "*mimo*v2.6*", levels: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}},
	{pattern: "*mimo*v2.6*", levels: []string{"none", "low", "medium", "high", "xhigh"}},
	{pattern: "*mimo*v2.5-pro*", levels: []string{"none", "low", "medium", "high", "xhigh"}},
	// DeepSeek v4.*. Provider-qualified codebuddy rows must stay above the
	// unqualified glob — first match wins.
	{provider: "codebuddy-cn", pattern: "deepseek-v4-pro", levels: []string{"low", "high", "xhigh"}},
	{provider: "codebuddy-cn", pattern: "deepseek-v4.1-flash", levels: []string{"low", "high", "max"}},
	{pattern: "*deepseek-v4.1-flash-free*", levels: []string{"low", "high", "max"}},
	{pattern: "*deepseek-v4.*", levels: []string{"none", "low", "medium", "high", "xhigh", "max"}},
	{provider: "codebuddy-cn", pattern: "glm-5.3", levels: []string{"low", "high", "max"}},
	{provider: "codebuddy-cn", pattern: "glm-5.3-flash", levels: []string{"low", "high", "max"}},
	{provider: "codebuddy-cn", pattern: "glm-5.2", levels: []string{"high", "xhigh"}},
	{provider: "codebuddy-cn", pattern: "kimi-k2.8-preview", levels: []string{"low", "medium", "high"}},
	{provider: "codebuddy-cn", pattern: "hy3", levels: []string{"low", "high"}},
	{provider: "codebuddy-cn", pattern: "hy4-preview", levels: []string{"high"}},
	{provider: "codebuddy-intl", pattern: "deepseek-v4*", levels: []string{"low", "high", "xhigh"}},
	{provider: "minimax-code", pattern: "MiniMax-M3.1*", levels: []string{"low", "medium", "high", "xhigh", "max"}},
	{provider: "minimax-code", pattern: "MiniMax-M3", levels: []string{"none", "high"}},
	{provider: "minimax-code-global", pattern: "MiniMax-M3.1*", levels: []string{"low", "medium", "high", "xhigh", "max"}},
	{provider: "minimax-code-global", pattern: "MiniMax-M3", levels: []string{"none", "high"}},
}

// OpenAI documents reasoning.effort "max" for GPT-5.6 / GPT-6.
var openAIMaxEffortModel = regexp.MustCompile(`(?i)(?:^|/)gpt-(?:5\.6|6)(?:[.-]|$)`)

// ThinkingLevels returns the selectable levels for a model, or nil when the
// model does not reason at all.
func ThinkingLevels(provider, model string) []string {
	if provider == "kiro" && !kiroHasEffortPath(model) {
		return nil
	}
	caps := CapabilitiesFor(provider, model)
	if !caps.Reasoning {
		return nil
	}
	baseID := stripLevelSuffix(model)
	levels := codexModelLevels(provider, baseID)
	if levels == nil {
		for _, p := range patternThinking {
			if (p.provider == "" || p.provider == provider) && Glob(p.pattern, model) {
				levels = p.levels
				break
			}
		}
	}
	if levels == nil && strings.HasPrefix(provider, "openai-compatible-") && openAIMaxEffortModel.MatchString(baseID) {
		levels = codexGPT56Levels
	}
	if levels == nil {
		levels = formatLevels[caps.ThinkingFormat]
	}
	if levels == nil {
		levels = lvBase
	}
	if !caps.ThinkingCanDisable {
		filtered := make([]string, 0, len(levels))
		for _, l := range levels {
			if l != "none" {
				filtered = append(filtered, l)
			}
		}
		levels = filtered
	}
	return levels
}

// codexModelLevels reads the per-model thinkingLevels the registry declares
// for the codex provider (responsesLite models carry their own set).
func codexModelLevels(provider, baseID string) []string {
	if provider != "codex" {
		return nil
	}
	e, ok := Get().ByID["codex"]
	if !ok {
		return nil
	}
	for i := range e.Models {
		if e.Models[i].ID != baseID {
			continue
		}
		if lv, ok := e.Models[i].Extra["thinkingLevels"].([]any); ok {
			out := make([]string, 0, len(lv))
			for _, v := range lv {
				if s, ok := v.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

// stripLevelSuffix removes a trailing "(level)" preset from a model id.
func stripLevelSuffix(model string) string {
	if i := strings.LastIndexByte(model, '('); i > 0 && strings.HasSuffix(model, ")") {
		return strings.TrimSpace(model[:i])
	}
	return model
}

var reClaudeVersion = regexp.MustCompile(`(?:^|[/.])claude(?:[/.][a-z]+)*[/.](\d+)(?:[/.](\d+))?(?:[/.]|$)`)

// kiroHasEffortPath mirrors resolveKiroEffortPath: Kiro accepts
// additionalModelRequestFields only for GPT 5.6 and Claude 4.6+.
func kiroHasEffortPath(model string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(model), "-", ".")
	if regexp.MustCompile(`(?:^|[/.])gpt[/.]5[/.]6(?:[/.]|$)`).MatchString(normalized) {
		return true
	}
	if !strings.Contains(normalized, "claude") {
		return false
	}
	m := reClaudeVersion.FindStringSubmatch(normalized)
	if m == nil {
		return false
	}
	major := atoi(m[1])
	minor, hasMinor := -1, false
	if m[2] != "" {
		minor, hasMinor = atoi(m[2]), true
	}
	dateSuffixMinor := hasMinor && minor >= 1000
	return !(major < 4 || (major == 4 && (!hasMinor || minor <= 5 || dateSuffixMinor)))
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
