package executor

// Free Pi (api.freepi.ai). Openai-shaped passthrough; upstream binds each
// completion to an account-stable x-session-id and enforces a minimum
// x-client-version, and clears a 409 concurrent_session with POST /session/reset.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const freePiOrigin = "https://api.freepi.ai"

// freePiSessionNamespace is the uuidv5 namespace for derived session ids; it
// must never change or every existing upstream session is orphaned.
var freePiSessionNamespace = uuid.MustParse("6f70656e-7069-2d66-7265-6570692d7365")

// freePiSessionID derives one session per account: a reconnect or restart
// converges on the same id instead of spending another of the account's four.
func freePiSessionID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return uuid.NewSHA1(freePiSessionNamespace, sum[:]).String()
}

var (
	freePiVersionMu  sync.Mutex
	freePiVersionVal string
	freePiVersionExp time.Time
)

// freePiClientVersion returns the server's published latest CLI version. A cold
// cache is filled once; a failed fetch leaves the compiled-in floor in place.
func freePiClientVersion(ctx context.Context) string {
	freePiVersionMu.Lock()
	defer freePiVersionMu.Unlock()
	if freePiVersionVal != "" && time.Now().Before(freePiVersionExp) {
		return freePiVersionVal
	}
	const fallback = "0.7"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, freePiOrigin+"/client-version", nil)
	if err != nil {
		return fallback
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	var out struct {
		Latest string `json:"latest"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.Latest == "" {
		return fallback
	}
	freePiVersionVal, freePiVersionExp = out.Latest, time.Now().Add(10*time.Minute)
	return out.Latest
}

// freePiHeaders is the registry auth hook: the two per-account headers.
func freePiHeaders(req Request, headers map[string]string) {
	token := firstNonEmpty(req.Creds.APIKey, req.Creds.AccessToken)
	if token == "" {
		return
	}
	headers["x-session-id"] = freePiSessionID(token)
	headers["x-client-version"] = freePiClientVersion(context.Background())
}

// freePiResetConcurrent releases the account's other sessions after a 409
// concurrent_session. Returns true when the retry is worth attempting.
func freePiResetConcurrent(ctx context.Context, client *http.Client, token string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, freePiOrigin+"/session/reset", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode == http.StatusOK
}

func freePiIsConcurrent(body []byte) bool {
	return strings.Contains(string(body), "concurrent_session")
}
