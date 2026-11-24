package gateway

// Opt-in background updates. The loop is off by default because this project
// ships often: an operator must choose to accept a release unattended. Manual
// update, the CLI and the dashboard button keep working regardless.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/updater"
)

const (
	autoUpdateDefaultInterval = 6 * time.Hour
	autoUpdateInitialDelay    = 5 * time.Second
)

func autoUpdateInterval() time.Duration {
	if h, err := strconv.Atoi(os.Getenv("AUTO_UPDATE_INTERVAL_HOURS")); err == nil && h > 0 {
		return time.Duration(h) * time.Hour
	}
	return autoUpdateDefaultInterval
}

func (g *Gateway) autoUpdateEnabled() bool {
	def := os.Getenv("AUTO_UPDATE") == "true"
	if g.store == nil {
		return def
	}
	return g.store.SettingBool("autoUpdate", def)
}

// handleVersionStatus reports the background updater's configuration and the
// last time it looked for a release.
func (g *Gateway) handleVersionStatus(w http.ResponseWriter, r *http.Request) {
	latest := ""
	if !IsDevBuild() {
		latest = latestVersionCached()
	}
	payload := versionPayload(latest)
	payload["autoUpdateEnabled"] = g.autoUpdateEnabled()
	payload["updateInProgress"] = g.updateInFlight()
	payload["lastCheckTime"] = g.lastUpdateCheck()
	payload["checkIntervalHours"] = int(autoUpdateInterval().Hours())
	writeJSON(w, http.StatusOK, payload)
}

// handleAutoUpdateToggle persists the opt-in flag. The running loop reads it
// from the store each cycle, so no process restart is needed.
func (g *Gateway) handleAutoUpdateToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "invalid request body"})
		return
	}
	if _, err := g.store.UpdateSettings(map[string]any{"autoUpdate": body.Enabled}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "autoUpdateEnabled": body.Enabled})
}

func (g *Gateway) updateInFlight() bool {
	g.updMu.Lock()
	defer g.updMu.Unlock()
	return g.updUpdating
}

func (g *Gateway) beginUpdate() bool {
	g.updMu.Lock()
	defer g.updMu.Unlock()
	if g.updUpdating {
		return false
	}
	g.updUpdating = true
	return true
}

func (g *Gateway) endUpdate() {
	g.updMu.Lock()
	defer g.updMu.Unlock()
	g.updUpdating = false
}

func (g *Gateway) lastUpdateCheck() string {
	g.updMu.Lock()
	defer g.updMu.Unlock()
	if g.updLastCheck.IsZero() {
		return ""
	}
	return g.updLastCheck.UTC().Format(time.RFC3339)
}

func (g *Gateway) markUpdateCheck() {
	g.updMu.Lock()
	g.updLastCheck = time.Now()
	g.updMu.Unlock()
}

// StartAutoUpdate launches the background loop. Call once from main after boot;
// a disabled flag costs one settings read per tick.
func (g *Gateway) StartAutoUpdate(ctx context.Context) {
	delay := autoUpdateInitialDelay
	if v, err := strconv.Atoi(os.Getenv("AUTO_UPDATE_INITIAL_SECONDS")); err == nil && v > 0 {
		delay = time.Duration(v) * time.Second
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		for {
			g.autoUpdateCycle(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(autoUpdateInterval()):
			}
		}
	}()
}

// autoUpdateCycle applies one release when the opt-in is on. It never runs a
// prerelease over a final release and never touches a docker install.
func (g *Gateway) autoUpdateCycle(ctx context.Context) {
	if IsDevBuild() || !g.autoUpdateEnabled() || InstallMethod() == updater.MethodDocker {
		return
	}
	if !g.beginUpdate() {
		return
	}
	defer g.endUpdate()

	g.markUpdateCheck()
	latest := refreshLatestVersion()
	if latest == "" || compareSemver(latest, Version) <= 0 || !autoApplyAllowed(Version, latest) {
		return
	}
	exe, _, err := g.installUpdate(ctx, latest)
	if err != nil {
		return
	}
	if err := updater.Restart(exe); err == nil {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}
}
