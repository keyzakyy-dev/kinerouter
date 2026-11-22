// Package store is the SQLite persistence layer. Schema mirrors
// src/lib/db/schema.js byte-for-byte so an existing KineRouter DB opens here
// unchanged (SCHEMA_VERSION 2, same table/column/index names).
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 2

type Store struct {
	db *sql.DB

	// usage emitter subscribers (usageStream fan-out)
	mu   sync.RWMutex
	subs map[chan struct{}]struct{}

	// in-flight request counters for /api/usage/stats (runtime only)
	pending pending
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=cache_size(-64000)&_pragma=mmap_size(30000000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single writer connection serializes writes (SQLite anyway allows one
	// writer); readers share the pool. WAL keeps readers unblocked.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, subs: map[chan struct{}]struct{}{}}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

// migrate creates missing tables/columns/indexes additively (mirror of
// src/lib/db/migrate.js syncSchemaFromTables) and stamps _meta.
func (s *Store) migrate() error {
	for _, stmt := range schemaSQL {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	if err := s.ensureColumns(); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var v string
	err := s.db.QueryRow(`SELECT value FROM _meta WHERE key='schemaVersion'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.Exec(`INSERT INTO _meta(key,value) VALUES('schemaVersion',?),
			('appVersion',?),('migratedAt',?)`, fmt.Sprint(SchemaVersion), "kinerouter-dev", now); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

// ensureColumns adds columns declared in schema but absent on disk. Keeps the
// codebase honest when a new column is appended to schema.js without a version
// bump — same behaviour as the JS auto-sync.
func (s *Store) ensureColumns() error {
	want := map[string][]string{
		"apiKeys": {"accessRestricted INTEGER DEFAULT 0", "accessAllow TEXT"},
	}
	for table, cols := range want {
		have, err := s.columns(table)
		if err != nil {
			return err
		}
		for _, def := range cols {
			name := colName(def)
			if have[name] {
				continue
			}
			if _, err := s.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + def); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) columns(table string) (map[string]bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func colName(def string) string {
	for i := 0; i < len(def); i++ {
		if def[i] == ' ' {
			return def[:i]
		}
	}
	return def
}

// NotifyUsage wakes every /api/usage/stream subscriber. Debounced by callers.
func (s *Store) NotifyUsage() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Store) SubscribeUsage() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// jsonCol helpers ------------------------------------------------------------

func parseJSON[T any](raw string, def T) T {
	if raw == "" {
		return def
	}
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return def
	}
	return v
}

func stringifyJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func stringifyJSONBytes(v any) ([]byte, error) { return json.Marshal(v) }
