package mitm

// The privileged MITM server: TLS on :443 with per-SNI leaf certificates,
// passthrough to the real upstreams (bypassing our own /etc/hosts entries), and
// per-tool intercepts that re-route chat turns through the local KineRouter.
// Mirrors src/mitm/server.js + src/mitm/handlers/*.
//
// Security: this process runs as root and terminates TLS for the tool hosts,
// so it sees those tools' own credentials. It never stores or logs them —
// request bodies are forwarded (or translated), headers are stripped down to a
// fixed allowlist before anything reaches the router, and the router URL comes
// from an operator setting, never from request data.

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Run is the `kinerouter mitm-server` body. Blocks until SIGTERM/SIGINT.
func Run(p Paths) error {
	if err := EnsureRootCA(p); err != nil {
		return fmt.Errorf("Root CA not found: %w", err)
	}
	srv := &server{
		paths:      p,
		apiKey:     os.Getenv("ROUTER_API_KEY"),
		routerBase: strings.TrimRight(envOr("MITM_ROUTER_BASE", defaultRouterBase), "/"),
		client:     newUpstreamClient(),
	}
	rootDER, err := rootCertDER(p)
	if err != nil {
		return err
	}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = "localhost"
			}
			if c, ok := srv.certCache.Load(name); ok {
				return c.(*tls.Certificate), nil
			}
			leaf, err := p.LeafFor(name)
			if err != nil {
				return nil, err
			}
			leaf.Certificate = append(leaf.Certificate, rootDER)
			srv.certCache.Store(name, leaf)
			return leaf, nil
		},
	}

	hc := &http.Server{
		Handler:           srv,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(mitmPort))
	if err != nil {
		return fmt.Errorf("listen :%d: %w", mitmPort, err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		// Strip tool hosts so other apps aren't broken after exit.
		RemoveAllDNS(envOr("SUDO_PASSWORD", ""))
		hc.Close()
	}()

	err = hc.ServeTLS(ln, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type server struct {
	paths      Paths
	apiKey     string
	routerBase string
	client     *http.Client
	certCache  sync.Map
}

const healthPath = "/_mitm_health"

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == healthPath {
		writeJSONRaw(w, http.StatusOK, map[string]any{"ok": true, "pid": os.Getpid()})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeJSONRaw(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
		return
	}

	// Anti-loop: our own gateway's outbound requests go straight through.
	if r.Header.Get("x-request-source") == "local" {
		s.passthrough(w, r, body)
		return
	}
	tool := ToolForHost(r.Host)
	if tool == "" || !IsChatRequest(tool, r) {
		s.passthrough(w, r, body)
		return
	}
	switch tool {
	case "cursor":
		writeJSONRaw(w, http.StatusNotImplemented, map[string]any{
			"error": map[string]any{"message": "Cursor MITM support is coming soon.", "type": "not_implemented"},
		})
		return
	}
	model := ExtractModel(r.URL.RequestURI(), body)
	// Tab-autocomplete and friends must stay native: latency-critical.
	if model != "" && NoMap(tool, model) {
		s.passthrough(w, r, body)
		return
	}
	mapped := GetMappedModel(LoadAliases(s.paths.AliasFile), tool, model)
	if mapped == "" {
		s.passthrough(w, r, body)
		return
	}
	switch tool {
	case "antigravity":
		s.interceptAntigravity(w, r, body, mapped)
	case "copilot":
		s.interceptCopilot(w, r, body, mapped)
	case "kiro":
		s.interceptKiro(w, r, body, mapped)
	}
}

// ---------------------------------------------------------------- passthrough

func (s *server) passthrough(w http.ResponseWriter, r *http.Request, body []byte) {
	originalHost := stripPort(r.Host)
	chat := strings.Contains(r.URL.RequestURI(), ":generateContent") ||
		strings.Contains(r.URL.RequestURI(), ":streamGenerateContent")
	target := originalHost
	if chat {
		if h, ok := hostRewrite[originalHost]; ok {
			target = h
		}
	}

	headers := r.Header.Clone()
	headers.Set("Host", target)
	bodyOut := body
	if ToolForHost(r.Host) == "antigravity" {
		bodyOut, headers = applyAntigravityVersionOverride(body, headers, r.URL.RequestURI())
	}
	if !bytes.Equal(bodyOut, body) {
		headers.Set("Content-Length", strconv.Itoa(len(bodyOut)))
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, "https://"+target+r.URL.RequestURI(), bytes.NewReader(bodyOut))
	if err != nil {
		writeJSONRaw(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	req.Header = headers
	req.ContentLength = int64(len(bodyOut))

	resp, err := s.client.Do(req)
	if err != nil {
		writeJSONRaw(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error(), "type": "mitm_error"}})
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if strings.EqualFold(k, "content-length") || strings.EqualFold(k, "transfer-encoding") ||
			strings.EqualFold(k, "connection") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flushCopy(w, resp.Body)
}

var hostRewrite = map[string]string{
	// PROD cloudcode is rate-limited (429); the dev endpoint takes the same body.
	"cloudcode-pa.googleapis.com": "daily-cloudcode-pa.googleapis.com",
}

func stripPort(h string) string {
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i:], "]") {
		return h[:i]
	}
	return h
}

// newUpstreamClient resolves hostnames through a public resolver so our own
// /etc/hosts redirect is bypassed. TLS verification is off: this process is
// the client for hosts whose real certificates cannot be validated anyway
// (the redirect is deliberate), and nothing is trusted back from them except
// the tool's own API response.
func newUpstreamClient() *http.Client {
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}},
		ForceAttemptHTTP2:   false,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip, err := resolvePublic(ctx, host)
			if err != nil {
				return nil, err
			}
			d := &net.Dialer{Timeout: 15 * time.Second}
			return d.DialContext(ctx, network, net.JoinHostPort(ip, port))
		},
		ResponseHeaderTimeout: 120 * time.Second,
	}
	return &http.Client{Transport: tr}
}
