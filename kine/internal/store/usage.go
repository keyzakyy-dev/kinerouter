package store

import (
	"database/sql"
	"time"
)

// UsageRecord is one row of usageHistory.
type UsageRecord struct {
	Timestamp        string  `json:"timestamp"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	ConnectionID     string  `json:"connectionId"`
	APIKey           string  `json:"apiKey"`
	Endpoint         string  `json:"endpoint"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	Cost             float64 `json:"cost"`
	Status           string  `json:"status"`
	Tokens           string  `json:"tokens"`
}

// SaveUsage inserts a history row and rolls the daily aggregate in one tx.
// Mirrors usageRepo.saveRequestUsage. The daily rollup keeps the same counter
// shape the JS app writes (byProvider/byModel/byAccount/byApiKey/byEndpoint),
// so a DB written by either app reads identically in both.
func (s *Store) SaveUsage(r UsageRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO usageHistory(timestamp,provider,model,connectionId,apiKey,endpoint,promptTokens,completionTokens,cost,status,tokens,meta)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,NULL)`,
		r.Timestamp, r.Provider, r.Model, nullableStr(r.ConnectionID), nullableStr(r.APIKey),
		nullableStr(r.Endpoint), r.PromptTokens, r.CompletionTokens, r.Cost, nullableStr(r.Status),
		nullableStr(r.Tokens)); err != nil {
		return err
	}

	// The stored `tokens` blob is the canonical per-request breakdown; the
	// cache counters only live there.
	tk := parseJSON[map[string]any](r.Tokens, map[string]any{})
	cached := toInt(tk["cached_tokens"])
	if cached == 0 {
		cached = toInt(tk["cache_read_input_tokens"])
	}

	ymd := localDateKey()
	var data string
	err = tx.QueryRow(`SELECT data FROM usageDaily WHERE dateKey = ?`, ymd).Scan(&data)
	agg := map[string]any{}
	if err == nil {
		agg = parseJSON[map[string]any](data, map[string]any{})
	} else if err != sql.ErrNoRows {
		return err
	}
	day := dayAgg(agg)
	day.add(r.Provider, r.Model, r.ConnectionID, r.APIKey, r.Endpoint,
		r.PromptTokens, r.CompletionTokens, cached, r.Cost)
	if _, err := tx.Exec(`INSERT INTO usageDaily(dateKey,data) VALUES(?,?)
		ON CONFLICT(dateKey) DO UPDATE SET data=excluded.data`, ymd, stringifyJSON(agg)); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT INTO _meta(key,value) VALUES('totalRequestsLifetime','1')
		ON CONFLICT(key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.NotifyUsage()
	return nil
}

// dayAgg reads/writes the usageDaily JSON document with the reference's
// counters (requests/promptTokens/completionTokens/cachedTokens/cost plus the
// five grouping maps whose entries carry `rawModel`/`provider` metadata).
type dayAgg map[string]any

func (d dayAgg) add(provider, model, connID, apiKey, endpoint string, prompt, completion, cached int64, cost float64) {
	d.bump("requests", 1)
	d.bump("promptTokens", prompt)
	d.bump("completionTokens", completion)
	d.bump("cachedTokens", cached)
	d.bump("cost", cost)
	vals := counter{Requests: 1, PromptTokens: prompt, CompletionTokens: completion, CachedTokens: cached, Cost: cost}
	if provider != "" {
		d.nested("byProvider", provider, vals, nil)
	}
	modelKey := model
	if provider != "" {
		modelKey = model + "|" + provider
	}
	d.nested("byModel", modelKey, vals, map[string]any{"rawModel": model, "provider": provider})
	if connID != "" {
		d.nested("byAccount", connID, vals, map[string]any{"rawModel": model, "provider": provider})
	}
	keyVal := apiKey
	if keyVal == "" {
		keyVal = "local-no-key"
	}
	akKey := keyVal + "|" + model + "|" + firstNonEmpty(provider, "unknown")
	d.nested("byApiKey", akKey, vals, map[string]any{"rawModel": model, "provider": provider, "apiKey": nullableOrEmpty(apiKey)})
	ep := firstNonEmpty(endpoint, "Unknown")
	epKey := ep + "|" + model + "|" + firstNonEmpty(provider, "unknown")
	d.nested("byEndpoint", epKey, vals, map[string]any{"endpoint": ep, "rawModel": model, "provider": provider})
}

func (d dayAgg) bump(key string, delta any) {
	cur := toFloat(d[key])
	switch n := delta.(type) {
	case int:
		d[key] = cur + float64(n)
	case int64:
		d[key] = cur + float64(n)
	case float64:
		d[key] = cur + n
	}
}

func (d dayAgg) nested(group, key string, vals counter, meta map[string]any) {
	if key == "" {
		return
	}
	nested, _ := d[group].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
		d[group] = nested
	}
	cur, _ := nested[key].(map[string]any)
	if cur == nil {
		cur = map[string]any{}
		nested[key] = cur
	}
	cur["requests"] = toFloat(cur["requests"]) + float64(vals.Requests)
	cur["promptTokens"] = toFloat(cur["promptTokens"]) + float64(vals.PromptTokens)
	cur["completionTokens"] = toFloat(cur["completionTokens"]) + float64(vals.CompletionTokens)
	cur["cachedTokens"] = toFloat(cur["cachedTokens"]) + float64(vals.CachedTokens)
	cur["cost"] = toFloat(cur["cost"]) + vals.Cost
	for k, v := range meta {
		cur[k] = v
	}
}

type counter struct {
	Requests, PromptTokens, CompletionTokens, CachedTokens int64
	Cost                                                   float64
}

// ModelStats is one row of the usage tables. Keys are the display key the
// dashboard groups on; RawModel/Provider/AccountName feed the other columns.
type ModelStats struct {
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	RawModel         string  `json:"rawModel,omitempty"`
	Provider         string  `json:"provider,omitempty"`
	ConnectionID     string  `json:"connectionId,omitempty"`
	AccountName      string  `json:"accountName,omitempty"`
	APIKeyMasked     *string `json:"apiKeyMasked"`
	KeyName          string  `json:"keyName,omitempty"`
	APIKeyKey        string  `json:"apiKeyKey,omitempty"`
	Endpoint         string  `json:"endpoint,omitempty"`
	LastUsed         string  `json:"lastUsed,omitempty"`
}

// UsageStats aggregates for /api/usage/stats, matching the reference contract.
// Period: today|24h|7d|30d|60d|all. Everything is aggregated from usageHistory
// so there is a single source of truth (the daily rollup is a write-side cache
// and re-reading it would double-count what the overlay then patches). The
// query is one pass over the period's rows — a year of history is a few MB.
func (s *Store) UsageStats(period string) (map[string]any, error) {
	since := periodStart(period)
	q := `SELECT timestamp, COALESCE(provider,''), COALESCE(model,''), COALESCE(connectionId,''),
		COALESCE(apiKey,''), COALESCE(endpoint,''), COALESCE(promptTokens,0), COALESCE(completionTokens,0),
		COALESCE(cost,0), COALESCE(status,''), COALESCE(tokens,'')
		FROM usageHistory`
	var args []any
	if since != "" {
		q += ` WHERE timestamp >= ?`
		args = append(args, since)
	}
	q += ` ORDER BY id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	connNames, _ := s.connNames()
	keyNames, _ := s.apiKeyNames()
	nodes, _ := s.ProviderNodes()

	agg := newStatsAgg()
	for rows.Next() {
		var ts, prov, model, conn, apiKey, endpoint, status, tokens string
		var pt, ct int64
		var cost float64
		if err := rows.Scan(&ts, &prov, &model, &conn, &apiKey, &endpoint, &pt, &ct, &cost, &status, &tokens); err != nil {
			return nil, err
		}
		tk := parseJSON[map[string]any](tokens, map[string]any{})
		cached := toInt(tk["cached_tokens"])
		if cached == 0 {
			cached = toInt(tk["cache_read_input_tokens"])
		}
		// The prompt counter already includes cache reads (chat.go adds them),
		// so cached must not be added on top here.
		agg.add(usageRow{ts: ts, prov: prov, model: model, conn: conn, apiKey: apiKey,
			endpoint: endpoint, prompt: pt, completion: ct, cached: cached, cost: cost},
			connNames, keyNames, nodes, false)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := agg.stats
	out["period"] = period
	out["totalRequests"] = 0
	var totalReq int64
	for _, p := range agg.byProvider {
		totalReq += p.Requests
	}
	out["totalRequests"] = totalReq
	out["totalPromptTokens"] = agg.totalPrompt
	out["totalCompletionTokens"] = agg.totalCompletion
	out["totalCachedTokens"] = agg.totalCached
	out["totalCost"] = agg.totalCost
	out["byProvider"] = agg.byProvider
	out["byModel"] = agg.byModel
	out["byAccount"] = agg.byAccount
	out["byApiKey"] = agg.byApiKey
	out["byEndpoint"] = agg.byEndpoint
	out["last10Minutes"] = s.last10Minutes()
	out["pending"], out["activeRequests"], out["recentRequests"], out["errorProvider"] = s.pendingView(connNames)
	return out, nil
}

// usageRow is the flattened history row the aggregator consumes.
type usageRow struct {
	ts, prov, model, conn, apiKey, endpoint string
	prompt, completion, cached              int64
	cost                                    float64
}

type statsAgg struct {
	stats           map[string]any
	byProvider      map[string]*ModelStats
	byModel         map[string]*ModelStats
	byAccount       map[string]*ModelStats
	byApiKey        map[string]*ModelStats
	byEndpoint      map[string]*ModelStats
	totalPrompt     int64
	totalCompletion int64
	totalCached     int64
	totalCost       float64
}

func newStatsAgg() *statsAgg {
	return &statsAgg{
		stats:      map[string]any{},
		byProvider: map[string]*ModelStats{},
		byModel:    map[string]*ModelStats{},
		byAccount:  map[string]*ModelStats{},
		byApiKey:   map[string]*ModelStats{},
		byEndpoint: map[string]*ModelStats{},
	}
}

func (a *statsAgg) add(r usageRow, connNames, keyNames map[string]string, nodes map[string]string, _ bool) {
	providerDisplay := nodes[r.prov]
	if providerDisplay == "" {
		providerDisplay = r.prov
	}
	a.totalPrompt += r.prompt
	a.totalCompletion += r.completion
	a.totalCached += r.cached
	a.totalCost += r.cost

	addRow(a.byProvider, r.prov, &ModelStats{}, r, "")
	modelKey := r.model
	if r.prov != "" {
		modelKey = r.model + " (" + r.prov + ")"
	}
	m := addRow(a.byModel, modelKey, &ModelStats{RawModel: r.model, Provider: providerDisplay}, r, r.ts)
	touchLastUsed(m, r.ts)

	if r.conn != "" {
		accountName := connNames[r.conn]
		if accountName == "" {
			accountName = "Account " + shortID(r.conn) + "..."
		}
		accKey := r.model + " (" + r.prov + " - " + accountName + ")"
		acc := addRow(a.byAccount, accKey, &ModelStats{RawModel: r.model, Provider: providerDisplay,
			ConnectionID: r.conn, AccountName: accountName}, r, r.ts)
		touchLastUsed(acc, r.ts)
	}

	masked, name := maskAndName(r.apiKey, keyNames)
	if r.apiKey == "" {
		name = "Local (No API Key)"
	}
	akKey := "local-no-key"
	if r.apiKey != "" {
		akKey = r.apiKey + "|" + r.model + "|" + firstNonEmpty(r.prov, "unknown")
	}
	ak := addRow(a.byApiKey, akKey, &ModelStats{RawModel: r.model, Provider: providerDisplay,
		APIKeyMasked: masked, KeyName: name, APIKeyKey: firstNonEmpty(derefStr(masked), "local-no-key")}, r, r.ts)
	touchLastUsed(ak, r.ts)

	endpoint := firstNonEmpty(r.endpoint, "Unknown")
	epKey := endpoint + "|" + r.model + "|" + firstNonEmpty(r.prov, "unknown")
	ep := addRow(a.byEndpoint, epKey, &ModelStats{Endpoint: endpoint, RawModel: r.model, Provider: providerDisplay}, r, r.ts)
	touchLastUsed(ep, r.ts)
}

// addRow merges one history row into a display-keyed bucket.
func addRow(m map[string]*ModelStats, key string, seed *ModelStats, r usageRow, ts string) *ModelStats {
	if key == "" {
		key = "unknown"
	}
	cur := m[key]
	if cur == nil {
		seed.LastUsed = ts
		m[key] = seed
		cur = seed
	}
	cur.Requests++
	cur.PromptTokens += r.prompt
	cur.CompletionTokens += r.completion
	cur.CachedTokens += r.cached
	cur.Cost += r.cost
	return cur
}

func touchLastUsed(m *ModelStats, ts string) {
	if ts > m.LastUsed {
		m.LastUsed = ts
	}
}

// last10Minutes returns 10 fixed minute buckets ending at the current minute,
// each {requests,promptTokens,completionTokens,cost} — the live sparkline.
func (s *Store) last10Minutes() []map[string]any {
	now := time.Now()
	minute := now.Truncate(time.Minute)
	out := make([]map[string]any, 10)
	idx := map[int64]int{}
	for i := 0; i < 10; i++ {
		out[i] = map[string]any{"requests": int64(0), "promptTokens": int64(0),
			"completionTokens": int64(0), "cost": float64(0)}
		idx[minute.Add(time.Duration(i-9)*time.Minute).Unix()] = i
	}
	start := minute.Add(-9 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	rows, err := s.db.Query(`SELECT timestamp, COALESCE(promptTokens,0), COALESCE(completionTokens,0), COALESCE(cost,0)
		FROM usageHistory WHERE timestamp >= ?`, start)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ts string
		var pt, ct int64
		var cost float64
		if rows.Scan(&ts, &pt, &ct, &cost) != nil {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			continue
		}
		i, ok := idx[t.UTC().Truncate(time.Minute).Unix()]
		if !ok {
			continue
		}
		b := out[i]
		b["requests"] = b["requests"].(int64) + 1
		b["promptTokens"] = b["promptTokens"].(int64) + pt
		b["completionTokens"] = b["completionTokens"].(int64) + ct
		b["cost"] = b["cost"].(float64) + cost
	}
	return out
}

// connNames maps connection id → display name for the account column.
func (s *Store) connNames() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(name,''), COALESCE(email,'') FROM connections`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name, email string
		if rows.Scan(&id, &name, &email) != nil {
			continue
		}
		out[id] = firstNonEmpty(name, email)
	}
	return out, rows.Err()
}

// apiKeyNames maps a raw API key → the name its owner gave it.
func (s *Store) apiKeyNames() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT COALESCE(key,''), COALESCE(name,'') FROM apiKeys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, name string
		if rows.Scan(&key, &name) != nil {
			continue
		}
		out[key] = name
	}
	return out, rows.Err()
}

// RecentUsage returns the newest N rows for the usage table / logs.
func (s *Store) RecentUsage(limit int) ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT timestamp, COALESCE(provider,''), COALESCE(model,''), COALESCE(connectionId,''),
		COALESCE(promptTokens,0), COALESCE(completionTokens,0), COALESCE(cost,0), COALESCE(status,''), COALESCE(endpoint,'')
		FROM usageHistory ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var ts, prov, model, conn, status, endpoint string
		var pt, ct int64
		var cost float64
		if err := rows.Scan(&ts, &prov, &model, &conn, &pt, &ct, &cost, &status, &endpoint); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"timestamp": ts, "provider": prov, "model": model, "connectionId": conn,
			"promptTokens": pt, "completionTokens": ct, "cost": cost, "status": status, "endpoint": endpoint,
		})
	}
	return out, rows.Err()
}

// UsageChart returns the reference's flat array of labelled buckets:
// [{label,tokens,cost,requests}] with a fixed bucket count per period
// (24 hourly for today/24h, one per day for 7d/30d/60d/all, gaps zero-filled).
func (s *Store) UsageChart(period string) ([]map[string]any, error) {
	now := time.Now()
	switch period {
	case "today", "24h":
		var start time.Time
		if period == "today" {
			start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		} else {
			start = now.Add(-24 * time.Hour).Truncate(time.Hour)
		}
		label := func(t time.Time) string { return t.Format("15:04") }
		return s.hourlyBuckets(start, 24, label)
	}

	var days int
	var start time.Time
	switch period {
	case "7d":
		days = 7
	case "30d":
		days = 30
	case "60d":
		days = 60
	case "all":
		var earliest string
		if err := s.db.QueryRow(`SELECT COALESCE(MIN(substr(timestamp,1,10)),'') FROM usageHistory`).Scan(&earliest); err != nil {
			return nil, err
		}
		if earliest == "" {
			return []map[string]any{}, nil
		}
		d, err := time.Parse("2006-01-02", earliest)
		if err != nil {
			return []map[string]any{}, nil
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		days = int(today.Sub(d).Hours()/24) + 1
		if days < 1 {
			days = 1
		}
	default:
		days = 7
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start = today.AddDate(0, 0, -(days - 1))
	return s.dailyBuckets(start, days)
}

func (s *Store) hourlyBuckets(start time.Time, count int, label func(time.Time) string) ([]map[string]any, error) {
	out := make([]map[string]any, count)
	keyIdx := map[string]int{}
	for i := 0; i < count; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		out[i] = map[string]any{"label": label(at), "tokens": int64(0), "cost": float64(0), "requests": int64(0)}
		keyIdx[at.Format("2006-01-02T15")] = i
	}
	return s.fillBuckets(out, keyIdx, "2006-01-02T15", `substr(timestamp,1,13)`, start)
}

func (s *Store) dailyBuckets(start time.Time, count int) ([]map[string]any, error) {
	out := make([]map[string]any, count)
	keyIdx := map[string]int{}
	for i := 0; i < count; i++ {
		at := start.AddDate(0, 0, i)
		out[i] = map[string]any{"label": at.Format("Jan 2"), "tokens": int64(0), "cost": float64(0), "requests": int64(0)}
		keyIdx[at.Format("2006-01-02")] = i
	}
	return s.fillBuckets(out, keyIdx, "2006-01-02", `substr(timestamp,1,10)`, start)
}

func (s *Store) fillBuckets(out []map[string]any, keyIdx map[string]int, layout, expr string, start time.Time) ([]map[string]any, error) {
	since := start.UTC().Format("2006-01-02T15:04:05.000Z")
	rows, err := s.db.Query(`SELECT `+expr+`, COALESCE(SUM(promptTokens),0)+COALESCE(SUM(completionTokens),0),
		COALESCE(SUM(cost),0), COUNT(*) FROM usageHistory WHERE timestamp >= ? GROUP BY 1`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var bucket string
		var tokens, reqs int64
		var cost float64
		if err := rows.Scan(&bucket, &tokens, &cost, &reqs); err != nil {
			return nil, err
		}
		at, err := time.Parse(layout, bucket)
		if err != nil {
			continue
		}
		i, ok := keyIdx[at.Format(layout)]
		if !ok {
			continue
		}
		b := out[i]
		b["tokens"] = b["tokens"].(int64) + tokens
		b["cost"] = b["cost"].(float64) + cost
		b["requests"] = b["requests"].(int64) + reqs
	}
	return out, rows.Err()
}

// periodStart returns an ISO lower bound for a period keyword.
func periodStart(period string) string {
	now := time.Now()
	switch period {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UTC().Format("2006-01-02T15:04:05.000Z")
	case "24h":
		return now.Add(-24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	case "7d":
		return now.AddDate(0, 0, -7).UTC().Format("2006-01-02T15:04:05.000Z")
	case "30d":
		return now.AddDate(0, 0, -30).UTC().Format("2006-01-02T15:04:05.000Z")
	case "60d":
		return now.AddDate(0, 0, -60).UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return ""
}

// SaveRequestDetail stores an observability record (bounded, oldest trimmed).
func (s *Store) SaveRequestDetail(id, ts, provider, model, connID, status string, data any, maxRecords int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO requestDetails(id,timestamp,provider,model,connectionId,status,data)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET timestamp=excluded.timestamp,
		provider=excluded.provider, model=excluded.model, connectionId=excluded.connectionId,
		status=excluded.status, data=excluded.data`,
		id, ts, nullableStr(provider), nullableStr(model),
		nullableStr(connID), nullableStr(status), stringifyJSON(data)); err != nil {
		return err
	}
	if maxRecords > 0 {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM requestDetails`).Scan(&count); err != nil {
			return err
		}
		if count > maxRecords {
			if _, err := tx.Exec(`DELETE FROM requestDetails WHERE id IN (
				SELECT id FROM requestDetails ORDER BY timestamp ASC LIMIT ?)`, count-maxRecords); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// RequestDetails returns a page of observability records in the reference
// envelope: {details:[…], pagination:{page,pageSize,totalItems,totalPages,
// hasNext,hasPrev}}.
func (s *Store) RequestDetails(page, pageSize int, provider, model, connID, status, startDate, endDate string) (map[string]any, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	where := " WHERE 1=1"
	var args []any
	for _, f := range []struct{ col, val string }{
		{"provider", provider}, {"model", model}, {"connectionId", connID}, {"status", status},
	} {
		if f.val != "" {
			where += " AND " + f.col + " = ?"
			args = append(args, f.val)
		}
	}
	if startDate != "" {
		where += " AND timestamp >= ?"
		args = append(args, startDate)
	}
	if endDate != "" {
		where += " AND timestamp <= ?"
		args = append(args, endDate)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM requestDetails`+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT data FROM requestDetails`+where+` ORDER BY timestamp DESC LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	details := []any{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		details = append(details, parseJSON[any](data, nil))
	}
	totalPages := (total + pageSize - 1) / pageSize
	return map[string]any{
		"details": details,
		"pagination": map[string]any{
			"page": page, "pageSize": pageSize, "totalItems": total, "totalPages": totalPages,
			"hasNext": page < totalPages, "hasPrev": page > 1,
		},
	}, rows.Err()
}

// ---------------------------------------------------------------- helpers

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func nullableOrEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func localDateKey() string { return time.Now().Format("2006-01-02") }

// maskAndName returns the display mask and the key's owner-given name.
func maskAndName(key string, names map[string]string) (*string, string) {
	if key == "" {
		return nil, ""
	}
	name := names[key]
	if name == "" {
		name = maskKey(key)
	}
	masked := maskKey(key)
	return &masked, name
}

func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 12 {
		return k[:1] + "***"
	}
	// Keep the tail: keys sharing a machine-id prefix must not collide.
	return k[:8] + "***" + k[len(k)-4:]
}
