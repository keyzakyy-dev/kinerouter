package store

// Provider nodes: the user-registered custom endpoints the dashboard creates
// for "OpenAI compatible" / "Anthropic compatible" / "Custom embedding"
// providers. Ported from src/lib/db/repos/nodesRepo.js — id/type/name are
// columns, the rest of the shape lives in the data JSON column.

import (
	"database/sql"
	"errors"
	"sort"
	"time"
)

// ProviderNode is one custom endpoint.
type ProviderNode struct {
	ID        string         `json:"id"`
	Type      string         `json:"type,omitempty"`
	Name      string         `json:"name,omitempty"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
	Data      map[string]any `json:"-"`
}

// MarshalJSON flattens Data to the top level, like rowToNode.
func (n ProviderNode) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	for k, v := range n.Data {
		out[k] = v
	}
	out["id"] = n.ID
	out["createdAt"] = n.CreatedAt
	out["updatedAt"] = n.UpdatedAt
	if n.Type != "" {
		out["type"] = n.Type
	}
	if n.Name != "" {
		out["name"] = n.Name
	}
	return stringifyJSONBytes(out)
}

const nodeSelect = `SELECT id, COALESCE(type,''), COALESCE(name,''), data, createdAt, updatedAt FROM providerNodes`

func scanNode(sc interface{ Scan(...any) error }) (*ProviderNode, error) {
	var n ProviderNode
	var data string
	if err := sc.Scan(&n.ID, &n.Type, &n.Name, &data, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	n.Data = parseJSON[map[string]any](data, map[string]any{})
	return &n, nil
}

// ListProviderNodes returns every node, newest-updated last is not imposed by
// the JS repo, so this keeps insertion order from SQLite.
func (s *Store) ListProviderNodes() ([]*ProviderNode, error) {
	rows, err := s.db.Query(nodeSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ProviderNode{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, rows.Err()
}

func (s *Store) GetProviderNode(id string) (*ProviderNode, error) {
	n, err := scanNode(s.db.QueryRow(nodeSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return n, err
}

// SaveProviderNode inserts or fully updates a node.
func (s *Store) SaveProviderNode(n *ProviderNode) error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if n.CreatedAt == "" {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO providerNodes(id,type,name,data,createdAt,updatedAt)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET type=excluded.type, name=excluded.name,
			data=excluded.data, updatedAt=excluded.updatedAt`,
		n.ID, nullableStr(n.Type), nullableStr(n.Name), stringifyJSON(n.Data), n.CreatedAt, n.UpdatedAt)
	return err
}

// DeleteProviderNode removes the node, its connections and its custom models
// (the kv rows keyed "<nodeId>|…"), matching deleteProviderNode.
func (s *Store) DeleteProviderNode(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM providerConnections WHERE provider = ?`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM kv WHERE scope = 'customModels' AND key LIKE ?`, id+"|%"); err != nil {
		return false, err
	}
	res, err := tx.Exec(`DELETE FROM providerNodes WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, tx.Commit()
}
