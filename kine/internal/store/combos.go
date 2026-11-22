package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Combo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind,omitempty"`
	Models    []string `json:"models"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

func scanCombo(row interface{ Scan(...any) error }) (*Combo, error) {
	var c Combo
	var kind sql.NullString
	var models string
	if err := row.Scan(&c.ID, &c.Name, &kind, &models, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Kind = kind.String
	c.Models = parseJSON[[]string](models, []string{})
	if c.Models == nil {
		c.Models = []string{}
	}
	return &c, nil
}

const comboSelect = `SELECT id, name, kind, models, createdAt, updatedAt FROM combos`

func (s *Store) ListCombos() ([]*Combo, error) {
	rows, err := s.db.Query(comboSelect + ` ORDER BY createdAt ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Combo{}
	for rows.Next() {
		c, err := scanCombo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetComboByName(name string) (*Combo, error) {
	c, err := scanCombo(s.db.QueryRow(comboSelect+` WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (s *Store) GetCombo(id string) (*Combo, error) {
	c, err := scanCombo(s.db.QueryRow(comboSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (s *Store) SaveCombo(c *Combo) (*Combo, error) {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if c.CreatedAt == "" {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	_, err := s.db.Exec(`INSERT INTO combos(id,name,kind,models,createdAt,updatedAt) VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind,
			models=excluded.models, updatedAt=excluded.updatedAt`,
		c.ID, c.Name, nullableStr(c.Kind), stringifyJSON(c.Models), c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return s.GetCombo(c.ID)
}

func (s *Store) DeleteCombo(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM combos WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// KV scopes used by the JS app (aliasRepo, customModels, mitmAlias, pricing,
// disabledModels). Values are opaque JSON strings, exactly as stored upstream.
const (
	ScopeModelAliases  = "modelAliases"
	ScopeCustomModels  = "customModels"
	ScopeMitmAlias     = "mitmAlias"
	ScopePricing       = "pricing"
	ScopeDisabledModel = "disabledModels"
)

func (s *Store) GetKV(scope string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM kv WHERE scope = ?`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SetKV(scope, key, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv(scope,key,value) VALUES(?,?,?)
		ON CONFLICT(scope,key) DO UPDATE SET value=excluded.value`, scope, key, value)
	return err
}

func (s *Store) DeleteKV(scope, key string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM kv WHERE scope=? AND key=?`, scope, key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// GetModelAliases / SetModelAlias mirror aliasRepo.js (kv scope modelAliases;
// values are JSON-encoded strings, e.g. "\"cc/claude-x\"").
func (s *Store) GetModelAliases() (map[string]string, error) {
	raw, err := s.GetKV(ScopeModelAliases)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = parseJSON[string](v, v)
	}
	return out, nil
}

func (s *Store) SetModelAlias(alias, target string) error {
	return s.SetKV(ScopeModelAliases, alias, stringifyJSON(target))
}
