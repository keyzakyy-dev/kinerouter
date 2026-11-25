package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The writers below had no test at all: a change to their base-URL handling or
// their key names would only show up in a user's config file. Each case applies
// once, checks the shape 9router writes, then resets and checks the managed
// keys are gone and everything else survived.

func TestCliWritersApplyThenReset(t *testing.T) {
	cases := []struct {
		name string
		path func() string
		post func(*Gateway, http.ResponseWriter, *http.Request)
		del  func(*Gateway, http.ResponseWriter, *http.Request)
		body string
		// want / gone are substrings of the written file.
		want []string
		gone []string
	}{
		{
			name: "cline",
			path: clineGlobalStatePath,
			post: (*Gateway).handleCliClinePost,
			del:  (*Gateway).handleCliClineDelete,
			body: `{"baseUrl":"http://localhost:20128/v1","apiKey":"sk-x","model":"kiro/m1"}`,
			// Cline takes the base WITHOUT /v1.
			want: []string{`"openAiBaseUrl": "http://localhost:20128"`, `"openAiModelId": "kiro/m1"`, `"actModeApiProvider": "openai"`},
			gone: []string{"openAiBaseUrl", "planModeOpenAiModelId"},
		},
		{
			name: "kilo",
			path: kiloAuthPath,
			post: (*Gateway).handleCliKiloPost,
			del:  (*Gateway).handleCliKiloDelete,
			body: `{"baseUrl":"http://localhost:20128","apiKey":"sk-x","model":"kiro/m1"}`,
			want: []string{`"openai-compatible"`, `"baseUrl": "http://localhost:20128/v1"`, `"model": "kiro/m1"`},
			gone: []string{"openai-compatible"},
		},
		{
			name: "copilot",
			path: copilotPath,
			post: (*Gateway).handleCliCopilotPost,
			del:  (*Gateway).handleCliCopilotDelete,
			body: `{"baseUrl":"http://localhost:20128/v1","apiKey":"sk-x","models":["kiro/m1"]}`,
			want: []string{"chat/completions#models.ai.azure.com", `"vendor": "azure"`, `"maxInputTokens": 128000`},
			gone: []string{"KineRouter"},
		},
		{
			name: "forge",
			path: func() string { return filepath.Join(homePath(".forge"), "config.toml") },
			post: (*Gateway).handleCliForgePost,
			del:  (*Gateway).handleCliForgeDelete,
			body: `{"baseUrl":"http://localhost:20128","apiKey":"sk-x","model":"kiro/m1"}`,
			want: []string{"# Forge config — managed by KineRouter", "[openai]", "base_url = 'http://localhost:20128/v1'"},
			gone: []string{"[openai]"},
		},
		{
			name: "codewhale",
			path: func() string { return filepath.Join(homePath(".codewhale"), "config.toml") },
			post: (*Gateway).handleCliCodewhalePost,
			del:  (*Gateway).handleCliCodewhaleDelete,
			body: `{"baseUrl":"http://localhost:20128","apiKey":"sk-x","model":"kiro/m1"}`,
			want: []string{"# CodeWhale config — managed by KineRouter", "[openai]"},
			gone: []string{"[openai]"},
		},
		{
			name: "smelt",
			path: smeltPath,
			post: (*Gateway).handleCliSmeltPost,
			del:  (*Gateway).handleCliSmeltDelete,
			body: `{"baseUrl":"http://localhost:20128","apiKey":"sk-x","model":"kiro/m1"}`,
			want: []string{`"baseUrl": "http://localhost:20128/v1"`, `"_managedBy": "kinerouter"`},
			gone: []string{"_managedBy", "baseUrl"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tempHome(t)
			g := testGateway(t)

			// Seed a foreign entry so a reset that wipes the whole file is caught.
			before := writeForeignEntry(t, tc.name, tc.path)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			tc.post(g, rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("apply: %d %s", rec.Code, rec.Body)
			}
			got := readTextOrEmpty(tc.path())
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Fatalf("missing %q in:\n%s", w, got)
				}
			}

			rec = httptest.NewRecorder()
			tc.del(g, rec, httptest.NewRequest(http.MethodDelete, "/", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("reset: %d %s", rec.Code, rec.Body)
			}
			got = readTextOrEmpty(tc.path())
			for _, g2 := range tc.gone {
				if strings.Contains(got, g2) {
					t.Fatalf("reset left %q in:\n%s", g2, got)
				}
			}
			if before != "" && !strings.Contains(got, before) {
				t.Fatalf("reset dropped a foreign entry %q, file now:\n%s", before, got)
			}
		})
	}
}

// writeForeignEntry seeds something the writer must not own, and returns the
// text to look for after reset. Files that start as JSON object, JSON array and
// TOML need different seeds.
func writeForeignEntry(t *testing.T, tool string, path func() string) string {
	t.Helper()
	p := path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	seed, marker := "", ""
	switch tool {
	case "copilot":
		seed, marker = `[{"name":"SomeoneElse","vendor":"openai"}]`, "SomeoneElse"
	case "forge", "codewhale":
		seed, marker = "[other]\nkey = \"keep\"\n", "[other]"
	default:
		seed, marker = `{"unrelated": "keep"}`, "unrelated"
	}
	if err := os.WriteFile(p, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	return marker
}
