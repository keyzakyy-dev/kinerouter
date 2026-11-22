// Package config resolves runtime configuration from the environment and the
// data directory. All state lives under ~/.kinerouter unless $DATA_DIR says
// otherwise.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// Name is this product's data directory name.
const Name = "kinerouter"

type Config struct {
	DataDir          string
	Port             int
	Host             string
	JWTSecret        []byte
	APIKeySecret     string
	MachineIDSalt    string
	InitialPassword  string
	AuthCookieSecure bool
	TrustProxy       bool
	BaseURL          string
}

func Load() (*Config, error) {
	c := &Config{
		DataDir:         DataDir(),
		Port:            envInt("PORT", 20128),
		Host:            envStr("HOST", "0.0.0.0"),
		APIKeySecret:    envStr("API_KEY_SECRET", "endpoint-proxy-api-key-secret"),
		MachineIDSalt:   envStr("MACHINE_ID_SALT", "endpoint-proxy-salt"),
		InitialPassword: envStr("INITIAL_PASSWORD", ""),
		BaseURL:         envStr("BASE_URL", ""),
	}
	c.AuthCookieSecure = envStr("AUTH_COOKIE_SECURE", "") == "true"
	c.TrustProxy = envStr("TRUST_PROXY", "") != ""

	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return nil, err
	}
	secret, err := loadOrCreateSecret(filepath.Join(c.DataDir, "jwt-secret"))
	if err != nil {
		return nil, err
	}
	if env := os.Getenv("JWT_SECRET"); env != "" {
		secret = []byte(env)
	}
	c.JWTSecret = secret
	return c, nil
}

// DBPath mirrors src/lib/db/paths.js: <DATA_DIR>/db/data.sqlite
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "db", "data.sqlite") }

func (c *Config) DBBackupsDir() string { return filepath.Join(c.DataDir, "db", "backups") }

// DataDir resolves this product's data directory. $DATA_DIR always wins.
func DataDir() string {
	if d := os.Getenv("DATA_DIR"); d != "" {
		return d
	}
	return defaultDataDir()
}

func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, Name)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "." + Name
	}
	return filepath.Join(home, "."+Name)
}

func loadOrCreateSecret(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return []byte(trimSpace(b)), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	s := []byte(hex.EncodeToString(buf))
	if err := os.WriteFile(path, s, 0o600); err != nil {
		return nil, err
	}
	return s, nil
}

func trimSpace(b []byte) string {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return string(b[start:end])
}

func isSpace(c byte) bool { return c == ' ' || c == '\n' || c == '\r' || c == '\t' }

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
