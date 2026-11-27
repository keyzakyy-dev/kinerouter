package store

// Pre-schema safety backup, ported from src/lib/db/backup.js. The reference
// takes one lightweight copy before a schema version change, excluding the
// auto-pruned requestDetails table so a huge observability log does not bloat
// every backup. There is no automatic restore path in either implementation.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const keepBackups = 3

var backupExcludeTables = map[string]bool{"requestDetails": true}

// maybeBackupBeforeSchemaChange mirrors runMigrationOnce's schemaChanging gate:
// only an existing database whose stored backupSchemaVersion is behind gets a
// backup, and only when the version actually changes.
func (s *Store) maybeBackupBeforeSchemaChange() error {
	storedText := ""
	err := s.db.QueryRow(`SELECT value FROM _meta WHERE key='backupSchemaVersion'`).Scan(&storedText)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	stored, _ := strconv.Atoi(strings.TrimSpace(storedText))
	if s.fresh || stored >= SchemaVersion {
		return nil
	}

	dir := filepath.Join(filepath.Dir(s.path), "backups",
		fmt.Sprintf("schema-%d-to-%d-%s", stored, SchemaVersion, time.Now().UTC().Format("20060102T150405.000Z")))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := s.backupLite(filepath.Join(dir, "data.sqlite")); err != nil {
		return err
	}
	pruneBackups(filepath.Join(filepath.Dir(s.path), "backups"))
	return nil
}

func (s *Store) backupLite(dest string) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	escaped := strings.ReplaceAll(dest, "'", "''")
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE '`+escaped+`' AS bak`); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, `DETACH DATABASE bak`)

	rows, err := conn.QueryContext(ctx, `SELECT name, sql FROM main.sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	type row struct{ name, ddl string }
	var tables []row
	for rows.Next() {
		var t row
		if err := rows.Scan(&t.name, &t.ddl); err != nil {
			rows.Close()
			return err
		}
		if !backupExcludeTables[t.name] {
			tables = append(tables, t)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range tables {
		ddl := strings.TrimSpace(t.ddl)
		open := strings.IndexByte(ddl, '(')
		if open < 0 {
			continue
		}
		name := `"` + strings.ReplaceAll(t.name, `"`, `""`) + `"`
		createBak := "CREATE TABLE bak." + name + " " + ddl[open:]
		if _, err := tx.ExecContext(ctx, createBak); err != nil {
			return fmt.Errorf("backup create %s: %w", t.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bak.`+name+` SELECT * FROM main.`+name); err != nil {
			return fmt.Errorf("backup copy %s: %w", t.name, err)
		}
	}
	return tx.Commit()
}

func pruneBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		mod  time.Time
	}
	var found []item
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if info, err := os.Stat(full); err == nil {
			found = append(found, item{full, info.ModTime()})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	if len(found) <= keepBackups {
		return
	}
	for _, old := range found[keepBackups:] {
		_ = os.RemoveAll(old.path)
	}
}
