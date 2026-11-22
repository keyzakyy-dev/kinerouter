package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

// Self-check for the searchViaChat answer extractors and the endpoint builder.
// No server, no DB: each extractor runs against a payload shaped like its
// upstream, and expansion/segment widening is checked directly.
func TestSearchViaChat(t *testing.T) {
	// --- gemini: text join + groundingChunks dedupe
	text, cites := extractGeminiAnswer(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{
				map[string]any{"text": "Hello "}, map[string]any{"text": "world"},
			}},
			"groundingMetadata": map[string]any{"groundingChunks": []any{
				map[string]any{"web": map[string]any{"uri": "https://a", "title": "A"}},
				map[string]any{"web": map[string]any{"uri": "https://a", "title": "A"}},
			}},
		}},
	})
	if text != "Hello world" || len(cites) != 1 || cites[0]["url"] != "https://a" {
		t.Fatalf("gemini: %q %v", text, cites)
	}

	// --- openai: annotations preferred over top-level citations
	oa := openAIStyleChat()
	text, cites = oa.extract(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"content": "ans",
			"annotations": []any{map[string]any{"url_citation": map[string]any{
				"url": "https://x", "title": "X"}}},
		}}},
		"citations": []any{"https://ignored"},
	})
	if text != "ans" || len(cites) != 1 || cites[0]["url"] != "https://x" {
		t.Fatalf("openai: %q %v", text, cites)
	}

	// --- xai responses: output[].content[].text + annotation
	text, cites = extractResponsesAnswer(map[string]any{
		"output": []any{map[string]any{
			"content": []any{map[string]any{
				"text":        "resp",
				"annotations": []any{map[string]any{"url": "https://y", "title": "Y"}},
			}},
		}},
	})
	if text != "resp" || len(cites) != 1 || cites[0]["url"] != "https://y" {
		t.Fatalf("xai: %q %v", text, cites)
	}

	// --- kimi: tool_call function arguments JSON
	text, cites = chatSearchCfgs["kimi"].extract(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"content": "k",
			"tool_calls": []any{map[string]any{"function": map[string]any{
				"arguments": `{"search_results":[{"url":"https://k","title":"K","snippet":"s"}]}`}}},
		}}},
	})
	if text != "k" || len(cites) != 1 || cites[0]["url"] != "https://k" || cites[0]["snippet"] != "s" {
		t.Fatalf("kimi: %q %v", text, cites)
	}

	// --- minimax: web_search_results direct
	_, cites = chatSearchCfgs["minimax"].extract(map[string]any{
		"choices":            []any{map[string]any{"message": map[string]any{"content": "m"}}},
		"web_search_results": []any{map[string]any{"link": "https://m", "title": "M"}},
	})
	if len(cites) != 1 || cites[0]["url"] != "https://m" {
		t.Fatalf("minimax: %v", cites)
	}

	// --- perplexity: bare URL citation strings
	_, cites = chatSearchCfgs["perplexity"].extract(map[string]any{
		"choices":   []any{map[string]any{"message": map[string]any{"content": "p"}}},
		"citations": []any{"https://p1", "https://p2"},
	})
	if len(cites) != 2 || cites[1]["url"] != "https://p2" {
		t.Fatalf("perplexity: %v", cites)
	}

	// --- expandSegment widens and trims partial words at both edges
	answer := "0123456789 first sentence here and the rest of the answer text goes on and on"
	got := expandSegment(answer, map[string]any{"startIndex": float64(10), "endIndex": float64(27)})
	if !strings.Contains(got, "first sentence") || strings.HasPrefix(got, "...") {
		t.Fatalf("expandSegment mid: %q", got)
	}
	long := strings.Repeat("padding words ", 40) + "TARGET phrase here" + strings.Repeat(" trailing words", 40)
	got = expandSegment(long, map[string]any{"startIndex": float64(560), "endIndex": float64(580)})
	if !strings.HasPrefix(got, "...") || !strings.HasSuffix(got, "...") {
		t.Fatalf("expandSegment both edges: %q", got)
	}

	// --- endpoint resolution: registry endpoint with {model}, transport fallback
	g := testGateway(t)
	g.reg = providerdata.Get()
	if u := g.chatSearchEndpoint("gemini", "m1"); !strings.Contains(u, "models/m1:generateContent") {
		t.Fatalf("gemini endpoint: %q", u)
	}
	if u := g.chatSearchEndpoint("openai", "x"); u != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("openai endpoint: %q", u)
	}

	// --- antigravity requires a project id
	if _, _, err := g.searchViaChat(httptest.NewRequest(http.MethodPost, "/v1/search", nil), "antigravity",
		searchParams{query: "q"}); err == nil || !strings.Contains(err.Error(), "No credentials") {
		t.Fatalf("antigravity no-conn: %v", err)
	}
}
