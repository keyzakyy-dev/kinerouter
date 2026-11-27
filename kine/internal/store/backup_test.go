package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateTakesLightweightBackupBeforeSchemaChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.sqlite")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO providerConnections(id,provider,authType,data,createdAt,updatedAt)
		VALUES('c1','openai','apikey','{}','2026-01-01','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO requestDetails(id,timestamp,data) VALUES('r1','2026-01-01','{}')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a database written by an older build: its backup marker is 1,
	// while the current schema is 2.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE _meta SET value='1' WHERE key='backupSchemaVersion'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	backupRoot := filepath.Join(filepath.Dir(path), "backups")
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		t.Fatalf("no backup created: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("backup count = %d, want 1", len(entries))
	}
	backupPath := filepath.Join(backupRoot, entries[0].Name(), "data.sqlite")
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup db missing: %v", err)
	}

	backup, err := sql.Open("sqlite", "file:"+backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var count int
	if err := backup.QueryRow(`SELECT COUNT(*) FROM providerConnections`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("backup connection count = %d (%v), want 1", count, err)
	}
	if _, err := backup.QueryRow(`SELECT 1 FROM requestDetails LIMIT 1`).Scan(&count); err == nil {
		t.Fatal("requestDetails should be excluded from the lightweight backup")
	}
}
