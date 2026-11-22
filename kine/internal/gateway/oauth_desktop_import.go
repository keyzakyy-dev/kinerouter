package gateway

// Local-desktop credential probes for Cursor and Xiaomi MiMo.
// Ported from src/app/api/oauth/cursor/auto-import/route.js and
// src/app/api/oauth/xiaomi-mimo/auto-import/route.js.
//
// Security: both routes only read their provider's own config file and return
// it masked/no-store; they never write a connection. The whole handler is
// behind requireAuth. The MiMo Desktop cookie DB is copied to a temp file
// because Chromium holds an exclusive lock while the app runs.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func (g *Gateway) registerDesktopImportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/oauth/cursor/auto-import", g.requireAuth(g.autoImportCursor))
	mux.HandleFunc("GET /api/oauth/xiaomi-mimo/auto-import", g.requireAuth(g.autoImportXiaomi))
}

// ---------------------------------------------------------------- cursor

var (
	cursorTokenKeys   = []string{"cursorAuth/accessToken", "cursorAuth/token"}
	cursorMachineKeys = []string{"storage.serviceMachineId", "storage.machineId", "telemetry.machineId"}
)

func cursorStateDBPaths() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{
			filepath.Join(home, "Library/Application Support/Cursor/User/globalStorage/state.vscdb"),
			filepath.Join(home, "Library/Application Support/Cursor - Insiders/User/globalStorage/state.vscdb"),
		}
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		return []string{
			filepath.Join(appData, "Cursor/User/globalStorage/state.vscdb"),
			filepath.Join(appData, "Cursor - Insiders/User/globalStorage/state.vscdb"),
			filepath.Join(local, "Cursor/User/globalStorage/state.vscdb"),
			filepath.Join(local, "Programs/Cursor/User/globalStorage/state.vscdb"),
		}
	}
	return []string{
		filepath.Join(home, ".config/Cursor/User/globalStorage/state.vscdb"),
		filepath.Join(home, ".config/cursor/User/globalStorage/state.vscdb"),
	}
}

// unwrapJSONValue mirrors the JS normalize: state.vscdb stores values as JSON
// strings, sometimes double-encoded.
func unwrapJSONValue(v string) string {
	var parsed any
	if json.Unmarshal([]byte(v), &parsed) == nil {
		if s, ok := parsed.(string); ok {
			return s
		}
	}
	return v
}

func cursorReadTokens(dbPath string) (accessToken, machineID string) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return "", ""
	}
	defer db.Close()
	query := func(key string) string {
		var v string
		if db.QueryRow("SELECT value FROM itemTable WHERE key=? LIMIT 1", key).Scan(&v) != nil {
			return ""
		}
		return unwrapJSONValue(v)
	}
	for _, k := range cursorTokenKeys {
		if v := query(k); v != "" {
			accessToken = v
			break
		}
	}
	for _, k := range cursorMachineKeys {
		if v := query(k); v != "" {
			machineID = v
			break
		}
	}
	return accessToken, machineID
}

func (g *Gateway) autoImportCursor(w http.ResponseWriter, r *http.Request) {
	candidates := cursorStateDBPaths()
	dbPath := ""
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			dbPath = c
			break
		}
	}
	if dbPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "Cursor database not found. Checked locations:\n" + strings.Join(candidates, "\n") +
				"\n\nMake sure Cursor IDE is installed and opened at least once."})
		return
	}
	// On Linux, a leftover config dir can outlive an uninstall.
	if runtime.GOOS == "linux" {
		installed := false
		if exec.CommandContext(r.Context(), "which", "cursor").Run() == nil {
			installed = true
		} else if home, _ := os.UserHomeDir(); home != "" {
			if _, err := os.Stat(filepath.Join(home, ".local/share/applications/cursor.desktop")); err == nil {
				installed = true
			}
		}
		if !installed {
			writeJSON(w, http.StatusOK, map[string]any{"found": false,
				"error": "Cursor config files found but Cursor IDE does not appear to be installed. Skipping auto-import."})
			return
		}
	}

	if token, machine := cursorReadTokens(dbPath); token != "" && machine != "" {
		writeJSON(w, http.StatusOK, map[string]any{"found": true,
			"accessToken": token, "machineId": machine})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": false, "windowsManual": true, "dbPath": dbPath})
}

// ---------------------------------------------------------------- xiaomi

func xiaomiAuthPaths() []string {
	home, _ := os.UserHomeDir()
	paths := []string{filepath.Join(home, ".local", "share", "mimocode", "auth.json")}
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		paths = append(paths, filepath.Join(appData, "Xiaomi MiMo", "auth.json"))
	case "darwin":
		paths = append(paths, filepath.Join(home, "Library", "Application Support", "mimocode", "auth.json"))
	}
	return paths
}

func mimoDesktopCookiePath() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Xiaomi MiMo", "Partitions", "xiaomi-account", "Network", "Cookies")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Xiaomi MiMo", "Partitions", "xiaomi-account", "Network", "Cookies")
	}
	return filepath.Join(home, ".config", "Xiaomi MiMo", "Partitions", "xiaomi-account", "Network", "Cookies")
}

// mimoDesktopPassToken copies Desktop's cookie DB out (it is exclusively locked
// while Desktop runs) and lifts the account identity cookies.
func mimoDesktopPassToken() (passToken, userID, cUserID string) {
	src := mimoDesktopCookiePath()
	if _, err := os.Stat(src); err != nil {
		return "", "", ""
	}
	tmp, err := os.CreateTemp("", "kine-mimo-cookies-*.db")
	if err != nil {
		return "", "", ""
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if copyFile(src, tmp.Name()) != nil {
		return "", "", ""
	}
	db, err := sql.Open("sqlite", "file:"+tmp.Name()+"?mode=ro")
	if err != nil {
		return "", "", ""
	}
	defer db.Close()
	rows, err := db.Query("SELECT name, value FROM cookies WHERE host_key = ?", ".account.xiaomi.com")
	if err != nil {
		return "", "", ""
	}
	defer rows.Close()
	jar := map[string]string{}
	for rows.Next() {
		var name, value string
		if rows.Scan(&name, &value) == nil && value != "" {
			jar[name] = value
		}
	}
	return jar["passToken"], jar["userId"], jar["cUserId"]
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = out.ReadFrom(in)
	return err
}

func (g *Gateway) autoImportXiaomi(w http.ResponseWriter, r *http.Request) {
	candidates := xiaomiAuthPaths()
	authPath := ""
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			authPath = c
			break
		}
	}
	if authPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "Xiaomi MiMo Desktop auth file not found. Checked:\n" + strings.Join(candidates, "\n") +
				"\n\nMake sure Xiaomi MiMo Desktop is installed and you are signed in."})
		return
	}
	raw, err := readFileLimited(authPath, 1<<20)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false, "error": err.Error()})
		return
	}
	var auth map[string]any
	if json.Unmarshal(raw, &auth) != nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "auth.json is not valid JSON. Please sign in to Xiaomi MiMo Desktop again."})
		return
	}
	xiaomi, _ := auth["xiaomi"].(map[string]any)
	key := strings.TrimSpace(strAny(xiaomi["key"]))
	if key == "" {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "No Xiaomi credentials found in auth.json. Please sign in to Xiaomi MiMo Desktop."})
		return
	}
	if !strings.HasPrefix(key, "sk-") {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "Xiaomi key does not appear to be a valid API key (expected sk- prefix)."})
		return
	}
	metadata, _ := xiaomi["metadata"].(map[string]any)
	baseURL := firstNonEmpty(strAny(metadata["base_url"]), "https://api.xiaomimimo.com/v1")
	passToken, userID, cUserID := mimoDesktopPassToken()
	var pt, uid, cuid any
	if passToken != "" {
		pt, uid, cuid = passToken, userID, cUserID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found": true, "apiKey": key, "uid": metadata["uid"], "baseUrl": baseURL,
		"source": authPath, "mimoPassToken": pt, "mimoUserId": uid, "mimoCUserId": cuid,
	})
}
