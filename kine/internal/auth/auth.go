// Package auth implements dashboard session auth (JWT cookie + bcrypt password),
// CLI-token auth, API key generation/validation and login rate limiting —
// behaviour-compatible with src/lib/auth/* and src/shared/utils/*.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName    = "auth_token"
	SessionMaxAge = 24 * 60 * 60
	// DEFAULT_PASSWORD is only the fallback when neither a stored hash nor
	// INITIAL_PASSWORD exists; it is not used to seed a remote-usable login.
	DEFAULT_PASSWORD = "123456"
	CLIAuthSalt      = "kine-cli-auth"
)

// ---------------------------------------------------------------- JWT (HS256)

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type claims struct {
	Authenticated bool   `json:"authenticated"`
	Iat           int64  `json:"iat"`
	Exp           int64  `json:"exp"`
	Iss           string `json:"iss,omitempty"`
	Sub           string `json:"sub,omitempty"`
}

// Minimal HS256 implementation: the project needs one algorithm and no
// dependency; jose-compatible enough that an existing cookie keeps
// verifying if it was signed with the same secret.
// Sign issues a token identical in shape to jose SignJWT({authenticated:true}).
func Sign(secret []byte, ttl time.Duration) (string, error) {
	now := time.Now()
	h := `{"alg":"HS256","typ":"JWT"}`
	c := fmt.Sprintf(`{"authenticated":true,"iat":%d,"exp":%d}`, now.Unix(), now.Add(ttl).Unix())
	signing := base64URLEncode([]byte(h)) + "." + base64URLEncode([]byte(c))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + base64URLEncode(mac.Sum(nil)), nil
}

// SignClaims issues a session token carrying extra claims (oidcName,
// samlEmail, …) alongside authenticated/iat/exp. Mirrors
// createDashboardAuthToken({...claims}) in src/lib/auth/dashboardSession.js.
// A claim that collides with a reserved key is overwritten by the reserved
// value so a caller can never widen or extend its own session.
func SignClaims(secret []byte, ttl time.Duration, extra map[string]any) (string, error) {
	now := time.Now()
	payload := make(map[string]any, len(extra)+3)
	for k, v := range extra {
		payload[k] = v
	}
	payload["authenticated"] = true
	payload["iat"] = now.Unix()
	payload["exp"] = now.Add(ttl).Unix()
	c, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signing := base64URLEncode([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64URLEncode(c)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + base64URLEncode(mac.Sum(nil)), nil
}

// ParseClaims verifies a token and returns its full claim set, so callers can
// read the identity claims an OIDC/SAML login stored in the session cookie.
func ParseClaims(secret []byte, token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	got, err := base64URLDecode(parts[2])
	if err != nil || !hmac.Equal(got, mac.Sum(nil)) {
		return nil, false
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, false
	}
	var c map[string]any
	if json.Unmarshal(payload, &c) != nil {
		return nil, false
	}
	if exp, ok := c["exp"].(float64); ok && time.Now().Unix() > int64(exp) {
		return nil, false
	}
	return c, true
}

// Verify checks signature + exp and returns the parsed claims.
func Verify(secret []byte, token string) (*claims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	signing := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	got, err := base64URLDecode(parts[2])
	if err != nil || !hmac.Equal(got, mac.Sum(nil)) {
		return nil, false
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, false
	}
	var c claims
	if err := jsonUnmarshal(payload, &c); err != nil {
		return nil, false
	}
	if c.Exp != 0 && time.Now().Unix() > c.Exp {
		return nil, false
	}
	return &c, true
}

// ---------------------------------------------------------------- password

// VerifyPassword compares against the stored hash, else INITIAL_PASSWORD.
// Mirrors dashboardSession.verifyDashboardPassword.
func VerifyPassword(storedHash, password, initialPassword string) bool {
	if password == "" {
		return false
	}
	if storedHash != "" {
		return bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)) == nil
	}
	if initialPassword == "" {
		initialPassword = DEFAULT_PASSWORD
	}
	// Constant time: this fallback compares the operator-supplied password
	// directly, so a length-then-bytes comparison would leak it byte by byte.
	if len(password) != len(initialPassword) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(initialPassword)) == 1
}

func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// ---------------------------------------------------------------- API keys
// Format sk-{machineId}-{keyId}-{crc8} (shared/utils/apiKey.js).

const keyChars = "abcdefghijklmnopqrstuvwxyz0123456789"

func GenerateAPIKey(machineID, secret string) (string, error) {
	keyID, err := randomString(6)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(machineID + keyID))
	crc := hex.EncodeToString(mac.Sum(nil))[:8]
	return "sk-" + machineID + "-" + keyID + "-" + crc, nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(keyChars)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = keyChars[v.Int64()]
	}
	return string(b), nil
}

// ParseAPIKey mirrors parseApiKey; returns machineID, keyID, isNewFormat.
func ParseAPIKey(key, secret string) (machineID, keyID string, isNew bool, ok bool) {
	if !strings.HasPrefix(key, "sk-") {
		return "", "", false, false
	}
	parts := strings.Split(key, "-")
	if len(parts) == 4 {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(parts[1] + parts[2]))
		if hex.EncodeToString(mac.Sum(nil))[:8] != parts[3] {
			return "", "", false, false
		}
		return parts[1], parts[2], true, true
	}
	if len(parts) == 2 {
		return "", parts[1], false, true
	}
	return "", "", false, false
}

// ---------------------------------------------------------------- machine id

// MachineID mirrors shared/utils/machineId.js getConsistentMachineId:
// sha256(raw + salt [+ cliSecret])[:16], with the raw id persisted to
// <DATA_DIR>/machine-id so CLI and server agree.
func LoadMachineID(dataDir, salt string) string {
	raw := readOrCreate(filepath.Join(dataDir, "machine-id"), func() string {
		if b, err := os.ReadFile("/etc/machine-id"); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			return strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile("/var/lib/dbus/machine-id"); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			return strings.TrimSpace(string(b))
		}
		buf := make([]byte, 16)
		rand.Read(buf)
		return hex.EncodeToString(buf)
	})
	if salt == "" {
		salt = "endpoint-proxy-salt"
	}
	extra := ""
	if salt == CLIAuthSalt {
		extra = readOrCreate(filepath.Join(dataDir, "auth", "cli-secret"), func() string {
			buf := make([]byte, 32)
			rand.Read(buf)
			return hex.EncodeToString(buf)
		})
	}
	sum := sha256.Sum256([]byte(raw + salt + extra))
	return hex.EncodeToString(sum[:])[:16]
}

func readOrCreate(path string, gen func() string) string {
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	s := gen()
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(s), 0o600)
	return s
}

// CLIToken is the value a caller sends as x-kine-cli-token for this process.
// Used by in-process self-pings (dashboard model test).
func CLIToken(cliMachineID string) string {
	sum := sha256.Sum256([]byte(cliMachineID + "kine-cli-auth"))
	return hex.EncodeToString(sum[:])[:16]
}

// ValidateCLIToken mirrors the x-kine-cli-token derivation.
func ValidateCLIToken(token, cliMachineID string) bool {
	if token == "" {
		return false
	}
	sum := sha256.Sum256([]byte(cliMachineID + "kine-cli-auth"))
	return hmac.Equal([]byte(token), []byte(hex.EncodeToString(sum[:])[:16]))
}

// ---------------------------------------------------------------- login limiter

// Limiter is an in-memory progressive lockout (5 fails → 30s/2m/10m/30m),
// matching src/lib/auth/loginLimiter.js. A lock level advances only after the
// next group of five failures, and the one-hour window follows the most recent
// failure, not the creation of the bucket.
type Limiter struct {
	mu    sync.Mutex
	state map[string]*bucket
}

type bucket struct {
	fails      int
	lockedTill time.Time
	lockLevel  int
	lastFailAt time.Time
}

var lockSteps = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
var errLocked = errors.New("too many failed attempts")

func NewLimiter() *Limiter { return &Limiter{state: map[string]*bucket{}} }

// getEntry mirrors loginLimiter.getEntry: an idle bucket (one hour since the
// last failure, and no active lock) is discarded before it is used.
func (l *Limiter) getEntry(key string) *bucket {
	b := l.state[key]
	if b != nil && !b.lastFailAt.IsZero() &&
		time.Since(b.lastFailAt) > time.Hour &&
		(b.lockedTill.IsZero() || !time.Now().Before(b.lockedTill)) {
		delete(l.state, key)
		return nil
	}
	return b
}

func (l *Limiter) Check(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.getEntry(key)
	if b == nil || b.lockedTill.IsZero() {
		return 0, true
	}
	if wait := time.Until(b.lockedTill); wait > 0 {
		return wait, false
	}
	return 0, true
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.getEntry(key)
	if b == nil {
		b = &bucket{}
		l.state[key] = b
	}
	b.fails++
	b.lastFailAt = time.Now()
	if b.fails >= 5 {
		idx := b.lockLevel
		if idx >= len(lockSteps) {
			idx = len(lockSteps) - 1
		}
		b.lockedTill = time.Now().Add(lockSteps[idx])
		b.lockLevel++
		// The reference resets the counter after every lock: the next lock
		// requires another five failures, not one.
		b.fails = 0
	}
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.state, key)
	l.mu.Unlock()
}

// ---------------------------------------------------------------- cookies

func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, forceSecure bool) {
	secure := forceSecure || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: SessionMaxAge,
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

func SessionToken(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	return ""
}

// ---------------------------------------------------------------- base64url

func base64URLEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func base64URLDecode(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
