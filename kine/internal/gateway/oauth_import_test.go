package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func reqWith(body string) *http.Request {
	return httptest.NewRequest("POST", "/", strings.NewReader(body))
}

// JWT identity extraction backs every import path that has no separate email
// field. Malformed tokens must degrade to "" rather than panic.
func TestJWTIdentity(t *testing.T) {
	// {"email":"a@b.c","https://api.openai.com/auth":{"chatgpt_account_id":"acc1",
	//  "chatgpt_plan_type":"plus"}} — unsigned, payload only is read.
	tok := "h.eyJlbWFpbCI6ImFAYi5jIiwiaHR0cHM6Ly9hcGkub3BlbmFpLmNvbS9hdXRoIjp7ImNoYXRncHRfYWNjb3VudF9pZCI6ImFjYzEiLCJjaGF0Z3B0X3BsYW5fdHlwZSI6InBsdXMifX0.s"
	if got := jwtEmail(tok); got != "a@b.c" {
		t.Fatalf("jwtEmail = %q", got)
	}
	c := codexConnFrom(tok, "")
	if c.Email != "a@b.c" {
		t.Fatalf("email not backfilled: %q", c.Email)
	}
	psd := c.Specific()
	if psd["chatgptAccountId"] != "acc1" || psd["chatgptPlanType"] != "plus" {
		t.Fatalf("claims not carried: %v", psd)
	}
	// Non-JWT input must not blow up.
	if got := jwtEmail("not-a-jwt"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if len(jwtPayload("a.b")) != 0 {
		t.Fatal("partial token should decode to empty map")
	}
}

// Region is interpolated into AWS OIDC / Amazon Q hosts, so anything outside
// the region grammar must be rejected (GHSA-6mwv-4mrm-5p3m).
func TestValidRegion(t *testing.T) {
	for _, ok := range []string{"", "us-east-1", "eu-west-2", "ap-southeast-12"} {
		if _, valid := validRegion(ok); !valid {
			t.Fatalf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"evil.com", "us-east-1/x", "US-EAST-1", "us-east-1?x=1", "../../"} {
		if _, valid := validRegion(bad); valid {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// Client-supplied base URLs (gitlab, xiaomi) must not reach internal hosts.
func TestCheckPublicURL(t *testing.T) {
	for _, bad := range []string{"http://127.0.0.1:8080", "http://169.254.169.254", "file:///etc/passwd", "gopher://x"} {
		if err := checkPublicURL(bad); err == nil {
			t.Fatalf("%q allowed", bad)
		}
	}
	if err := checkPublicURL("https://8.8.8.8"); err != nil {
		t.Fatalf("public host rejected: %v", err)
	}
}

// normalizeAccountList accepts the three shapes the bulk-import routes document.
func TestNormalizeAccountShapes(t *testing.T) {
	cases := []string{`[{"accessToken":"a"},{"accessToken":"b"}]`, `{"accounts":[{"accessToken":"a"}]}`, `{"accessToken":"a"}`}
	for _, body := range cases {
		list, err := normalizeAccountList(reqWith(body))
		if err != nil || len(list) == 0 {
			t.Fatalf("%s → %v %v", body, list, err)
		}
	}
	if _, err := normalizeAccountList(reqWith(`[]`)); err == nil {
		t.Fatal("empty list accepted")
	}
	if _, err := normalizeAccountList(reqWith(`"x"`)); err == nil {
		t.Fatal("scalar accepted")
	}
}
