package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/auth"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// Custom + disabled model admin round-trips through the HTTP surface.
func TestModelsAdminRoutes(t *testing.T) {
	g := testGateway(t)
	mux := http.NewServeMux()
	g.registerModelsAdminRoutes(mux)
	h := mux
	g.cliMachineID = "m1"

	do := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}

	code, body := do("POST", "/api/models/custom", `{"providerAlias":"acme","id":"m1","type":"llm","name":"M1","caps":{"vision":true,"bogus":true}}`)
	if code != 200 || !strings.Contains(body, `"added":true`) {
		t.Fatalf("add custom: %d %s", code, body)
	}
	_, body = do("GET", "/api/models/custom", "")
	var list struct {
		Models []map[string]any `json:"models"`
	}
	json.Unmarshal([]byte(body), &list)
	if len(list.Models) != 1 || list.Models[0]["name"] != "M1" {
		t.Fatalf("list custom: %s", body)
	}
	caps, _ := list.Models[0]["caps"].(map[string]any)
	if _, ok := caps["bogus"]; ok {
		t.Fatalf("bogus cap survived: %v", caps)
	}

	if code, body = do("POST", "/api/models/disabled", `{"providerAlias":"acme","ids":["m1","m1","m2"]}`); code != 200 {
		t.Fatalf("disable: %d %s", code, body)
	}
	_, body = do("GET", "/api/models/disabled?providerAlias=acme", "")
	if body != "{\"ids\":[\"m1\",\"m2\"]}\n" {
		t.Fatalf("disabled ids: %s", body)
	}
	if code, _ = do("DELETE", "/api/models/disabled?providerAlias=acme&id=m1", ""); code != 200 {
		t.Fatalf("enable one: %d", code)
	}
	_, body = do("GET", "/api/models/disabled?providerAlias=acme", "")
	if body != "{\"ids\":[\"m2\"]}\n" {
		t.Fatalf("after enable: %s", body)
	}
	if code, _ = do("DELETE", "/api/models/custom?providerAlias=acme&id=m1", ""); code != 200 {
		t.Fatalf("delete custom: %d", code)
	}
	_, body = do("GET", "/api/models/custom", "")
	if body != "{\"models\":[]}\n" {
		t.Fatalf("after delete: %s", body)
	}
}

// A stored model lock surfaces as a cooldown row and clearCooldown removes it.
func TestModelsAvailabilityCooldown(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	mux := http.NewServeMux()
	g.registerModelsAdminRoutes(mux)
	h := mux

	c := &store.Connection{Provider: "acme", AuthType: "oauth", IsActive: true,
		Data: map[string]any{"modelLock_m1": "2099-01-01T00:00:00Z", "lastError": "boom"}}
	if err := g.store.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/models/availability", nil)
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got struct {
		Models           []map[string]any `json:"models"`
		UnavailableCount int              `json:"unavailableCount"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.UnavailableCount != 1 || got.Models[0]["model"] != "m1" || got.Models[0]["status"] != "cooldown" {
		t.Fatalf("availability: %s", rr.Body.String())
	}

	req = httptest.NewRequest("POST", "/api/models/availability",
		strings.NewReader(`{"action":"clearCooldown","provider":"acme","model":"m1"}`))
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("clear: %d %s", rr.Code, rr.Body.String())
	}
	fresh, _ := g.store.GetConnection(c.ID)
	if fresh.Str("modelLock_m1") != "" {
		t.Fatalf("lock survived: %q", fresh.Str("modelLock_m1"))
	}
}

// The synthetic "__all" row (connection unavailable, no model lock) reports
// lastErrorAt and can be cleared, which re-marks the connection active.
func TestModelsAvailabilityClearAll(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	mux := http.NewServeMux()
	g.registerModelsAdminRoutes(mux)
	h := mux

	c := &store.Connection{Provider: "acme", AuthType: "oauth", IsActive: true,
		Data: map[string]any{"testStatus": "unavailable", "lastError": "402 no plan",
			"lastErrorAt": "2026-11-23T17:04:31Z"}}
	if err := g.store.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/models/availability", nil)
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got struct {
		Models []map[string]any `json:"models"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Models) != 1 || got.Models[0]["model"] != "__all" ||
		got.Models[0]["lastErrorAt"] != "2026-11-23T17:04:31Z" {
		t.Fatalf("availability: %s", rr.Body.String())
	}

	req = httptest.NewRequest("POST", "/api/models/availability",
		strings.NewReader(`{"action":"clearCooldown","provider":"acme","model":"__all"}`))
	req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("clear: %d %s", rr.Code, rr.Body.String())
	}
	fresh, _ := g.store.GetConnection(c.ID)
	if fresh.Str("testStatus") != "active" || fresh.Str("lastError") != "" {
		t.Fatalf("still unavailable: %q %q", fresh.Str("testStatus"), fresh.Str("lastError"))
	}
}

func cliTok(machineID string) string { return auth.CLIToken(machineID) }
