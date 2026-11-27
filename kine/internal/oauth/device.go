package oauth

// Device-code flow (RFC 8628) driven purely by the registry's providerOAuth
// entry: deviceCodeUrl + tokenUrl + clientId. Covers grok-cli, kimi, muse,
// nous, minimax-code(-global) and github. The browser opens the verification
// URL; the dashboard polls until the user authorises.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// DeviceSession is one in-flight device authorization.
type DeviceSession struct {
	Provider   string
	State      string
	DeviceCode string
	PollToken  string
	Meta       map[string]string
	Interval   int
	CreatedAt  time.Time
}

// DeviceStart requests a device + user code and returns what the SPA shows.
func (m *Manager) DeviceStart(ctx context.Context, provider string) (map[string]any, error) {
	o := m.reg.OAuthFor(provider)
	if o == nil {
		return nil, fmt.Errorf("provider %q has no OAuth configuration", provider)
	}
	if provider == "zcode" {
		return m.zcodeDeviceStart(ctx, provider)
	}
	if provider == "free-pi" {
		return m.freePiDeviceStart(ctx, provider)
	}
	if provider == "freebuff" {
		return m.freebuffDeviceStart(ctx, provider)
	}
	deviceURL := o.DeviceCodeURL
	if deviceURL == "" {
		deviceURL = o.DeviceURL
	}
	if deviceURL == "" {
		return nil, fmt.Errorf("provider %q has no device-code flow", provider)
	}
	form := url.Values{"client_id": {o.ClientID}}
	if scope := scopeString(o); scope != "" {
		form.Set("scope", scope)
	}
	if aud, ok := o.Extra["audience"].(string); ok && aud != "" {
		form.Set("audience", aud)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	data, err := m.raw(req)
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %w", err)
	}
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("invalid device code response: %w", err)
	}
	deviceCode, _ := d["device_code"].(string)
	if deviceCode == "" {
		return nil, fmt.Errorf("device code response had no device_code")
	}
	state, err := token(16)
	if err != nil {
		return nil, err
	}
	interval := 5
	if v, ok := d["interval"].(float64); ok && v > 0 {
		interval = int(v)
	}
	m.mu.Lock()
	m.devices[state] = &DeviceSession{Provider: provider, State: state, DeviceCode: deviceCode, Interval: interval, CreatedAt: time.Now()}
	m.mu.Unlock()
	return map[string]any{
		"state":                   state,
		"provider":                provider,
		"userCode":                d["user_code"],
		"verificationUri":         firstOf(d["verification_uri"], d["verification_url"]),
		"verificationUriComplete": firstOf(d["verification_uri_complete"], d["verification_url_complete"]),
		"expiresIn":               d["expires_in"],
		"interval":                interval,
	}, nil
}

// DevicePollByCode accepts the reference 9Router poll body shape
// ({deviceCode, codeVerifier}) and resolves it to the session minted by
// DeviceStart. The state-based poll remains the canonical route for the
// embedded SPA; this one is an additive compatibility alias.
func (m *Manager) DevicePollByCode(ctx context.Context, st *store.Store, provider, deviceCode string) (map[string]any, error) {
	if provider == "" || deviceCode == "" {
		return nil, fmt.Errorf("provider and deviceCode are required")
	}
	m.mu.Lock()
	var state string
	for key, sess := range m.devices {
		if sess.Provider == provider && sess.DeviceCode == deviceCode {
			state = key
			break
		}
	}
	m.mu.Unlock()
	if state == "" {
		return nil, fmt.Errorf("unknown or expired device session")
	}
	return m.DevicePoll(ctx, st, state)
}

// DevicePoll checks once whether the user has authorised. A pending result is
// not an error: {status:"pending"} tells the SPA to keep polling.
func (m *Manager) DevicePoll(ctx context.Context, st *store.Store, state string) (map[string]any, error) {
	m.mu.Lock()
	sess := m.devices[state]
	m.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("unknown or expired device session")
	}
	if sess.Provider == "zcode" {
		return m.zcodeDevicePoll(ctx, st, sess)
	}
	if sess.Provider == "free-pi" {
		return m.freePiDevicePoll(ctx, st, sess)
	}
	if sess.Provider == "freebuff" {
		return m.freebuffDevicePoll(ctx, st, sess)
	}
	o := m.reg.OAuthFor(sess.Provider)
	if o == nil {
		return nil, fmt.Errorf("provider %q has no OAuth configuration", sess.Provider)
	}
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {sess.DeviceCode},
		"client_id":   {o.ClientID},
	}
	if aud, ok := o.Extra["audience"].(string); ok && aud != "" {
		form.Set("audience", aud)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	data, err := m.raw(req)
	if err != nil {
		return nil, err
	}
	var tok map[string]any
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("invalid token response: %w", err)
	}
	if e, _ := tok["error"].(string); e == "authorization_pending" || e == "slow_down" {
		return map[string]any{"status": "pending", "interval": sess.Interval}, nil
	}
	if e, _ := tok["error"].(string); e != "" {
		desc, _ := tok["error_description"].(string)
		return nil, fmt.Errorf("%s: %s", e, desc)
	}
	conn, err := m.persistConnection(ctx, st, sess.Provider, tok)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	delete(m.devices, state)
	m.mu.Unlock()
	return map[string]any{"status": "connected", "connection": conn}, nil
}

// raw issues a request and returns the body regardless of HTTP status, so the
// caller can read a device-flow `error` field carried on a 400.
func (m *Manager) raw(req *http.Request) ([]byte, error) {
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

// scopeString resolves the registry's scope field, which may be a string or an
// array.
func scopeString(o *providerdata.OAuth) string {
	if o.Scope != "" {
		return o.Scope
	}
	if s := o.ScopeList(); len(s) > 0 {
		return strings.Join(s, " ")
	}
	return ""
}

func firstOf(vals ...any) any {
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return nil
}

// Providers lists the browser authorization-code providers.
func (m *Manager) Providers() []string {
	out := []string{}
	for _, id := range sortedKeys(browserFlows) {
		out = append(out, id)
	}
	return out
}

// DeviceProviders lists the providers with a device-code flow.
func (m *Manager) DeviceProviders() []string {
	out := []string{}
	for _, id := range sortedKeys(m.reg.DeviceProviders()) {
		out = append(out, id)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
