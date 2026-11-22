package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
)

// A fetch() to an unimplemented dashboard route must get JSON, not the SPA
// shell: HTML makes JSON.parse throw and hides the real 404.
func TestUnknownAPIPathsAnswerJSON(t *testing.T) {
	g := testGateway(t)
	g.cfg = &config.Config{JWTSecret: []byte("test"), InitialPassword: "x"}
	g.static = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html>"))
	})
	h := g.Handler()

	for _, path := range []string{"/api/does-not-exist", "/v1/does-not-exist"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("{}")))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s: status %d, want 404", method, path, rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Fatalf("%s %s: content-type %q, want JSON", method, path, ct)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s: body is not JSON: %v", method, path, err)
			}
		}
	}
}

// The SPA still serves client-side routes, which is what the fallback is for.
func TestSPAFallbackStillServesDashboardRoutes(t *testing.T) {
	g := testGateway(t)
	g.static = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html>"))
	})
	h := g.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/providers", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Fatalf("SPA route: status %d body %q", rec.Code, rec.Body.String())
	}
}
