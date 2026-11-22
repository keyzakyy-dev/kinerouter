package store

import (
	"database/sql"
	"errors"
)

// defaultSettings mirrors DEFAULT_SETTINGS in
// src/lib/db/repos/settingsRepo.js. Values are JSON-native so the same blob
// can be written/read by both implementations.
var defaultSettings = map[string]any{
	"cloudEnabled":                 false,
	"tunnelEnabled":                false,
	"tunnelUrl":                    "",
	"tunnelProvider":               "cloudflare",
	"tailscaleEnabled":             false,
	"tailscaleUrl":                 "",
	"stickyRoundRobinLimit":        3,
	"providerStrategies":           map[string]any{},
	"quotaVisibility":              map[string]any{},
	"comboStrategy":                "fallback",
	"comboStickyRoundRobinLimit":   1,
	"comboStrategies":              map[string]any{},
	"requireLogin":                 true,
	"requireApiKey":                true,
	"tunnelDashboardAccess":        true,
	"authMode":                     "password",
	"ssoType":                      "oidc",
	"oidcIssuerUrl":                "",
	"oidcClientId":                 "",
	"oidcClientSecret":             "",
	"oidcScopes":                   "openid profile email",
	"oidcLoginLabel":               "Sign in with OIDC",
	"samlEntryPoint":               "",
	"samlIssuer":                   "urn:kinerouter:sp",
	"samlCert":                     "",
	"samlLoginLabel":               "Sign in with SAML SSO",
	"samlAttributeEmail":           "email",
	"samlAttributeName":            "name",
	"enableObservability":          false,
	"observabilityMaxRecords":      1000,
	"observabilityBatchSize":       20,
	"observabilityFlushIntervalMs": 5000,
	"observabilityMaxJsonSize":     5,
	"outboundProxyEnabled":         false,
	"outboundProxyUrl":             "",
	"outboundNoProxy":              "",
	"rtkEnabled":                   true,
	"headroomEnabled":              false,
	"headroomUrl":                  "http://localhost:8787",
	"headroomCompressUserMessages": false,
	"headroomTimeoutMs":            3000,
	"cavemanEnabled":               false,
	"cavemanLevel":                 "full",
	"ponytailEnabled":              false,
	"ponytailLevel":                "full",
	"customPromptEnabled":          false,
	"customPrompt":                 "",
	"pxpipeEnabled":                false,
	"pxpipeAutoInstall":            true,
	"pxpipeMinChars":               25000,
	"pxpipeTimeoutMs":              15000,
	"providerOverrides":            map[string]any{},
	"fallbackStrategy":             "fill-first",
	"capacityAdapter": map[string]any{
		"vision":     map[string]any{"enabled": true, "roundRobin": false, "models": []any{}},
		"pdf":        map[string]any{"enabled": false, "roundRobin": false, "models": []any{}},
		"audioInput": map[string]any{"enabled": true, "roundRobin": false, "models": []any{}},
		"videoInput": map[string]any{"enabled": false, "roundRobin": false, "models": []any{}},
	},
}

// GetSettings returns defaults merged with the stored blob. Read-merge-write is
// atomic (single writer connection) and returns the merged view.
func (s *Store) GetSettings() (map[string]any, error) {
	raw, err := s.readRawSettings()
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(defaultSettings)+len(raw))
	for k, v := range defaultSettings {
		out[k] = v
	}
	for k, v := range raw {
		out[k] = v
	}
	return out, nil
}

func (s *Store) readRawSettings() (map[string]any, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM settings WHERE id = 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseJSON[map[string]any](data, map[string]any{}), nil
}

// UpdateSettings merges updates into the stored blob inside one transaction.
func (s *Store) UpdateSettings(updates map[string]any) (map[string]any, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	raw := map[string]any{}
	var data string
	err = tx.QueryRow(`SELECT data FROM settings WHERE id = 1`).Scan(&data)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		raw = parseJSON[map[string]any](data, map[string]any{})
	}
	for k, v := range updates {
		raw[k] = v
	}
	if _, err := tx.Exec(`INSERT INTO settings(id, data) VALUES(1, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data`, stringifyJSON(raw)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetSettings()
}

// SettingBool is a convenience for hot-path settings reads.
func (s *Store) SettingBool(key string, def bool) bool {
	all, err := s.GetSettings()
	if err != nil {
		return def
	}
	if v, ok := all[key].(bool); ok {
		return v
	}
	return def
}

func (s *Store) SettingString(key, def string) string {
	all, err := s.GetSettings()
	if err != nil {
		return def
	}
	if v, ok := all[key].(string); ok && v != "" {
		return v
	}
	return def
}
