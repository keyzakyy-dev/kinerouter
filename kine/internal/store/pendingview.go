package store

import (
	"strconv"
	"time"
)

// pendingView renders the live in-flight state the dashboard polls through
// /api/usage/stats and /api/usage/stream: the byModel/byAccount counters, the
// active-request list with resolved account names, the last 20 completed
// requests for the recent panel, and the provider that most recently errored.
func (s *Store) pendingView(connNames map[string]string) (map[string]any, []map[string]any, []map[string]any, string) {
	p := &s.pending
	p.mu.Lock()
	now := time.Now()
	p.expire(now)
	byModel := map[string]int{}
	for k, v := range p.byModel {
		byModel[k] = v
	}
	byAcc := map[string]map[string]int{}
	for conn, models := range p.byAcc {
		cp := map[string]int{}
		for k, v := range models {
			if v > 0 {
				cp[k] = v
			}
		}
		if len(cp) > 0 {
			byAcc[conn] = cp
		}
	}
	lastErr := ""
	if !p.lastErrT.IsZero() && now.Sub(p.lastErrT) < errorProviderTTL {
		lastErr = p.lastErr
	}
	p.mu.Unlock()

	active := []map[string]any{}
	for conn, models := range byAcc {
		name := connNames[conn]
		if name == "" {
			name = "Account " + shortID(conn) + "..."
		}
		for key, count := range models {
			model, provider := splitModelKey(key)
			active = append(active, map[string]any{
				"model": model, "provider": provider, "account": name, "count": count,
			})
		}
	}

	recent, err := s.recentCompleted(20)
	if err != nil {
		recent = []map[string]any{}
	}
	return map[string]any{"byModel": byModel, "byAccount": byAcc}, active, recent, lastErr
}

// splitModelKey reverses modelKeyOf ("model (provider)").
func splitModelKey(key string) (model, provider string) {
	if len(key) < 4 || key[len(key)-1] != ')' {
		return key, "unknown"
	}
	open := -1
	for i := len(key) - 2; i >= 0; i-- {
		if key[i] == '(' {
			open = i
			break
		}
	}
	if open <= 0 || key[open-1] != ' ' {
		return key, "unknown"
	}
	return key[:open-1], key[open+1 : len(key)-1]
}

// recentCompleted returns the newest request rows with a token breakdown,
// deduped per (model, provider, minute) like the reference's ring buffer.
func (s *Store) recentCompleted(limit int) ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT timestamp, COALESCE(provider,''), COALESCE(model,''), COALESCE(tokens,''), COALESCE(status,'')
		FROM usageHistory ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	seen := map[string]bool{}
	for rows.Next() && len(out) < limit {
		var ts, prov, model, tokens, status string
		if err := rows.Scan(&ts, &prov, &model, &tokens, &status); err != nil {
			return nil, err
		}
		tk := parseJSON[map[string]any](tokens, map[string]any{})
		pt, ct := toInt(tk["prompt_tokens"]), toInt(tk["completion_tokens"])
		if pt == 0 && ct == 0 {
			continue
		}
		minute := ts
		if len(ts) > 16 {
			minute = ts[:16]
		}
		key := model + "|" + prov + "|" + strconv.FormatInt(pt, 10) + "|" + strconv.FormatInt(ct, 10) + "|" + minute
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, map[string]any{
			"timestamp": ts, "model": model, "provider": prov,
			"promptTokens": pt, "completionTokens": ct,
			"cachedTokens": toInt(tk["cached_tokens"]),
			"status":       firstNonEmpty(status, "ok"),
		})
	}
	return out, rows.Err()
}

// LiveUsage is the subset the SSE feed pushes: pending counters, active and
// recent requests, and the erroring provider. Cheap enough to send on every
// notify (no period scan).
func (s *Store) LiveUsage() (map[string]any, error) {
	connNames, _ := s.connNames()
	pending, active, recent, lastErr := s.pendingView(connNames)
	return map[string]any{
		"pending":        pending,
		"activeRequests": active,
		"recentRequests": recent,
		"errorProvider":  lastErr,
	}, nil
}
