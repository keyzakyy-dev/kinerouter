package mitm

// Aliases: the model mapping the MITM proxy applies per tool. Source of truth
// is SQLite kv['mitmAlias'] (a map of tool → {aliasKey: routerModel}); the
// privileged server process cannot open SQLite, so the dashboard writes a JSON
// mirror at <DATA_DIR>/mitm/aliases.json that the server reads. Mirrors
// src/lib/mitmAliasCache.js + src/mitm/dbReader.js.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type AliasMap map[string]map[string]string

// LoadAliases reads the JSON mirror. A missing or corrupt file is an empty map.
func LoadAliases(file string) AliasMap {
	out := AliasMap{}
	data, err := os.ReadFile(file)
	if err != nil {
		return out
	}
	if json.Unmarshal(data, &out) != nil {
		return AliasMap{}
	}
	return out
}

// SetToolAliases replaces one tool's mapping and writes the file atomically.
func SetToolAliases(file, tool string, mappings map[string]string) error {
	all := LoadAliases(file)
	all[tool] = mappings
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// GetAllAliases writes the whole mirror from the DB (app startup + after edit).
func WriteAliasesFile(file string, all AliasMap) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
