package store

import (
	"strings"
	"sync"
	"time"
)

// pendingTimeout mirrors usageRepo PENDING_TIMEOUT_MS: a slot that is never
// released (client disconnect mid-stream) expires so the dashboard stops
// showing a phantom in-flight request.
const pendingTimeout = 60 * time.Second

// errorProviderTTL mirrors the reference's 10s window for the red edge.
const errorProviderTTL = 10 * time.Second

// pending tracks in-flight upstream calls. Runtime-only state, like the JS
// module-level `_pendingRequests` global; it never goes to the DB.
type pending struct {
	mu       sync.Mutex
	byModel  map[string]int
	byAcc    map[string]map[string]int
	expires  map[string]time.Time
	lastErr  string
	lastErrT time.Time
}

func modelKeyOf(model, provider string) string {
	if provider == "" {
		return model
	}
	return model + " (" + provider + ")"
}

// TrackPending adds or removes one in-flight slot. `failed` marks the provider
// as the most recent error for the topology's red edge.
func (s *Store) TrackPending(model, provider, connID string, started, failed bool) {
	p := &s.pending
	p.mu.Lock()
	defer func() {
		p.mu.Unlock()
		// Push the in-flight change now so the topology lights up while a
		// (streaming) request is still running, and clears as soon as it ends.
		s.NotifyUsage()
	}()
	if p.byModel == nil {
		p.byModel = map[string]int{}
		p.byAcc = map[string]map[string]int{}
		p.expires = map[string]time.Time{}
	}
	key := modelKeyOf(model, provider)
	now := time.Now()

	if started {
		p.byModel[key]++
		if connID != "" {
			if p.byAcc[connID] == nil {
				p.byAcc[connID] = map[string]int{}
			}
			p.byAcc[connID][key]++
		}
		p.expires[key] = now.Add(pendingTimeout)
	} else {
		if p.byModel[key] > 0 {
			p.byModel[key]--
		}
		if p.byModel[key] == 0 {
			delete(p.byModel, key)
			delete(p.expires, key)
		}
		if connID != "" && p.byAcc[connID] != nil {
			if p.byAcc[connID][key] > 0 {
				p.byAcc[connID][key]--
			}
			if p.byAcc[connID][key] == 0 {
				delete(p.byAcc[connID], key)
			}
			if len(p.byAcc[connID]) == 0 {
				delete(p.byAcc, connID)
			}
		}
	}

	if !started && failed && provider != "" {
		p.lastErr = strings.ToLower(provider)
		p.lastErrT = now
	}
}

// pendingEventually drops slots whose request never reported completion.
func (p *pending) expire(now time.Time) {
	for key, at := range p.expires {
		if now.After(at) {
			delete(p.expires, key)
			delete(p.byModel, key)
			for connID, models := range p.byAcc {
				delete(models, key)
				if len(models) == 0 {
					delete(p.byAcc, connID)
				}
			}
		}
	}
}
