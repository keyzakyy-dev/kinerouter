package oauth

// Freebuff login: the CLI mints an auth code + fingerprint, the browser page
// approves it, and a status poll hands back the CLI token. No device code is
// typed by the user; the login URL already carries the code.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	freebuffBase    = "https://freebuff.com"
	freebuffCLIUA   = "Freebuff-CLI/0.2.36"
	freebuffPollTTL = 60 * time.Minute
)

func (m *Manager) freebuffDeviceStart(ctx context.Context, provider string) (map[string]any, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	authCode := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(authCode))
	fingerprintID := uuid.NewString()
	fingerprintHash := hex.EncodeToString(sum[:])
	expiresAt := time.Now().Add(freebuffPollTTL).UnixMilli()
	loginURL := freebuffBase + "/login?auth_code=" + authCode

	payload, _ := json.Marshal(map[string]string{"fingerprintId": fingerprintID, "fingerprintHash": fingerprintHash})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, freebuffBase+"/api/auth/cli/code", bytes.NewReader(payload))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", freebuffCLIUA)
		if resp, err := m.http.Do(req); err == nil {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			var data struct {
				FingerprintID   string `json:"fingerprintId"`
				FingerprintHash string `json:"fingerprintHash"`
				LoginURL        string `json:"loginUrl"`
				ExpiresAt       int64  `json:"expiresAt"`
			}
			if resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &data) == nil {
				if data.FingerprintID != "" {
					fingerprintID = data.FingerprintID
				}
				if data.FingerprintHash != "" {
					fingerprintHash = data.FingerprintHash
				}
				if data.LoginURL != "" {
					loginURL = data.LoginURL
					if i := strings.Index(loginURL, "auth_code="); i >= 0 {
						authCode = loginURL[i+len("auth_code="):]
					}
				}
				if data.ExpiresAt > 0 {
					expiresAt = data.ExpiresAt
				}
			}
		}
	}

	state, err := token(16)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.devices[state] = &DeviceSession{
		Provider:   provider,
		State:      state,
		DeviceCode: authCode,
		Interval:   5,
		CreatedAt:  time.Now(),
		Meta: map[string]string{
			"fingerprintId":   fingerprintID,
			"fingerprintHash": fingerprintHash,
			"expiresAt":       strconv.FormatInt(expiresAt, 10),
		},
	}
	m.mu.Unlock()
	return map[string]any{
		"state":                   state,
		"provider":                provider,
		"userCode":                "",
		"verificationUri":         loginURL,
		"verificationUriComplete": loginURL,
		"expiresIn":               int(time.Until(time.UnixMilli(expiresAt)).Seconds()),
		"interval":                5,
	}, nil
}

func (m *Manager) freebuffDevicePoll(ctx context.Context, st *store.Store, sess *DeviceSession) (map[string]any, error) {
	params := url.Values{}
	params.Set("fingerprintId", sess.Meta["fingerprintId"])
	params.Set("fingerprintHash", sess.Meta["fingerprintHash"])
	params.Set("expiresAt", sess.Meta["expiresAt"])
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, freebuffBase+"/api/auth/cli/status?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", freebuffCLIUA)
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNoContent || len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{"status": "pending", "interval": 5}, nil
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil, fmt.Errorf("freebuff login expired")
	}
	var top map[string]any
	_ = json.Unmarshal(raw, &top)
	user, _ := top["user"].(map[string]any)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := top[k].(string); ok && v != "" {
				return v
			}
			if v, ok := user[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	authToken := pick("authToken", "auth_token", "accessToken", "access_token", "token", "apiKey")
	if authToken == "" {
		return map[string]any{"status": "pending", "interval": 5}, nil
	}
	conn := &store.Connection{
		Provider: "freebuff",
		AuthType: "oauth",
		IsActive: true,
		Email:    pick("email"),
		Data: map[string]any{
			"apiKey":               authToken,
			"providerSpecificData": map[string]any{"fingerprintId": sess.Meta["fingerprintId"]},
		},
	}
	if err := st.UpsertConnection(conn); err != nil {
		return nil, err
	}
	m.mu.Lock()
	delete(m.devices, sess.State)
	m.mu.Unlock()
	return map[string]any{"status": "connected", "connection": conn}, nil
}
