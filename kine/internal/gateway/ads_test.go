package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

func TestPendingAdSingleUse(t *testing.T) {
	storePendingAd("conn-1", "ad-1", freePiAd{AdID: "ad-1"})
	if _, ok := takePendingAd("conn-1", "ad-2"); ok {
		t.Fatal("a mismatched ad id must not consume the pending creative")
	}
	raw, ok := takePendingAd("conn-1", "ad-1")
	if !ok {
		t.Fatal("the served creative should be reportable once")
	}
	if ad, _ := raw.(freePiAd); ad.AdID != "ad-1" {
		t.Fatalf("took %q, want ad-1", ad.AdID)
	}
	if _, ok := takePendingAd("conn-1", "ad-1"); ok {
		t.Fatal("an impression must not be reportable twice")
	}
}

func TestFreePiPixelAllowlist(t *testing.T) {
	for _, good := range []string{"https://trygravity.ai/p", "https://px.trygravity.com/i"} {
		if !freePiPixelAllowed(good) {
			t.Fatalf("%s should be allowed", good)
		}
	}
	for _, bad := range []string{"", "http://trygravity.ai/p", "https://evil.example/p", "https://trygravity.ai.evil.example/p"} {
		if freePiPixelAllowed(bad) {
			t.Fatalf("%s should be refused", bad)
		}
	}
}

func TestAdOrigins(t *testing.T) {
	if got := freePiOrigin("https://api.freepi.ai/v1/chat/completions"); got != "https://api.freepi.ai" {
		t.Fatalf("free-pi origin = %q", got)
	}
	if got := freebuffOrigin(""); got != "https://www.codebuff.com" {
		t.Fatalf("freebuff origin = %q", got)
	}
	if freebuffAdID("https://x/a") == freebuffAdID("https://x/b") {
		t.Fatal("ad ids must differ per impression URL")
	}
}

// A served creative can be reported once; the stub upstream sees the fetch and
// exactly one impression.
func TestFreebuffAdServeAndImpression(t *testing.T) {
	impressions := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/ads":
			json.NewEncoder(w).Encode(map[string]any{"ads": []map[string]any{{
				"title": "Try it", "adText": "Body", "cta": "Go",
				"url": "https://example.com", "impUrl": "https://pixel.example/1",
			}}})
		case "/api/v1/ads/impression":
			impressions++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	g := testGateway(t)
	g.cliMachineID = "m1"
	conn := &store.Connection{Provider: "freebuff", AuthType: "oauth", IsActive: true,
		Data: map[string]any{"apiKey": "tok", "baseUrl": upstream.URL}}
	if err := g.store.UpsertConnection(conn); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	g.registerAdRoutes(mux)
	do := func(method, path string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"connectionId":"`+conn.ID+`","adId":"`+freebuffAdID("https://pixel.example/1")+`"}`))
		req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}

	code, body := do("GET", "/api/freebuff/ad?connectionId="+conn.ID)
	if code != 200 || !strings.Contains(body, `"adId":"fb-ad-`) {
		t.Fatalf("ad serve: %d %s", code, body)
	}
	if code, body := do("POST", "/api/freebuff/ad/impression"); code != 200 {
		t.Fatalf("impression: %d %s", code, body)
	}
	if impressions != 1 {
		t.Fatalf("upstream impressions = %d, want 1", impressions)
	}
	if code, _ := do("POST", "/api/freebuff/ad/impression"); code != 409 {
		t.Fatalf("second report: %d, want 409", code)
	}
	if impressions != 1 {
		t.Fatalf("second report reached upstream (%d)", impressions)
	}
}
