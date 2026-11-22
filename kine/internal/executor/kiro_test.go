package executor

import (
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

func kiroTransport() *providerdata.Transport {
	return &providerdata.Transport{BaseURLs: []string{
		"https://runtime.us-east-1.kiro.dev/generateAssistantResponse",
		"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
		"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
	}}
}

func TestKiroURLPrefersQ(t *testing.T) {
	got := kiroURL("fallback", kiroTransport())
	if got != "https://q.us-east-1.amazonaws.com/generateAssistantResponse" {
		t.Fatalf("got %q", got)
	}
}

func TestKiroRegionalize(t *testing.T) {
	q := "https://q.us-east-1.amazonaws.com/generateAssistantResponse"
	if got := kiroRegionalize(q, "eu-central-1"); got != "https://q.eu-central-1.amazonaws.com/generateAssistantResponse" {
		t.Fatalf("covered region not spliced: %q", got)
	}
	// codewhisperer only covers us-east-1.
	cw := "https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse"
	if got := kiroRegionalize(cw, "eu-central-1"); got != cw {
		t.Fatalf("uncovered region must not retarget the host: %q", got)
	}
	// Injection attempt: a region that is not a plain region slug is ignored.
	if got := kiroRegionalize(q, "x.amazonaws.com/evil"); got != q {
		t.Fatalf("crafted region retargeted the host: %q", got)
	}
	if got := kiroRegionalize("https://runtime.us-east-1.kiro.dev/x", "eu-central-1"); got != "https://runtime.us-east-1.kiro.dev/x" {
		t.Fatalf("non-amazon host rewritten: %q", got)
	}
}

func TestKiroHeaders(t *testing.T) {
	cw := "https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse"
	q := "https://q.us-east-1.amazonaws.com/generateAssistantResponse"

	h := kiroHeaders(Credentials{AuthMethod: "api_key", AccessToken: "k"}, q)
	if h["Authorization"] != "Bearer k" || h["TokenType"] != "API_KEY" {
		t.Fatalf("api_key headers: %+v", h)
	}
	if _, ok := h["X-Amz-Target"]; ok {
		t.Fatalf("target header must be codewhisperer-only: %+v", h)
	}
	if h["x-amzn-kiro-agent-mode"] != "spec" || h["x-amzn-codewhisperer-machine-id"] != "kiro-desktop" {
		t.Fatalf("markers missing: %+v", h)
	}

	h = kiroHeaders(Credentials{AuthMethod: "external_idp", AccessToken: "t", ProfileARN: "arn:p"}, cw)
	if h["TokenType"] != "EXTERNAL_IDP" || h["X-Amz-Target"] != kiroCodewhispererTarget {
		t.Fatalf("external_idp headers: %+v", h)
	}
	if h["x-amzn-codewhisperer-profile-arn"] != "arn:p" {
		t.Fatalf("profile arn missing: %+v", h)
	}

	h = kiroHeaders(Credentials{AuthMethod: "social", AccessToken: "t"}, q)
	if _, ok := h["TokenType"]; ok {
		t.Fatalf("social tokens must not claim a TokenType: %+v", h)
	}
}
