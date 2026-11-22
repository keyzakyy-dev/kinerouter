package store

// Proxy pools. Ported from src/lib/db/repos/proxyPoolsRepo.js: the table holds
// id/isActive/testStatus as columns and the rest of the shape in the data JSON
// column, so an existing file opens unchanged.

import (
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

// ProxyPool is a shared outbound proxy an account can be bound to.
type ProxyPool struct {
	ID         string         `json:"id"`
	IsActive   bool           `json:"isActive"`
	TestStatus string         `json:"testStatus,omitempty"`
	CreatedAt  string         `json:"createdAt"`
	UpdatedAt  string         `json:"updatedAt"`
	Data       map[string]any `json:"-"`
}

// MarshalJSON flattens Data to the top level, like rowToPool.
func (p ProxyPool) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	for k, v := range p.Data {
		out[k] = v
	}
	out["id"] = p.ID
	out["isActive"] = p.IsActive
	if p.TestStatus != "" {
		out["testStatus"] = p.TestStatus
	}
	out["createdAt"] = p.CreatedAt
	out["updatedAt"] = p.UpdatedAt
	return stringifyJSONBytes(out)
}

const poolSelect = `SELECT id, COALESCE(isActive,1), COALESCE(testStatus,''), data, createdAt, updatedAt FROM proxyPools`

func scanPool(sc interface{ Scan(...any) error }) (*ProxyPool, error) {
	var p ProxyPool
	var active int
	var data string
	if err := sc.Scan(&p.ID, &active, &p.TestStatus, &data, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.IsActive = active == 1
	p.Data = parseJSON[map[string]any](data, map[string]any{})
	return &p, nil
}

// ListProxyPools returns pools sorted newest-updated first, as the JS repo does.
func (s *Store) ListProxyPools(activeOnly *bool) ([]*ProxyPool, error) {
	q := poolSelect
	var args []any
	if activeOnly != nil {
		if *activeOnly {
			q += ` WHERE isActive = 1`
		} else {
			q += ` WHERE isActive = 0`
		}
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ProxyPool{}
	for rows.Next() {
		p, err := scanPool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, rows.Err()
}

func (s *Store) GetProxyPool(id string) (*ProxyPool, error) {
	p, err := scanPool(s.db.QueryRow(poolSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// SaveProxyPool inserts or fully updates a pool.
func (s *Store) SaveProxyPool(p *ProxyPool) error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.TestStatus == "" {
		p.TestStatus = "unknown"
	}
	active := 0
	if p.IsActive {
		active = 1
	}
	_, err := s.db.Exec(`INSERT INTO proxyPools(id,isActive,testStatus,data,createdAt,updatedAt)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			isActive=excluded.isActive, testStatus=excluded.testStatus,
			data=excluded.data, updatedAt=excluded.updatedAt`,
		p.ID, active, p.TestStatus, stringifyJSON(p.Data), p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) DeleteProxyPool(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM proxyPools WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CountConnectionsUsingPool counts accounts bound to a pool (the delete guard).
func (s *Store) CountConnectionsUsingPool(poolID string) int {
	conns, err := s.ListConnections("", false)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range conns {
		if c.SpecificStr("proxyPoolId") == poolID {
			n++
		}
	}
	return n
}
