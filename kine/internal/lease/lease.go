// Package lease borrows the MiniMax Code desktop app's sign-in over its local
// auth-lease pipe. Ported from open-sse/services/minimaxCodeLease.js.
//
// MiniMax refresh tokens are single-use, so a router that holds its own token
// invalidates the desktop app's. Lease mode instead asks the running desktop
// broker for a short-lived access token, which the broker keeps refreshing.
// No credential file is ever read — only the broker-published capability file
// and the pipe itself.
//
// Security: the capability value comes from a file inside the desktop app's
// data dir; a caller-supplied data dir is only accepted from the dashboard
// session and must be absolute. Nothing here is exposed to client requests.
//
// ponytail: unix sockets only. The Windows broker uses a named pipe
// (\\.\pipe\mcode-auth-lease-<sha256(dataDir)>), which needs go-winio; add it
// when this server ships on Windows.
package lease

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion   = 1
	MaxFrameBytes     = 64 * 1024
	MaxMinValidityMs  = 5 * 60 * 1000
	SocketFile        = "mcode-auth-lease-v1.sock"
	CapabilityFile    = "mcode-auth-lease-v1.cap"
	refreshMarginMs   = 60 * 1000
	defaultDataDirSub = ".minimax"
)

// Code is the broker's error taxonomy; surfaced to the dashboard verbatim.
type Code string

const (
	CodeAuthRequired       Code = "AUTH_REQUIRED"
	CodeBrokerUnavailable  Code = "BROKER_UNAVAILABLE"
	CodeCapabilityRejected Code = "CAPABILITY_REJECTED"
	CodeProtocolMismatch   Code = "PROTOCOL_MISMATCH"
	CodeInvalidRequest     Code = "INVALID_REQUEST"
	CodeInternal           Code = "INTERNAL_ERROR"
)

var messages = map[Code]string{
	CodeAuthRequired:       "Desktop authentication is required.",
	CodeBrokerUnavailable:  "Desktop authentication integration is unavailable.",
	CodeCapabilityRejected: "Desktop authentication capability was rejected.",
	CodeProtocolMismatch:   "Desktop authentication protocol is incompatible.",
	CodeInvalidRequest:     "Desktop authentication request is invalid.",
	CodeInternal:           "Desktop authentication request failed.",
}

type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if m, ok := messages[e.Code]; ok {
		return m
	}
	return "auth lease failed"
}

func errf(c Code, format string, a ...any) *Error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, a...)}
}

// ResolveDataDir picks the desktop app's data dir: per-connection override →
// env → the default install location.
func ResolveDataDir(override string) string {
	if s := strings.TrimSpace(override); s != "" {
		if abs, err := filepath.Abs(s); err == nil {
			return abs
		}
		return s
	}
	if s := strings.TrimSpace(os.Getenv("MINIMAX_DESKTOP_DATA_DIR")); s != "" {
		if abs, err := filepath.Abs(s); err == nil {
			return abs
		}
		return s
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, defaultDataDirSub)
}

// DefaultDataDir is the install location without any override.
func DefaultDataDir() string { return ResolveDataDir("") }

var capabilityRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func endpointOf(dataDir string) (socket, capability string, err error) {
	if !filepath.IsAbs(dataDir) {
		return "", "", errf(CodeInvalidRequest, "dataDir must be an absolute path.")
	}
	canonical := filepath.Clean(dataDir)
	run := filepath.Join(canonical, "run")
	return filepath.Join(run, SocketFile), filepath.Join(run, CapabilityFile), nil
}

func readCapability(capabilityFile string) (string, error) {
	raw, err := os.ReadFile(capabilityFile)
	if err != nil {
		return "", errf(CodeBrokerUnavailable,
			"Cannot read capability file %s. Is the MiniMax Code desktop app installed and logged in on this machine? (%v)",
			capabilityFile, err)
	}
	cap := strings.TrimSpace(string(raw))
	if !capabilityRe.MatchString(cap) {
		return "", errf(CodeBrokerUnavailable, "Capability file has unexpected content.")
	}
	return cap, nil
}

// Status is the broker's own view of its sign-in.
type Status struct {
	Status      string `json:"status"`
	Generation  int64  `json:"generation"`
	ExpiresAtMs int64  `json:"expiresAtMs"`
}

// Token is one leased access token.
type Token struct {
	AccessToken string `json:"accessToken"`
	ExpiresAtMs int64  `json:"expiresAtMs"`
	Generation  int64  `json:"generation"`
}

type request struct {
	Version       int    `json:"version"`
	RequestID     string `json:"requestId"`
	Capability    string `json:"capability"`
	Method        string `json:"method"`
	MinValidityMs int64  `json:"minValidityMs,omitempty"`
}

type response struct {
	OK     bool  `json:"ok"`
	Error  *body `json:"error"`
	Result *body `json:"result"`
}

type body struct {
	Code        string `json:"code"`
	Status      string `json:"status"`
	Generation  int64  `json:"generation"`
	ExpiresAtMs int64  `json:"expiresAtMs"`
	AccessToken string `json:"accessToken"`
}

func encodeFrame(v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, errf(CodeInvalidRequest, "frame encode failed: %v", err)
	}
	if len(payload) == 0 || len(payload) > MaxFrameBytes {
		return nil, errf(CodeInvalidRequest, "Frame payload out of bounds.")
	}
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out, uint32(len(payload)))
	copy(out[4:], payload)
	return out, nil
}

// send writes one framed request and returns the first framed response.
func send(socket string, req request, timeout time.Duration) (*response, error) {
	conn, err := net.DialTimeout("unix", socket, timeout)
	if err != nil {
		return nil, errf(CodeBrokerUnavailable,
			"Cannot reach auth broker at %s: %v. Is the MiniMax Code desktop app running?", socket, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	frame, err := encodeFrame(req)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(frame); err != nil {
		return nil, errf(CodeBrokerUnavailable, "Broker closed the connection early.")
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, errf(CodeBrokerUnavailable, "Broker did not respond within %s.", timeout)
	}
	n := binary.BigEndian.Uint32(header)
	if n == 0 || n > MaxFrameBytes {
		return nil, errf(CodeProtocolMismatch, "Broker sent an invalid frame length.")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, errf(CodeProtocolMismatch, "Broker sent a truncated frame.")
	}
	var out response
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, errf(CodeProtocolMismatch, "Broker sent a non-JSON frame.")
	}
	if !out.OK {
		code := CodeInternal
		if out.Error != nil {
			if _, known := messages[Code(out.Error.Code)]; known {
				code = Code(out.Error.Code)
			}
		}
		return nil, &Error{Code: code}
	}
	if out.Result == nil {
		return nil, &Error{Code: CodeInternal}
	}
	return &out, nil
}

func requestID(prefix string) string {
	return fmt.Sprintf("%s-%x", prefix, time.Now().UnixNano())
}

// RequestStatus asks the broker whether it is signed in. Read-only.
func RequestStatus(dataDir string) (*Status, error) {
	socket, capabilityFile, err := endpointOf(dataDir)
	if err != nil {
		return nil, err
	}
	capability, err := readCapability(capabilityFile)
	if err != nil {
		return nil, err
	}
	res, err := send(socket, request{
		Version: ProtocolVersion, RequestID: requestID("kr-status"), Capability: capability, Method: "status",
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &Status{Status: res.Result.Status, Generation: res.Result.Generation, ExpiresAtMs: res.Result.ExpiresAtMs}, nil
}

// RequestLease asks for a token valid for at least minValidityMs (clamped).
func RequestLease(dataDir string, minValidityMs int64) (*Token, error) {
	socket, capabilityFile, err := endpointOf(dataDir)
	if err != nil {
		return nil, err
	}
	capability, err := readCapability(capabilityFile)
	if err != nil {
		return nil, err
	}
	clamped := minValidityMs
	if clamped < 0 {
		clamped = 0
	}
	if clamped > MaxMinValidityMs {
		clamped = MaxMinValidityMs
	}
	res, err := send(socket, request{
		Version: ProtocolVersion, RequestID: requestID("kr-lease"), Capability: capability,
		Method: "lease", MinValidityMs: clamped,
	}, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if res.Result.AccessToken == "" {
		return nil, &Error{Code: CodeAuthRequired}
	}
	return &Token{
		AccessToken: res.Result.AccessToken,
		ExpiresAtMs: res.Result.ExpiresAtMs,
		Generation:  res.Result.Generation,
	}, nil
}

// manager caches one leased token per data dir (cn/global connections sharing a
// desktop install share the lease) and single-flights concurrent requests.
type manager struct {
	mu       sync.Mutex
	cached   *Token
	inflight chan struct{}
}

var (
	managersMu sync.Mutex
	managers   = map[string]*manager{}
)

func managerFor(dataDir string) *manager {
	managersMu.Lock()
	defer managersMu.Unlock()
	m, ok := managers[dataDir]
	if !ok {
		m = &manager{}
		managers[dataDir] = m
	}
	return m
}

// TokenFor returns a cached lease when it stays fresh for another minute, else
// leases a new one. Errors are *Error with the broker's code.
func TokenFor(dataDir string) (*Token, error) {
	m := managerFor(dataDir)
	for {
		m.mu.Lock()
		now := time.Now().UnixMilli()
		if m.cached != nil && m.cached.ExpiresAtMs-now > refreshMarginMs {
			tok := m.cached
			m.mu.Unlock()
			return tok, nil
		}
		if m.inflight == nil {
			done := make(chan struct{})
			m.inflight = done
			m.mu.Unlock()
			tok, err := RequestLease(dataDir, MaxMinValidityMs)
			m.mu.Lock()
			if err == nil {
				m.cached = tok
			}
			m.inflight = nil
			m.mu.Unlock()
			close(done)
			return tok, err
		}
		done := m.inflight
		m.mu.Unlock()
		<-done
	}
}

// Invalidate drops the cached token (upstream 401: the desktop app rotated it).
func Invalidate(dataDir string) {
	m := managerFor(dataDir)
	m.mu.Lock()
	m.cached = nil
	m.mu.Unlock()
}

// dataDirDigest is the Windows pipe suffix; kept here so the naming rule lives
// next to the protocol constants.
func dataDirDigest(dataDir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dataDir))))
	return hex.EncodeToString(sum[:])[:16]
}

// IsLeaseAuth reports whether a stored connection borrows the desktop sign-in.
func IsLeaseAuth(authType string) bool { return authType == "mcode-lease" }

// ErrCode extracts the broker code from an error, or CodeInternal.
func ErrCode(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}
