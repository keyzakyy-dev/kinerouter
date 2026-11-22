package gateway

// One-click relay deploys: Vercel, Cloudflare Workers, Deno Deploy and Netlify.
// Each route deploys the same header-spec relay (x-relay-target + x-relay-path)
// and records the resulting public URL as a proxy pool.
// Ported from src/app/api/proxy-pools/{vercel,cloudflare,deno,netlify}-deploy/route.js
// and src/lib/network/netlifyRelay.js.
//
// Security notes:
//   - every route is behind requireAuth; deploy tokens come from the request
//     body and are used only as an Authorization header to the provider's fixed
//     API host — never logged, never persisted
//   - the deploy endpoints are constants; no caller-supplied URL is fetched
//   - provider responses are size-capped before decoding

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	vercelAPI     = "https://api.vercel.com"
	cfAPI         = "https://api.cloudflare.com/client/v4"
	denoAPI       = "https://api.deno.com/v2"
	netlifyAPI    = "https://api.netlify.com/api/v1"
	netlifyFnName = "relay"
)

var deployHTTP = &http.Client{Timeout: 60 * time.Second}

// ---------------------------------------------------------------- relay sources

const relayVercelCode = `
export const config = { runtime: "edge" };

export default async function handler(req) {
  const target = req.headers.get("x-relay-target");
  const relayPath = req.headers.get("x-relay-path") || "/";
  if (!target) {
    return new Response(JSON.stringify({ error: "Missing x-relay-target header" }), {
      status: 400,
      headers: { "content-type": "application/json" },
    });
  }

  const targetUrl = target.replace(/\/$/, "") + relayPath;

  const rawHeaders = {};
  for (const [k, v] of req.headers.entries()) rawHeaders[k] = v;
  delete rawHeaders["x-relay-target"];
  delete rawHeaders["x-relay-path"];
  delete rawHeaders["host"];

  const response = await fetch(targetUrl, {
    method: req.method,
    headers: rawHeaders,
    body: req.method !== "GET" && req.method !== "HEAD" ? req.body : undefined,
    duplex: "half",
  });

  return new Response(response.body, {
    status: response.status,
    headers: response.headers,
  });
}
`

const relayCFWorkerCode = `
export default {
  async fetch(request, env, ctx) {
    const target = request.headers.get("x-relay-target");
    const relayPath = request.headers.get("x-relay-path") || "/";

    if (!target) {
      return new Response(JSON.stringify({ error: "Missing x-relay-target header" }), {
        status: 400,
        headers: { "content-type": "application/json" },
      });
    }

    const targetUrl = target.replace(/\/$/, "") + relayPath;
    const newRequestInit = {
      method: request.method,
      headers: new Headers(request.headers),
    };

    if (request.method !== "GET" && request.method !== "HEAD") {
      newRequestInit.body = request.body;
      newRequestInit.duplex = "half";
    }

    newRequestInit.headers.delete("x-relay-target");
    newRequestInit.headers.delete("x-relay-path");
    newRequestInit.headers.delete("host");

    try {
      const response = await fetch(targetUrl, newRequestInit);
      return new Response(response.body, {
        status: response.status,
        headers: response.headers,
      });
    } catch (error) {
      return new Response(JSON.stringify({ error: error.message }), {
        status: 502,
        headers: { "content-type": "application/json" },
      });
    }
  },
};
`

const relayDenoCode = `Deno.serve(async (request) => {
  const target = request.headers.get("x-relay-target");
  const relayPath = request.headers.get("x-relay-path") || "/";

  if (!target) {
    return new Response(JSON.stringify({ error: "Missing x-relay-target header" }), {
      status: 400,
      headers: { "content-type": "application/json" },
    });
  }

  const targetUrl = target.replace(/\/$/, "") + relayPath;
  const newHeaders = new Headers(request.headers);
  newHeaders.delete("x-relay-target");
  newHeaders.delete("x-relay-path");
  newHeaders.delete("host");

  const init = {
    method: request.method,
    headers: newHeaders,
  };

  if (request.method !== "GET" && request.method !== "HEAD") {
    init.body = request.body;
    init.duplex = "half";
  }

  try {
    const response = await fetch(targetUrl, init);
    return new Response(response.body, {
      status: response.status,
      headers: response.headers,
    });
  } catch (error) {
    return new Response(JSON.stringify({ error: error.message }), {
      status: 502,
      headers: { "content-type": "application/json" },
    });
  }
});`

// Netlify digest deploys run on the Lambda runtime: exports.handler, string body.
const relayNetlifyCode = `exports.handler = async (event) => {
  const headers = {};
  for (const [k, v] of Object.entries(event.headers || {})) {
    headers[k.toLowerCase()] = v;
  }

  const target = headers["x-relay-target"];
  const relayPath = headers["x-relay-path"] || "/";

  if (!target) {
    return {
      statusCode: 400,
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ error: "Missing x-relay-target header" }),
    };
  }

  const targetUrl = target.replace(/\/$/, "") + relayPath;
  const forwardHeaders = { ...event.headers };
  for (const k of Object.keys(forwardHeaders)) {
    const lower = k.toLowerCase();
    if (lower === "x-relay-target" || lower === "x-relay-path" || lower === "host") {
      delete forwardHeaders[k];
    }
  }

  try {
    const upstream = await fetch(targetUrl, {
      method: event.httpMethod,
      headers: forwardHeaders,
      body: event.httpMethod !== "GET" && event.httpMethod !== "HEAD" && event.body
        ? event.isBase64Encoded
          ? Buffer.from(event.body, "base64")
          : event.body
        : undefined,
    });
    const responseHeaders = {};
    upstream.headers.forEach((value, key) => {
      responseHeaders[key] = value;
    });
却不
    const contentType = upstream.headers.get("content-type") || "";
    const isText = new RegExp("^(text/|[^;]*json|[^;]*event-stream|[^;]*javascript|[^;]*xml|[^;]*urlencoded)", "i").test(contentType);
    const rawBody = await upstream.arrayBuffer();
    const responseBody = isText
      ? Buffer.from(rawBody).toString("utf8")
      : Buffer.from(rawBody).toString("base64");
    return {
      statusCode: upstream.status,
      headers: responseHeaders,
      body: responseBody,
      isBase64Encoded: !isText,
    };
  } catch (error) {
    return {
      statusCode: 502,
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ error: error.message || "Relay fetch failed" }),
    };
  }
};
`

const netlifyIndexHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>KineRouter Relay</title></head>
<body><p>KineRouter relay function lives at <code>/.netlify/functions/relay</code>.</p></body></html>
`

// ---------------------------------------------------------------- helpers

func deployRequest(method, url, token string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return deployHTTP.Do(req)
}

// decodeCapped reads a provider JSON response with a bounded size.
func decodeCapped(res *http.Response) map[string]any {
	var m map[string]any
	_ = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&m)
	return m
}

func deployTimestamp() string {
	return strings.ToLower(strconvBase36(time.Now().UnixMilli()))
}

func strconvBase36(n int64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%36]}, b...)
		n /= 36
	}
	return string(b)
}

// createRelayPool records a deployed relay as a proxy pool and returns it.
func (g *Gateway) createRelayPool(name, proxyURL, typ string) (*store.ProxyPool, error) {
	p := &store.ProxyPool{
		IsActive: true,
		Data: map[string]any{
			"name": name, "proxyUrl": proxyURL, "noProxy": "",
			"type": typ, "strictProxy": false,
		},
	}
	if err := g.store.SaveProxyPool(p); err != nil {
		return nil, err
	}
	return p, nil
}

func deployErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// ---------------------------------------------------------------- vercel

func (g *Gateway) handleVercelDeploy(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if decodeBody(r, &body) != nil {
		deployErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	token := strings.TrimSpace(str(body, "vercelToken"))
	name := strings.TrimSpace(str(body, "projectName"))
	if name == "" {
		name = "relay-" + deployTimestamp()
	}
	if token == "" {
		deployErr(w, http.StatusBadRequest, "Vercel API token is required")
		return
	}
	pkg, _ := json.Marshal(map[string]any{"name": name, "version": "1.0.0"})
	cfg, _ := json.Marshal(map[string]any{"rewrites": []any{map[string]any{"source": "/(.*)", "destination": "/api/relay"}}})
	payload, _ := json.Marshal(map[string]any{
		"name": name,
		"files": []any{
			map[string]any{"file": "api/relay.js", "data": relayVercelCode},
			map[string]any{"file": "package.json", "data": string(pkg)},
			map[string]any{"file": "vercel.json", "data": string(cfg)},
		},
		"projectSettings": map[string]any{"framework": nil},
		"target":          "production",
	})
	res, err := deployRequest(http.MethodPost, vercelAPI+"/v13/deployments", token, bytes.NewReader(payload), "application/json")
	if err != nil {
		deployErr(w, http.StatusBadGateway, err.Error())
		return
	}
	data := decodeCapped(res)
	res.Body.Close()
	if res.StatusCode >= 300 {
		deployErr(w, res.StatusCode, firstNonEmpty(strAny(errorsOf(data, "error")), "Failed to create Vercel deployment"))
		return
	}
	deployID := firstNonEmpty(str(data, "id"), str(data, "uid"))
	projectID := firstNonEmpty(str(data, "projectId"), name)
	// Disable Vercel Authentication so the relay is reachable without a cookie.
	prot, _ := json.Marshal(map[string]any{"ssoProtection": nil})
	if pres, err := deployRequest(http.MethodPatch, vercelAPI+"/v9/projects/"+projectID, token, bytes.NewReader(prot), "application/json"); err == nil {
		pres.Body.Close()
	}
	if err := waitVercelReady(deployID, token); err != nil {
		deployErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The ready response carries the final host; re-poll once for it.
	res2, err := deployRequest(http.MethodGet, vercelAPI+"/v13/deployments/"+deployID, token, nil, "")
	if err != nil {
		deployErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ready := decodeCapped(res2)
	res2.Body.Close()
	deployURL := "https://" + str(ready, "url")
	pool, err := g.createRelayPool(name, deployURL, "vercel")
	if err != nil {
		deployErr(w, http.StatusInternalServerError, "Failed to create proxy pool")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"proxyPool": pool, "deployUrl": deployURL})
}

func errorsOf(m map[string]any, key string) string {
	if e, ok := m[key].(map[string]any); ok {
		return str(e, "message")
	}
	return ""
}

func waitVercelReady(id, token string) error {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		res, err := deployRequest(http.MethodGet, vercelAPI+"/v13/deployments/"+id, token, nil, "")
		if err != nil {
			return err
		}
		data := decodeCapped(res)
		res.Body.Close()
		switch str(data, "readyState") {
		case "READY":
			return nil
		case "ERROR", "CANCELED":
			return errors.New("Deployment failed: " + str(data, "readyState"))
		}
		time.Sleep(3 * time.Second)
	}
	return errors.New("Deployment timed out")
}

// ---------------------------------------------------------------- cloudflare workers

func (g *Gateway) handleCloudflareDeploy(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if decodeBody(r, &body) != nil {
		deployErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	accountID := strings.TrimSpace(str(body, "accountId"))
	token := strings.TrimSpace(str(body, "apiToken"))
	name := strings.TrimSpace(str(body, "projectName"))
	if name == "" {
		name = "relay-" + deployTimestamp()
	}
	if accountID == "" || token == "" {
		deployErr(w, http.StatusBadRequest, "Cloudflare Account ID and API Token are required")
		return
	}
	scriptURL := cfAPI + "/accounts/" + accountID + "/workers/scripts/" + name
	meta, _ := json.Marshal(map[string]any{
		"main_module": "index.js", "compatibility_date": "2024-03-20",
		"observability": map[string]any{"enabled": true},
	})
	up, err := uploadWorker(scriptURL, token, relayCFWorkerCode, string(meta))
	if err != nil {
		deployErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !up.ok {
		deployErr(w, up.status, firstNonEmpty(up.message, "Failed to upload Worker to Cloudflare"))
		return
	}
	// Enable the workers.dev route (best-effort, like the JS).
	en, _ := json.Marshal(map[string]any{"enabled": true})
	if res, err := deployRequest(http.MethodPost, scriptURL+"/subdomain", token, bytes.NewReader(en), "application/json"); err == nil {
		res.Body.Close()
	}
	res, err := deployRequest(http.MethodGet, cfAPI+"/accounts/"+accountID+"/workers/subdomain", token, nil, "")
	deployURL := ""
	if err == nil {
		data := decodeCapped(res)
		res.Body.Close()
		sub := strAny(anyPath(data, "result", "subdomain"))
		if sub != "" {
			deployURL = "https://" + name + "." + sub + ".workers.dev"
		}
	}
	if deployURL == "" {
		deployErr(w, http.StatusBadRequest, "Worker deployed but failed to retrieve workers.dev subdomain. Make sure you have setup a workers.dev subdomain in Cloudflare Dashboard.")
		return
	}
	pool, err := g.createRelayPool(name, deployURL, "cloudflare")
	if err != nil {
		deployErr(w, http.StatusInternalServerError, "Failed to create proxy pool")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"proxyPool": pool, "deployUrl": deployURL})
}

type blobResp struct {
	ok      bool
	status  int
	message string
}

// uploadWorker posts the multipart module bundle Cloudflare's script API needs.
func uploadWorker(url, token, code, meta string) (blobResp, error) {
	var buf bytes.Buffer
	boundary := "----KineRouterRelay"
	writePart := func(name, ctype, filename, content string) {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Disposition: form-data; name=%q; filename=%q\r\n", name, filename)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n\r\n", ctype)
		buf.WriteString(content)
		buf.WriteString("\r\n")
	}
	writePart("index.js", "application/javascript+module", "index.js", code)
	writePart("metadata", "application/json", "metadata.json", meta)
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	req, err := http.NewRequest(http.MethodPut, url, &buf)
	if err != nil {
		return blobResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	res, err := deployHTTP.Do(req)
	if err != nil {
		return blobResp{}, err
	}
	defer res.Body.Close()
	data := decodeCapped(res)
	msg := ""
	if errs, ok := data["errors"].([]any); ok && len(errs) > 0 {
		if e, ok := errs[0].(map[string]any); ok {
			msg = str(e, "message")
		}
	}
	return blobResp{ok: res.StatusCode >= 200 && res.StatusCode < 300, status: res.StatusCode, message: msg}, nil
}

// anyPath walks nested maps.
func anyPath(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

// ---------------------------------------------------------------- deno deploy

func (g *Gateway) handleDenoDeploy(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if decodeBody(r, &body) != nil {
		deployErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	token := strings.TrimSpace(str(body, "denoToken"))
	orgDomain := strings.TrimSpace(str(body, "orgDomain"))
	name := strings.TrimSpace(str(body, "projectName"))
	if name == "" {
		name = "relay-" + deployTimestamp()
	}
	if orgDomain == "" {
		deployErr(w, http.StatusBadRequest, "Organization domain is required")
		return
	}
	if token == "" {
		deployErr(w, http.StatusBadRequest, "Deno Deploy API token is required")
		return
	}
	appPayload, _ := json.Marshal(map[string]any{
		"slug":   name,
		"labels": map[string]any{"custom.kind": "kinerouter-relay"},
		"config": map[string]any{
			"install": "deno install",
			"runtime": map[string]any{"type": "dynamic", "entrypoint": "main.ts"},
		},
	})
	res, err := deployRequest(http.MethodPost, denoAPI+"/apps", token, bytes.NewReader(appPayload), "application/json")
	if err != nil {
		deployErr(w, http.StatusBadGateway, err.Error())
		return
	}
	app := decodeCapped(res)
	res.Body.Close()
	if res.StatusCode == http.StatusConflict {
		deployErr(w, http.StatusConflict, fmt.Sprintf("App %q already exists. Choose a different name.", name))
		return
	}
	if res.StatusCode >= 300 {
		deployErr(w, res.StatusCode, fmt.Sprintf("Failed to create app (%d)", res.StatusCode))
		return
	}
	appID := firstNonEmpty(str(app, "id"), str(app, "slug"))
	delApp := func() {
		if req, err := http.NewRequest(http.MethodDelete, denoAPI+"/apps/"+appID, nil); err == nil {
			req.Header.Set("Authorization", "Bearer "+token)
			if rr, err := deployHTTP.Do(req); err == nil {
				rr.Body.Close()
			}
		}
	}
	deployPayload, _ := json.Marshal(map[string]any{
		"assets": map[string]any{
			"main.ts": map[string]any{"kind": "file", "content": relayDenoCode, "encoding": "utf-8"},
		},
	})
	res2, err := deployRequest(http.MethodPost, denoAPI+"/apps/"+appID+"/deploy", token, bytes.NewReader(deployPayload), "application/json")
	if err != nil {
		deployErr(w, http.StatusBadGateway, err.Error())
		return
	}
	rev := decodeCapped(res2)
	res2.Body.Close()
	if res2.StatusCode >= 300 {
		delApp()
		deployErr(w, res2.StatusCode, fmt.Sprintf("Deploy failed (%d)", res2.StatusCode))
		return
	}
	revID := str(rev, "id")
	status := str(rev, "status")
	for i := 0; (status == "queued" || status == "building") && i < 30; i++ {
		time.Sleep(2 * time.Second)
		sr, err := deployRequest(http.MethodGet, denoAPI+"/revisions/"+revID, token, nil, "")
		if err != nil {
			break
		}
		sd := decodeCapped(sr)
		sr.Body.Close()
		status = str(sd, "status")
	}
	if status != "succeeded" {
		delApp()
		deployErr(w, http.StatusInternalServerError, "Deploy failed with status: "+status)
		return
	}
	orgSlug := strings.SplitN(orgDomain, ".", 2)[0]
	deployURL := "https://" + name + "." + orgSlug + ".deno.net"
	pool, err := g.createRelayPool(name, deployURL, "deno")
	if err != nil {
		deployErr(w, http.StatusInternalServerError, "Failed to create proxy pool")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"proxyPool": pool, "deployUrl": deployURL})
}

// ---------------------------------------------------------------- netlify

func sanitizeSiteName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "relay-" + deployTimestamp()
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "relay-" + deployTimestamp()
	}
	return out
}

// storedZip writes an uncompressed (stored) ZIP — Netlify's digest deploy of a
// single ~1.5 KB function needs no compression.
func storedZip(name string, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fh := &zip.FileHeader{Name: name, Method: zip.Store}
	fh.SetMode(0o644)
	w, _ := zw.CreateHeader(fh)
	_, _ = w.Write(data)
	_ = zw.Close()
	return buf.Bytes()
}

func netlifyHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "KineRouter")
}

func (g *Gateway) handleNetlifyDeploy(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if decodeBody(r, &body) != nil {
		deployErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	token := strings.TrimSpace(str(body, "netlifyToken"))
	name := sanitizeSiteName(firstNonEmpty(str(body, "projectName"), str(body, "siteName")))
	if token == "" {
		deployErr(w, http.StatusBadRequest, "Netlify API token is required")
		return
	}
	// 1. Create site.
	sitePayload, _ := json.Marshal(map[string]any{"name": name})
	res, err := deployRequest(http.MethodPost, netlifyAPI+"/sites", token, bytes.NewReader(sitePayload), "application/json")
	if err != nil {
		deployErr(w, http.StatusBadGateway, err.Error())
		return
	}
	siteData := decodeCapped(res)
	res.Body.Close()
	if res.StatusCode >= 300 {
		msg := firstNonEmpty(str(siteData, "message"), str(siteData, "error"), "Failed to create Netlify site")
		status := res.StatusCode
		if status == http.StatusUnprocessableEntity {
			status = http.StatusConflict
			msg += fmt.Sprintf(` Site name %q is taken — choose a different name.`, name)
		}
		deployErr(w, status, msg)
		return
	}
	siteID := firstNonEmpty(str(siteData, "id"), str(siteData, "site_id"))
	siteURL := firstNonEmpty(str(siteData, "ssl_url"), str(siteData, "url"))
	if siteID == "" || siteURL == "" {
		deployErr(w, http.StatusBadGateway, "Netlify site created but no site URL returned")
		return
	}
	// 2. Digest deploy: index.html (SHA1) + relay function bundle (SHA256).
	zipBytes := storedZip(netlifyFnName+".js", []byte(relayNetlifyCode))
	fnSHA := sha256Hex(zipBytes)
	indexBytes := []byte(netlifyIndexHTML)
	idxSHA := sha1Hex(indexBytes)
	manifest, _ := json.Marshal(map[string]any{
		"files":     map[string]any{"/index.html": idxSHA},
		"functions": map[string]any{netlifyFnName: fnSHA},
	})
	res2, err := deployRequest(http.MethodPost, netlifyAPI+"/sites/"+siteID+"/deploys", token, bytes.NewReader(manifest), "application/json")
	if err != nil {
		deployErr(w, http.StatusBadGateway, err.Error())
		return
	}
	depData := decodeCapped(res2)
	res2.Body.Close()
	if res2.StatusCode >= 300 {
		deployErr(w, res2.StatusCode, firstNonEmpty(str(depData, "message"), "Failed to create Netlify deploy"))
		return
	}
	deployID := str(depData, "id")
	if deployID == "" {
		deployErr(w, http.StatusBadGateway, "Netlify deploy created but no deploy ID returned")
		return
	}
	// 3. Upload only what Netlify asked for.
	required := toSet(depData["required"])
	requiredFn := toSet(depData["required_functions"])
	if required[idxSHA] {
		req, err := http.NewRequest(http.MethodPut, netlifyAPI+"/deploys/"+deployID+"/files/index.html", bytes.NewReader(indexBytes))
		if err == nil {
			netlifyHeaders(req, token)
			req.Header.Set("Content-Type", "application/octet-stream")
			if rr, err := deployHTTP.Do(req); err == nil {
				rr.Body.Close()
			}
		}
	}
	if requiredFn[fnSHA] {
		req, err := http.NewRequest(http.MethodPut,
			netlifyAPI+"/deploys/"+deployID+"/functions/"+netlifyFnName+"?runtime=js", bytes.NewReader(zipBytes))
		if err == nil {
			netlifyHeaders(req, token)
			req.Header.Set("Content-Type", "application/octet-stream")
			if rr, err := deployHTTP.Do(req); err == nil {
				rr.Body.Close()
			}
		}
	}
	// 4. Poll until live.
	if err := waitNetlifyReady(deployID, token); err != nil {
		deployErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	relayURL := strings.TrimRight(siteURL, "/") + "/.netlify/functions/" + netlifyFnName
	pool, err := g.createRelayPool(name, relayURL, "netlify")
	if err != nil {
		deployErr(w, http.StatusInternalServerError, "Failed to create proxy pool")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"proxyPool": pool, "deployUrl": relayURL})
}

func toSet(v any) map[string]bool {
	out := map[string]bool{}
	if l, ok := v.([]any); ok {
		for _, e := range l {
			if s, ok := e.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

func sha1Hex(b []byte) string   { sum := sha1.Sum(b); return hex.EncodeToString(sum[:]) }
func sha256Hex(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func waitNetlifyReady(id, token string) error {
	deadline := time.Now().Add(120 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, netlifyAPI+"/deploys/"+id, nil)
		if err != nil {
			return err
		}
		netlifyHeaders(req, token)
		res, err := deployHTTP.Do(req)
		if err != nil {
			return err
		}
		data := decodeCapped(res)
		res.Body.Close()
		switch str(data, "state") {
		case "ready":
			return nil
		case "error":
			return errors.New(firstNonEmpty(str(data, "error_message"), "Netlify deploy failed"))
		}
		if time.Now().After(deadline) {
			return errors.New("Netlify deploy timed out")
		}
		time.Sleep(3 * time.Second)
	}
}
