package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Pricing PATCH validates fields, merges into kv, and DELETE resets.
func TestPricingRoutes(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	mux := http.NewServeMux()
	g.registerMiscAdminRoutes(mux)

	do := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}

	if code, _ := do("PATCH", "/api/pricing", `{"acme":{"m1":{"bogus":1}}}`); code != 400 {
		t.Fatalf("bogus field should be 400, got %d", code)
	}
	if code, _ := do("PATCH", "/api/pricing", `{"acme":{"m1":{"input":-1}}}`); code != 400 {
		t.Fatalf("negative should be 400, got %d", code)
	}
	code, body := do("PATCH", "/api/pricing", `{"acme":{"m1":{"input":2.5,"output":7}}}`)
	if code != 200 {
		t.Fatalf("patch: %d %s", code, body)
	}
	var userPricing map[string]map[string]map[string]float64
	json.Unmarshal([]byte(body), &userPricing)
	if userPricing["acme"]["m1"]["input"] != 2.5 || userPricing["acme"]["m1"]["output"] != 7 {
		t.Fatalf("user pricing: %s", body)
	}
	_, body = do("GET", "/api/pricing", "")
	var merged map[string]map[string]map[string]float64
	json.Unmarshal([]byte(body), &merged)
	if merged["acme"]["m1"]["input"] != 2.5 {
		t.Fatalf("merged: %s", body)
	}
	_, body = do("DELETE", "/api/pricing?provider=acme&model=m1", "")
	reset := map[string]map[string]map[string]float64{}
	json.Unmarshal([]byte(body), &reset)
	if _, ok := reset["acme"]["m1"]; ok {
		t.Fatalf("model survived reset: %s", body)
	}
}

// Combo presets preview and create seed the combos table once.
func TestComboPresets(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	mux := http.NewServeMux()
	g.registerMiscAdminRoutes(mux)

	req := httptest.NewRequest("GET", "/api/combos/presets?source=claude", nil)
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var preview struct {
		Items    []map[string]any `json:"items"`
		ToCreate int              `json:"toCreate"`
	}
	json.Unmarshal(rr.Body.Bytes(), &preview)
	if preview.ToCreate == 0 || len(preview.Items) == 0 {
		t.Fatalf("preview: %s", rr.Body.String())
	}

	req = httptest.NewRequest("POST", "/api/combos/presets", strings.NewReader(`{"source":"claude"}`))
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var created struct {
		CreatedCount int `json:"createdCount"`
	}
	json.Unmarshal(rr.Body.Bytes(), &created)
	if created.CreatedCount == 0 {
		t.Fatalf("create: %s", rr.Body.String())
	}

	// Second create skips everything that now exists.
	req = httptest.NewRequest("POST", "/api/combos/presets", strings.NewReader(`{"source":"claude"}`))
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	json.Unmarshal(rr.Body.Bytes(), &created)
	if created.CreatedCount != 0 {
		t.Fatalf("second create: %s", rr.Body.String())
	}

	if code, _ := do2(mux, g, "GET", "/api/combos/presets?source=nope"); code != 400 {
		t.Fatalf("bad source: %d", code)
	}
}

// Export → import round-trip keeps connections and API keys.
func TestDatabaseRoundTrip(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	mux := http.NewServeMux()
	g.registerMiscAdminRoutes(mux)

	if _, err := g.store.CreateAPIKey("k1", "mm", "sk-test-1"); err != nil {
		t.Fatal(err)
	}
	code, body := do2body(mux, g, "GET", "/api/settings/database", "")
	if code != 200 {
		t.Fatalf("export: %d %s", code, body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("export json: %v", err)
	}
	if _, ok := payload["settings"]; !ok {
		t.Fatalf("missing settings: %s", body)
	}
	keys, _ := payload["apiKeys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("apiKeys: %s", body)
	}
	if code, body = do2body(mux, g, "POST", "/api/settings/database", body); code != 200 {
		t.Fatalf("import: %d %s", code, body)
	}
	after, _ := g.store.ListAPIKeys()
	if len(after) != 1 {
		t.Fatalf("keys after import: %d", len(after))
	}
}

func do2(mux *http.ServeMux, g *Gateway, method, path string) (int, string) {
	return do2body(mux, g, method, path, "")
}

func do2body(mux *http.ServeMux, g *Gateway, method, path, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}
