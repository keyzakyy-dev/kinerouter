package providerusage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

// ponytail: the JS handler keeps per-token in-flight/5-min caches for Claude to
// dodge its 429 rate limit. Skipped here — the dashboard calls this on demand,
// not once per tab; add a small token→(result,expiry) map here if 429s appear.

// fetchClaude ports usage/claude.js: OAuth usage endpoint first, then the
// legacy settings+org usage fallback.
func fetchClaude(ctx context.Context, c *http.Client, e *providerdata.Entry, token string, force bool) map[string]any {
	if token == "" {
		return map[string]any{"message": "Claude connected. Unable to fetch usage: no access token"}
	}
	oauthURL := usageURL(e, "oauthUrl")
	if oauthURL == "" {
		return map[string]any{"message": "Usage API not implemented for claude"}
	}
	h := map[string]string{
		"Authorization":     "Bearer " + token,
		"anthropic-beta":    "oauth-2025-04-20",
		"anthropic-version": anthropicAPIVersion,
		"User-Agent":        claudeUserAgent,
	}
	status, data, err := getJSON(ctx, c, oauthURL+"?cedar_ember=1", h)
	if err != nil {
		return map[string]any{"message": "Claude connected. Unable to fetch usage: " + err.Error()}
	}
	if status >= 200 && status < 300 && data != nil {
		return map[string]any{
			"plan":         "Claude Code",
			"extraUsage":   data["extra_usage"],
			"resetCredits": parseClaudeResetGrants(data["cedar_ember"]),
			"quotas":       claudeQuotas(data),
		}
	}
	return claudeLegacy(ctx, c, e, token)
}

const (
	anthropicAPIVersion = "2023-06-01"
	claudeUserAgent     = "claude-cli/2.0.0 (external, cli)"
)

func claudeQuota(used, resetAt any) map[string]any {
	u := toNum(used, 0)
	rem := 100 - u
	if rem < 0 {
		rem = 0
	}
	return map[string]any{
		"used":                u,
		"total":               100,
		"remaining":           rem,
		"remainingPercentage": rem,
		"resetAt":             parseResetTime(resetAt),
		"unlimited":           false,
	}
}

func claudeQuotas(data map[string]any) map[string]any {
	quotas := map[string]any{}
	if w, ok := obj(data["five_hour"]); ok {
		if _, has := w["utilization"]; has {
			quotas["session (5h)"] = claudeQuota(w["utilization"], w["resets_at"])
		}
	}
	if w, ok := obj(data["seven_day"]); ok {
		if _, has := w["utilization"]; has {
			quotas["weekly (7d)"] = claudeQuota(w["utilization"], w["resets_at"])
		}
	}
	for k, v := range data {
		if !strings.HasPrefix(k, "seven_day_") || k == "seven_day" {
			continue
		}
		w, ok := obj(v)
		if !ok {
			continue
		}
		if _, has := w["utilization"]; has {
			quotas["weekly "+strings.TrimPrefix(k, "seven_day_")+" (7d)"] = claudeQuota(w["utilization"], w["resets_at"])
		}
	}
	if limits, ok := data["limits"].([]any); ok {
		for _, l := range limits {
			lm, ok := obj(l)
			if !ok || lm["kind"] != "weekly_scoped" {
				continue
			}
			scope, _ := obj(lm["scope"])
			modelObj, _ := obj(scope["model"])
			name := strings.ToLower(strings.TrimSpace(strAny(modelObj["display_name"])))
			if name == "" {
				continue
			}
			used := toNum(lm["percent"], -1)
			if used < 0 {
				continue
			}
			if used > 100 {
				used = 100
			}
			quotas["weekly "+name+" (7d)"] = claudeQuota(used, lm["resets_at"])
		}
	}
	return quotas
}

func claudeLegacy(ctx context.Context, c *http.Client, e *providerdata.Entry, token string) map[string]any {
	settingsURL := usageURL(e, "settingsUrl")
	orgURL := usageURL(e, "orgUrl")
	if settingsURL == "" || orgURL == "" {
		return map[string]any{"message": "Claude connected. Usage API requires admin permissions."}
	}
	h := map[string]string{"Authorization": "Bearer " + token, "anthropic-version": anthropicAPIVersion}
	status, settings, err := getJSON(ctx, c, settingsURL, h)
	if err != nil || status < 200 || status >= 300 {
		return map[string]any{"message": "Claude connected. Usage API requires admin permissions."}
	}
	orgID := strAny(settings["organization_id"])
	if orgID == "" {
		return map[string]any{
			"plan":         firstNonEmptyStr(strAny(settings["plan"]), "Unknown"),
			"organization": settings["organization_name"],
			"message":      "Claude connected. Usage details require admin access.",
		}
	}
	us, usage, err := getJSON(ctx, c, strings.ReplaceAll(orgURL, "{org_id}", orgID), h)
	if err != nil || us < 200 || us >= 300 {
		return map[string]any{
			"plan":         firstNonEmptyStr(strAny(settings["plan"]), "Unknown"),
			"organization": settings["organization_name"],
			"message":      "Claude connected. Usage details require admin access.",
		}
	}
	return map[string]any{
		"plan":         firstNonEmptyStr(strAny(settings["plan"]), "Unknown"),
		"organization": settings["organization_name"],
		"quotas":       usage,
	}
}

// parseClaudeResetGrants mirrors parseClaudeResetGrants.
func parseClaudeResetGrants(v any) any {
	block, ok := obj(v)
	if !ok {
		return nil
	}
	if eligible, _ := block["eligible"].(bool); !eligible {
		return nil
	}
	grantsIn, ok := block["grants"].([]any)
	if !ok {
		return nil
	}
	available := 0.0
	usable := []map[string]any{}
	for _, g := range grantsIn {
		gm, ok := obj(g)
		if !ok || strAny(gm["id"]) == "" {
			continue
		}
		paused, _ := gm["paused"].(bool)
		left := toNum(gm["resets_left"], 0)
		if !paused && left > 0 {
			available += left
			usable = append(usable, gm)
		}
	}
	next := map[string]any(nil)
	if len(usable) > 0 {
		nextID := strAny(block["next_grant_id"])
		next = usable[0]
		for _, g := range usable {
			if strAny(g["id"]) == nextID {
				next = g
				break
			}
		}
	}
	grants := []map[string]any{}
	for _, g := range grantsIn {
		gm, ok := obj(g)
		if !ok || strAny(gm["id"]) == "" {
			continue
		}
		clears := []any{}
		if arr, ok := gm["clears"].([]any); ok {
			clears = arr
		}
		paused, _ := gm["paused"].(bool)
		usableNow, _ := gm["usable_now"].(bool)
		requires := true
		if v, ok := gm["use_requires_limit"].(bool); ok {
			requires = v
		}
		grants = append(grants, map[string]any{
			"id":               gm["id"],
			"label":            firstNonEmptyStr(strAny(gm["label"]), ""),
			"resetsLeft":       toNum(gm["resets_left"], 0),
			"resetsTotal":      toNum(gm["resets_total"], 0),
			"startsAt":         gm["starts_at"],
			"endsAt":           gm["ends_at"],
			"clears":           clears,
			"paused":           paused,
			"usableNow":        usableNow,
			"useRequiresLimit": requires,
		})
	}
	out := map[string]any{
		"availableCount": available,
		"nextGrantId":    nil,
		"expiresAt":      nil,
		"clears":         []any{},
		"cooldownUntil":  block["cooldown_until"],
		"weeklyResetsAt": block["weekly_resets_at"],
		"grants":         grants,
	}
	if next != nil {
		out["nextGrantId"] = next["id"]
		out["expiresAt"] = next["ends_at"]
		if arr, ok := next["clears"].([]any); ok {
			out["clears"] = arr
		}
	}
	return out
}

// ConsumeClaudeResetGrant spends one grant (irreversible).
func ConsumeClaudeResetGrant(ctx context.Context, c *http.Client, e *providerdata.Entry, token, grantID string) (map[string]any, error) {
	if token == "" {
		return nil, fmt.Errorf("No Claude access token available. Please re-authorize the connection.")
	}
	if !validGrantID(grantID) {
		return nil, fmt.Errorf("Invalid reset grant id.")
	}
	profileURL := usageURL(e, "profileUrl")
	resetURL := usageURL(e, "resetUrl")
	h := map[string]string{
		"Authorization":     "Bearer " + token,
		"anthropic-beta":    "oauth-2025-04-20",
		"anthropic-version": anthropicAPIVersion,
		"User-Agent":        claudeUserAgent,
	}
	ps, profile, err := getJSON(ctx, c, profileURL, h)
	if err != nil {
		return nil, err
	}
	org, _ := obj(profile["organization"])
	orgID := strAny(org["uuid"])
	if ps < 200 || ps >= 300 || orgID == "" {
		return nil, fmt.Errorf("Cannot resolve Claude organization (%d).", ps)
	}
	body, _ := json.Marshal(map[string]any{
		"program":    "cedar_ember",
		"grant_id":   grantID,
		"request_id": newUUID(),
	})
	res, data, err := postJSON(ctx, c, strings.ReplaceAll(resetURL, "{org_id}", orgID), h, body)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"ok":     res >= 200 && res < 300 && strAny(data["result"]) == "reset",
		"status": res,
		"result": data["result"],
		"reason": data["reason"],
	}
	if v, ok := data["resets_left"]; ok {
		out["resetsLeft"] = v
	}
	if e, ok := obj(data["error"]); ok {
		out["message"] = e["message"]
	}
	return out, nil
}

// validGrantID matches the JS /^[a-z0-9_-]{1,40}$/ guard: the id is
// interpolated into a provider request body, so keep the check server-side.
func validGrantID(s string) bool {
	if len(s) == 0 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- codex

func fetchCodex(ctx context.Context, c *http.Client, e *providerdata.Entry, token string) map[string]any {
	url := usageURL(e, "url")
	if url == "" || token == "" {
		return map[string]any{"message": "Codex connected. Usage API temporarily unavailable (no token)."}
	}
	status, data, err := getJSON(ctx, c, url, map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/json",
	})
	if err != nil {
		return map[string]any{"message": "Failed to fetch Codex usage: " + err.Error()}
	}
	if status < 200 || status >= 300 {
		return map[string]any{"message": fmt.Sprintf("Codex connected. Usage API temporarily unavailable (%d).", status)}
	}
	normal := data["rate_limit"]
	if normal == nil {
		normal = data["rate_limits"]
	}
	if normal == nil {
		if by, ok := obj(data["rate_limits_by_limit_id"]); ok {
			normal = by["codex"]
		}
	}
	review := codexReviewLimit(data)
	spark := codexSparkLimit(data)
	quotas := map[string]any{}
	appendCodexWindows(quotas, "", normal)
	appendCodexWindows(quotas, "review", review)
	appendCodexWindows(quotas, "spark", spark)
	rc, _ := obj(data["rate_limit_reset_credits"])
	available := toNum(rc["available_count"], 0)
	if available < 0 {
		available = 0
	}
	return map[string]any{
		"plan":               firstNonEmptyStr(strAny(data["plan_type"]), codexSummaryPlan(data), "unknown"),
		"limitReached":       codexLimitReached(normal),
		"reviewLimitReached": codexLimitReached(review),
		"sparkLimitReached":  codexLimitReached(spark),
		"resetCredits":       map[string]any{"availableCount": available},
		"quotas":             quotas,
	}
}

func codexSummaryPlan(data map[string]any) string {
	if s, ok := obj(data["summary"]); ok {
		return strAny(s["plan"])
	}
	return ""
}

func codexLimitReached(v any) bool {
	if v == nil {
		return false
	}
	body, ok := obj(v)
	if !ok {
		return false
	}
	if rl, ok := obj(body["rate_limit"]); ok {
		body = rl
	}
	b, _ := body["limit_reached"].(bool)
	return b
}

func codexWindow(w map[string]any) map[string]any {
	used := toNum(firstNonNull(w["used_percent"], w["percent_used"]), 0)
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	rem := 100 - used
	if rem < 0 {
		rem = 0
	}
	return map[string]any{
		"used":      used,
		"total":     100,
		"remaining": rem,
		"resetAt":   parseResetTime(firstNonNull(w["reset_at"], w["resets_at"], w["resetAt"])),
		"unlimited": false,
	}
}

func appendCodexWindows(quotas map[string]any, prefix string, snapshot any) bool {
	if snapshot == nil {
		return false
	}
	snap, ok := obj(snapshot)
	if !ok {
		return false
	}
	rateLimit := snap
	if rl, ok := obj(snap["rate_limit"]); ok {
		rateLimit = rl
	}
	primary := firstNonNull(rateLimit["primary_window"], rateLimit["primary"], snap["primary_window"], snap["primary"])
	secondary := firstNonNull(rateLimit["secondary_window"], rateLimit["secondary"], snap["secondary_window"], snap["secondary"])
	added := false
	if m, ok := obj(primary); ok {
		quotas[prefix+"session"] = codexWindow(m)
		added = true
	}
	if m, ok := obj(secondary); ok {
		quotas[prefix+"weekly"] = codexWindow(m)
		added = true
	}
	return added
}

func codexReviewLimit(data map[string]any) any {
	if v := firstNonNull(data["code_review_rate_limit"], data["review_rate_limit"]); v != nil {
		return v
	}
	if by, ok := obj(data["rate_limits_by_limit_id"]); ok {
		if v := firstNonNull(by["code_review"], by["codex_review"], by["review"]); v != nil {
			return v
		}
	}
	if arr, ok := data["additional_rate_limits"].([]any); ok {
		for _, item := range arr {
			m, ok := obj(item)
			if !ok {
				continue
			}
			id := strings.ToLower(strAny(firstNonNull(m["limit_name"], m["metered_feature"], m["id"])))
			if id == "code_review" || id == "codex_review" || id == "review" || strings.Contains(id, "review") {
				return m
			}
		}
	}
	return nil
}

func codexSparkLimit(data map[string]any) any {
	if v := firstNonNull(data["spark_rate_limit"], data["gpt_5_3_codex_spark_rate_limit"]); v != nil {
		return v
	}
	if by, ok := obj(data["rate_limits_by_limit_id"]); ok {
		if v := firstNonNull(by["gpt-5.3-codex-spark"], by["gpt_5_3_codex_spark"], by["spark"]); v != nil {
			return v
		}
	}
	if arr, ok := data["additional_rate_limits"].([]any); ok {
		for _, item := range arr {
			m, ok := obj(item)
			if !ok {
				continue
			}
			id := strings.ToLower(strAny(firstNonNull(m["limit_name"], m["metered_feature"], m["id"])))
			if strings.Contains(id, "spark") {
				return m
			}
		}
	}
	return nil
}

// CodexResetCredits reads available reset credits.
func CodexResetCredits(ctx context.Context, c *http.Client, e *providerdata.Entry, token, accountID string) (map[string]any, error) {
	if token == "" {
		return nil, fmt.Errorf("No Codex access token available. Please re-authorize the connection.")
	}
	h := map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/json",
		"OpenAI-Beta":   "codex-1",
		"originator":    "codex_cli_rs",
	}
	if accountID != "" {
		h["ChatGPT-Account-ID"] = accountID
	}
	status, data, err := getJSON(ctx, c, usageURL(e, "resetCreditsUrl"), h)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		msg := codexErrMessage(data, fmt.Sprintf("Codex reset credits API unavailable (%d).", status))
		return nil, fmt.Errorf("%s", msg)
	}
	credits := []map[string]any{}
	if arr, ok := data["credits"].([]any); ok {
		for _, item := range arr {
			m, ok := obj(item)
			if !ok {
				continue
			}
			credits = append(credits, map[string]any{
				"status":    firstNonEmptyStr(strAny(m["status"]), "unknown"),
				"grantedAt": toISODate(firstNonNull(m["granted_at"], m["grantedAt"])),
				"expiresAt": toISODate(firstNonNull(m["expires_at"], m["expiresAt"])),
			})
		}
	}
	avail := toNum(firstNonNull(data["available_count"], data["availableCount"]), 0)
	if avail < 0 {
		avail = 0
	}
	return map[string]any{"availableCount": avail, "credits": credits}, nil
}

func codexErrMessage(data map[string]any, fallback string) string {
	if data == nil {
		return fallback
	}
	for _, k := range []string{"message", "error", "detail"} {
		switch v := data[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]any:
			if s := strAny(v["message"]); s != "" {
				return s
			}
		}
	}
	return fallback
}

func toISODate(v any) any {
	if v == nil {
		return nil
	}
	n := toNum(v, -1)
	if n < 0 {
		return nil
	}
	if n >= 1e12 {
		n /= 1000
	}
	return time.Unix(int64(n), 0).UTC().Format(time.RFC3339)
}

// ConsumeCodexResetCredit spends one reset credit (irreversible).
// redeemID is server-generated by the caller so a client cannot replay it.
func ConsumeCodexResetCredit(ctx context.Context, c *http.Client, e *providerdata.Entry, token, redeemID string) map[string]any {
	if token == "" {
		return map[string]any{"ok": false, "status": 0, "message": "No Codex access token available. Please re-authorize the connection."}
	}
	body, _ := json.Marshal(map[string]any{"redeem_request_id": redeemID})
	status, data, err := postJSON(ctx, c, usageURL(e, "resetCreditsConsumeUrl"), map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/json",
	}, body)
	if err != nil {
		return map[string]any{"ok": false, "status": 0, "message": "Failed to consume Codex reset credit: " + err.Error()}
	}
	code := strAny(data["code"])
	windowsReset := toNum(data["windows_reset"], 0)
	success := status >= 200 && status < 300 && (code == "reset" || windowsReset > 0)
	return map[string]any{
		"ok":           success,
		"noCredit":     status >= 200 && status < 300 && code == "no_credit",
		"status":       status,
		"code":         code,
		"windowsReset": windowsReset,
		"message":      data["message"],
		"raw":          data,
		"credit":       data["credit"],
	}
}

// ---------------------------------------------------------------- api-key quotas

const deepseekBalanceURL = "https://api.deepseek.com/user/balance"

func fetchDeepseek(ctx context.Context, c *http.Client, e *providerdata.Entry, apiKey string) map[string]any {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return map[string]any{"message": "DeepSeek API key not available. Add a key to view usage."}
	}
	status, data, err := getJSON(ctx, c, deepseekBalanceURL, map[string]string{
		"Authorization": "Bearer " + key,
		"Accept":        "application/json",
	})
	if err != nil {
		return map[string]any{"message": "DeepSeek error: " + err.Error()}
	}
	if status == 401 || status == 403 {
		return map[string]any{"plan": "DeepSeek", "message": "DeepSeek authentication failed. Check the API key."}
	}
	if status < 200 || status >= 300 {
		return map[string]any{"plan": "DeepSeek", "message": fmt.Sprintf("DeepSeek balance API error (%d)", status)}
	}
	if data == nil {
		return map[string]any{"message": "DeepSeek balance response was not JSON."}
	}
	infos, _ := data["balance_infos"].([]any)
	quotas := map[string]any{}
	for _, it := range infos {
		m, ok := obj(it)
		if !ok {
			continue
		}
		cur := strings.ToUpper(strAny(m["currency"]))
		if cur == "" {
			continue
		}
		total := toNum(firstNonNull(m["total_balance"], m["totalBalance"]), 0)
		if total < 0 {
			total = 0
		}
		pct := 0.0
		if total > 0 {
			pct = 100
		}
		quotas["Balance ("+cur+")"] = map[string]any{
			"used":                0,
			"total":               total,
			"remainingPercentage": pct,
			"resetAt":             nil,
			"unlimited":           false,
			"isCreditBalance":     true,
			"currency":            cur,
		}
	}
	if len(quotas) == 0 {
		return map[string]any{"plan": "DeepSeek", "message": "DeepSeek connected. No balance data returned."}
	}
	plan := "DeepSeek (Insufficient Balance)"
	if b, _ := firstNonNull(data["is_available"], data["isAvailable"]).(bool); b {
		plan = "DeepSeek"
	}
	return map[string]any{"plan": plan, "quotas": quotas}
}

func fetchGlm(ctx context.Context, c *http.Client, e *providerdata.Entry, apiKey, accessToken string) map[string]any {
	key := strings.TrimSpace(firstNonEmptyStr(apiKey, accessToken))
	if key == "" {
		return map[string]any{"message": "GLM API key not available."}
	}
	url := usageURL(e, "url")
	if url == "" {
		return map[string]any{"message": "Usage API not implemented for " + e.ID}
	}
	status, data, err := getJSON(ctx, c, url, map[string]string{
		"Authorization": "Bearer " + key,
		"Accept":        "application/json",
	})
	if err != nil {
		return map[string]any{"message": "GLM error: " + err.Error()}
	}
	if status == 401 {
		return map[string]any{"message": "GLM API key invalid or expired."}
	}
	if status < 200 || status >= 300 {
		return map[string]any{"message": fmt.Sprintf("GLM quota API error (%d).", status)}
	}
	d, _ := obj(data["data"])
	limits, _ := d["limits"].([]any)
	quotas := map[string]any{}
	for _, l := range limits {
		lm, ok := obj(l)
		if !ok {
			continue
		}
		typ := strAny(lm["type"])
		if typ != "TOKENS_LIMIT" && typ != "CREDIT_LIMIT" {
			continue
		}
		used := toNum(lm["percentage"], 0)
		rem := 100 - used
		if rem < 0 {
			rem = 0
		}
		unit := toNum(lm["unit"], 0)
		var key string
		switch {
		case unit == 3:
			key = fmt.Sprintf("Session (%sh)", trimNumStr(toNum(lm["number"], 0)))
		case unit == 6:
			key = "Weekly (7d)"
		case typ == "TOKENS_LIMIT":
			key = "Tokens"
		default:
			key = fmt.Sprintf("Limit (%s)", trimNumStr(toNum(lm["number"], 0)))
		}
		resetMs := toNum(lm["nextResetTime"], 0)
		var resetAt any
		if resetMs > 0 {
			resetAt = time.Unix(int64(resetMs)/1000, 0).UTC().Format(time.RFC3339)
		}
		quotas[key] = map[string]any{
			"used":                used,
			"total":               100,
			"remaining":           rem,
			"remainingPercentage": rem,
			"resetAt":             resetAt,
			"unlimited":           false,
		}
	}
	level := strAny(d["level"])
	plan := "Unknown"
	if level != "" {
		plan = strings.ToUpper(level[:1]) + strings.ToLower(level[1:])
	}
	return map[string]any{"plan": plan, "quotas": quotas}
}

func fetchGroq(ctx context.Context, c *http.Client, e *providerdata.Entry, apiKey string) map[string]any {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return map[string]any{"message": "Groq API key not available. Add a key to view usage."}
	}
	url := usageURL(e, "url")
	if url == "" {
		return map[string]any{"message": "Usage API not implemented for groq"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return map[string]any{"message": "Groq error: " + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return map[string]any{"message": "Groq error: " + err.Error()}
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))

	if res.StatusCode == 401 || res.StatusCode == 403 {
		return map[string]any{"plan": "Groq", "message": "Groq authentication failed. Check the API key."}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return map[string]any{"plan": "Groq", "message": fmt.Sprintf("Groq usage API error (%d)", res.StatusCode)}
	}
	requests := groqQuota(res.Header, "x-ratelimit-limit-requests", "x-ratelimit-remaining-requests", "x-ratelimit-reset-requests")
	tokens := groqQuota(res.Header, "x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens")
	if requests == nil && tokens == nil {
		return map[string]any{
			"plan":    "Groq",
			"message": "Groq connected. No rate-limit data reported for this key yet.",
			"quotas":  map[string]any{},
		}
	}
	quotas := map[string]any{}
	if requests != nil {
		quotas["Requests"] = requests
	}
	if tokens != nil {
		quotas["Tokens"] = tokens
	}
	return map[string]any{"plan": "Groq", "quotas": quotas}
}

func groqQuota(h http.Header, limitKey, remainingKey, resetKey string) map[string]any {
	limitRaw := h.Get(limitKey)
	remainRaw := h.Get(remainingKey)
	if limitRaw == "" || remainRaw == "" {
		return nil
	}
	limit := toNum(limitRaw, -1)
	remaining := toNum(remainRaw, -1)
	if limit < 0 || remaining < 0 {
		return nil
	}
	used := limit - remaining
	if used < 0 {
		used = 0
	}
	var resetAt any
	if ms, ok := parseGroqDuration(h.Get(resetKey)); ok {
		resetAt = time.Now().Add(ms).UTC().Format(time.RFC3339)
	}
	return map[string]any{"used": used, "total": limit, "resetAt": resetAt, "unlimited": false}
}

// parseGroqDuration parses Go-style duration strings ("2m59.56s", "7.66s").
func parseGroqDuration(s string) (time.Duration, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	var total time.Duration
	matched := false
	num := ""
	for i := 0; i < len(s); {
		ch := s[i]
		if (ch >= '0' && ch <= '9') || ch == '.' {
			num += string(ch)
			i++
			continue
		}
		unit := ""
		switch {
		case strings.HasPrefix(s[i:], "ms"):
			unit, i = "ms", i+2
		case ch == 's':
			unit, i = "s", i+1
		case ch == 'm':
			unit, i = "m", i+1
		case ch == 'h':
			unit, i = "h", i+1
		default:
			i++
			num = ""
			continue
		}
		if num == "" {
			continue
		}
		v := toNum(num, 0)
		num = ""
		matched = true
		switch unit {
		case "ms":
			total += time.Duration(v * float64(time.Millisecond))
		case "s":
			total += time.Duration(v * float64(time.Second))
		case "m":
			total += time.Duration(v * float64(time.Minute))
		case "h":
			total += time.Duration(v * float64(time.Hour))
		}
	}
	return total, matched
}

func fetchOpenCode(ctx context.Context, c *http.Client, e *providerdata.Entry, apiKey string) map[string]any {
	label := "OpenCode Go"
	if e != nil && e.ID == "opencode-zen" {
		label = "OpenCode Zen"
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return map[string]any{"message": label + " API key not available. Add a key to view usage."}
	}
	url := usageURL(e, "url")
	if url == "" {
		return map[string]any{"message": "Usage API not implemented for " + e.ID}
	}
	status, data, err := getJSON(ctx, c, url, map[string]string{
		"Authorization": "Bearer " + key,
		"Accept":        "application/json",
	})
	if err != nil {
		return map[string]any{"message": label + " error: " + err.Error()}
	}
	if status == 401 {
		return map[string]any{"plan": label, "message": label + " authentication failed. Check the API key."}
	}
	if status == 403 {
		errObj, _ := obj(data["error"])
		if strAny(errObj["type"]) == "EntitlementError" {
			msg := label + " subscription required for this API key."
			if label == "OpenCode Zen" {
				msg = label + " billing required for this API key."
			}
			return map[string]any{"plan": label, "message": msg}
		}
		return map[string]any{"plan": label, "message": label + " access forbidden for this API key."}
	}
	if status < 200 || status >= 300 {
		return map[string]any{"plan": label, "message": fmt.Sprintf("%s usage API error (%d).", label, status)}
	}
	usage, _ := obj(data["usage"])
	if usage == nil {
		return map[string]any{"plan": label, "message": label + " usage response did not contain quota data."}
	}
	quotas := map[string]any{}
	for _, p := range []struct{ key, name string }{
		{"rolling", "Rolling"}, {"weekly", "Weekly"}, {"monthly", "Monthly"},
	} {
		q, ok := obj(usage[p.key])
		if !ok {
			continue
		}
		percent := toNum(q["percent"], -1)
		if percent < 0 {
			continue
		}
		used := percent
		if used < 0 {
			used = 0
		}
		if used > 100 {
			used = 100
		}
		rem := 100 - used
		quotas[p.name] = map[string]any{
			"used":                used,
			"total":               100,
			"remaining":           rem,
			"remainingPercentage": rem,
			"resetAt":             parseResetTime(q["resetsAt"]),
			"unlimited":           false,
		}
	}
	if len(quotas) == 0 {
		return map[string]any{"plan": label, "message": label + " usage response did not contain valid quota data."}
	}
	return map[string]any{"plan": label, "quotas": quotas}
}

// ---------------------------------------------------------------- small helpers

func obj(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func strAny(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonNull(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func trimNumStr(f float64) string {
	s := fmt.Sprintf("%g", f)
	return s
}

func postJSON(ctx context.Context, c *http.Client, url string, headers map[string]string, body []byte) (int, map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	var m map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return res.StatusCode, m, nil
}

func newUUID() string { return uuid.NewString() }
