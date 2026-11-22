package executor

// Google Cloud Code Assist envelope (gemini-cli, antigravity). Both speak the
// Gemini body shape but expect it wrapped as {project, model, request} and hit
// `:generateContent` / `:streamGenerateContent` on a /v1internal base.
// Ported from open-sse/executors/{gemini-cli,antigravity}.js + the
// wrapInCloudCodeEnvelope helper in translator/request/openai-to-gemini.js.
//
// ponytail: antigravity's thoughtSignature backfill and aiguardrails prompt
// rewrites are skipped — they only matter for Gemini 3 tool-call replay.
// Add when a Gemini 3 antigravity connection starts 400-ing on functionCall.

import (
	"encoding/json"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

func isCloudCodeFormat(format string) bool {
	return format == "gemini-cli" || format == "antigravity"
}

// wrapCloudCode turns the Gemini request body into the Cloud Code envelope.
func wrapCloudCode(format, model string, body []byte, creds Credentials) ([]byte, error) {
	var gemini map[string]any
	if err := json.Unmarshal(body, &gemini); err != nil {
		return nil, err
	}
	req := map[string]any{
		"contents":          gemini["contents"],
		"systemInstruction": gemini["systemInstruction"],
		"generationConfig":  gemini["generationConfig"],
		"tools":             gemini["tools"],
		"sessionId":         creds.SessionID,
	}
	if tools, _ := gemini["tools"].([]any); len(tools) > 0 {
		req["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "VALIDATED"}}
	}
	userAgent := "gemini-cli"
	if format == "antigravity" {
		userAgent = "antigravity"
	} else {
		req["safetySettings"] = gemini["safetySettings"]
	}
	if req["sessionId"] == "" {
		req["sessionId"] = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	project := creds.ProjectID
	if project == "" {
		project = generatedProjectID()
	}
	envelope := map[string]any{
		"project":   project,
		"model":     model,
		"userAgent": userAgent,
		"requestId": "agent-" + uuid.NewString(),
		"request":   req,
	}
	return json.Marshal(envelope)
}

// generatedProjectID mirrors antigravity's generateProjectId: a plausible
// name rather than an empty string, which Google rejects outright.
func generatedProjectID() string {
	adj := []string{"useful", "bright", "swift", "calm", "bold"}
	noun := []string{"fuze", "wave", "spark", "flow", "core"}
	id := uuid.NewString()
	return adj[pick(len(adj))] + "-" + noun[pick(len(noun))] + "-" + id[:5]
}

func pick(n int) int {
	b := uuid.New()
	return int(b[0]) % n
}

// cloudCodeURL builds the /v1internal endpoint for the action.
func cloudCodeURL(base, format string, stream bool) string {
	action := ":generateContent"
	if stream {
		action = ":streamGenerateContent?alt=sse"
	}
	base = strings.TrimRight(base, "/")
	if format == "antigravity" && !strings.HasSuffix(base, "/v1internal") {
		base += "/v1internal"
	}
	return base + action
}

// geminiCLIUserAgent mirrors appConstants.geminiCLIUserAgent.
func geminiCLIUserAgent(cliVersion, model string) string {
	arch := runtime.GOARCH
	if arch == "386" {
		arch = "x86"
	}
	return "GeminiCLI/" + cliVersion + "/" + model + " (" + runtime.GOOS + "; " + arch + "; terminal)"
}

// ---------------------------------------------------------------- ollama

const ollamaLocalDefaultHost = "http://localhost:11434"

// ollamaEndpoint resolves the /api/chat URL: the connection's own host wins so
// a user can point ollama-local at a LAN box (mirrors resolveOllamaLocalHost).
// ollama (cloud)'s registry baseUrl already carries /api/chat, so it falls
// through to the registry value.
func ollamaEndpoint(provider string, creds Credentials) string {
	host := creds.BaseURL
	if host == "" && provider == "ollama-local" {
		host = ollamaLocalDefaultHost
	}
	if host == "" {
		return ""
	}
	host = strings.TrimRight(host, "/")
	for _, suffix := range []string{"/api/chat", "/api/generate"} {
		if strings.HasSuffix(host, suffix) {
			return host[:len(host)-len("chat")] + "chat"
		}
	}
	return host + "/api/chat"
}

func isOllamaFormat(format string) bool { return format == "ollama" }
