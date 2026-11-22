package store

// Full-database export/import, ported from exportDb/importDb in
// src/lib/db/index.js. The payload shape is identical so a file exported by
// either implementation restores into the other.
//
// Security: the payload holds every stored credential (OAuth tokens, API keys,
// password hash). Callers must gate both directions — the gateway route
// requires a dashboard session plus a password re-check.

import (
	"database/sql"
	"errors"
	"time"
)

// ExportDB returns the whole database as the JS payload object.
func (s *Store) ExportDB() (map[string]any, error) {
	out := map[string]any{
		"modelAliases": map[string]any{},
		"customModels": []any{},
		"mitmAlias":    map[string]any{},
		"pricing":      map[string]any{},
	}
	if raw, err := s.readRawSettings(); err == nil {
		out["settings"] = raw
	}

	conns, err := s.ListConnections("", true)
	if err != nil {
		return nil, err
	}
	pc := make([]any, 0, len(conns))
	for _, c := range conns {
		m := map[string]any{}
		for k, v := range c.Data {
			m[k] = v
		}
		m["id"] = c.ID
		m["provider"] = c.Provider
		m["authType"] = c.AuthType
		m["name"] = nullableStr(c.Name)
		m["email"] = nullableStr(c.Email)
		if c.Priority != 0 {
			m["priority"] = c.Priority
		}
		m["isActive"] = c.IsActive
		m["createdAt"] = c.CreatedAt
		m["updatedAt"] = c.UpdatedAt
		pc = append(pc, m)
	}
	out["providerConnections"] = pc

	nodes, err := s.exportRows(`SELECT id, COALESCE(type,''), COALESCE(name,''), data, createdAt, updatedAt FROM providerNodes`,
		[]string{"id", "type", "name"}, "data")
	if err != nil {
		return nil, err
	}
	out["providerNodes"] = nodes

	pools, err := s.ListProxyPools(nil)
	if err != nil {
		return nil, err
	}
	pp := make([]any, 0, len(pools))
	for _, p := range pools {
		m := map[string]any{}
		for k, v := range p.Data {
			m[k] = v
		}
		m["id"] = p.ID
		m["isActive"] = p.IsActive
		m["testStatus"] = nullableStr(p.TestStatus)
		m["createdAt"] = p.CreatedAt
		m["updatedAt"] = p.UpdatedAt
		pp = append(pp, m)
	}
	out["proxyPools"] = pp

	keys, err := s.ListAPIKeys()
	if err != nil {
		return nil, err
	}
	ak := make([]any, 0, len(keys))
	for _, k := range keys {
		access := map[string]any{"restricted": k.AccessRestricted}
		if k.AccessRestricted {
			access["allow"] = k.AccessAllow
		}
		ak = append(ak, map[string]any{
			"id": k.ID, "key": k.Key, "name": nullableStr(k.Name),
			"machineId": nullableStr(k.MachineID), "isActive": k.IsActive,
			"createdAt": k.CreatedAt, "access": access,
		})
	}
	out["apiKeys"] = ak

	combos, err := s.ListCombos()
	if err != nil {
		return nil, err
	}
	cb := make([]any, 0, len(combos))
	for _, c := range combos {
		cb = append(cb, map[string]any{
			"id": c.ID, "name": c.Name, "kind": nullableStr(c.Kind),
			"models": c.Models, "createdAt": c.CreatedAt, "updatedAt": c.UpdatedAt,
		})
	}
	out["combos"] = cb

	for scope, dst := range map[string]string{
		"modelAliases": "modelAliases", "mitmAlias": "mitmAlias", "pricing": "pricing",
	} {
		raw, err := s.GetKV(scope)
		if err != nil {
			return nil, err
		}
		m := out[dst].(map[string]any)
		for k, v := range raw {
			m[k] = parseJSON[any](v, v)
		}
	}
	if raw, err := s.GetKV("customModels"); err == nil {
		list := []any{}
		for _, v := range raw {
			list = append(list, parseJSON[any](v, v))
		}
		out["customModels"] = list
	}
	return out, nil
}

// exportRows flattens a table whose JSON blob column is data.
func (s *Store) exportRows(query string, cols []string, blobCol string) ([]any, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		vals := make([]any, len(cols)+3)
		ptrs := make([]any, len(vals))
		var id, createdAt, updatedAt string
		var blob string
		ptrs[0] = &id
		for i := range cols[1:] {
			str := new(string)
			ptrs[i+1] = str
			vals[i+1] = str
		}
		ptrs[len(cols)] = &blob
		ptrs[len(cols)+1] = &createdAt
		ptrs[len(cols)+2] = &updatedAt
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := parseJSON[map[string]any](blob, map[string]any{})
		m["id"] = id
		for i, c := range cols[1:] {
			if v := *(vals[i+1].(*string)); v != "" {
				m[c] = v
			}
		}
		m["createdAt"] = createdAt
		m["updatedAt"] = updatedAt
		out = append(out, m)
	}
	return out, rows.Err()
}

// ImportDB replaces the whole database with payload, inside one transaction.
func (s *Store) ImportDB(payload map[string]any) error {
	if payload == nil {
		return errors.New("Invalid database payload")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, q := range []string{
		`DELETE FROM settings`, `DELETE FROM providerConnections`, `DELETE FROM providerNodes`,
		`DELETE FROM proxyPools`, `DELETE FROM apiKeys`, `DELETE FROM combos`,
		`DELETE FROM kv WHERE scope IN ('modelAliases','customModels','mitmAlias','pricing')`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	strOr := func(m map[string]any, k, def string) string {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
		return def
	}
	rest := func(m map[string]any, drop ...string) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			out[k] = v
		}
		for _, d := range drop {
			delete(out, d)
		}
		return out
	}

	if st, ok := payload["settings"].(map[string]any); ok && st != nil {
		if _, err := tx.Exec(`INSERT INTO settings(id,data) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`,
			stringifyJSON(st)); err != nil {
			return err
		}
	}

	for _, item := range asList(payload["providerConnections"]) {
		c := item.(map[string]any)
		active := 1
		if b, ok := c["isActive"].(bool); ok && !b {
			active = 0
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO providerConnections(id,provider,authType,name,email,priority,isActive,data,createdAt,updatedAt)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			strOr(c, "id", ""), strOr(c, "provider", ""), strOr(c, "authType", "oauth"),
			optStr(c, "name"), optStr(c, "email"), optInt(c, "priority"), active,
			stringifyJSON(rest(c, "id", "provider", "authType", "name", "email", "priority", "isActive", "createdAt", "updatedAt")),
			strOr(c, "createdAt", now), strOr(c, "updatedAt", now)); err != nil {
			return err
		}
	}
	for _, item := range asList(payload["providerNodes"]) {
		n := item.(map[string]any)
		if _, err := tx.Exec(`INSERT OR REPLACE INTO providerNodes(id,type,name,data,createdAt,updatedAt) VALUES(?,?,?,?,?,?)`,
			strOr(n, "id", ""), optStr(n, "type"), optStr(n, "name"),
			stringifyJSON(rest(n, "id", "type", "name", "createdAt", "updatedAt")),
			strOr(n, "createdAt", now), strOr(n, "updatedAt", now)); err != nil {
			return err
		}
	}
	for _, item := range asList(payload["proxyPools"]) {
		p := item.(map[string]any)
		active := 1
		if b, ok := p["isActive"].(bool); ok && !b {
			active = 0
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO proxyPools(id,isActive,testStatus,data,createdAt,updatedAt) VALUES(?,?,?,?,?,?)`,
			strOr(p, "id", ""), active, strOr(p, "testStatus", "unknown"),
			stringifyJSON(rest(p, "id", "isActive", "testStatus", "createdAt", "updatedAt")),
			strOr(p, "createdAt", now), strOr(p, "updatedAt", now)); err != nil {
			return err
		}
	}
	for _, item := range asList(payload["apiKeys"]) {
		k := item.(map[string]any)
		active := 1
		if b, ok := k["isActive"].(bool); ok && !b {
			active = 0
		}
		restricted, allow := 0, ""
		if a, ok := k["access"].(map[string]any); ok {
			if b, ok := a["restricted"].(bool); ok && b {
				restricted = 1
				allow = stringifyJSON(a["allow"])
			}
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO apiKeys(id,key,name,machineId,isActive,createdAt,accessRestricted,accessAllow)
			VALUES(?,?,?,?,?,?,?,?)`,
			strOr(k, "id", ""), strOr(k, "key", ""), optStr(k, "name"), optStr(k, "machineId"),
			active, strOr(k, "createdAt", now), restricted, nullableStr(allow)); err != nil {
			return err
		}
	}
	for _, item := range asList(payload["combos"]) {
		c := item.(map[string]any)
		models, _ := c["models"].([]any)
		if _, err := tx.Exec(`INSERT OR REPLACE INTO combos(id,name,kind,models,createdAt,updatedAt) VALUES(?,?,?,?,?,?)`,
			strOr(c, "id", ""), strOr(c, "name", ""), optStr(c, "kind"),
			stringifyJSON(models), strOr(c, "createdAt", now), strOr(c, "updatedAt", now)); err != nil {
			return err
		}
	}
	if aliases, ok := payload["modelAliases"].(map[string]any); ok {
		for k, v := range aliases {
			if err := setKVtx(tx, "modelAliases", k, stringifyJSON(v)); err != nil {
				return err
			}
		}
	}
	if aliases, ok := payload["mitmAlias"].(map[string]any); ok {
		for k, v := range aliases {
			if err := setKVtx(tx, "mitmAlias", k, stringifyJSON(v)); err != nil {
				return err
			}
		}
	}
	if pricing, ok := payload["pricing"].(map[string]any); ok {
		for k, v := range pricing {
			if err := setKVtx(tx, "pricing", k, stringifyJSON(v)); err != nil {
				return err
			}
		}
	}
	for _, item := range asList(payload["customModels"]) {
		c := item.(map[string]any)
		alias := firstStr(c["providerAlias"], c["provider"])
		typ := firstStr(c["type"])
		if typ == "" {
			typ = "llm"
		}
		if err := setKVtx(tx, "customModels", alias+"|"+firstStr(c["id"])+"|"+typ, stringifyJSON(c)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func setKVtx(tx *sql.Tx, scope, key, value string) error {
	_, err := tx.Exec(`INSERT INTO kv(scope,key,value) VALUES(?,?,?)
		ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value`, scope, key, value)
	return err
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func optStr(m map[string]any, k string) any {
	if s, ok := m[k].(string); ok && s != "" {
		return s
	}
	return nil
}

func optInt(m map[string]any, k string) any {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return nil
}

func firstStr(vals ...any) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}
