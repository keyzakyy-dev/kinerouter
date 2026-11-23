package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Role-only frames are not content: nothing reaches the client until a real
// delta arrives.
func TestStreamPeekerBuffersUntilContent(t *testing.T) {
	rec := httptest.NewRecorder()
	p := newStreamPeeker(rec)
	p.Header().Set("Content-Type", "text/event-stream")
	p.WriteHeader(200)
	_, _ = p.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
	if rec.Body.Len() != 0 || rec.Code != 200 || rec.Body.String() != "" {
		t.Fatalf("committed early: body=%q", rec.Body.String())
	}
	_, _ = p.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	if err := p.Finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if !strings.Contains(rec.Body.String(), `"content":"hi"`) {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// A silent stream fails without committing, so the caller can fail over.
func TestStreamPeekerZeroContent(t *testing.T) {
	rec := httptest.NewRecorder()
	p := newStreamPeeker(rec)
	_, _ = p.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
	_, _ = p.Write([]byte("data: [DONE]\n\n"))
	if err := p.Finish(); err != errStreamingZeroContent {
		t.Fatalf("finish err = %v", err)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("zero-content stream committed: %q", rec.Body.String())
	}
}

// An in-band error frame is committed for the client and reported, never
// treated as zero content to retry.
func TestStreamPeekerInbandError(t *testing.T) {
	rec := httptest.NewRecorder()
	p := newStreamPeeker(rec)
	_, _ = p.Write([]byte("data: {\"error\":{\"message\":\"dead downstream\"}}\n\n"))
	if err := p.Finish(); err != errStreamingInbandError {
		t.Fatalf("finish err = %v", err)
	}
	if !strings.Contains(rec.Body.String(), "dead downstream") {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

func TestHasSSEContent(t *testing.T) {
	for chunk, want := range map[string]bool{
		`data: {"choices":[{"delta":{"content":"x"}}]}`:    true,
		`data: {"choices":[{"delta":{"content":""}}]}`:     false,
		`data: {"choices":[{"delta":{"reasoning":"r"}}]}`:  true,
		`data: {"delta":{"tool_calls":[{"id":"t"}]}}`:      true,
		`data: {"delta":{"tool_calls":[]}}`:                false,
		`data: {"type":"content_block_delta","delta":{"type":"thinking","thinking":"t"}}`: true,
		`data: {"response":{"output_text.delta":"x"}}`:     false,
	} {
		if got := hasSSEContent([]byte(chunk)); got != want {
			t.Errorf("hasSSEContent(%q) = %v, want %v", chunk, got, want)
		}
	}
}
