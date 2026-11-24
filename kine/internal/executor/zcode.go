package executor

// ZCode start-plan lane. The gateway inspects identity: a fixed 3-block system
// prompt, metadata.user_id, and a header bundle (session/trace ids, OS,
// platform, CLI version). Anything missing comes back as an opaque 3012.

import (
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

//go:embed zcode_identity_system.json
var zcodeIdentityRaw []byte

type zcodeIdentity struct {
	CliPrefix        string `json:"cliPrefix"`
	StablePrefix     string `json:"stablePrefix"`
	AnthropicVersion string `json:"anthropicVersion"`
	AnthropicBeta    string `json:"anthropicBeta"`
	SDKUserAgent     string `json:"sdkUserAgent"`
}

var zcodeID = func() zcodeIdentity {
	var id zcodeIdentity
	_ = json.Unmarshal(zcodeIdentityRaw, &id)
	return id
}()

func zcodeIdentityErr() error {
	if zcodeID.CliPrefix == "" || zcodeID.StablePrefix == "" {
		return fmt.Errorf("zcode: embedded identity asset is malformed")
	}
	return nil
}

const (
	zcodeAppVersion       = "3.16.3"
	zcodeDefaultMaxTokens = 8192
)

// zcodeSessionID mirrors the desktop bridge: an HMAC of the conversation key
// seeded per connection, so a reconnect resumes the same upstream session.
func zcodeSessionID(conversationKey, seed string) string {
	if conversationKey == "" {
		return "session_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if strings.TrimSpace(seed) == "" {
		seed = "kinerouter-zcode"
	}
	mac := hmac.New(sha256.New, []byte(seed))
	mac.Write([]byte{0})
	mac.Write([]byte(conversationKey))
	return "session_" + fmt.Sprintf("%x", mac.Sum(nil))[:32]
}

func zcodeOSCategory() string {
	switch runtime.GOOS {
	case "darwin":
		return "mac"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

// zcodeShapeBody injects the mandatory identity blocks into an Anthropic body.
func zcodeShapeBody(body []byte, sessionID, deviceID, accountUUID string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("zcode: parse body: %w", err)
	}
	dynamic := ""
	switch v := m["system"].(type) {
	case string:
		dynamic = strings.TrimSpace(v)
	case []any:
		parts := []string{}
		for _, item := range v {
			switch iv := item.(type) {
			case string:
				if s := strings.TrimSpace(iv); s != "" {
					parts = append(parts, s)
				}
			case map[string]any:
				if t, ok := iv["text"].(string); ok && strings.TrimSpace(t) != "" {
					parts = append(parts, strings.TrimSpace(t))
				}
			}
		}
		dynamic = strings.TrimSpace(strings.Join(parts, "\n\n"))
	}
	if dynamic != "" {
		dynamic = "\n\n" + dynamic
	}
	ephemeral := map[string]any{"type": "ephemeral"}
	m["system"] = []any{
		map[string]any{"type": "text", "text": zcodeID.CliPrefix},
		map[string]any{"type": "text", "text": zcodeID.StablePrefix, "cache_control": ephemeral},
		map[string]any{"type": "text", "text": dynamic, "cache_control": ephemeral},
	}
	meta, _ := m["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	uid, _ := json.Marshal(map[string]string{
		"device_id":    deviceID,
		"account_uuid": accountUUID,
		"session_id":   sessionID,
	})
	meta["user_id"] = string(uid)
	m["metadata"] = meta
	if m["max_tokens"] == nil {
		m["max_tokens"] = zcodeDefaultMaxTokens
	}
	return json.Marshal(m)
}

// zcodeHeaders is the registry auth hook: the identity bundle the start-plan
// gateway fingerprints.
func zcodeHeaders(req Request, headers map[string]string) {
	seed, _ := req.Creds.Extra["sessionSeed"].(string)
	sessionID := zcodeSessionID(req.Creds.SessionID, seed)
	requestID := uuid.NewString()
	traceID := uuid.NewString()

	for k, v := range map[string]string{
		"http-referer":         "https://zcode.z.ai",
		"user-agent":           zcodeID.SDKUserAgent,
		"x-title":              "Z Code@electron",
		"x-release-channel":    "production",
		"x-client-language":    "en",
		"x-client-timezone":    "UTC",
		"x-zcode-agent":        "glm",
		"x-platform":           runtime.GOOS + "-" + runtime.GOARCH,
		"x-os-category":        zcodeOSCategory(),
		"x-os-version":         "unknown",
		"x-zcode-app-version":  zcodeAppVersion,
		"anthropic-version":    zcodeID.AnthropicVersion,
		"anthropic-beta":       zcodeID.AnthropicBeta,
		"x-zcode-session-type": "main",
		"x-zcode-query-source": "main_turn",
		"x-session-id":         sessionID,
		"x-request-id":         requestID,
		"x-zcode-trace-id":     traceID,
		"x-query-id":           "01a1" + requestID[4:],
	} {
		if v != "" {
			headers[k] = v
		}
	}
}

func zcodeDeviceID(req Request) string {
	if v, ok := req.Creds.Extra["deviceId"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return uuid.NewString()
}

func zcodeAccountUUID(req Request) string {
	if v, ok := req.Creds.Extra["accountUUID"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
