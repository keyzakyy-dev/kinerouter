package gateway

// Antigravity MITM control API. Mirrors
// src/app/api/cli-tools/antigravity-mitm/route.js (+ /alias/route.js).
//
// Security notes:
//   - Only authenticated dashboard sessions reach these routes (requireAuth).
//   - sudoPassword is never echoed back and never logged; at rest it is sealed
//     with AES-256-GCM keyed off the machine id (internal/mitm/crypto.go).
//   - The privileged child is this same binary spawned through `sudo -S sh -c`
//     with a fixed argument list — no request value is ever interpolated into
//     the shell line (router base URL is single-quoted and validated http/https).
//   - /etc/hosts writes come from the fixed ToolHosts allowlist, never from a
//     request-supplied hostname; `tool` is validated against that map.
//   - The alias JSON mirror is written atomically with mode 0600.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/mitm"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

func (g *Gateway) registerMitmRoutes(mux *http.ServeMux) {
	mitm.InitHooks(g.store.GetSettings, g.store.UpdateSettings)
	mitm.SetMachineID(g.machineID)
	if g.cfg != nil && g.cfg.DataDir != "" { // routes may be built before config in tests
		g.syncMitmAliases()
	}

	r := g.requireAuth
	mux.HandleFunc("GET /api/cli-tools/antigravity-mitm", r(g.handleMitmGet))
	mux.HandleFunc("POST /api/cli-tools/antigravity-mitm", r(g.handleMitmStart))
	mux.HandleFunc("DELETE /api/cli-tools/antigravity-mitm", r(g.handleMitmStop))
	mux.HandleFunc("PATCH /api/cli-tools/antigravity-mitm", r(g.handleMitmPatch))
	mux.HandleFunc("GET /api/cli-tools/antigravity-mitm/alias", r(g.handleMitmAliasGet))
	mux.HandleFunc("PUT /api/cli-tools/antigravity-mitm/alias", r(g.handleMitmAliasPut))
}

func (g *Gateway) mitmPaths() mitm.Paths { return mitm.NewPaths(g.cfg.DataDir) }

// syncMitmAliases mirrors kv['mitmAlias'] from SQLite into the JSON file the
// privileged server reads (aliases are written as JSON strings per key).
func (g *Gateway) syncMitmAliases() {
	paths := g.mitmPaths()
	all := mitm.AliasMap{}
	kv, err := g.store.GetKV(store.ScopeMitmAlias)
	if err != nil {
		return
	}
	for tool, raw := range kv {
		var mappings map[string]string
		if json.Unmarshal([]byte(raw), &mappings) != nil {
			continue
		}
		all[tool] = mappings
	}
	if len(all) == 0 {
		return // nothing to mirror; avoid creating an empty file
	}
	mitm.WriteAliasesFile(paths.AliasFile, all)
}

func (g *Gateway) handleMitmGet(w http.ResponseWriter, r *http.Request) {
	paths := g.mitmPaths()
	st := mitm.GetStatus(paths)
	hasCached := mitm.CachedPassword() != "" || mitm.LoadEncryptedPassword() != ""
	base := mitm.RouterBaseURL()
	if s, err := g.store.GetSettings(); err == nil {
		if v, _ := s["mitmRouterBaseUrl"].(string); strings.TrimSpace(v) != "" {
			base = strings.TrimSpace(v)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running":           st.Running,
		"pid":               pidOrNil(st.PID),
		"certExists":        st.CertExists,
		"certTrusted":       st.CertTrusted,
		"dnsStatus":         st.DNSStatus,
		"hasCachedPassword": hasCached,
		"isWin":             runtime.GOOS == "windows",
		"needsSudoPassword": !hasCached && mitm.SudoPasswordRequired(),
		"isAdmin":           mitm.IsRoot(),
		"mitmRouterBaseUrl": base,
	})
}

func (g *Gateway) handleMitmStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKey           string  `json:"apiKey"`
		SudoPassword     string  `json:"sudoPassword"`
		MitmRouterBase   *string `json:"mitmRouterBaseUrl"`
		ForceKillPort443 bool    `json:"forceKillPort443"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid JSON body"})
		return
	}
	pwd := mitm.Password(body.SudoPassword)
	if pwd == "" {
		pwd = mitm.LoadEncryptedPassword()
	}
	if body.APIKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing apiKey"})
		return
	}
	if pwd == "" && mitm.SudoPasswordRequired() && !mitm.IsRoot() {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing sudoPassword"})
		return
	}

	if body.MitmRouterBase != nil {
		normalized, err := mitm.ParseRouterURL(*body.MitmRouterBase)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if _, err := g.store.UpdateSettings(map[string]any{"mitmRouterBaseUrl": normalized}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to save settings"})
			return
		}
	}

	pid, err := mitm.Start(g.mitmPaths(), body.APIKey, pwd, body.ForceKillPort443)
	if err != nil {
		var busy *mitm.ErrPortBusy
		if errors.As(err, &busy) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": err.Error(), "code": "PORT_443_BUSY", "portOwner": busy.Owner,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	mitm.SetCachedPassword(pwd)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "running": true, "pid": pid})
}

func (g *Gateway) handleMitmStop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SudoPassword string `json:"sudoPassword"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	pwd := mitm.Password(body.SudoPassword)
	if pwd == "" {
		pwd = mitm.LoadEncryptedPassword()
	}
	if pwd == "" && mitm.SudoPasswordRequired() && !mitm.IsRoot() {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing sudoPassword"})
		return
	}
	if err := mitm.Stop(g.mitmPaths(), pwd); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if body.SudoPassword != "" {
		mitm.SetCachedPassword(body.SudoPassword)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "running": false})
}

// validateMitmPatch returns "" when the tool/action pair is acceptable.
// trust-cert is server-wide; enable/disable need a tool id.
func validateMitmPatch(tool, action string) string {
	if action == "" {
		return "action required"
	}
	if tool == "" && action != "trust-cert" {
		return "tool required"
	}
	return ""
}

func (g *Gateway) handleMitmPatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tool         string `json:"tool"`
		Action       string `json:"action"`
		SudoPassword string `json:"sudoPassword"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid JSON body"})
		return
	}
	if msg := validateMitmPatch(body.Tool, body.Action); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
		return
	}
	pwd := mitm.Password(body.SudoPassword)
	if pwd == "" {
		pwd = mitm.LoadEncryptedPassword()
	}
	if pwd == "" && mitm.SudoPasswordRequired() && !mitm.IsRoot() {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing sudoPassword"})
		return
	}

	switch body.Action {
	case "enable":
		if err := mitm.EnableDNS(g.mitmPaths(), body.Tool, pwd); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	case "disable":
		if err := mitm.DisableDNS(body.Tool, pwd); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	case "trust-cert":
		if err := mitm.TrustCertOnly(g.mitmPaths(), pwd); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if body.SudoPassword != "" {
			mitm.SetCachedPassword(body.SudoPassword)
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "certTrusted": mitm.CertTrusted(g.mitmPaths())})
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "action must be enable, disable, or trust-cert"})
		return
	}
	if body.SudoPassword != "" {
		mitm.SetCachedPassword(body.SudoPassword)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "dnsStatus": mitm.DNSStatus()})
}

// ---------------------------------------------------------------- aliases

func (g *Gateway) handleMitmAliasGet(w http.ResponseWriter, r *http.Request) {
	kv, err := g.store.GetKV(store.ScopeMitmAlias)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to fetch aliases"})
		return
	}
	if tool := r.URL.Query().Get("tool"); tool != "" {
		var one map[string]string
		json.Unmarshal([]byte(kv[tool]), &one)
		if one == nil {
			one = map[string]string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"aliases": one})
		return
	}
	all := map[string]map[string]string{}
	for tool, raw := range kv {
		var m map[string]string
		if json.Unmarshal([]byte(raw), &m) == nil {
			all[tool] = m
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"aliases": all})
}

func (g *Gateway) handleMitmAliasPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tool     string            `json:"tool"`
		Mappings map[string]string `json:"mappings"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tool and mappings required"})
		return
	}
	if body.Tool == "" || body.Mappings == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tool and mappings required"})
		return
	}
	if !mitm.DNSStatus()[body.Tool] {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "DNS must be enabled for " + body.Tool + " before editing model mappings",
		})
		return
	}
	filtered := map[string]string{}
	for alias, model := range body.Mappings {
		if t := strings.TrimSpace(model); t != "" {
			filtered[alias] = t
		}
	}
	encoded, _ := json.Marshal(filtered)
	if err := g.store.SetKV(store.ScopeMitmAlias, body.Tool, string(encoded)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to save aliases"})
		return
	}
	mitm.SetToolAliases(g.mitmPaths().AliasFile, body.Tool, filtered)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "aliases": filtered})
}

func pidOrNil(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
