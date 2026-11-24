package oauth

// ZCode's device flow is not RFC 8628: the CLI mints a poll token, opens a
// browser approval page, then polls a Z.ai flow id. Two custom calls, so it
// lives here instead of the registry-driven generic path.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const zcodeOAuthBase = "https://zcode.z.ai"

func uuidOrEmpty() string { return uuid.NewString() }

func (m *Manager) zcodeDeviceStart(ctx context.Context, provider string) (map[string]any, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	pollToken := hex.EncodeToString(buf)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, zcodeOAuthBase+"/api/v1/oauth/cli/init",
		strings.NewReader(`{"provider":"zai"}`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+pollToken)
	raw, err := m.raw(req)
	if err != nil {
		return nil, fmt.Errorf("zcode device init failed: %w", err)
	}
	var out struct {
		Data *struct {
			FlowID       string  `json:"flow_id"`
			AuthorizeURL string  `json:"authorize_url"`
			ExpiresAt    float64 `json:"expires_at"`
			IntervalSec  float64 `json:"poll_interval_sec"`
			PollToken    string  `json:"poll_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Data == nil {
		return nil, fmt.Errorf("zcode device init: invalid response")
	}
	d := out.Data
	if d.FlowID == "" || d.AuthorizeURL == "" {
		return nil, fmt.Errorf("zcode device init: missing flow_id or authorize_url")
	}
	if d.PollToken != "" {
		pollToken = d.PollToken
	}
	interval := int(d.IntervalSec)
	if interval < 2 {
		interval = 2
	}
	expiresIn := 300
	if d.ExpiresAt > 0 {
		if remaining := int(d.ExpiresAt - float64(time.Now().Unix())); remaining > 0 {
			expiresIn = remaining
		}
	}
	state, err := token(16)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.devices[state] = &DeviceSession{Provider: provider, DeviceCode: d.FlowID, PollToken: pollToken, Interval: interval, CreatedAt: time.Now()}
	m.mu.Unlock()
	return map[string]any{
		"state":                   state,
		"provider":                provider,
		"userCode":                "",
		"verificationUri":         d.AuthorizeURL,
		"verificationUriComplete": d.AuthorizeURL,
		"expiresIn":               expiresIn,
		"interval":                interval,
	}, nil
}

func (m *Manager) zcodeDevicePoll(ctx context.Context, st *store.Store, sess *DeviceSession) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		zcodeOAuthBase+"/api/v1/oauth/cli/poll/"+sess.DeviceCode, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+sess.PollToken)
	raw, err := m.raw(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data *struct {
			Status string `json:"status"`
			Token  string `json:"token"`
			User   map[string]any
			Zai    *struct {
				RefreshToken string `json:"refresh_token"`
			} `json:"zai"`
			Bigmodel *struct {
				RefreshToken string `json:"refresh_token"`
			} `json:"bigmodel"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Data == nil {
		return nil, fmt.Errorf("zcode poll: invalid response")
	}
	switch out.Data.Status {
	case "failed", "expired":
		return nil, fmt.Errorf("zcode flow %s", out.Data.Status)
	case "ready":
	default:
		return map[string]any{"status": "pending", "interval": sess.Interval}, nil
	}
	jwt := strings.TrimSpace(out.Data.Token)
	if jwt == "" {
		return nil, fmt.Errorf("zcode: ready but no plan JWT")
	}
	refreshToken := ""
	if out.Data.Zai != nil {
		refreshToken = out.Data.Zai.RefreshToken
	} else if out.Data.Bigmodel != nil {
		refreshToken = out.Data.Bigmodel.RefreshToken
	}
	email, _ := out.Data.User["email"].(string)
	accountUUID, _ := out.Data.User["user_id"].(string)
	deviceID, sessionSeed := uuidOrEmpty(), uuidOrEmpty()

	data := map[string]any{
		"apiKey":       jwt,
		"refreshToken": refreshToken,
		"providerSpecificData": map[string]any{
			"deviceId":    deviceID,
			"sessionSeed": sessionSeed,
			"accountUUID": accountUUID,
		},
	}
	conn := &store.Connection{Provider: "zcode", AuthType: "oauth", IsActive: true, Email: email, Data: data}
	// Re-login mints a fresh plan JWT and bumps token_version, so reuse the row
	// for this account instead of accumulating unusable credentials.
	if conns, _ := st.ListConnections("zcode", true); len(conns) > 0 {
		for _, old := range conns {
			if email != "" && strings.EqualFold(old.Email, email) {
				conn.ID = old.ID
				conn.CreatedAt = old.CreatedAt
				break
			}
		}
	}
	if err := st.UpsertConnection(conn); err != nil {
		return nil, err
	}
	m.mu.Lock()
	delete(m.devices, sess.State)
	m.mu.Unlock()
	return map[string]any{"status": "connected", "connection": conn}, nil
}
