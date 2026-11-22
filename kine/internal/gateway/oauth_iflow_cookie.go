package gateway

// iFlow cookie-based login: exchange a browser BXAuth cookie for a fresh API
// key and save it as a connection. Ported from
// src/app/api/oauth/iflow/cookie/route.js.
//
// Security: only the BXAuth field is persisted (never the whole cookie jar), and
// only after iFlow itself confirms the key. The upstream host is a fixed
// constant, so the request cannot be redirected.

import (
	"encoding/json"
	"net/http"
	"strings"
)

const iflowAPIKeyURL = "https://platform.iflow.cn/api/openapi/apikey"

func (g *Gateway) registerIflowCookieRoute(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/oauth/iflow/cookie", g.requireAuth(g.importIflowCookie))
}

func (g *Gateway) importIflowCookie(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cookie string `json:"cookie"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	cookie := strings.TrimSpace(body.Cookie)
	if cookie == "" {
		writeErr(w, http.StatusBadRequest, "Cookie is required")
		return
	}
	if !strings.Contains(cookie, "BXAuth=") {
		writeErr(w, http.StatusBadRequest, "Cookie must contain BXAuth field")
		return
	}
	if !strings.HasSuffix(cookie, ";") {
		cookie += ";"
	}
	headers := map[string]string{
		"Cookie":          cookie,
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
	}
	status, raw, err := probeJSON(http.DefaultClient, http.MethodGet, iflowAPIKeyURL, headers, nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Failed to fetch API key info: "+err.Error())
		return
	}
	var getResult map[string]any
	_ = json.Unmarshal(raw, &getResult)
	if status >= 300 {
		writeErr(w, status, "Failed to fetch API key info: "+truncHead(string(raw), 500))
		return
	}
	if ok, _ := getResult["success"].(bool); !ok {
		writeErr(w, http.StatusBadRequest, "API key fetch failed: "+strAny(getResult["message"]))
		return
	}
	keyData, _ := getResult["data"].(map[string]any)
	name := strings.TrimSpace(strAny(keyData["name"]))
	if name == "" {
		writeErr(w, http.StatusBadRequest, "Missing name in API key info")
		return
	}

	postHeaders := map[string]string{"Cookie": cookie, "User-Agent": headers["User-Agent"],
		"Accept": headers["Accept"], "Accept-Language": headers["Accept-Language"],
		"Origin": "https://platform.iflow.cn", "Referer": "https://platform.iflow.cn/"}
	payload, _ := json.Marshal(map[string]any{"name": name})
	status, raw, err = probeJSON(http.DefaultClient, http.MethodPost, iflowAPIKeyURL, postHeaders, payload)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Failed to refresh API key: "+err.Error())
		return
	}
	var postResult map[string]any
	_ = json.Unmarshal(raw, &postResult)
	if status >= 300 {
		writeErr(w, status, "Failed to refresh API key: "+truncHead(string(raw), 500))
		return
	}
	if ok, _ := postResult["success"].(bool); !ok {
		writeErr(w, http.StatusBadRequest, "API key refresh failed: "+strAny(postResult["message"]))
		return
	}
	refreshed, _ := postResult["data"].(map[string]any)
	apiKey := strings.TrimSpace(strAny(refreshed["apiKey"]))
	if apiKey == "" {
		writeErr(w, http.StatusBadRequest, "Missing API key in response")
		return
	}

	bxAuth := ""
	if i := strings.Index(cookie, "BXAuth="); i >= 0 {
		if end := strings.IndexAny(cookie[i:], ";"); end > 0 {
			bxAuth = cookie[i : i+end]
		}
	}
	conn := newConn("iflow", "cookie")
	conn.Email = firstNonEmpty(strAny(refreshed["name"]), name)
	conn.Name = conn.Email
	conn.Data["apiKey"] = apiKey
	conn.Data["providerSpecificData"] = map[string]any{
		"cookie": bxAuth + ";", "expireTime": refreshed["expireTime"],
	}
	if !saveConn(w, g.store, conn) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "connection": map[string]any{
		"id": conn.ID, "provider": conn.Provider, "email": conn.Email,
		"apiKey": truncHead(apiKey, 10) + "...", "expireTime": refreshed["expireTime"],
	}})
}
