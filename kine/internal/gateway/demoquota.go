package gateway

import (
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// demoUsage expands the canned usage payload a dev connection carries at
// providerSpecificData.demoUsage, so the quota tracker has rows without live
// provider calls. Reset times are stored as resetInMinutes and expanded per
// request, so a seeded demo never goes stale.
// ponytail: no real connection carries the field, so production is unaffected;
// drop it from the dev DB when real accounts are available.
func demoUsage(c *store.Connection) map[string]any {
	raw, _ := c.Specific()["demoUsage"].(map[string]any)
	if raw == nil {
		return nil
	}
	out := map[string]any{}
	if plan, ok := raw["plan"]; ok {
		out["plan"] = plan
	}
	quotas, _ := raw["quotas"].(map[string]any)
	next := make(map[string]any, len(quotas))
	for name, v := range quotas {
		q, _ := v.(map[string]any)
		if q == nil {
			continue
		}
		cp := make(map[string]any, len(q)+1)
		for k, val := range q {
			cp[k] = val
		}
		if mins, ok := cp["resetInMinutes"].(float64); ok {
			delete(cp, "resetInMinutes")
			cp["resetAt"] = time.Now().Add(time.Duration(mins * float64(time.Minute))).UTC().Format(time.RFC3339)
		}
		next[name] = cp
	}
	out["quotas"] = next
	return out
}
