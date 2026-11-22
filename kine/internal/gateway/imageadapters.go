package gateway

// Image adapters for the providers that are not OpenAI-shaped: async polling
// queues (fal, BFL, runway, nanobanana), a sync base64 endpoint (stability),
// static local servers (sdwebui, comfyui), Cloudflare's account-scoped workers
// AI and the HuggingFace router. Mirrors open-sse/handlers/imageProviders/*.js.
//
// Security: every call reuses the connection's own proxy settings and never
// logs the credential. URLs are built from registry data plus the client's
// model id; only huggingface embeds the model in a path and it rejects
// traversal/query characters first.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	imagePollInterval = 1500 * time.Millisecond
	imagePollTimeout  = 120 * time.Second
)

type imageAdapter struct {
	// run performs the whole exchange and returns an OpenAI-shaped body.
	run func(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error)
	// body builds the request body; for multipart providers the payload is a
	// *bytes.Buffer plus its content type.
}

var imageAdapters = map[string]*imageAdapter{
	"fal-ai":            {run: runFal},
	"stability-ai":      {run: runStability},
	"black-forest-labs": {run: runBFL},
	"runwayml":          {run: runRunway},
	"cloudflare-ai":     {run: runCloudflare},
	"sdwebui":           {run: runSDWebUI},
	"comfyui":           {run: runComfyUI},
	"nanobanana":        {run: runNanoBanana},
	"huggingface":       {run: runHuggingFace},
}

// imageAdapterOnce dispatches one attempt to the provider's adapter.
func (g *Gateway) imageAdapterOnce(r *http.Request, conn *store.Connection, t target, body, cfg map[string]any) (int, []byte, error) {
	ad := imageAdapters[t.Provider]
	if ad == nil {
		return http.StatusBadRequest, mustJSON(map[string]any{"error": map[string]any{
			"message": fmt.Sprintf("Provider '%s' image adapter is not implemented", t.Provider)}}), nil
	}
	creds := executor.CredentialsFromConnection(conn)
	client := mediaClient
	if creds.ProxyURL != "" {
		client = executor.ClientWithProxy(creds.ProxyURL)
	}
	return ad.run(r.Context(), client, creds, t.Model, body, cfg)
}

// ---------------------------------------------------------------- helpers

func cfgString(cfg map[string]any, key string) string {
	v, _ := cfg[key].(string)
	return v
}

func nowUnix() int64 { return time.Now().Unix() }

func imageResult(items ...map[string]any) []byte {
	list := make([]any, 0, len(items))
	for _, it := range items {
		if it != nil {
			list = append(list, it)
		}
	}
	return mustJSON(map[string]any{"created": nowUnix(), "data": list})
}

// sizeToAspectRatio mirrors the JS map used by fal/bfl/runway/nanobanana.
func sizeToAspectRatio(v any) string {
	s, _ := v.(string)
	switch s {
	case "1024x1024":
		return "1:1"
	case "1024x1792":
		return "9:16"
	case "1792x1024":
		return "16:9"
	case "1024x1536":
		return "2:3"
	case "1536x1024":
		return "3:2"
	}
	return "1:1"
}

func sizeDims(v any) (w, h int) {
	s, _ := v.(string)
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return 0, 0
	}
	w, _ = strconv.Atoi(parts[0])
	h, _ = strconv.Atoi(parts[1])
	return w, h
}

func doJSON(ctx context.Context, c *http.Client, method, url string, headers map[string]string, payload any) (int, map[string]any, []byte, error) {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(mustJSON(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	var m map[string]any
	json.Unmarshal(raw, &m)
	return resp.StatusCode, m, raw, nil
}

func bearerOrKey(creds executor.Credentials) string {
	if creds.APIKey != "" {
		return creds.APIKey
	}
	return creds.AccessToken
}

// pollUntil runs the adapter's poll loop until done returns true, the deadline
// passes, or the body reports a terminal failure.
func pollUntil(ctx context.Context, c *http.Client, url string, headers map[string]string,
	step func(map[string]any) (done bool, failed string)) (map[string]any, error) {
	deadline := time.Now().Add(imagePollTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(imagePollInterval):
		}
		status, m, _, err := doJSON(ctx, c, http.MethodGet, url, headers, nil)
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("poll status %d", status)
		}
		done, failed := step(m)
		if failed != "" {
			return nil, fmt.Errorf("%s", failed)
		}
		if done {
			return m, nil
		}
	}
	return nil, fmt.Errorf("polling timeout")
}

// ---------------------------------------------------------------- fal.ai

func runFal(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	base := strings.TrimRight(cfgString(cfg, "baseUrl"), "/")
	headers := map[string]string{"Content-Type": "application/json", "Authorization": "Key " + bearerOrKey(creds)}
	payload := map[string]any{"prompt": body["prompt"], "num_images": numOr(body["n"], 1)}
	if s, ok := body["size"]; ok && s != "" {
		payload["image_size"] = sizeToAspectRatio(s)
	}
	if img, ok := body["image"]; ok && img != nil {
		payload["image_url"] = img
	}
	status, m, raw, err := doJSON(ctx, c, http.MethodPost, base+"/"+model, headers, payload)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if status < 200 || status >= 300 {
		return status, raw, fmt.Errorf("upstream %d", status)
	}
	statusURL, _ := m["status_url"].(string)
	if statusURL == "" {
		return status, imageResult(), nil
	}
	res, err := pollUntil(ctx, c, statusURL, headers, func(s map[string]any) (bool, string) {
		switch s["status"] {
		case "COMPLETED":
			return true, ""
		case "FAILED":
			return false, firstNonEmpty(stringOf(s["error"]), "Fal generation failed")
		}
		return false, ""
	})
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	_ = res
	var final map[string]any
	if responseURL, _ := m["response_url"].(string); responseURL != "" {
		_, final, _, _ = doJSON(ctx, c, http.MethodGet, responseURL, headers, nil)
	}
	var items []map[string]any
	if list, ok := final["images"].([]any); ok {
		for _, it := range list {
			if mm, ok := it.(map[string]any); ok {
				items = append(items, map[string]any{"url": firstNonEmpty(stringOf(mm["url"]), stringOf(it))})
			}
		}
	} else if img, ok := final["image"].(map[string]any); ok {
		items = append(items, map[string]any{"url": stringOf(img["url"])})
	}
	return status, imageResult(items...), nil
}

// ---------------------------------------------------------------- stability

func runStability(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	base := strings.TrimRight(cfgString(cfg, "baseUrl"), "/")
	endpoint := "core"
	if strings.Contains(model, "ultra") {
		endpoint = "ultra"
	} else if strings.Contains(model, "sd3") {
		endpoint = "sd3"
	}
	headers := map[string]string{"Content-Type": "application/json", "Accept": "application/json",
		"Authorization": "Bearer " + bearerOrKey(creds)}
	payload := map[string]any{"prompt": body["prompt"], "output_format": strings.ToLower(firstNonEmpty(stringOf(body["output_format"]), "png"))}
	if s, ok := body["size"]; ok && s != "" {
		payload["aspect_ratio"] = sizeToAspectRatio(s)
	}
	if st, ok := body["style"]; ok && st != "" {
		payload["style_preset"] = st
	}
	if endpoint == "sd3" {
		payload["model"] = model
	}
	status, m, raw, err := doJSON(ctx, c, http.MethodPost, base+"/"+endpoint, headers, payload)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if status < 200 || status >= 300 {
		return status, raw, fmt.Errorf("upstream %d", status)
	}
	if img, _ := m["image"].(string); img != "" {
		return status, imageResult(map[string]any{"b64_json": img}), nil
	}
	return status, imageResult(), nil
}

// ---------------------------------------------------------------- black forest labs

func runBFL(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	base := strings.TrimRight(cfgString(cfg, "baseUrl"), "/")
	headers := map[string]string{"Content-Type": "application/json", "x-key": bearerOrKey(creds)}
	payload := map[string]any{"prompt": body["prompt"]}
	if w, h := sizeDims(body["size"]); w > 0 {
		payload["width"], payload["height"] = w, h
	}
	if img, ok := body["image"]; ok && img != nil {
		payload["image_prompt"] = img
	}
	status, m, raw, err := doJSON(ctx, c, http.MethodPost, base+"/"+model, headers, payload)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if status < 200 || status >= 300 {
		return status, raw, fmt.Errorf("upstream %d", status)
	}
	pollURL, _ := m["polling_url"].(string)
	if pollURL == "" {
		return http.StatusBadGateway, nil, fmt.Errorf("BFL: no polling_url returned")
	}
	res, err := pollUntil(ctx, c, pollURL, map[string]string{"x-key": headers["x-key"], "Accept": "application/json"},
		func(s map[string]any) (bool, string) {
			switch s["status"] {
			case "Ready":
				return true, ""
			case "Error", "Failed":
				return false, firstNonEmpty(stringOf(s["error"]), "BFL generation failed")
			}
			return false, ""
		})
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	var sample string
	if result, ok := res["result"].(map[string]any); ok {
		sample, _ = result["sample"].(string)
	}
	if sample == "" {
		return status, imageResult(), nil
	}
	return status, imageResult(map[string]any{"url": sample}), nil
}

// ---------------------------------------------------------------- runway

func runRunway(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	base := strings.TrimRight(cfgString(cfg, "baseUrl"), "/")
	headers := map[string]string{"Content-Type": "application/json",
		"Authorization": "Bearer " + bearerOrKey(creds), "X-Runway-Version": "2024-11-06"}
	isVideo := !strings.Contains(model, "image")
	path := "text_to_image"
	if isVideo {
		path = "image_to_video"
	}
	payload := map[string]any{"promptText": body["prompt"], "model": model, "ratio": sizeToAspectRatio(body["size"])}
	if isVideo {
		payload["duration"] = 5
		if img, ok := body["image"]; ok && img != nil {
			payload["promptImage"] = img
		}
	} else if img, ok := body["image"]; ok && img != nil {
		payload["referenceImages"] = []any{map[string]any{"uri": img}}
	}
	status, m, raw, err := doJSON(ctx, c, http.MethodPost, base+"/"+path, headers, payload)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if status < 200 || status >= 300 {
		return status, raw, fmt.Errorf("upstream %d", status)
	}
	id, _ := m["id"].(string)
	if id == "" {
		return http.StatusBadGateway, nil, fmt.Errorf("Runway: no task id returned")
	}
	res, err := pollUntil(ctx, c, base+"/tasks/"+id, headers, func(s map[string]any) (bool, string) {
		switch s["status"] {
		case "SUCCEEDED":
			return true, ""
		case "FAILED", "CANCELLED":
			return false, firstNonEmpty(stringOf(s["failure"]), "Runway task failed")
		}
		return false, ""
	})
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	var items []map[string]any
	if out, ok := res["output"].([]any); ok {
		for _, u := range out {
			if s, ok := u.(string); ok {
				items = append(items, map[string]any{"url": s})
			}
		}
	}
	return status, imageResult(items...), nil
}

// ---------------------------------------------------------------- cloudflare

var cloudflareMultipart = map[string]bool{
	"@cf/black-forest-labs/flux-2-dev":      true,
	"@cf/black-forest-labs/flux-2-klein-4b": true,
	"@cf/black-forest-labs/flux-2-klein-9b": true,
}

var cloudflareOptional = []string{"negative_prompt", "guidance", "seed", "num_steps", "steps", "strength"}

func runCloudflare(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	accountID, _ := creds.Extra["accountId"].(string)
	if accountID == "" {
		return http.StatusBadRequest, nil, fmt.Errorf("cloudflare-ai requires accountId in providerSpecificData")
	}
	url := strings.TrimRight(cfgString(cfg, "baseUrl"), "/") + "/" + accountID + "/ai/run/" + model

	var payload []byte
	contentType := "application/json"
	headers := map[string]string{"Authorization": "Bearer " + bearerOrKey(creds)}
	if cloudflareMultipart[model] {
		buf := &bytes.Buffer{}
		fw := multipart.NewWriter(buf)
		fw.WriteField("prompt", stringOf(body["prompt"]))
		if w, h := sizeDims(body["size"]); w > 0 {
			fw.WriteField("width", strconv.Itoa(w))
			fw.WriteField("height", strconv.Itoa(h))
		}
		for _, k := range cloudflareOptional {
			if v, ok := body[k]; ok && v != nil && v != "" {
				fw.WriteField(k, fmt.Sprint(v))
			}
		}
		fw.Close()
		payload = buf.Bytes()
		contentType = fw.FormDataContentType()
	} else {
		req := map[string]any{"prompt": body["prompt"]}
		if w, h := sizeDims(body["size"]); w > 0 {
			req["width"], req["height"] = w, h
		}
		for _, k := range cloudflareOptional {
			if v, ok := body[k]; ok && v != nil && v != "" {
				req[k] = v
			}
		}
		payload = mustJSON(req)
	}
	headers["Content-Type"] = contentType

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.Do(httpReq)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, raw, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "image/") {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		return resp.StatusCode, imageResult(map[string]any{"b64_json": base64.StdEncoding.EncodeToString(raw)}), nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var m map[string]any
	json.Unmarshal(raw, &m)
	return resp.StatusCode, normalizeCloudflare(m), nil
}

func normalizeCloudflare(m map[string]any) []byte {
	if _, ok := m["created"]; ok {
		if _, ok := m["data"].([]any); ok {
			return mustJSON(m)
		}
	}
	result, _ := m["result"].(map[string]any)
	if result == nil {
		if s, ok := m["result"].(string); ok && s != "" {
			return imageResult(cloudflareItem(s))
		}
		result = m
	}
	if list, ok := result["responses"].([]any); ok {
		for _, it := range list {
			mm, _ := it.(map[string]any)
			if mm != nil && mm["success"] != false {
				if r, ok := mm["result"].(map[string]any); ok {
					return normalizeCloudflare(r)
				}
			}
		}
	}
	if s, ok := result["image"].(string); ok && s != "" {
		return imageResult(cloudflareItem(s))
	}
	if data, ok := result["data"].([]any); ok && len(data) > 0 {
		if d, ok := data[0].(map[string]any); ok {
			if b, ok := d["b64_json"].(string); ok && b != "" {
				return imageResult(map[string]any{"b64_json": b})
			}
			if u, ok := d["url"].(string); ok && u != "" {
				return imageResult(map[string]any{"url": u})
			}
		}
	}
	return imageResult()
}

func cloudflareItem(s string) map[string]any {
	if strings.HasPrefix(s, "data:image/") {
		if i := strings.Index(s, ";base64,"); i >= 0 {
			return map[string]any{"b64_json": s[i+8:]}
		}
	}
	if strings.HasPrefix(s, "http") {
		return map[string]any{"url": s}
	}
	return map[string]any{"b64_json": s}
}

// ---------------------------------------------------------------- local (sdwebui / comfyui)

func runSDWebUI(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	base := cfgString(cfg, "baseUrl")
	w, h := sizeDims(firstNonEmpty(stringOf(body["size"]), "1024x1024"))
	if w == 0 || h == 0 {
		w, h = 512, 512
	}
	payload := map[string]any{"prompt": body["prompt"], "width": w, "height": h,
		"steps": 20, "batch_size": numOr(body["n"], 1)}
	status, m, raw, err := doJSON(ctx, c, http.MethodPost, base, map[string]string{"Content-Type": "application/json"}, payload)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if status < 200 || status >= 300 {
		return status, raw, fmt.Errorf("upstream %d", status)
	}
	var items []map[string]any
	if list, ok := m["images"].([]any); ok {
		for _, it := range list {
			if s, ok := it.(string); ok {
				items = append(items, map[string]any{"b64_json": s})
			}
		}
	}
	return status, imageResult(items...), nil
}

// runComfyUI forwards the prompt to the local server and passes its reply
// through, exactly like the JS placeholder adapter.
func runComfyUI(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	status, _, raw, err := doJSON(ctx, c, http.MethodPost, cfgString(cfg, "baseUrl"),
		map[string]string{"Content-Type": "application/json"}, map[string]any{"prompt": body["prompt"]})
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	return status, raw, nil
}

// ---------------------------------------------------------------- nanobanana

func runNanoBanana(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	submit := cfgString(cfg, "baseUrl")
	pollBase := cfgString(cfg, "pollUrl")
	headers := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + bearerOrKey(creds)}
	urls := strArrOf(body["images"])
	if img, ok := body["image"].(string); ok && img != "" {
		urls = append(urls, img)
	}
	isEdit := len(urls) > 0
	payload := map[string]any{"prompt": body["prompt"], "numImages": numOr(body["n"], 1),
		"image_size": sizeToAspectRatio(body["size"]), "callBackUrl": "https://localhost/callback",
		"type": "TEXTTOIAMGE"}
	if isEdit {
		payload["type"] = "IMAGETOIAMGE"
		payload["imageUrls"] = urls
	}
	status, m, raw, err := doJSON(ctx, c, http.MethodPost, submit, headers, payload)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	if status < 200 || status >= 300 {
		return status, raw, fmt.Errorf("upstream %d", status)
	}
	if code, _ := m["code"].(float64); code != 0 && code != 200 {
		return status, raw, fmt.Errorf("%s", firstNonEmpty(stringOf(m["msg"]), "NanoBanana submit failed"))
	}
	data, _ := m["data"].(map[string]any)
	taskID, _ := data["taskId"].(string)
	if taskID == "" {
		return http.StatusBadGateway, nil, fmt.Errorf("NanoBanana: no taskId returned")
	}
	res, err := pollUntil(ctx, c, pollBase+"?taskId="+urlQueryEscape(taskID), headers, func(s map[string]any) (bool, string) {
		d, _ := s["data"].(map[string]any)
		flag, _ := d["successFlag"].(float64)
		switch flag {
		case 1:
			return true, ""
		case 2, 3:
			return false, firstNonEmpty(stringOf(d["errorMessage"]), "NanoBanana generation failed")
		}
		return false, ""
	})
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	// The JS adapter returns the poll's `data` object, then normalizes its
	// `response` sub-object.
	bodyRes, _ := res["data"].(map[string]any)
	if bodyRes == nil {
		bodyRes = res
	}
	result, _ := bodyRes["response"].(map[string]any)
	url := firstNonEmpty(stringOf(result["resultImageUrl"]), stringOf(result["originImageUrl"]))
	if url == "" {
		return status, imageResult(), nil
	}
	return status, imageResult(map[string]any{"url": url, "revised_prompt": body["prompt"]}), nil
}

// ---------------------------------------------------------------- huggingface

func runHuggingFace(ctx context.Context, c *http.Client, creds executor.Credentials, model string, body, cfg map[string]any) (int, []byte, error) {
	headers := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + bearerOrKey(creds)}
	custom, _ := creds.Extra["baseUrl"].(string)
	custom = strings.TrimSpace(strings.TrimRight(custom, "/"))

	var url, task string
	if custom != "" {
		if strings.Contains(model, "..") || strings.Contains(model, "//") || strings.ContainsAny(model, "?#") {
			return http.StatusBadRequest, nil, fmt.Errorf("HuggingFace: invalid model ID %q", model)
		}
		url = custom + "/" + model
		task = "text-to-image"
	} else {
		mm, _ := cfg["modelMap"].(map[string]any)
		raw, ok := mm[model]
		if !ok {
			return http.StatusBadRequest, nil, fmt.Errorf(
				"HuggingFace: no HuggingFace router mapping for model %q; add it to imageConfig.modelMap or set a custom base URL on the connection", model)
		}
		path, taskName := "", "text-to-image"
		switch v := raw.(type) {
		case string:
			path = v
		case map[string]any:
			path, _ = v["path"].(string)
			taskName = firstNonEmpty(stringOf(v["task"]), "text-to-image")
		}
		task = taskName
		url = strings.TrimRight(cfgString(cfg, "baseUrl"), "/") + "/" + path
	}

	var payload any
	if task == "image-to-image" {
		src := firstNonEmpty(stringOf(body["image"]), func() string {
			l := strArrOf(body["images"])
			if len(l) > 0 {
				return l[0]
			}
			return ""
		}())
		if src == "" {
			return http.StatusBadRequest, nil, fmt.Errorf("HuggingFace: model %q requires a source image in \"image\"", model)
		}
		if strings.HasPrefix(src, "http") {
			b64, err := fetchAsBase64(ctx, c, src)
			if err != nil {
				return http.StatusBadGateway, nil, err
			}
			src = b64
		} else if i := strings.Index(src, ";base64,"); i >= 0 {
			src = src[i+8:]
		}
		payload = map[string]any{"inputs": src, "parameters": map[string]any{"prompt": body["prompt"]}}
	} else {
		payload = map[string]any{"inputs": body["prompt"]}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(mustJSON(payload)))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, raw, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return resp.StatusCode, imageResult(map[string]any{"b64_json": base64.StdEncoding.EncodeToString(raw)}), nil
}

func fetchAsBase64(ctx context.Context, c *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch image: %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return base64.StdEncoding.EncodeToString(raw), nil
}

// ---------------------------------------------------------------- small helpers

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func numOr(v any, def int) int {
	if f, ok := v.(float64); ok && f > 0 {
		return int(f)
	}
	return def
}

func strArrOf(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, it := range list {
		if s, ok := it.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
