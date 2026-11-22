package executor

// Cursor transport: the ConnectRPC headers (including the Jyh-checksum
// x-cursor-checksum and the sha256 x-client-key, both derived from the bearer
// token) and the HTTP/2 duplex session the AgentService endpoint needs.
// Ported from open-sse/utils/cursorChecksum.js and the Cursor executor.

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

// CursorURL resolves the endpoint for a surface. The agent surface is a
// separate host and only speaks HTTP/2.
func CursorURL(agent bool) (string, error) {
	reg := providerdata.Get()
	entry := reg.ByID["cursor"]
	if entry == nil || entry.Transport == nil {
		return "", errors.New("cursor: registry entry missing")
	}
	if agent {
		if o := reg.OAuthFor("cursor"); o != nil {
			if ep, ok := o.Extra["agentEndpoint"].(string); ok && ep != "" {
				return strings.TrimRight(ep, "/") + "/agent.v1.AgentService/Run", nil
			}
		}
		return "", errors.New("cursor: agent endpoint not configured")
	}
	path := "/aiserver.v1.ChatService/StreamUnifiedChatWithTools"
	if p, ok := entry.Transport.Quirks["chatPath"].(string); ok && p != "" {
		path = p
	}
	return strings.TrimRight(entry.Transport.BaseURL, "/") + path, nil
}

// CursorHeaders builds the ConnectRPC header set. machineId is mandatory: the
// checksum is unverifiable without it, so failing closed beats sending a
// request the backend rejects.
func CursorHeaders(creds Credentials) (map[string]string, error) {
	machineID := creds.MachineID
	if machineID == "" {
		return nil, errors.New("cursor: machine ID is required")
	}
	token := creds.AccessToken
	if i := strings.Index(token, "::"); i >= 0 {
		token = token[i+2:]
	}
	if token == "" {
		return nil, errors.New("cursor: access token is required")
	}
	goos, arch := "linux", "x64"
	switch runtime.GOOS {
	case "windows":
		goos = "windows"
	case "darwin":
		goos = "macos"
	}
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}
	ghost := "true"
	if v, ok := creds.Extra["ghostMode"].(bool); ok && !v {
		ghost = "false"
	}
	// goTimeZone: same IANA name the IDE reports; falls back to UTC.
	tz := "UTC"
	if name := time.Local.String(); name != "" && name != "Local" {
		tz = name
	}
	return map[string]string{
		"authorization":               "Bearer " + token,
		"connect-accept-encoding":     "gzip",
		"connect-protocol-version":    "1",
		"Content-Type":                "application/connect+proto",
		"User-Agent":                  "connect-es/1.6.1",
		"x-amzn-trace-id":             "Root=" + cursorUUID(),
		"x-client-key":                cursorHash(token),
		"x-cursor-checksum":           cursorChecksum(machineID),
		"x-cursor-client-version":     "3.12.17",
		"x-cursor-client-commit":      "0fb762053c34788bb7760d5673f8a6d4c8589d50",
		"x-cursor-client-type":        "ide",
		"x-cursor-client-os":          goos,
		"x-cursor-client-arch":        arch,
		"x-cursor-client-device-type": "desktop",
		"x-cursor-config-version":     cursorUUID(),
		"x-cursor-timezone":           tz,
		"x-ghost-mode":                ghost,
		"x-request-id":                cursorUUID(),
		"x-session-id":                cursorSessionID(token),
	}, nil
}

func cursorHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// cursorSessionID is uuidv5(DNS, token).
func cursorSessionID(token string) string {
	ns := []byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	h := sha1.New()
	h.Write(ns)
	h.Write([]byte(token))
	sum := h.Sum(nil)[:16]
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// cursorChecksum is the Jyh cipher over the 6-byte big-endian timestamp
// (seconds since epoch), URL-safe base64 without padding, then the machine id.
func cursorChecksum(machineID string) string {
	ts := uint64(time.Now().Unix())
	b := [6]byte{
		byte(ts >> 40), byte(ts >> 32), byte(ts >> 24),
		byte(ts >> 16), byte(ts >> 8), byte(ts),
	}
	t := byte(165)
	for i := range b {
		b[i] = (b[i] ^ t) + byte(i)
		t = b[i]
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		a := b[i]
		var c byte
		bb := byte(0)
		if i+1 < len(b) {
			bb = b[i+1]
		}
		if i+2 < len(b) {
			c = b[i+2]
		}
		sb.WriteByte(alphabet[a>>2])
		sb.WriteByte(alphabet[((a&3)<<4)|(bb>>4)])
		if i+1 < len(b) {
			sb.WriteByte(alphabet[((bb&15)<<2)|(c>>6)])
		}
		if i+2 < len(b) {
			sb.WriteByte(alphabet[c&63])
		}
	}
	return sb.String() + machineID
}

func cursorUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// CursorSession is a duplex AgentService call: the response body streams while
// the caller keeps writing ack frames into the request body.
type CursorSession struct {
	*Response
	writer io.WriteCloser
	pipe   *io.PipeReader
}

// Write sends one frame upstream.
func (s *CursorSession) Write(frame []byte) error {
	_, err := s.writer.Write(frame)
	return err
}

// Close ends the request body and the response body.
func (s *CursorSession) Close() {
	if s.writer != nil {
		s.writer.Close()
	}
	if s.pipe != nil {
		s.pipe.Close()
	}
	if s.Response != nil && s.Response.Body != nil {
		s.Response.Body.Close()
	}
}

// CursorAgentSession opens the h2 duplex call. An account behind a proxy is
// refused rather than dialed directly: the duplex path cannot carry the proxy
// and a direct socket would leak the host's real IP.
func (e *Executor) CursorAgentSession(ctx context.Context, req Request, endpoint string) (*CursorSession, error) {
	if req.Creds.ProxyURL != "" {
		return nil, errors.New("cursor: AgentService cannot ride a proxy; refusing a direct connection")
	}
	headers, err := CursorHeaders(req.Creds)
	if err != nil {
		return nil, err
	}
	pipeR, pipeW := io.Pipe()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pipeR)
	if err != nil {
		pipeR.Close()
		return nil, err
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	if _, err := url.Parse(endpoint); err != nil {
		pipeR.Close()
		return nil, err
	}
	resp, err := e.client.Do(httpReq)
	if err != nil {
		pipeR.Close()
		return nil, err
	}
	return &CursorSession{
		Response: &Response{Status: resp.StatusCode, Header: resp.Header, Body: resp.Body},
		writer:   pipeW,
		pipe:     pipeR,
	}, nil
}
