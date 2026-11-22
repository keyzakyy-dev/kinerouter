package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type APIKey struct {
	ID               string   `json:"id"`
	Key              string   `json:"key"`
	Name             string   `json:"name"`
	MachineID        string   `json:"machineId"`
	IsActive         bool     `json:"isActive"`
	CreatedAt        string   `json:"createdAt"`
	AccessRestricted bool     `json:"accessRestricted"`
	AccessAllow      []string `json:"accessAllow"`
}

const (
	keySelect = `SELECT id, COALESCE(key,''), COALESCE(name,''), COALESCE(machineId,''), COALESCE(isActive,1), COALESCE(createdAt,''), COALESCE(accessRestricted,0), COALESCE(accessAllow,'') FROM apiKeys`
)

func scanKey(row interface{ Scan(...any) error }) (*APIKey, error) {
	var k APIKey
	var active, restricted int
	var allow string
	if err := row.Scan(&k.ID, &k.Key, &k.Name, &k.MachineID, &active, &k.CreatedAt, &restricted, &allow); err != nil {
		return nil, err
	}
	k.IsActive = active == 1
	k.AccessRestricted = restricted == 1
	if allow != "" {
		k.AccessAllow = parseJSON[[]string](allow, nil)
	}
	if k.AccessAllow == nil {
		k.AccessAllow = []string{}
	}
	return &k, nil
}

func (s *Store) ListAPIKeys() ([]*APIKey, error) {
	rows, err := s.db.Query(keySelect + ` ORDER BY createdAt ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIKey{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) GetAPIKeyByID(id string) (*APIKey, error) {
	k, err := scanKey(s.db.QueryRow(keySelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

// GetAPIKeyByValue is the /v1 hot path. Returns nil when absent.
func (s *Store) GetAPIKeyByValue(key string) (*APIKey, error) {
	k, err := scanKey(s.db.QueryRow(keySelect+` WHERE key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

// ValidateAPIKey mirrors apiKeysRepo.validateApiKey: active row by exact key.
func (s *Store) ValidateAPIKey(key string) (bool, *APIKey, error) {
	k, err := s.GetAPIKeyByValue(key)
	if err != nil || k == nil {
		return false, nil, err
	}
	return k.IsActive, k, nil
}

func (s *Store) CreateAPIKey(name, machineID, key string) (*APIKey, error) {
	k := &APIKey{
		ID:        uuid.NewString(),
		Key:       key,
		Name:      name,
		MachineID: machineID,
		IsActive:  true,
		CreatedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	}
	_, err := s.db.Exec(`INSERT INTO apiKeys(id,key,name,machineId,isActive,createdAt,accessRestricted,accessAllow)
		VALUES(?,?,?,?,?,?,0,NULL)`, k.ID, k.Key, k.Name, k.MachineID, 1, k.CreatedAt)
	if err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Store) UpdateAPIKey(id string, patch map[string]any) (*APIKey, error) {
	cur, err := s.GetAPIKeyByID(id)
	if err != nil || cur == nil {
		return cur, err
	}
	name, machineID, key := cur.Name, cur.MachineID, cur.Key
	active, restricted := cur.IsActive, cur.AccessRestricted
	allow := cur.AccessAllow
	if v, ok := patch["name"].(string); ok {
		name = v
	}
	if v, ok := patch["machineId"].(string); ok {
		machineID = v
	}
	if v, ok := patch["key"].(string); ok && v != "" {
		key = v
	}
	if v, ok := patch["isActive"].(bool); ok {
		active = v
	}
	accessUnchanged := true
	if acc, ok := patch["access"].(map[string]any); ok {
		accessUnchanged = false
		if v, ok := acc["restricted"].(bool); ok {
			restricted = v
		}
		if v, ok := acc["allow"].([]any); ok {
			allow = allow[:0]
			for _, item := range v {
				if str, ok := item.(string); ok {
					allow = append(allow, str)
				}
			}
		}
	}
	allowJSON := "NULL"
	if accessUnchanged && cur.AccessAllow != nil && len(cur.AccessAllow) > 0 {
		allowJSON = stringifyJSON(cur.AccessAllow)
	} else if !accessUnchanged {
		allowJSON = stringifyJSON(allow)
	}
	activeInt := 0
	if active {
		activeInt = 1
	}
	restrictedInt := 0
	if restricted {
		restrictedInt = 1
	}
	if _, err := s.db.Exec(`UPDATE apiKeys SET key=?, name=?, machineId=?, isActive=?, accessRestricted=?, accessAllow=?
		WHERE id=?`, key, name, machineID, activeInt, restrictedInt, nullable(allowJSON), id); err != nil {
		return nil, err
	}
	return s.GetAPIKeyByID(id)
}

func (s *Store) DeleteAPIKey(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM apiKeys WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// nullable turns the literal "NULL" sentinel (kept for parity with the JS repo
// that writes SQL NULL) into a real sql.NullString.
func nullable(v string) any {
	if v == "NULL" {
		return nil
	}
	return v
}
