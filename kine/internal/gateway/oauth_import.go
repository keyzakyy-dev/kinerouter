package gateway

// Credential import routes: POST /api/oauth/{provider}/{kind} for the providers
// whose login is not a browser flow — a pasted PAT, API key, refresh token or
// IDE token. Mirrors src/app/api/oauth/{gitlab/pat,kiro/*,cursor/import,
// codex/import-token,codex/bulk-import,grok-cli/bulk-import,xiaomi-mimo/api-key,
// zed/import}.
//
// The GUI-dependent half of the JS surface (local callback proxies, OS keyring /
// IDE DB readers) is NOT ported: it needs a desktop process on the user's
// machine. Those routes stay 404 rather than pretending to work.
// ponytail: xiaomi-mimo/login/{start,status} is not ported either — it is a
// server-side HTTP proxy (src/lib/mimoLoginSession.js, 726 lines) that pipes the
// MiMo Desktop SSO page through this origin. Add it when the browser-login lane
// is wanted beyond the imported api-key / auto-import lanes that already work.
//
// Security: every route requires a dashboard session (requireAuth). Secrets are
// written to the connection row and never echoed back; the echo the reference service does for
// xiaomi/mimo apiKey is deliberately omitted.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/netguard"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const longHorizon = 365 * 24 * time.Hour

// awsRegionRe is the same allowlist KineRouter uses to stop SSRF via region
// injection into the OIDC / Amazon Q URLs (GHSA-6mwv-4mrm-5p3m).
var awsRegionRe = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d{1,2}$`)

func (g *Gateway) registerImportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/oauth/gitlab/pat", g.requireAuth(g.importGitlabPAT))
	mux.HandleFunc("POST /api/oauth/kiro/api-key", g.requireAuth(g.importKiroAPIKey))
	mux.HandleFunc("POST /api/oauth/kiro/import", g.requireAuth(g.importKiroToken))
	mux.HandleFunc("POST /api/oauth/cursor/import", g.requireAuth(g.importCursor))
	mux.HandleFunc("POST /api/oauth/codex/import-token", g.requireAuth(g.importCodexToken))
	mux.HandleFunc("POST /api/oauth/codex/bulk-import", g.requireAuth(g.importCodexBulk))
	mux.HandleFunc("POST /api/oauth/grok-cli/bulk-import", g.requireAuth(g.importGrokBulk))
	mux.HandleFunc("POST /api/oauth/xiaomi-mimo/api-key", g.requireAuth(g.importXiaomiKey))
	mux.HandleFunc("POST /api/oauth/zed/import", g.requireAuth(g.importZed))

	// File-system auto-import probes (read-only; they never create a connection).
	mux.HandleFunc("GET /api/oauth/commandcode/auto-import", g.requireAuth(g.autoImportCommandCode))
	mux.HandleFunc("GET /api/oauth/kiro/auto-import", g.requireAuth(g.autoImportKiro))
	// Zed reads the OS keyring/Keychain; see zedkeyring.go.
	mux.HandleFunc("GET /api/oauth/zed/auto-import", g.requireAuth(g.autoImportZed))
}

func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

// newConn builds a connection row with the standard active/oauth defaults.
func newConn(provider, authType string) *store.Connection {
	return &store.Connection{Provider: provider, AuthType: authType, IsActive: true, Data: map[string]any{}}
}

// connBrief is the import response payload: identity only, never a token.
// KineRouter's routes do the same — the SPA re-reads the full row from
// /api/providers/{id} under the same session.
func connBrief(c *store.Connection) map[string]any {
	out := map[string]any{"id": c.ID, "provider": c.Provider, "authType": c.AuthType,
		"email": c.Email, "name": c.Name}
	if psd, ok := c.Data["providerSpecificData"].(map[string]any); ok {
		out["plan"], out["workspace"] = psd["chatgptPlanType"], psd["chatgptAccountId"]
	}
	return out
}

func saveConn(w http.ResponseWriter, st *store.Store, c *store.Connection) bool {
	if err := st.UpsertConnection(c); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to save connection")
		return false
	}
	return true
}

// --------------------------------------------------------------------- gitlab

func (g *Gateway) importGitlabPAT(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token   string `json:"token"`
		BaseURL string `json:"baseUrl"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		writeErr(w, http.StatusBadRequest, "Personal Access Token is required")
		return
	}
	base := strings.TrimRight(strings.TrimSpace(body.BaseURL), "/")
	if base == "" {
		base = "https://gitlab.com"
	}
	if err := checkPublicURL(base); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/api/v4/user", nil)
	req.Header.Set("Private-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := mediaClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "GitLab token verification failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeErr(w, http.StatusUnauthorized, "GitLab token verification failed: "+string(raw))
		return
	}
	var user map[string]any
	if json.Unmarshal(raw, &user) != nil {
		writeErr(w, http.StatusBadGateway, "GitLab returned an unreadable user object")
		return
	}

	conn := newConn("gitlab", "oauth")
	conn.Email = firstStr(user, "email", "public_email")
	conn.Name = firstStr(user, "name", "username")
	conn.Data["accessToken"] = token
	conn.Data["providerSpecificData"] = map[string]any{
		"username": firstStr(user, "username"),
		"email":    conn.Email,
		"name":     firstStr(user, "name"),
		"baseUrl":  base,
		"authKind": "personal_access_token",
	}
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "connection": connBrief(conn)})
	}
}

// ----------------------------------------------------------------------- kiro

func validRegion(region string) (string, bool) {
	if region == "" {
		return "us-east-1", true
	}
	return region, awsRegionRe.MatchString(region)
}

// importKiroAPIKey validates a long-lived Kiro bearer against the Amazon Q
// model catalog, then stores it with a long expiry (no refresh token).
func (g *Gateway) importKiroAPIKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKey string `json:"apiKey"`
		Region string `json:"region"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		writeErr(w, http.StatusBadRequest, "API key is required")
		return
	}
	region, ok := validRegion(body.Region)
	if !ok {
		writeErr(w, http.StatusBadRequest, "Invalid region")
		return
	}

	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet,
		"https://q."+region+".amazonaws.com/ListAvailableModels?origin=AI_EDITOR", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("TokenType", "API_KEY")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AWS-SDK-JS/3.0.0 kiro-ide/1.0.0")
	req.Header.Set("X-Amz-User-Agent", "aws-sdk-js/3.0.0 kiro-ide/1.0.0")
	resp, err := mediaClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "API key validation failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeErr(w, http.StatusBadRequest, "API key validation failed: "+string(raw))
		return
	}
	var models struct {
		Models []any `json:"models"`
	}
	if json.Unmarshal(raw, &models) != nil || len(models.Models) == 0 {
		writeErr(w, http.StatusBadRequest, "API key returned no available models")
		return
	}

	conn := newConn("kiro", "api_key")
	conn.Email = jwtEmail(key)
	conn.Data["accessToken"] = key
	conn.Data["expiresAt"] = time.Now().Add(longHorizon).UTC().Format(time.RFC3339)
	conn.Data["providerSpecificData"] = map[string]any{
		"region": region, "authMethod": "api_key", "provider": "API Key",
	}
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "connection": connBrief(conn)})
	}
}

// importKiroToken exchanges an IDE refresh token for an access token. IDC
// (organization) tokens refresh through the regional AWS OIDC endpoint with
// client credentials; social tokens use the Kiro desktop auth service.
func (g *Gateway) importKiroToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refreshToken"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		Region       string `json:"region"`
		ProfileArn   string `json:"profileArn"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	refresh := strings.TrimSpace(body.RefreshToken)
	if refresh == "" {
		writeErr(w, http.StatusBadRequest, "Refresh token is required")
		return
	}
	isIDC := body.ClientID != "" && body.ClientSecret != ""

	var endpoint string
	var payload []byte
	region, ok := validRegion(body.Region)
	if !ok {
		writeErr(w, http.StatusBadRequest, "Invalid region")
		return
	}
	if isIDC {
		endpoint = "https://oidc." + region + ".amazonaws.com/token"
		payload, _ = json.Marshal(map[string]any{
			"clientId": body.ClientID, "clientSecret": body.ClientSecret,
			"refreshToken": refresh, "grantType": "refresh_token",
		})
	} else {
		endpoint = "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken"
		payload, _ = json.Marshal(map[string]any{"refreshToken": refresh})
	}

	tok, err := postJSONBody(r.Context(), endpoint, payload, nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Token refresh failed: "+err.Error())
		return
	}
	access, _ := tok["accessToken"].(string)
	if access == "" {
		writeErr(w, http.StatusBadGateway, "Token refresh returned no access token")
		return
	}
	expiresIn := num(tok["expiresIn"])
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	newRefresh, _ := tok["refreshToken"].(string)
	if newRefresh == "" {
		newRefresh = refresh
	}
	arn, _ := tok["profileArn"].(string)
	if arn == "" {
		arn = body.ProfileArn
	}

	psd := map[string]any{
		"profileArn": arn,
		"authMethod": map[bool]string{true: "idc", false: "imported"}[isIDC],
		"provider":   map[bool]string{true: "Enterprise", false: "Imported"}[isIDC],
	}
	if isIDC {
		psd["clientId"], psd["clientSecret"], psd["region"] = body.ClientID, body.ClientSecret, region
	}

	conn := newConn("kiro", "oauth")
	conn.Email = jwtEmail(access)
	conn.Data["accessToken"] = access
	conn.Data["refreshToken"] = newRefresh
	conn.Data["expiresAt"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC().Format(time.RFC3339)
	conn.Data["providerSpecificData"] = psd
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "connection": connBrief(conn)})
	}
}

// --------------------------------------------------------------------- cursor

func (g *Gateway) importCursor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccessToken string `json:"accessToken"`
		MachineID   string `json:"machineId"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	token, machine := strings.TrimSpace(body.AccessToken), strings.TrimSpace(body.MachineID)
	if token == "" {
		writeErr(w, http.StatusBadRequest, "Access token is required")
		return
	}
	if len(token) < 50 {
		writeErr(w, http.StatusBadRequest, "Invalid token format. Token appears too short.")
		return
	}
	// Cursor validates lazily on the first protobuf call, so only the machine id
	// shape is checked here (UUID-ish). Dashes stripped before the check.
	if !cursorMachineRe.MatchString(strings.ReplaceAll(machine, "-", "")) {
		writeErr(w, http.StatusBadRequest, "Invalid machine ID format. Expected UUID format.")
		return
	}

	conn := newConn("cursor", "oauth")
	conn.Email = jwtEmail(token)
	conn.Data["accessToken"] = token
	conn.Data["expiresAt"] = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	conn.Data["providerSpecificData"] = map[string]any{
		"machineId": machine, "authMethod": "imported", "provider": "Imported",
	}
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "connection": connBrief(conn)})
	}
}

var cursorMachineRe = regexp.MustCompile(`^[a-f0-9]{32,}$`)

// ---------------------------------------------------------------------- codex

func (g *Gateway) importCodexToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccessToken string `json:"accessToken"`
		Name        string `json:"name"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	token := strings.TrimSpace(body.AccessToken)
	if token == "" {
		writeErr(w, http.StatusBadRequest, "Access token is required")
		return
	}
	conn := codexConnFrom(token, "")
	conn.Name = strings.TrimSpace(body.Name)
	conn.AuthType = "access_token"
	if conn.Name == "" {
		conn.Name = firstNonEmpty(conn.Email, "ChatGPT Access Token")
	}
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "connection": connBrief(conn)})
	}
}

// codexConnFrom builds a codex connection, backfilling email / workspace / plan
// from the token's JWT claims. Extra fields from a pasted account JSON are
// merged over the derived ones.
func codexConnFrom(accessToken, idToken string) *store.Connection {
	claims := jwtPayload(firstNonEmpty(idToken, accessToken))
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	profile, _ := claims["https://api.openai.com/profile"].(map[string]any)

	conn := newConn("codex", "oauth")
	conn.Email = firstNonEmpty(strAny(profile["email"]), strAny(claims["email"]),
		strAny(claims["preferred_username"]), jwtEmail(accessToken))
	conn.Data["accessToken"] = accessToken
	psd := map[string]any{"authMethod": "access_token"}
	if v := firstNonEmpty(strAny(auth["chatgpt_account_id"]), strAny(claims["account_id"])); v != "" {
		psd["chatgptAccountId"] = v
	}
	if v := firstNonEmpty(strAny(auth["chatgpt_plan_type"]), strAny(claims["plan_type"])); v != "" {
		psd["chatgptPlanType"] = v
	}
	if v, ok := claims["exp"]; ok {
		psd["jwtExp"] = v
	}
	conn.Data["providerSpecificData"] = psd
	return conn
}

func (g *Gateway) importCodexBulk(w http.ResponseWriter, r *http.Request) {
	accounts, err := normalizeAccountList(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	results := make([]map[string]any, 0, len(accounts))
	success, failed := 0, 0
	for i, raw := range accounts {
		token := strings.TrimSpace(strAny(raw["accessToken"]))
		if token == "" {
			failed++
			results = append(results, map[string]any{"index": i, "ok": false, "error": "Missing accessToken"})
			continue
		}
		conn := codexConnFrom(token, strAny(raw["idToken"]))
		// Pasted account JSON is applied on top, minus server-owned fields.
		for k, v := range raw {
			switch k {
			case "id", "provider", "authType", "createdAt", "updatedAt", "accessToken", "idToken":
				continue
			case "email":
				conn.Email = strAny(v)
			case "providerSpecificData":
				if extra, ok := v.(map[string]any); ok {
					for ek, ev := range extra {
						conn.Data["providerSpecificData"].(map[string]any)[ek] = ev
					}
				}
			case "expiresAt":
				conn.Data["expiresAt"] = v
			case "refreshToken":
				conn.Data["refreshToken"] = v
			}
		}
		if v, _ := conn.Get("expiresAt").(string); v == "" {
			if secs := intAt(raw, "expiresIn", 0); secs > 0 {
				conn.Data["expiresAt"] = time.Now().Add(time.Duration(secs) * time.Second).UTC().Format(time.RFC3339)
			}
		}
		if err := g.store.UpsertConnection(conn); err != nil {
			failed++
			results = append(results, map[string]any{"index": i, "ok": false, "error": err.Error()})
			continue
		}
		success++
		results = append(results, map[string]any{"index": i, "ok": true, "id": conn.ID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": success, "failed": failed, "results": results})
}

func (g *Gateway) importGrokBulk(w http.ResponseWriter, r *http.Request) {
	accounts, err := normalizeAccountList(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	results := make([]map[string]any, 0, len(accounts))
	success, failed := 0, 0
	for i, raw := range accounts {
		access := firstNonEmpty(strAny(raw["access_token"]), strAny(raw["accessToken"]))
		if access == "" {
			failed++
			results = append(results, map[string]any{"index": i, "ok": false, "error": "Missing access_token / accessToken"})
			continue
		}
		idToken := firstNonEmpty(strAny(raw["id_token"]), strAny(raw["idToken"]))
		email := firstNonEmpty(strAny(raw["email"]), jwtEmail(firstNonEmpty(idToken, access)))

		conn := newConn("grok-cli", "oauth")
		conn.Email = email
		conn.Name = firstNonEmpty(strAny(raw["displayName"]), strAny(raw["name"]))
		conn.Data["accessToken"] = access
		conn.Data["refreshToken"] = firstNonEmpty(strAny(raw["refresh_token"]), strAny(raw["refreshToken"]))
		expiresAt := firstNonEmpty(strAny(raw["expires_at"]), strAny(raw["expiresAt"]))
		if expiresAt == "" {
			if secs := num(firstAnyKey(raw, "expires_in", "expiresIn")); secs > 0 {
				expiresAt = time.Now().Add(time.Duration(secs) * time.Second).UTC().Format(time.RFC3339)
			}
		}
		conn.Data["expiresAt"] = expiresAt
		psd := map[string]any{"authMethod": "device_code"}
		if idToken != "" {
			psd["idToken"] = idToken
		}
		if email != "" {
			psd["email"] = email
		}
		if extra, ok := raw["providerSpecificData"].(map[string]any); ok {
			for k, v := range extra {
				psd[k] = v
			}
		}
		conn.Data["providerSpecificData"] = psd
		if err := g.store.UpsertConnection(conn); err != nil {
			failed++
			results = append(results, map[string]any{"index": i, "ok": false, "error": err.Error()})
			continue
		}
		success++
		results = append(results, map[string]any{"index": i, "ok": true, "id": conn.ID, "email": conn.Email})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(accounts), "success": success, "failed": failed, "results": results})
}

// normalizeAccountList accepts a bare array, {accounts:[...]} or a single object.
func normalizeAccountList(r *http.Request) ([]map[string]any, error) {
	var v any
	if err := decodeBody(r, &v); err != nil {
		return nil, fmt.Errorf("Invalid JSON body: %v", err)
	}
	var list []any
	switch t := v.(type) {
	case []any:
		list = t
	case map[string]any:
		if inner, ok := t["accounts"].([]any); ok {
			list = inner
		} else {
			list = []any{t}
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("No accounts provided")
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Item is not an object")
		}
		out = append(out, m)
	}
	return out, nil
}

// ---------------------------------------------------------------- xiaomi-mimo

func (g *Gateway) importXiaomiKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKey      string `json:"apiKey"`
		UID         string `json:"uid"`
		BaseURL     string `json:"baseUrl"`
		MimoPass    string `json:"mimoPassToken"`
		MimoUserID  string `json:"mimoUserId"`
		MimoCUserID string `json:"mimoCUserId"`
		Region      string `json:"region"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	key := strings.TrimSpace(body.APIKey)
	sessionOnly := key == "" && body.MimoPass != ""
	if key == "" && !sessionOnly {
		writeErr(w, http.StatusBadRequest, "API key is required")
		return
	}
	if key != "" && !strings.HasPrefix(key, "sk-") {
		writeErr(w, http.StatusBadRequest, "Invalid key format — expected sk- prefix")
		return
	}
	base := strings.TrimRight(firstNonEmpty(strings.TrimSpace(body.BaseURL), "https://api.xiaomimimo.com/v1"), "/")
	if err := checkPublicURL(base); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Soft validation: a blocked network or a non-model key still imports.
	validated, modelCount := false, 0
	if key != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-Mimo-Source", "mimocode-cli")
		if resp, err := mediaClient.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var data struct {
					Data []any `json:"data"`
				}
				if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&data) == nil {
					validated, modelCount = true, len(data.Data)
				}
			}
		}
	}

	region := firstNonEmpty(body.Region, "cn")
	psd := map[string]any{
		"uid": firstOrNil(body.UID), "baseUrl": base,
		"authMethod": map[bool]string{true: "session", false: "api_key"}[sessionOnly],
		"provider":   map[bool]string{true: "Session Login", false: "API Key"}[sessionOnly],
		"region":     region, "modelCount": modelCount,
		"mimoPassToken": firstOrNil(body.MimoPass), "mimoUserId": firstOrNil(body.MimoUserID),
		"mimoCUserId": firstOrNil(body.MimoCUserID),
	}
	authType := "api_key"
	if sessionOnly {
		authType = "oauth"
	}

	conn := newConn("xiaomi-mimo", authType)
	conn.Data["accessToken"] = firstOrNil(key)
	conn.Data["expiresAt"] = time.Now().Add(longHorizon).UTC().Format(time.RFC3339)
	conn.Data["providerSpecificData"] = psd
	if body.UID != "" {
		conn.Email = body.UID + "@xiaomi"
		conn.Name = "Xiaomi " + body.UID
		if sessionOnly {
			conn.Name += " (Session)"
		}
	} else {
		conn.Name = "Xiaomi MiMo"
	}
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "validated": validated, "modelCount": modelCount, "connection": connBrief(conn),
		})
	}
}

// ------------------------------------------------------------------------ zed

func (g *Gateway) importZed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccessToken string `json:"accessToken"`
		UserID      string `json:"userId"`
		SystemID    string `json:"systemId"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	token := strings.TrimSpace(body.AccessToken)
	if token == "" {
		writeErr(w, http.StatusBadRequest, "Access token is required")
		return
	}
	userID := strings.TrimSpace(body.UserID)
	if userID == "" {
		writeErr(w, http.StatusBadRequest, "User id is required")
		return
	}
	systemID := firstNonEmpty(strings.TrimSpace(body.SystemID), randomUUID())

	// Validate against Zed cloud; the token is useless if it does not answer.
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://cloud.zed.dev/client/users/me", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-zed-system-id", systemID)
	resp, err := mediaClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "Zed token validation failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeErr(w, http.StatusUnauthorized, "Zed token validation failed: "+string(raw))
		return
	}
	var user map[string]any
	json.Unmarshal(raw, &user)

	conn := newConn("zed", "oauth")
	conn.Email = strAny(user["email"])
	conn.Name = firstNonEmpty(strAny(user["name"]), strAny(user["display_name"]), strAny(user["username"]), "Zed "+userID)
	conn.Data["accessToken"] = token
	conn.Data["providerSpecificData"] = map[string]any{
		"authMethod": "imported", "userId": userID, "systemId": systemID,
		"organizationId": zedOrgID(user),
	}
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "connection": connBrief(conn)})
	}
}

// zedOrgID picks the personal org (else the first) from a /users/me payload.
func zedOrgID(user map[string]any) string {
	if v := firstNonEmpty(strAny(user["default_organization_id"]), strAny(user["defaultOrganizationId"])); v != "" {
		return v
	}
	orgs, _ := user["organizations"].([]any)
	var first any
	for _, o := range orgs {
		m, _ := o.(map[string]any)
		if m == nil {
			continue
		}
		if personal, _ := m["is_personal"].(bool); personal {
			return strAny(m["id"])
		}
		if first == nil {
			first = m["id"]
		}
	}
	return strAny(first)
}

// ----------------------------------------------------------------- auto-import

// autoImportCommandCode reads ~/.commandcode/auth.json (written by the CLI).
// Read-only: the SPA decides whether to POST it back as an API key.
func (g *Gateway) autoImportCommandCode(w http.ResponseWriter, r *http.Request) {
	path := homePath(".commandcode", "auth.json")
	raw, err := readFileLimited(path, 1<<20)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "Command Code auth file not found. Run 'commandcode login' in your terminal first."})
		return
	}
	var auth map[string]any
	if json.Unmarshal(raw, &auth) != nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "auth.json is not valid JSON. Please run 'commandcode login' again."})
		return
	}
	apiKey := strings.TrimSpace(strAny(auth["apiKey"]))
	if apiKey == "" {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "No apiKey found in ~/.commandcode/auth.json. Please run 'commandcode login'."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found": true, "apiKey": apiKey, "userId": auth["userId"], "userName": auth["userName"],
		"keyName": auth["keyName"], "authenticatedAt": auth["authenticatedAt"], "source": path,
	})
}

// autoImportKiro scans the AWS SSO cache for a Kiro refresh token.
func (g *Gateway) autoImportKiro(w http.ResponseWriter, r *http.Request) {
	dir := homePath(".aws", "sso", "cache")
	entries, err := readDirNames(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "AWS SSO cache not found. Please login to Kiro IDE first."})
		return
	}

	var token map[string]any
	var foundFile string
	tryFile := func(name string) bool {
		raw, err := readFileLimited(dir+"/"+name, 1<<20)
		if err != nil {
			return false
		}
		var data map[string]any
		if json.Unmarshal(raw, &data) != nil {
			return false
		}
		rt := strAny(data["refreshToken"])
		if strings.HasPrefix(rt, "aorAAAAAG") {
			token, foundFile = data, name
			return true
		}
		return false
	}
	if !tryFile("kiro-auth-token.json") {
		for _, name := range entries {
			if strings.HasSuffix(name, ".json") && tryFile(name) {
				break
			}
		}
	}
	if token == nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false,
			"error": "Kiro token not found in AWS SSO cache. Please login to Kiro IDE first."})
		return
	}

	out := map[string]any{"found": true, "refreshToken": token["refreshToken"], "source": foundFile,
		"region": token["region"], "authMethod": token["authMethod"]}
	// IDC tokens carry only a hash; the client registration file holds the secret.
	if hash := strAny(token["clientIdHash"]); hash != "" {
		if raw, err := readFileLimited(dir+"/"+hash+".json", 1<<20); err == nil {
			var client map[string]any
			if json.Unmarshal(raw, &client) == nil {
				out["clientId"], out["clientSecret"] = client["clientId"], client["clientSecret"]
			}
		}
	}
	// The runtime gateway needs us-east-1 in the profile ARN regardless of the
	// IDC region, so normalize it — same as the JS auto-import.
	for _, p := range []string{homePath(".config", "Kiro", "User", "globalStorage", "kiro.kiroagent", "profile.json")} {
		if raw, err := readFileLimited(p, 1<<20); err == nil {
			var profile map[string]any
			if json.Unmarshal(raw, &profile) == nil {
				if arn := strAny(profile["arn"]); arn != "" {
					out["profileArn"] = kiroArnRe.ReplaceAllString(arn, "arn:aws:codewhisperer:us-east-1:")
					break
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

var kiroArnRe = regexp.MustCompile(`arn:aws:codewhisperer:[^:]+:`)

// --------------------------------------------------------------- HTTP + JWT

// postJSON POSTs a JSON body and returns the decoded object. Non-2xx is an
// error carrying the response text (the routes surface it verbatim).
func postJSONBody(ctx context.Context, endpoint string, payload []byte, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := mediaClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(raw)))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unreadable response")
	}
	return out, nil
}

// checkProxyPoolURL validates a pool's proxyUrl. Unlike checkPublicURL it
// allows loopback and RFC1918 hosts — a proxy pool's whole purpose is usually a
// local or LAN proxy (the UI's own placeholder is http://127.0.0.1:7897), and
// the JS route accepts these too. Link-local / metadata addresses stay blocked
// so a pool cannot be pointed at a cloud metadata service.
func checkProxyPoolURL(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") || u.Host == "" {
		return fmt.Errorf("Invalid proxy URL")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("Proxy URL is not allowed")
		}
	}
	return nil
}

// checkPublicURL blocks a client-supplied base URL from pointing at loopback or
// link-local space (SSRF). Admin/registry base URLs skip this — they are trusted.
func checkPublicURL(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("Invalid base URL")
	}
	if err := netguard.CheckURL(context.Background(), raw); err != nil {
		return fmt.Errorf("Base URL is not allowed: %v", err)
	}
	return nil
}

// jwtPayload decodes the middle segment of a JWT. Unparseable input is {}.
func jwtPayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]any{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return map[string]any{}
	}
	return out
}

func jwtEmail(token string) string {
	p := jwtPayload(token)
	return firstNonEmpty(strAny(p["email"]), strAny(p["preferred_username"]), strAny(p["sub"]))
}

// randomUUID is a v4 UUID from crypto/rand (avoids a dependency for one field).
func randomUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// homePath joins a path under the current user's home directory.
func homePath(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(parts...)
	}
	return filepath.Join(append([]string{home}, parts...)...)
}

// readFileLimited reads at most max bytes; a missing file is an error.
func readFileLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func firstOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func firstAnyKey(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}
