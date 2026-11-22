package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The default location is this product's own directory, and nothing else on
// disk is read or moved.
func TestDataDirDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DATA_DIR", "")
	t.Setenv("APPDATA", "")

	got := DataDir()
	want := filepath.Join(home, ".kinerouter")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if again := DataDir(); again != want {
		t.Fatalf("second call: got %s", again)
	}
}

// $DATA_DIR wins over the default.
func TestDataDirEnvWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	explicit := filepath.Join(t.TempDir(), "explicit")
	t.Setenv("DATA_DIR", explicit)

	if got := DataDir(); got != explicit {
		t.Fatalf("got %s want %s", got, explicit)
	}
	if _, err := os.Stat(filepath.Join(home, ".kinerouter")); !os.IsNotExist(err) {
		t.Fatalf("default dir was created or moved: %v", err)
	}
}
