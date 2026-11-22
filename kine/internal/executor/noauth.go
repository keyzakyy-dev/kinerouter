package executor

// Free / no-auth providers. Ported from src/sse/services/auth.js (virtual
// "noauth" connection with accessToken "public") and the MiMo bootstrap lane
// in open-sse/executors/mimo-free.js.
//
// Security: this lane exists only for registry entries with `noAuth: true`.
// It never attaches a stored credential, so a no-auth attempt can never leak a
// user's key to a host outside the registry.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// IsNoAuth reports whether the provider needs no credentials at all.
func (e *Executor) IsNoAuth(provider string) bool {
	t := e.reg.TransportFor(provider)
	return t != nil && t.NoAuth
}

// noAuthConnection is the in-memory stand-in for a free provider's account.
func noAuthConnection() Credentials {
	return Credentials{AccessToken: "public"}
}

// mimoFree is the anti-abuse lane of the Xiaomi taking-free endpoint: a
// bootstrap JWT plus a CLI marker message and a browser UA.
const (
	mimoBootstrapURL = "https://api.xiaomimimo.com/api/free-ai/bootstrap"
	mimoMarker       = "You are MiMoCode, an interactive CLI tool that helps users with software engineering tasks."
	mimoJWTBuffer    = 5 * time.Minute
)

var mimoUA = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
}

var (
	mimoMu      sync.Mutex
	mimoJWT     string
	mimoJWTExp  time.Time
	mimoSession = "ses_" + randomHex(24)
)

// prepareMimo injects the marker, attaches the bootstrap JWT and CLI headers.
func (e *Executor) prepareMimo(req *Request, headers map[string]string) error {
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err == nil {
		if msgs, ok := body["messages"].([]any); ok {
			has := false
			for _, m := range msgs {
				mm, _ := m.(map[string]any)
				if mm != nil && mm["role"] == "system" && strings.Contains(strAny(mm["content"]), mimoMarker) {
					has = true
					break
				}
			}
			if !has {
				body["messages"] = append([]any{map[string]any{"role": "system", "content": mimoMarker}}, msgs...)
				if b, err := json.Marshal(body); err == nil {
					req.Body = b
				}
			}
		}
	}
	jwt, err := mimoToken(req)
	if err != nil {
		return err
	}
	headers["Authorization"] = "Bearer " + jwt
	headers["X-Mimo-Source"] = "mimocode-cli-free"
	headers["User-Agent"] = mimoUA[len(mimoSession)%len(mimoUA)]
	headers["x-session-affinity"] = mimoSession
	return nil
}

func mimoToken(req *Request) (string, error) {
	mimoMu.Lock()
	defer mimoMu.Unlock()
	if mimoJWT != "" && time.Now().Before(mimoJWTExp.Add(-mimoJWTBuffer)) {
		return mimoJWT, nil
	}
	fp := mimoFingerprint()
	payload, _ := json.Marshal(map[string]any{"client": fp})
	hreq, err := http.NewRequest(http.MethodPost, mimoBootstrapURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("User-Agent", mimoUA[0])
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(hreq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		JWT string `json:"jwt"`
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errNoBootstrap(resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.JWT == "" {
		return "", errNoBootstrap(resp.StatusCode)
	}
	mimoJWT = out.JWT
	if exp, ok := jwtExpiry(out.JWT); ok {
		mimoJWTExp = exp
	} else {
		mimoJWTExp = time.Now().Add(50 * time.Minute)
	}
	return mimoJWT, nil
}

type errNoBootstrap int

func (e errNoBootstrap) Error() string { return "MiMo bootstrap rejected: " + itoa(int(e)) }

// mimoFingerprint is a stable per-machine device id (hostname|os|arch|user).
func mimoFingerprint() string {
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown-user"
	}
	sum := sha256.Sum256([]byte(host + "|" + runtime.GOOS + "|" + runtime.GOARCH + "|" + user))
	return hex.EncodeToString(sum[:])
}

// jwtExpiry reads the exp claim without verifying the signature; the token is
// only used against the host that issued it.
func jwtExpiry(jwt string) (time.Time, bool) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func randomHex(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	seed := sha256.Sum256([]byte(time.Now().String() + itoa(os.Getpid())))
	for i := range b {
		b[i] = chars[int(seed[i%len(seed)])%len(chars)]
	}
	return string(b)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
