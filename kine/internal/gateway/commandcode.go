package gateway

// CommandCode request identity + NDJSON handling. Ported from
// open-sse/shared/commandCodeHeaders.js and
// open-sse/translator/response/commandcode-to-openai.js.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

var slugSeps = regexp.MustCompile(`[^a-z0-9]+`)

// projectSlug mirrors slugify(process.cwd()): downcase, run non-alphanumerics
// to "-", trim. The Unicode transliteration table is skipped — a non-ASCII cwd
// simply loses those runs instead of romanising them.
func projectSlug(dir string) string {
	s := slugSeps.ReplaceAllString(strings.ToLower(dir), "-")
	return strings.Trim(s, "-")
}

// commandCodeEnvelope builds the CLI body and the x-session-id/x-project-slug
// headers share.
func commandCodeRequest(raw map[string]any, model, sessionID, workingDir string) map[string]any {
	return translate.ToCommandCode(raw, model, workingDir, translate.SessionUUID(sessionID))
}

// isNDJSONStream reports whether the upstream body is AI-SDK-v5 line events
// (CommandCode, Cursor-ish) rather than SSE.
func isNDJSONStream(provider string) bool { return provider == "commandcode" }

// collectNDJSON folds a whole NDJSON body into one chat.completion payload.
func collectNDJSON(provider string, body io.Reader) map[string]any {
	switch provider {
	case "commandcode":
		return translate.CollectCommandCode(body)
	}
	return nil
}

// ndjsonToSSE replays a collected chat.completion as a single-chunk SSE so a
// streaming client still sees the event it expects.
func ndjsonToSSE(w io.Writer, body map[string]any) {
	chunk := map[string]any{
		"id": body["id"], "object": "chat.completion.chunk",
		"created": body["created"], "model": body["model"],
	}
	choices, _ := body["choices"].([]any)
	if len(choices) > 0 {
		c, _ := choices[0].(map[string]any)
		chunk["choices"] = []any{map[string]any{
			"index": 0, "delta": c["message"], "finish_reason": c["finish_reason"],
		}}
	}
	if u, ok := body["usage"]; ok {
		chunk["usage"] = u
	}
	b, _ := json.Marshal(chunk)
	w.Write(append(append([]byte("data: "), b...), []byte("\n\n")...))
	io.WriteString(w, "data: [DONE]\n\n")
}

// commandCodeWorkingDir is reported as config.workingDir. the reference client sends its
// process cwd; the server has no client workspace, so this doubles as the
// project slug source.
func commandCodeWorkingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "/"
	}
	return dir
}

var _ = bytes.MinRead
