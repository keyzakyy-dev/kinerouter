package oauth

// Free Pi login: GitHub device flow hosted by api.freepi.ai (not GitHub's own
// endpoint). The server ties the login to a session id and refuses to mint a
// token until that session records consent to the ad-supported terms, so the
// consent POST goes out when the user starts the login.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	freePiOrigin         = "https://api.freepi.ai"
	freePiConsentVersion = "4"
	freePiTokenCode      = "consent_required"
)

func (m *Manager) freePiPost(ctx context.Context, path string, payload any, bearer string) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, freePiOrigin+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, nil
}

func (m *Manager) freePiDeviceStart(ctx context.Context, provider string) (map[string]any, error) {
	status, raw, err := m.freePiPost(ctx, "/auth/github/device", map[string]any{}, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("free-pi device start failed: HTTP %d", status)
	}
	var d struct {
		SessionID               string `json:"session_id"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		Interval                int    `json:"interval"`
		ExpiresIn               int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &d); err != nil || d.SessionID == "" {
		return nil, fmt.Errorf("free-pi device start: invalid response")
	}
	if d.Interval <= 0 {
		d.Interval = 5
	}
	if status, _, err := m.freePiPost(ctx, "/auth/consent", map[string]any{
		"session_id":      d.SessionID,
		"consent_version": freePiConsentVersion,
	}, ""); err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("free-pi consent submission failed")
	}
	state, err := token(16)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.devices[state] = &DeviceSession{Provider: provider, State: state, DeviceCode: d.SessionID, Interval: d.Interval, CreatedAt: time.Now()}
	m.mu.Unlock()
	return map[string]any{
		"state":                   state,
		"provider":                provider,
		"userCode":                d.UserCode,
		"verificationUri":         d.VerificationURI,
		"verificationUriComplete": firstOf(d.VerificationURIComplete, d.VerificationURI),
		"expiresIn":               d.ExpiresIn,
		"interval":                d.Interval,
	}, nil
}

func (m *Manager) freePiDevicePoll(ctx context.Context, st *store.Store, sess *DeviceSession) (map[string]any, error) {
	status, raw, err := m.freePiPost(ctx, "/auth/token", map[string]any{"session_id": sess.DeviceCode}, "")
	if err != nil {
		return nil, err
	}
	var out struct {
		Token      string `json:"token"`
		Code       string `json:"code"`
		Status     string `json:"status"`
		RetryAfter int    `json:"retry_after"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("free-pi token poll: unparseable response (HTTP %d)", status)
	}
	if out.Token != "" {
		conn := &store.Connection{
			Provider: "free-pi",
			AuthType: "oauth",
			IsActive: true,
			Data:     map[string]any{"apiKey": out.Token},
		}
		if err := st.UpsertConnection(conn); err != nil {
			return nil, err
		}
		m.mu.Lock()
		delete(m.devices, sess.State)
		m.mu.Unlock()
		return map[string]any{"status": "connected", "connection": conn}, nil
	}
	if out.Code == freePiTokenCode || out.Status == freePiTokenCode || status == http.StatusForbidden {
		return nil, fmt.Errorf("free-pi: consent not recorded for this login session")
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("free-pi token poll failed: HTTP %d: %s", status, strings.TrimSpace(string(raw)))
	}
	interval := sess.Interval
	if out.RetryAfter > interval {
		interval = out.RetryAfter
	}
	return map[string]any{"status": "pending", "interval": interval}, nil
}
