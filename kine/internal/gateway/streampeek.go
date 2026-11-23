package gateway

// Streaming zero-content guard. An upstream can answer 200 with an SSE stream
// that produces no assistant content (dead account, silent quota cut). Without
// buffering, the first byte commits the 200 to the client and failover is
// impossible. This peeker holds the response until content or an in-band error
// appears, matching 9router-go proxy/stream_peeker.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

var (
	// errStreamingZeroContent means the upstream stream ended without content.
	// The response was never committed, so the caller may try the next account.
	errStreamingZeroContent = errors.New("streaming upstream finished without content")
	// errStreamingInbandError means the stream carried a provider error event.
	errStreamingInbandError = errors.New("streaming upstream carried an in-band error")
)

// streamPeeker buffers initial SSE frames until client-visible content.
type streamPeeker struct {
	w          http.ResponseWriter
	flusher    http.Flusher
	header     http.Header
	status     int
	buf        bytes.Buffer
	committed  bool
	sawContent bool
	inband     bool
}

func newStreamPeeker(w http.ResponseWriter) *streamPeeker {
	f, _ := w.(http.Flusher)
	return &streamPeeker{w: w, flusher: f, header: http.Header{}, status: http.StatusOK}
}

func (p *streamPeeker) Header() http.Header {
	if p.committed {
		return p.w.Header()
	}
	return p.header
}

func (p *streamPeeker) WriteHeader(code int) {
	if p.committed {
		p.w.WriteHeader(code)
		return
	}
	p.status = code
}

func (p *streamPeeker) Write(b []byte) (int, error) {
	if p.committed {
		n, err := p.w.Write(b)
		if p.flusher != nil {
			p.flusher.Flush()
		}
		return n, err
	}
	if isInbandError(b) {
		p.sawContent = true
		p.inband = true
		p.commit()
		n, err := p.w.Write(b)
		if p.flusher != nil {
			p.flusher.Flush()
		}
		return n, err
	}
	if hasSSEContent(b) {
		p.sawContent = true
		p.commit()
		n, err := p.w.Write(b)
		if p.flusher != nil {
			p.flusher.Flush()
		}
		return n, err
	}
	return p.buf.Write(b)
}

func (p *streamPeeker) Flush() {
	if p.committed && p.flusher != nil {
		p.flusher.Flush()
	}
}

func (p *streamPeeker) commit() {
	if p.committed {
		return
	}
	p.committed = true
	for k, vv := range p.header {
		for _, v := range vv {
			p.w.Header().Add(k, v)
		}
	}
	p.w.WriteHeader(p.status)
	if p.buf.Len() > 0 {
		_, _ = p.w.Write(p.buf.Bytes())
		p.buf.Reset()
	}
	if p.flusher != nil {
		p.flusher.Flush()
	}
}

// Finish commits a healthy stream and reports why an unusable one never
// reached the client.
func (p *streamPeeker) Finish() error {
	switch {
	case p.inband:
		return errStreamingInbandError
	case !p.sawContent:
		return errStreamingZeroContent
	}
	p.commit()
	return nil
}

// hasSSEContent reports whether one chunk carries visible text, reasoning or a
// tool call. Role-only and empty frames do not count.
func hasSSEContent(chunk []byte) bool {
	for _, line := range bytes.Split(chunk, []byte("\n")) {
		if hasSSEContentLine(line) {
			return true
		}
	}
	return false
}

func hasSSEContentLine(s []byte) bool {
	nonEmpty := func(key string) bool {
		if !bytes.Contains(s, []byte(`"`+key+`":"`)) {
			return false
		}
		return !bytes.Contains(s, []byte(`"`+key+`":""`)) && !bytes.Contains(s, []byte(`"`+key+`":null`))
	}
	if nonEmpty("content") || nonEmpty("reasoning_content") || nonEmpty("reasoning") || nonEmpty("thinking") {
		return true
	}
	if bytes.Contains(s, []byte(`"tool_calls":[`)) && !bytes.Contains(s, []byte(`"tool_calls":[]`)) {
		return true
	}
	if bytes.Contains(s, []byte(`"type":"tool_use"`)) || bytes.Contains(s, []byte(`"type":"thinking"`)) ||
		bytes.Contains(s, []byte(`"redacted_thinking"`)) || bytes.Contains(s, []byte(`"signature_delta"`)) {
		return true
	}
	return bytes.Contains(s, []byte(`"response.output_text.delta"`)) ||
		bytes.Contains(s, []byte(`"response.output_item.added"`))
}

// isInbandError recognizes a data line whose top-level error field is present
// and non-null; a successful stream never carries one.
func isInbandError(chunk []byte) bool {
	for _, line := range bytes.Split(chunk, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
		if len(payload) == 0 || payload[0] != '{' || !bytes.Contains(payload, []byte(`"error"`)) {
			continue
		}
		var env struct {
			Error any `json:"error"`
		}
		if json.Unmarshal(payload, &env) == nil && env.Error != nil {
			return true
		}
	}
	return false
}
