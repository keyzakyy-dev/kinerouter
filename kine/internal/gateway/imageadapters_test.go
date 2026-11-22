package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
)

// Self-check for the media image adapters: each one talks to a fake upstream
// and must produce an OpenAI-shaped {created,data:[{url|b64_json}]} body.
func TestMediaImageAdapters(t *testing.T) {
	ctx := t.Context()
	creds := executor.Credentials{APIKey: "k"}

	// --- fal: submit → COMPLETED poll → response_url
	var fal *httptest.Server
	fal = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/m":
			json.NewEncoder(w).Encode(map[string]any{"status_url": fal.URL + "/status", "response_url": fal.URL + "/res"})
		case "/status":
			json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
		case "/res":
			json.NewEncoder(w).Encode(map[string]any{"images": []any{map[string]any{"url": "https://img/1.png"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fal.Close()
	status, out, err := runFal(ctx, fal.Client(), creds, "m", map[string]any{"prompt": "x"}, map[string]any{"baseUrl": fal.URL})
	if err != nil || status != 200 {
		t.Fatalf("fal: %d %v", status, err)
	}
	if u := imageDataUrl(t, out, 0); u != "https://img/1.png" {
		t.Fatalf("fal url %q", u)
	}

	// --- stability: sync image → b64_json, sd3 sends model
	var got map[string]any
	st := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		switch r.URL.Path {
		case "/sd3":
			json.NewEncoder(w).Encode(map[string]any{"image": "QUJD"})
		default:
			json.NewEncoder(w).Encode(map[string]any{"image": "QUJD"})
		}
	}))
	defer st.Close()
	_, out, _ = runStability(ctx, st.Client(), creds, "stable-image-ultra", map[string]any{"prompt": "x"}, map[string]any{"baseUrl": st.URL})
	if b := imageDataB64(t, out, 0); b != "QUJD" {
		t.Fatalf("stability b64 %q", b)
	}

	// --- bfl: listClip → Ready
	var bfl *httptest.Server
	bfl = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/m" {
			json.NewEncoder(w).Encode(map[string]any{"polling_url": bfl.URL + "/poll"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "Ready", "result": map[string]any{"sample": "https://bfl/1.png"}})
	}))
	defer bfl.Close()
	_, out, err = runBFL(ctx, bfl.Client(), creds, "m", map[string]any{"prompt": "x"}, map[string]any{"baseUrl": bfl.URL})
	if err != nil || imageDataUrl(t, out, 0) != "https://bfl/1.png" {
		t.Fatalf("bfl: %v %s", err, out)
	}

	// --- runway: submit id → SUCCEEDED with output list
	var rw *httptest.Server
	rw = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/text_to_image" {
			json.NewEncoder(w).Encode(map[string]any{"id": "t1"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "SUCCEEDED", "output": []any{"https://rw/1.png"}})
	}))
	defer rw.Close()
	_, out, err = runRunway(ctx, rw.Client(), creds, "gen4_image", map[string]any{"prompt": "x"}, map[string]any{"baseUrl": rw.URL})
	if err != nil || imageDataUrl(t, out, 0) != "https://rw/1.png" {
		t.Fatalf("runway: %v %s", err, out)
	}

	// --- nanobanana: submit → SUCCESS flag 1
	var nb *httptest.Server
	nb = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gen" {
			json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"taskId": "t9"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"successFlag": 1,
			"response": map[string]any{"resultImageUrl": "https://nb/1.png"}}})
	}))
	defer nb.Close()
	_, out, err = runNanoBanana(ctx, nb.Client(), creds, "m", map[string]any{"prompt": "x"},
		map[string]any{"baseUrl": nb.URL + "/gen", "pollUrl": nb.URL + "/poll"})
	if err != nil || imageDataUrl(t, out, 0) != "https://nb/1.png" {
		t.Fatalf("nanobanana: %v %s", err, out)
	}

	// --- sdwebui: batch_size preserved, images → b64_json
	var sd map[string]any
	sdw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&sd)
		json.NewEncoder(w).Encode(map[string]any{"images": []any{"QUJD"}})
	}))
	defer sdw.Close()
	_, out, err = runSDWebUI(ctx, sdw.Client(), creds, "m", map[string]any{"prompt": "x", "n": float64(2), "size": "512x768"},
		map[string]any{"baseUrl": sdw.URL})
	if err != nil || imageDataB64(t, out, 0) != "QUJD" {
		t.Fatalf("sdwebui: %v %s", err, out)
	}
	if sd["batch_size"] != float64(2) || sd["width"] != float64(512) || sd["height"] != float64(768) {
		t.Fatalf("sdwebui body %v", sd)
	}

	// --- comfyui: passthrough
	cw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"prompt_id":"p"}`))
	}))
	defer cw.Close()
	_, out, err = runComfyUI(ctx, cw.Client(), creds, "m", map[string]any{"prompt": "x"}, map[string]any{"baseUrl": cw.URL})
	if err != nil || string(out) != `{"prompt_id":"p"}` {
		t.Fatalf("comfyui: %v %s", err, out)
	}

	// --- huggingface: binary response → b64_json; unmapped model rejected
	hf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("JPEGDATA"))
	}))
	defer hf.Close()
	cfg := map[string]any{"baseUrl": hf.URL, "modelMap": map[string]any{"org/m": "fal-ai/path"}}
	_, out, err = runHuggingFace(ctx, hf.Client(), creds, "org/m", map[string]any{"prompt": "x"}, cfg)
	if err != nil {
		t.Fatalf("hf: %v", err)
	}
	if imageDataB64(t, out, 0) == "" {
		t.Fatalf("hf empty: %s", out)
	}
	if _, _, err := runHuggingFace(ctx, hf.Client(), creds, "unknown", map[string]any{"prompt": "x"}, cfg); err == nil {
		t.Fatal("hf must reject an unmapped model")
	}
	if _, _, err := runHuggingFace(ctx, hf.Client(), creds, "../etc", map[string]any{"prompt": "x"},
		map[string]any{"baseUrl": hf.URL}); err == nil {
		t.Fatal("hf must reject a traversal model id on a custom base")
	}

	// --- cloudflare: image/* response → b64_json, accountId required
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNG"))
	}))
	defer cf.Close()
	cfCreds := executor.Credentials{APIKey: "k", Extra: map[string]any{"accountId": "acct"}}
	_, out, err = runCloudflare(ctx, cf.Client(), cfCreds, "@cf/m/x", map[string]any{"prompt": "x"},
		map[string]any{"baseUrl": cf.URL})
	if err != nil || imageDataB64(t, out, 0) == "" {
		t.Fatalf("cloudflare: %v %s", err, out)
	}
	if _, _, err := runCloudflare(ctx, cf.Client(), executor.Credentials{APIKey: "k"}, "m",
		map[string]any{"prompt": "x"}, map[string]any{"baseUrl": cf.URL}); err == nil {
		t.Fatal("cloudflare must require accountId")
	}

	// --- registry paths: settings-free paths still resolve under a temp HOME
}

func imageDataUrl(t *testing.T, raw []byte, i int) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("bad body %s", raw)
	}
	list, _ := m["data"].([]any)
	if len(list) <= i {
		return ""
	}
	d, _ := list[i].(map[string]any)
	u, _ := d["url"].(string)
	return u
}

func imageDataB64(t *testing.T, raw []byte, i int) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("bad body %s", raw)
	}
	list, _ := m["data"].([]any)
	if len(list) <= i {
		return ""
	}
	d, _ := list[i].(map[string]any)
	b, _ := d["b64_json"].(string)
	return b
}
