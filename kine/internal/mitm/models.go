package mitm

// Model alias resolution + chat-request detection for intercepted traffic.
// Mirrors src/mitm/config.js.

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// URLPatterns marks requests that are chat turns (vs passthrough).
var URLPatterns = map[string][]string{
	"antigravity": {":generateContent", ":streamGenerateContent"},
	"copilot":     {"/chat/completions", "/v1/messages", "/responses"},
	"kiro":        {"/generateAssistantResponse"},
	"cursor":      {"/BidiAppend", "/RunSSE", "/RunPoll", "/Run"},
}

// IsChatRequest reports whether this request should be intercepted.
func IsChatRequest(tool string, req *http.Request) bool {
	for _, p := range URLPatterns[tool] {
		if strings.Contains(req.URL.Path, p) || strings.Contains(req.URL.RawQuery, p) {
			return true
		}
	}
	if tool == "kiro" {
		return strings.Contains(req.Header.Get("x-amz-target"), "GenerateAssistantResponse")
	}
	return false
}

// modelSynonyms maps public Antigravity model names to the canonical alias key.
var modelSynonyms = map[string]map[string]string{
	"antigravity": {
		"gemini-default":             "gemini-3.8-flash-medium",
		"gemini-3.5-flash-high":      "gemini-3.8-flash-high",
		"gemini-3.5-flash-medium":    "gemini-3.8-flash-medium",
		"gemini-3.5-flash-low":       "gemini-3.8-flash-low",
		"gemini-3.5-flash-extra-low": "gemini-3.8-flash-low",
		"gemini-3.8-flash":           "gemini-3.8-flash-medium",
		"gemini-3.1-pro-high":        "gemini-pro-agent",
		"gemini-3-pro-high":          "gemini-pro-agent",
		"gemini-3-pro-low":           "gemini-3.1-pro-low",
		"claude-sonnet-5.5":          "claude-sonnet-5-5",
		"claude-sonnet-5.5-high":     "claude-sonnet-5-5-high",
		"claude-sonnet-5.5-medium":   "claude-sonnet-5-5-medium",
		"claude-sonnet-5.5-low":      "claude-sonnet-5-5-low",
		"claude-sonnet-5-5-thinking": "claude-sonnet-5-5",
		"claude-sonnet-5.5-thinking": "claude-sonnet-5-5",
		"claude-opus-5.5":            "claude-opus-5-5",
		"claude-opus-5.5-high":       "claude-opus-5-5-high",
		"claude-opus-5.5-medium":     "claude-opus-5-5-medium",
		"claude-opus-5.5-low":        "claude-opus-5-5-low",
		"claude-opus-5-5-thinking":   "claude-opus-5-5",
		"claude-opus-5.5-thinking":   "claude-opus-5-5",
		"claude-sonnet-4.6":          "claude-sonnet-4-6",
		"claude-opus-4.6":            "claude-opus-4-6-thinking",
		"claude-opus-4.6-thinking":   "claude-opus-4-6-thinking",
	},
}

type modelPattern struct {
	re    *regexp.Regexp
	alias string
}

// modelPatterns is the ordered regex fallback used when exact/prefix lookup fails.
var modelPatterns = map[string][]modelPattern{
	"antigravity": {
		{regexp.MustCompile(`(?i)flash.*extra.*low|extra.*low.*flash|flash.*low|low.*flash`), "gemini-3.8-flash-low"},
		{regexp.MustCompile(`(?i)flash.*medium|medium.*flash`), "gemini-3.8-flash-medium"},
		{regexp.MustCompile(`(?i)flash.*agent|agent.*flash|flash`), "gemini-3.8-flash-high"},
		{regexp.MustCompile(`(?i)pro.*low|low.*pro`), "gemini-3.1-pro-low"},
		{regexp.MustCompile(`(?i)gemini.*pro|pro.*gemini`), "gemini-pro-agent"},
		{regexp.MustCompile(`(?i)opus.*5[.-]5.*high|5[.-]5.*opus.*high`), "claude-opus-5-5-high"},
		{regexp.MustCompile(`(?i)opus.*5[.-]5.*medium|5[.-]5.*opus.*medium`), "claude-opus-5-5-medium"},
		{regexp.MustCompile(`(?i)opus.*5[.-]5.*low|5[.-]5.*opus.*low`), "claude-opus-5-5-low"},
		{regexp.MustCompile(`(?i)sonnet.*5[.-]5.*high|5[.-]5.*sonnet.*high`), "claude-sonnet-5-5-high"},
		{regexp.MustCompile(`(?i)sonnet.*5[.-]5.*medium|5[.-]5.*sonnet.*medium`), "claude-sonnet-5-5-medium"},
		{regexp.MustCompile(`(?i)sonnet.*5[.-]5.*low|5[.-]5.*sonnet.*low`), "claude-sonnet-5-5-low"},
		{regexp.MustCompile(`(?i)opus.*5[.-]5|5[.-]5.*opus`), "claude-opus-5-5"},
		{regexp.MustCompile(`(?i)sonnet.*5[.-]5|5[.-]5.*sonnet`), "claude-sonnet-5-5"},
		{regexp.MustCompile(`(?i)opus`), "claude-opus-4-6-thinking"},
		{regexp.MustCompile(`(?i)sonnet|claude`), "claude-sonnet-4-6"},
		{regexp.MustCompile(`(?i)gpt.*oss|oss`), "gpt-oss-120b-medium"},
	},
}

// modelNoMap: models that must NEVER be re-routed (latency-critical completion).
var modelNoMap = map[string][]*regexp.Regexp{
	"antigravity": {regexp.MustCompile(`(?i)^tab[_-]`)},
}

// GetMappedModel resolves a raw client model id to a router model via the
// per-tool alias map. Returns "" when nothing matches (=> passthrough).
func GetMappedModel(aliases AliasMap, tool, model string) string {
	if model == "" {
		return ""
	}
	toolAliases := aliases[tool]
	if len(toolAliases) == 0 {
		return ""
	}
	lookup := strings.TrimPrefix(model, "models/")
	if syn := modelSynonyms[tool][lookup]; syn != "" {
		lookup = syn
	}
	if v := toolAliases[lookup]; v != "" {
		return v
	}
	for k, v := range toolAliases {
		if k != "" && v != "" && (strings.HasPrefix(lookup, k) || strings.HasPrefix(k, lookup)) {
			return v
		}
	}
	for _, p := range modelPatterns[tool] {
		if p.re.MatchString(lookup) && toolAliases[p.alias] != "" {
			return toolAliases[p.alias]
		}
	}
	return ""
}

// NoMap reports whether model must be left on the real upstream.
func NoMap(tool, model string) bool {
	for _, re := range modelNoMap[tool] {
		if re.MatchString(model) {
			return true
		}
	}
	return false
}

var urlModelRe = regexp.MustCompile(`/models/([^/:]+)`)

// ExtractModel pulls the requested model from URL path, body.model, or Kiro's
// conversationState. Returns "" when none can be found.
func ExtractModel(url string, body []byte) string {
	urlModel := ""
	if m := urlModelRe.FindStringSubmatch(url); m != nil {
		urlModel = m[1]
	}
	if isBinaryData(body) {
		return urlModel
	}
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil {
		return urlModel
	}
	if cs, ok := parsed["conversationState"].(map[string]any); ok {
		if cur, ok := cs["currentMessage"].(map[string]any); ok {
			if uim, ok := cur["userInputMessage"].(map[string]any); ok {
				if m, ok := uim["modelId"].(string); ok {
					return m
				}
			}
		}
	}
	model := urlModel
	if m, ok := parsed["model"].(string); ok {
		model = m
	}
	clean := strings.TrimPrefix(model, "models/")
	for _, ver := range []string{"3.8", "3.7", "3.6"} {
		if clean == "gemini-"+ver+"-flash-tiered" {
			level := "medium"
			rawLevel := tieredThinkingLevel(parsed)
			switch strings.ToLower(rawLevel) {
			case "high", "medium", "low":
				level = strings.ToLower(rawLevel)
			}
			return "gemini-" + ver + "-flash-" + level
		}
	}
	return model
}

func tieredThinkingLevel(parsed map[string]any) string {
	if req, ok := parsed["request"].(map[string]any); ok {
		if v := thinkingLevel(req); v != "" {
			return v
		}
	}
	return thinkingLevel(parsed)
}

func thinkingLevel(body map[string]any) string {
	gc, _ := body["generationConfig"].(map[string]any)
	if gc == nil {
		return ""
	}
	tc, _ := gc["thinkingConfig"].(map[string]any)
	if tc == nil {
		return ""
	}
	s, _ := tc["thinkingLevel"].(string)
	return s
}

// isBinaryData samples the buffer and reports whether it looks non-JSON.
func isBinaryData(buf []byte) bool {
	if len(buf) == 0 {
		return false
	}
	n := len(buf)
	if n > 100 {
		n = 100
	}
	nonPrintable := 0
	for i := 0; i < n; i++ {
		b := buf[i]
		if b < 0x20 && b != 0x09 && b != 0x0A && b != 0x0D {
			nonPrintable++
		}
		if b > 0x7E {
			nonPrintable++
		}
	}
	return float64(nonPrintable)/float64(n) > 0.3
}
