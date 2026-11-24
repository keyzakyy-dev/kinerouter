package gateway

// Sponsored banners for the two ad-funded providers.
//
// free-pi and freebuff pay for inference with advertising. Their upstreams serve
// a creative and accept an impression report; the dashboard renders the creative
// and reports the impression only after it was painted. A served creative is
// recorded here and consumed by its report, so a client can neither report an ad
// it was never shown nor report the same view twice.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	adTimeout    = 8 * time.Second
	adTTL        = 15 * time.Minute
	adMaxEntries = 64
)

// freePiImpressionHosts are the only hosts an ad tracking pixel may be fetched
// from; imp_url arrives upstream, so an unvalidated host is an SSRF primitive.
var freePiImpressionHosts = []string{"trygravity.ai", "trygravity.com"}

var adsClient = &http.Client{Timeout: adTimeout}

type adCreative struct {
	Title       string `json:"title,omitempty"`
	Body        string `json:"body,omitempty"`
	CTA         string `json:"cta,omitempty"`
	ImageURL    string `json:"imageUrl,omitempty"`
	SponsorName string `json:"sponsorName,omitempty"`
	BrandColor  string `json:"brandColor,omitempty"`
}

type freePiAd struct {
	AdID      string     `json:"adId"`
	ClickURL  string     `json:"clickUrl,omitempty"`
	Creative  adCreative `json:"creative"`
	Source    string     `json:"source,omitempty"`
	clickTok  string
	impURL    string
	fetchedAt time.Time
}

type freebuffAd struct {
	AdID      string     `json:"adId"`
	ClickURL  string     `json:"clickUrl,omitempty"`
	Creative  adCreative `json:"creative"`
	impURL    string
	fetchedAt time.Time
}

type pendingAd struct {
	id        string
	ad        any
	expiresAt time.Time
}

var (
	adsMu      sync.Mutex
	adsPending = map[string]pendingAd{}
)

// storePendingAd records a served creative, evicting expired and oldest entries.
func storePendingAd(connID, adID string, ad any) {
	adsMu.Lock()
	defer adsMu.Unlock()
	now := time.Now()
	for k, v := range adsPending {
		if now.After(v.expiresAt) {
			delete(adsPending, k)
		}
	}
	if _, ok := adsPending[connID]; !ok && len(adsPending) >= adMaxEntries {
		var oldestKey string
		var oldest time.Time
		for k, v := range adsPending {
			if oldestKey == "" || v.expiresAt.Before(oldest) {
				oldestKey, oldest = k, v.expiresAt
			}
		}
		delete(adsPending, oldestKey)
	}
	adsPending[connID] = pendingAd{id: adID, ad: ad, expiresAt: now.Add(adTTL)}
}

// takePendingAd consumes the creative matching adID; consuming makes a second
// report for the same ad impossible.
func takePendingAd(connID, adID string) (any, bool) {
	adsMu.Lock()
	defer adsMu.Unlock()
	p, ok := adsPending[connID]
	if !ok {
		return nil, false
	}
	if time.Now().After(p.expiresAt) {
		delete(adsPending, connID)
		return nil, false
	}
	if p.id != adID {
		return nil, false
	}
	delete(adsPending, connID)
	return p.ad, true
}

// adsConnection resolves a connection and its credential for the ad lanes.
func (g *Gateway) adsConnection(provider, connID string) (*store.Connection, string, string, bool) {
	if connID == "" {
		return nil, "", "", false
	}
	conn, err := g.store.GetConnection(connID)
	if err != nil || conn == nil || conn.Provider != provider {
		return nil, "", "", false
	}
	token := firstNonEmpty(conn.Str("apiKey"), conn.Str("accessToken"), conn.Str("authToken"))
	if token == "" {
		return nil, "", "", false
	}
	return conn, conn.Str("baseUrl"), token, true
}

func (g *Gateway) registerAdRoutes(mux *http.ServeMux) {
	r := g.requireAuth
	mux.HandleFunc("GET /api/free-pi/ad", r(g.handleGetFreePiAd))
	mux.HandleFunc("POST /api/free-pi/ad/impression", r(g.handleFreePiImpression))
	mux.HandleFunc("GET /api/freebuff/ad", r(g.handleGetFreebuffAd))
	mux.HandleFunc("POST /api/freebuff/ad/impression", r(g.handleFreebuffImpression))
}

// adImpression is the client report body both providers share.
type adImpression struct {
	ConnectionID string `json:"connectionId"`
	AdID         string `json:"adId"`
}

func readAdImpression(r *http.Request) (adImpression, error) {
	var body adImpression
	if r.Body == nil {
		return body, fmt.Errorf("connectionId and adId are required")
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err := json.Unmarshal(raw, &body); err != nil {
		return body, fmt.Errorf("connectionId and adId are required")
	}
	if body.ConnectionID == "" || body.AdID == "" {
		return body, fmt.Errorf("connectionId and adId are required")
	}
	return body, nil
}

// ---------------------------------------------------------------- free-pi

func (g *Gateway) handleGetFreePiAd(w http.ResponseWriter, r *http.Request) {
	connID := r.URL.Query().Get("connectionId")
	_, base, token, ok := g.adsConnection("free-pi", connID)
	if !ok {
		writeErr(w, http.StatusNotFound, "no free-pi connection with a credential for that id")
		return
	}
	ad, err := freePiFetchAd(r.Context(), token, base)
	if err != nil {
		logf("free-pi ad fetch failed (conn %s): %v", connID, err)
		writeJSON(w, http.StatusOK, map[string]any{"ad": nil})
		return
	}
	if ad == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ad": nil})
		return
	}
	storePendingAd(connID, ad.AdID, *ad)
	writeJSON(w, http.StatusOK, map[string]any{"ad": ad})
}

func (g *Gateway) handleFreePiImpression(w http.ResponseWriter, r *http.Request) {
	body, err := readAdImpression(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, ok := takePendingAd(body.ConnectionID, body.AdID)
	if !ok {
		writeErr(w, http.StatusConflict, "no matching unreported ad for this connection; an impression requires a creative served by this gateway")
		return
	}
	ad, ok := raw.(freePiAd)
	if !ok {
		writeErr(w, http.StatusConflict, "ad record does not match this provider")
		return
	}
	_, base, token, ok := g.adsConnection("free-pi", body.ConnectionID)
	if !ok {
		writeErr(w, http.StatusNotFound, "no free-pi connection with a credential for that id")
		return
	}
	if err := freePiReportImpression(r.Context(), token, base, ad); err != nil {
		logf("free-pi impression failed (conn %s): %v", body.ConnectionID, err)
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

// freePiOrigin maps a connection base URL to its API root.
func freePiOrigin(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions", "/v1"} {
		if trimmed := strings.TrimSuffix(base, suffix); trimmed != base {
			return trimmed
		}
	}
	if base == "" {
		return "https://api.freepi.ai"
	}
	return base
}

func freePiPixelAllowed(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range freePiImpressionHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func freePiFetchAd(ctx context.Context, token, baseURL string) (*freePiAd, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, freePiOrigin(baseURL)+"/ads/next?slot=banner", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := adsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("free-pi ad: HTTP %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var wire struct {
		AdID       string `json:"ad_id"`
		ClickToken string `json:"click_token"`
		ImpURL     string `json:"imp_url"`
		ClickURL   string `json:"click_url"`
		Source     string `json:"source"`
		Creative   struct {
			Title    string `json:"title"`
			Body     string `json:"body"`
			CTA      string `json:"cta"`
			ImageURL string `json:"image_url"`
		} `json:"creative"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("free-pi ad: %w", err)
	}
	if wire.AdID == "" {
		return nil, nil
	}
	return &freePiAd{
		AdID: wire.AdID, ClickURL: wire.ClickURL, Source: wire.Source,
		Creative: adCreative{Title: wire.Creative.Title, Body: wire.Creative.Body, CTA: wire.Creative.CTA, ImageURL: wire.Creative.ImageURL},
		clickTok: wire.ClickToken, impURL: wire.ImpURL, fetchedAt: time.Now(),
	}, nil
}

func freePiReportImpression(ctx context.Context, token, baseURL string, ad freePiAd) error {
	if ad.AdID == "" || ad.clickTok == "" {
		return fmt.Errorf("free-pi ad: impression needs an ad id and click token")
	}
	if ad.impURL != "" {
		if !freePiPixelAllowed(ad.impURL) {
			return fmt.Errorf("free-pi ad: refused impression pixel on a non-allowlisted host")
		}
		if err := firePixel(ctx, ad.impURL); err != nil {
			// The pixel is the ad network's own measurement; the server-side
			// record is what upstream gates on, so this stays best-effort.
			logf("free-pi impression pixel failed: %v", err)
		}
	}
	payload, _ := json.Marshal(map[string]any{"ad_id": ad.AdID, "click_token": ad.clickTok, "imp_fired": true})
	return postAdJSON(ctx, freePiOrigin(baseURL)+"/ads/impression", token, "", payload, "free-pi")
}

// ---------------------------------------------------------------- freebuff

func (g *Gateway) handleGetFreebuffAd(w http.ResponseWriter, r *http.Request) {
	connID := r.URL.Query().Get("connectionId")
	_, base, token, ok := g.adsConnection("freebuff", connID)
	if !ok {
		writeErr(w, http.StatusNotFound, "no freebuff connection with a credential for that id")
		return
	}
	ad, err := freebuffFetchAd(r.Context(), token, base)
	if err != nil {
		logf("freebuff ad fetch failed (conn %s): %v", connID, err)
		writeJSON(w, http.StatusOK, map[string]any{"ad": nil})
		return
	}
	if ad == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ad": nil})
		return
	}
	storePendingAd(connID, ad.AdID, *ad)
	writeJSON(w, http.StatusOK, map[string]any{"ad": ad})
}

func (g *Gateway) handleFreebuffImpression(w http.ResponseWriter, r *http.Request) {
	body, err := readAdImpression(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, ok := takePendingAd(body.ConnectionID, body.AdID)
	if !ok {
		writeErr(w, http.StatusConflict, "no matching unreported ad for this connection; an impression requires a creative served by this gateway")
		return
	}
	ad, ok := raw.(freebuffAd)
	if !ok {
		writeErr(w, http.StatusConflict, "ad record does not match this provider")
		return
	}
	_, base, token, ok := g.adsConnection("freebuff", body.ConnectionID)
	if !ok {
		writeErr(w, http.StatusNotFound, "no freebuff connection with a credential for that id")
		return
	}
	if err := freebuffReportImpression(r.Context(), token, base, ad); err != nil {
		logf("freebuff impression failed (conn %s): %v", body.ConnectionID, err)
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

func freebuffOrigin(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions", "/v1"} {
		if trimmed := strings.TrimSuffix(base, suffix); trimmed != base {
			return trimmed
		}
	}
	if base == "" {
		return "https://www.codebuff.com"
	}
	return base
}

// freebuffAdID derives a stable id from the impression URL, so a client cannot
// invent an ad id without also knowing the URL upstream handed out.
func freebuffAdID(impURL string) string {
	sum := sha256.Sum256([]byte(impURL))
	return fmt.Sprintf("fb-ad-%x", sum[:8])
}

func freebuffFetchAd(ctx context.Context, token, baseURL string) (*freebuffAd, error) {
	payload, _ := json.Marshal(map[string]any{"surface": "cli_chat", "placementId": "CLI-Statusline"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, freebuffOrigin(baseURL)+"/api/v1/ads", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Freebuff-CLI/0.2.36")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := adsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("freebuff ad: HTTP %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var wire struct {
		Ads []struct {
			Title       string `json:"title"`
			AdText      string `json:"adText"`
			CTA         string `json:"cta"`
			URL         string `json:"url"`
			ClickURL    string `json:"clickUrl"`
			ImpURL      string `json:"impUrl"`
			Favicon     string `json:"favicon"`
			SponsorName string `json:"sponsorName"`
			BrandColor  string `json:"brandColor"`
		} `json:"ads"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("freebuff ad: %w", err)
	}
	if len(wire.Ads) == 0 || wire.Ads[0].ImpURL == "" {
		return nil, nil
	}
	first := wire.Ads[0]
	clickURL := first.ClickURL
	if clickURL == "" {
		clickURL = first.URL
	}
	return &freebuffAd{
		AdID: freebuffAdID(first.ImpURL), ClickURL: clickURL,
		Creative: adCreative{
			Title: first.Title, Body: first.AdText, CTA: first.CTA,
			ImageURL: first.Favicon, SponsorName: first.SponsorName, BrandColor: first.BrandColor,
		},
		impURL: first.ImpURL, fetchedAt: time.Now(),
	}, nil
}

func freebuffReportImpression(ctx context.Context, token, baseURL string, ad freebuffAd) error {
	if ad.impURL == "" {
		return fmt.Errorf("freebuff ad: impression needs impUrl")
	}
	payload, _ := json.Marshal(map[string]any{"impUrl": ad.impURL, "mode": "LITE", "surface": "cli_chat"})
	return postAdJSON(ctx, freebuffOrigin(baseURL)+"/api/v1/ads/impression", token, "Freebuff-CLI/0.2.36", payload, "freebuff")
}

// ---------------------------------------------------------------- helpers

func firePixel(ctx context.Context, impURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, impURL, nil)
	if err != nil {
		return err
	}
	resp, err := adsClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return nil
}

func postAdJSON(ctx context.Context, endpoint, token, userAgent string, payload []byte, label string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := adsClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s ad: %w", label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("%s ad: impression HTTP %d: %s", label, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
