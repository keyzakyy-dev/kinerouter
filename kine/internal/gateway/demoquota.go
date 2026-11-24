package gateway

import (
	"os"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// demoQuota reports whether /api/usage answers with canned payloads. Local UI
// work needs quota rows without real provider accounts; set
// KINEROUTER_DEMO_QUOTA=1 on a dev server only.
func demoQuota() bool { return os.Getenv("KINEROUTER_DEMO_QUOTA") != "" }

// demoUsage builds a provider-shaped usage payload the dashboard parsers
// already understand. Values are stable per provider so screenshots repeat.
func demoUsage(c *store.Connection) map[string]any {
	now := time.Now()
	at := func(d time.Duration) string { return now.Add(d).UTC().Format(time.RFC3339) }

	quotas := map[string]any{}
	plan := "Pro"
	switch c.Provider {
	case "openai":
		plan = "ChatGPT Team"
		quotas["5h"] = map[string]any{"used": 38, "total": 100, "resetAt": at(2*time.Hour + 14*time.Minute)}
		quotas["weekly"] = map[string]any{"used": 66, "total": 100, "resetAt": at(4*24*time.Hour + 3*time.Hour)}
	case "anthropic":
		plan = "Claude Max"
		quotas["Session (5h)"] = map[string]any{"used": 24, "total": 100, "resetAt": at(1*time.Hour + 42*time.Minute)}
		quotas["Weekly (7d)"] = map[string]any{"used": 61, "total": 100, "resetAt": at(5*24*time.Hour + 6*time.Hour)}
		quotas["Weekly Opus (7d)"] = map[string]any{"used": 12, "total": 100, "resetAt": at(5*24*time.Hour + 6*time.Hour)}
	case "gemini":
		plan = "Google AI Pro"
		quotas["Gemini (Flash / Pro)"] = map[string]any{"used": 52, "total": 100, "resetAt": at(3*time.Hour + 8*time.Minute)}
		quotas["Gemini (Weekly)"] = map[string]any{"used": 79, "total": 100, "resetAt": at(6*24*time.Hour + 2*time.Hour)}
	case "openrouter":
		plan = "Pay as you go"
		quotas["Credits"] = map[string]any{"used": 0, "total": 42.75, "isCreditBalance": true, "currency": "USD", "neverExpires": true}
	case "deepseek":
		plan = "Top-up"
		quotas["Balance (USD)"] = map[string]any{"used": 0, "total": 12.4, "isCreditBalance": true, "currency": "USD", "neverExpires": true}
	default:
		quotas["Requests (5h)"] = map[string]any{"used": 120, "total": 500, "resetAt": at(2*time.Hour + 5*time.Minute)}
		quotas["Requests (Weekly)"] = map[string]any{"used": 2100, "total": 7000, "resetAt": at(3*24*time.Hour + 9*time.Hour)}
	}
	return map[string]any{"plan": plan, "quotas": quotas}
}
