package store

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"time"
)

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// formatLogDate renders `dd-mm-yyyy hh:mm:ss` in local time (formatLogDate).
func formatLogDate(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		if t2, err2 := time.Parse("2006-01-02T15:04:05.000Z07:00", ts); err2 == nil {
			t = t2
		} else {
			return ts
		}
	}
	return t.Local().Format("02-01-2006 15:04:05")
}

// Custom-model + disabled-model registry helpers. Ported from
// src/lib/db/repos/aliasRepo.js (customModels) and disabledModelsRepo.js
// (disabledModels). Both are plain kv rows, so an existing KineRouter DB is read
// and written unchanged.

// ListCustomModels returns every custom row (key `${alias}|${id}|${type}`).
func (s *Store) ListCustomModels() ([]map[string]any, error) {
	raw, err := s.GetKV(ScopeCustomModels)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		if m := parseJSON[map[string]any](raw[k], nil); m != nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// AddCustomModel upserts one row. An existing key merges the new name/caps/
// transport over the stored object (addCustomModel in the JS repo); a new key
// inserts the full object. Returns true when the row was newly added.
func (s *Store) AddCustomModel(providerAlias, id, typ, name string, caps map[string]any, transport string) (bool, error) {
	key := providerAlias + "|" + id + "|" + typ
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var existing string
	err = tx.QueryRow(`SELECT value FROM kv WHERE scope=? AND key=?`, ScopeCustomModels, key).Scan(&existing)
	if err == nil {
		next := parseJSON[map[string]any](existing, map[string]any{})
		if name != "" {
			next["name"] = name
		}
		if caps != nil {
			next["caps"] = caps
		}
		if transport != "" {
			next["transport"] = transport
		}
		if _, err := tx.Exec(`UPDATE kv SET value=? WHERE scope=? AND key=?`,
			stringifyJSON(next), ScopeCustomModels, key); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	value := map[string]any{"providerAlias": providerAlias, "id": id, "type": typ}
	if name != "" {
		value["name"] = name
	} else {
		value["name"] = id
	}
	if caps != nil {
		value["caps"] = caps
	}
	if transport != "" {
		value["transport"] = transport
	}
	if _, err := tx.Exec(`INSERT INTO kv(scope,key,value) VALUES(?,?,?)`,
		ScopeCustomModels, key, stringifyJSON(value)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// DeleteCustomModel removes one row (deleteCustomModel).
func (s *Store) DeleteCustomModel(providerAlias, id, typ string) error {
	_, err := s.DeleteKV(ScopeCustomModels, providerAlias+"|"+id+"|"+typ)
	return err
}

// DisabledModels returns alias → disabled model ids for every provider.
func (s *Store) DisabledModels() (map[string][]string, error) {
	raw, err := s.GetKV(ScopeDisabledModel)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(raw))
	for k, v := range raw {
		out[k] = parseJSON[[]string](v, []string{})
	}
	return out, nil
}

// DisableModels merges ids into the per-provider list (read-merge-write in one
// tx, matching disableModels' de-dupe).
func (s *Store) DisableModels(providerAlias string, ids []string) error {
	if providerAlias == "" || len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	current := []string{}
	var existing string
	err = tx.QueryRow(`SELECT value FROM kv WHERE scope=? AND key=?`, ScopeDisabledModel, providerAlias).Scan(&existing)
	if err == nil {
		current = parseJSON[[]string](existing, []string{})
	} else if err != sql.ErrNoRows {
		return err
	}
	seen := map[string]bool{}
	merged := make([]string, 0, len(current)+len(ids))
	for _, v := range append(append([]string{}, current...), ids...) {
		if !seen[v] {
			seen[v] = true
			merged = append(merged, v)
		}
	}
	if _, err := tx.Exec(`INSERT INTO kv(scope,key,value) VALUES(?,?,?)
		ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value`,
		ScopeDisabledModel, providerAlias, stringifyJSON(merged)); err != nil {
		return err
	}
	return tx.Commit()
}

// EnableModels removes ids from the list; an empty ids slice re-enables the
// whole provider (the key is deleted).
func (s *Store) EnableModels(providerAlias string, ids []string) error {
	if providerAlias == "" {
		return nil
	}
	if len(ids) == 0 {
		_, err := s.DeleteKV(ScopeDisabledModel, providerAlias)
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existing string
	err = tx.QueryRow(`SELECT value FROM kv WHERE scope=? AND key=?`, ScopeDisabledModel, providerAlias).Scan(&existing)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	remove := map[string]bool{}
	for _, id := range ids {
		remove[id] = true
	}
	next := []string{}
	for _, id := range parseJSON[[]string](existing, []string{}) {
		if !remove[id] {
			next = append(next, id)
		}
	}
	if len(next) == 0 {
		if _, err := tx.Exec(`DELETE FROM kv WHERE scope=? AND key=?`, ScopeDisabledModel, providerAlias); err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE kv SET value=? WHERE scope=? AND key=?`,
		stringifyJSON(next), ScopeDisabledModel, providerAlias); err != nil {
		return err
	}
	return tx.Commit()
}

// ProviderNodes returns id → node name from providerNodes (custom provider
// nodes the JS app registers per provider).
func (s *Store) ProviderNodes() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(name,'') FROM providerNodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// DistinctUsageProviders lists the providers present in usageHistory, newest
// first (base for /api/usage/providers).
func (s *Store) DistinctUsageProviders() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT provider FROM usageHistory
		WHERE provider IS NOT NULL AND provider <> '' ORDER BY provider ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UsageLogLines returns the newest N usage rows pre-formatted as the dashboard
// request log lines: `dd-mm-yyyy hh:mm:ss | model | PROVIDER | account | in |
// out | status` (getRecentLogs in usageRepo.js).
func (s *Store) UsageLogLines(limit int) ([]string, error) {
	rows, err := s.db.Query(`SELECT timestamp, COALESCE(provider,''), COALESCE(model,''), COALESCE(connectionId,''),
		COALESCE(promptTokens,0), COALESCE(completionTokens,0), COALESCE(status,'')
		FROM usageHistory ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	connName := map[string]string{}
	if conns, err := s.ListConnections("", false); err == nil {
		for _, c := range conns {
			if c.Name != "" {
				connName[c.ID] = c.Name
			} else if c.Email != "" {
				connName[c.ID] = c.Email
			}
		}
	}

	out := []string{}
	for rows.Next() {
		var ts, prov, model, conn, status string
		var pt, ct int64
		if err := rows.Scan(&ts, &prov, &model, &conn, &pt, &ct, &status); err != nil {
			return nil, err
		}
		account := "-"
		if v := connName[conn]; v != "" {
			account = v
		} else if len(conn) > 8 {
			account = conn[:8]
		} else if conn != "" {
			account = conn
		}
		out = append(out, strings.Join([]string{
			formatLogDate(ts),
			orDash(model),
			orDash(strings.ToUpper(prov)),
			account,
			itoa64(pt),
			itoa64(ct),
			orDash(status),
		}, " | "))
	}
	return out, rows.Err()
}
