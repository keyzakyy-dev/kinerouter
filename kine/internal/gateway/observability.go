package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// Observability: the request-detail writer behind /api/usage/request-details.
// Gated on the enableObservability setting, mirroring
// src/lib/db/repos/requestDetailsRepo.js (defaults: 200 records).

type obsConfig struct {
	enabled    bool
	maxRecords int
}

var (
	obsMu   sync.Mutex
	obsCfg  obsConfig
	obsCfgT time.Time
)

func (g *Gateway) observability() obsConfig {
	obsMu.Lock()
	defer obsMu.Unlock()
	if !obsCfgT.IsZero() && time.Since(obsCfgT) < 5*time.Second {
		return obsCfg
	}
	cfg := obsConfig{maxRecords: 200}
	if s, err := g.store.GetSettings(); err == nil {
		if v, ok := s["enableObservability"].(bool); ok {
			cfg.enabled = v
		}
		if v, ok := s["observabilityMaxRecords"].(float64); ok && v > 0 {
			cfg.maxRecords = int(v)
		}
	}
	obsCfg, obsCfgT = cfg, time.Now()
	return cfg
}

var detailModelSanitizer = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// saveDetail stores one observability record. Fail-open: observability must
// never break the proxied request, so every error is dropped.
//
// ponytail: the record carries metadata only (model, tokens, latency, status).
// Request/response bodies are dropped on read by the dashboard API anyway
// (handlers.go handleRequestDetails) and took the bulk of the write cost, so
// storing them would be dead weight. Add body capture here if an export or
// debug CLI ever needs it. TTFT stays 0 until the stream path measures
// first-byte time.
func (g *Gateway) saveDetail(conn *store.Connection, t target, u translate.Usage,
	start time.Time, status, errMsg string) {
	cfg := g.observability()
	if !cfg.enabled {
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var rnd [3]byte
	rand.Read(rnd[:])
	id := fmt.Sprintf("%s-%s-%s", ts, hex.EncodeToString(rnd[:]),
		detailModelSanitizer.ReplaceAllString(t.Model, "-"))

	response := map[string]any{}
	if errMsg != "" {
		response["error"] = errMsg
		response["status"] = 502
	}
	rec := map[string]any{
		"id":           id,
		"provider":     t.Provider,
		"model":        t.Model,
		"connectionId": conn.ID,
		"timestamp":    ts,
		"status":       status,
		"latency": map[string]any{
			"ttft":  0,
			"total": time.Since(start).Milliseconds(),
		},
		"tokens": map[string]any{
			"prompt_tokens":               u.PromptTokens,
			"completion_tokens":           u.CompletionTokens,
			"cached_tokens":               u.CacheRead,
			"cache_creation_input_tokens": u.CacheCreation,
		},
		"response": response,
	}
	g.store.SaveRequestDetail(id, ts, t.Provider, t.Model, conn.ID, status, rec, cfg.maxRecords)
}
