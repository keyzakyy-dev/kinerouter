package store

import (
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Connection is a provider account. Extra fields live in Data (JSON) exactly as
// in the JS repo, so token shapes, model locks and providerSpecificData all
// round-trip.
type Connection struct {
	ID        string         `json:"id"`
	Provider  string         `json:"provider"`
	AuthType  string         `json:"authType"`
	Name      string         `json:"name,omitempty"`
	Email     string         `json:"email,omitempty"`
	Priority  int            `json:"priority,omitempty"`
	IsActive  bool           `json:"isActive"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
	Data      map[string]any `json:"-"`
}

func (c *Connection) Get(key string) any {
	if c.Data == nil {
		return nil
	}
	return c.Data[key]
}

func (c *Connection) Str(key string) string {
	if v, ok := c.Get(key).(string); ok {
		return v
	}
	return ""
}

// ModelLockKeys returns the connection's Data keys of the form modelLock_*.
func (c *Connection) ModelLockKeys() []string {
	out := []string{}
	for k := range c.Data {
		if len(k) > 10 && k[:10] == "modelLock_" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Connection) Specific() map[string]any {
	if v, ok := c.Get("providerSpecificData").(map[string]any); ok {
		return v
	}
	return nil
}

func (c *Connection) SpecificStr(key string) string {
	if v, ok := c.Specific()[key].(string); ok {
		return v
	}
	return ""
}

// MarshalJSON flattens Data back into the top-level object, matching
// connectionsRepo.rowToConn so the HTTP response is byte-compatible.
func (c Connection) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	for k, v := range c.Data {
		out[k] = v
	}
	out["id"] = c.ID
	out["provider"] = c.Provider
	out["authType"] = c.AuthType
	out["isActive"] = c.IsActive
	out["createdAt"] = c.CreatedAt
	out["updatedAt"] = c.UpdatedAt
	if c.Name != "" {
		out["name"] = c.Name
	}
	if c.Email != "" {
		out["email"] = c.Email
	}
	if c.Priority != 0 {
		out["priority"] = c.Priority
	}
	return stringifyJSONBytes(out)
}

func scanConn(row interface{ Scan(...any) error }) (*Connection, error) {
	var c Connection
	var name, email sql.NullString
	var priority sql.NullInt64
	var active int
	var data string
	if err := row.Scan(&c.ID, &c.Provider, &c.AuthType, &name, &email, &priority, &active, &data, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Name = name.String
	c.Email = email.String
	c.Priority = int(priority.Int64)
	c.IsActive = active == 1
	c.Data = parseJSON[map[string]any](data, map[string]any{})
	return &c, nil
}

const connSelect = `SELECT id, provider, authType, name, email, priority, COALESCE(isActive,1), data, createdAt, updatedAt FROM providerConnections`

// ListConnections returns active (or all) connections for a provider sorted by
// priority ascending then updatedAt descending — the order auth.go relies on.
func (s *Store) ListConnections(provider string, activeOnly bool) ([]*Connection, error) {
	q := connSelect
	var args []any
	switch {
	case provider != "" && activeOnly:
		q += ` WHERE provider = ? AND isActive = 1`
		args = append(args, provider)
	case provider != "":
		q += ` WHERE provider = ?`
		args = append(args, provider)
	case activeOnly:
		q += ` WHERE isActive = 1`
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Connection{}
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := out[i].Priority, out[j].Priority
		if pi == 0 {
			pi = 999
		}
		if pj == 0 {
			pj = 999
		}
		if pi != pj {
			return pi < pj
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, rows.Err()
}

func (s *Store) GetConnection(id string) (*Connection, error) {
	c, err := scanConn(s.db.QueryRow(connSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// UpsertConnection writes a full connection row.
func (s *Store) UpsertConnection(c *Connection) error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if c.CreatedAt == "" {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	active := 0
	if c.IsActive {
		active = 1
	}
	_, err := s.db.Exec(`INSERT INTO providerConnections(id,provider,authType,name,email,priority,isActive,data,createdAt,updatedAt)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			provider=excluded.provider, authType=excluded.authType, name=excluded.name,
			email=excluded.email, priority=excluded.priority, isActive=excluded.isActive,
			data=excluded.data, updatedAt=excluded.updatedAt`,
		c.ID, c.Provider, c.AuthType, nullableStr(c.Name), nullableStr(c.Email), nullInt(c.Priority), active,
		stringifyJSON(c.Data), c.CreatedAt, c.UpdatedAt)
	return err
}

// PatchConnection merges data keys into an existing connection. Used by the
// model-lock / rate-limit machinery which touches one key at a time.
func (s *Store) PatchConnection(id string, patch map[string]any) (*Connection, error) {
	c, err := s.GetConnection(id)
	if err != nil || c == nil {
		return c, err
	}
	if c.Data == nil {
		c.Data = map[string]any{}
	}
	for k, v := range patch {
		c.Data[k] = v
	}
	return c, s.UpsertConnection(c)
}

func (s *Store) DeleteConnection(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM providerConnections WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func nullableStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
