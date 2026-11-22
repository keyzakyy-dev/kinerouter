package gateway

// POST /v1/audio/speech and /v1/audio/transcriptions — OpenAI-compatible TTS and
// STT. Mirrors open-sse/handlers/ttsCore.js + sttCore.js for the JSON and
// multipart transports; websocket/browser-scrape providers (edge-tts, google-tts,
// local-device, xiaomi-mimo, gemini-live) are out of scope here.

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// ---------------------------------------------------------------- TTS

func (g *Gateway) handleTTS(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	modelStr, _ := body["model"].(string)
	input, _ := body["input"].(string)
	if modelStr == "" {
		writeErr(w, http.StatusBadRequest, "Missing model")
		return
	}
	if strings.TrimSpace(input) == "" {
		writeErr(w, http.StatusBadRequest, "Missing required field: input")
		return
	}
	if _, ok := g.authorizeRequest(w, r, modelStr); !ok {
		return
	}
	targets, err := g.resolveTargets(modelStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t := targets[0]
	cfg := g.mediaConfig(t.Provider, "ttsConfig")
	if cfg == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider '%s' does not support TTS via this route.", t.Provider))
		return
	}
	responseFormat := r.URL.Query().Get("response_format")
	if s, _ := body["response_format"].(string); responseFormat == "" {
		responseFormat = s
	}
	if responseFormat == "" {
		responseFormat = "mp3"
	}
	lang, _ := body["language"].(string)
	style, _ := body["style"].(string)

	g.serveAudio(w, r, t, cfg, func(conn []byte) ([]byte, string, int, error) {
		return g.ttsOnce(r, t, cfg, conn, input, responseFormat, lang, style)
	}, responseFormat)
}

// serveAudio runs the account-fallback loop shared by TTS and STT.
func (g *Gateway) serveAudio(w http.ResponseWriter, r *http.Request, t target, cfg map[string]any,
	call func(credJSON []byte) ([]byte, string, int, error), responseFormat string) {
	conns, _ := g.store.ListConnections(t.Provider, true)
	tried := map[string]bool{}
	var lastStatus int
	var lastBody []byte
	authNone := cfg["authType"] == "none"
	for i := 0; i <= len(conns); i++ {
		var conn = g.pickConnection(conns, t.Model, tried)
		if conn != nil {
			tried[conn.ID] = true
		} else if authNone && i == 0 {
			// noAuth provider: one attempt without credentials.
		} else {
			break
		}
		credsJSON := mustJSON(credentialMap(conn))
		out, format, status, err := call(credsJSON)
		if err == nil && status >= 200 && status < 300 {
			if responseFormat == "json" {
				writeJSON(w, http.StatusOK, map[string]any{
					"audio": base64.StdEncoding.EncodeToString(out), "format": format,
				})
				return
			}
			w.Header().Set("Content-Type", "audio/"+format)
			w.WriteHeader(http.StatusOK)
			w.Write(out)
			return
		}
		lastStatus, lastBody = status, out
		if conn != nil && (status == http.StatusUnauthorized || status == http.StatusForbidden ||
			status == http.StatusTooManyRequests || status == http.StatusPaymentRequired) {
			g.markUnavailable(conn, status, upstreamMessage(out), t.Model)
			continue
		}
		break
	}
	g.writeAudioFailure(w, lastStatus, lastBody)
}

func (g *Gateway) writeAudioFailure(w http.ResponseWriter, status int, body []byte) {
	if status == 0 {
		writeErr(w, http.StatusServiceUnavailable, "No available credentials for this model")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if len(body) == 0 {
		body = mustJSON(map[string]any{"error": map[string]any{"message": "upstream error"}})
	}
	w.Write(body)
}

// credentialMap flattens a connection for the audio callbacks.
func credentialMap(c *store.Connection) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{
		"apiKey": c.Str("apiKey"), "accessToken": c.Str("accessToken"),
		"baseUrl": c.Str("baseUrl"),
	}
}

func (g *Gateway) ttsOnce(r *http.Request, t target, cfg map[string]any, conn []byte,
	input, responseFormat, lang, style string) ([]byte, string, int, error) {
	var creds map[string]any
	json.Unmarshal(conn, &creds)
	base, _ := cfg["baseUrl"].(string)
	formatName, _ := cfg["format"].(string)
	apiKey := strOf(creds, "apiKey", strOf(creds, "accessToken", ""))
	modelID, voiceID := splitModelVoice(t.Model)

	switch {
	case formatName == "gemini-tts":
		return g.geminiTTS(base, apiKey, t.Model, input, lang)
	case formatName == "openai" || formatName == "openai-speech" || formatName == "":
		if modelID == "" {
			modelID, _ = cfg["defaultModel"].(string)
		}
		return postJSON(base, map[string]string{"Authorization": "Bearer " + apiKey}, map[string]any{
			"model": modelID, "input": input, "voice": orDefault(voiceID, "alloy"),
			"response_format": "mp3", "speed": 1.0,
		})
	case formatName == "minimax-tts":
		out, _, status, err := postJSON(base, map[string]string{"Authorization": "Bearer " + apiKey}, map[string]any{
			"model": orDefault(modelID, "speech-2.8-hd"), "text": input, "stream": false,
			"language_boost": "auto", "output_format": "hex",
			"voice_setting": map[string]any{"voice_id": orDefault(voiceID, "English_expressive_narrator"), "speed": 1, "vol": 1, "pitch": 0},
			"audio_setting": map[string]any{"sample_rate": 32000, "bitrate": 128000, "format": "mp3", "channel": 1},
		})
		if err != nil {
			return out, "", status, err
		}
		var m map[string]any
		if json.Unmarshal(out, &m) != nil {
			return out, "", status, fmt.Errorf("minimax: invalid JSON")
		}
		data, _ := m["data"].(map[string]any)
		audioHex, _ := data["audio"].(string)
		raw, err := hex.DecodeString(strings.TrimSpace(audioHex))
		if err != nil || len(raw) == 0 {
			return out, "", http.StatusBadGateway, fmt.Errorf("minimax: no audio")
		}
		return raw, "mp3", status, nil
	case formatName == "cartesia":
		return postJSON(base, map[string]string{
			"X-API-Key": apiKey, "Cartesia-Version": "2024-06-10",
		}, map[string]any{
			"model_id": orDefault(modelID, "sonic-2"), "transcript": input,
			"voice":         map[string]any{"mode": "id", "id": voiceID},
			"output_format": map[string]any{"container": "mp3", "bit_rate": 128000, "sample_rate": 44100},
		})
	case formatName == "inworld":
		out, _, status, err := postJSON(base, map[string]string{"Authorization": "Basic " + apiKey}, map[string]any{
			"text": input, "voiceId": orDefault(voiceID, "Alex"),
			"modelId":     orDefault(modelID, "inworld-tts-1.5-mini"),
			"audioConfig": map[string]any{"audioEncoding": "MP3"},
		})
		if err != nil {
			return out, "", status, err
		}
		var m map[string]any
		json.Unmarshal(out, &m)
		b64, _ := m["audioContent"].(string)
		raw, err := base64.StdEncoding.DecodeString(b64)
		return raw, "mp3", status, err
	case formatName == "fish-audio":
		return postJSON(base, map[string]string{"Authorization": "Bearer " + apiKey, "model": orDefault(modelID, "s2.1-pro-free")},
			map[string]any{"text": input, "format": "mp3", "reference_id": voiceID})
	case formatName == "deepgram":
		url := base + "?model=" + urlQueryEscape(orDefault(modelID, "aura-asteria-en"))
		return postJSON(url, map[string]string{"Authorization": "Token " + apiKey}, map[string]any{"text": input})
	case formatName == "nvidia-tts":
		return postJSON(base, map[string]string{"Authorization": "Bearer " + apiKey}, map[string]any{
			"input": map[string]any{"text": input}, "voice": orDefault(voiceID, "default"), "model": modelID,
		})
	case formatName == "huggingface-tts":
		if strings.Contains(modelID, "..") || modelID == "" {
			return nil, "", http.StatusBadRequest, fmt.Errorf("invalid HuggingFace model ID")
		}
		return postJSON(strings.TrimRight(base, "/")+"/"+modelID,
			map[string]string{"Authorization": "Bearer " + apiKey}, map[string]any{"inputs": input})
	case formatName == "coqui" || formatName == "tortoise":
		payload := map[string]any{"text": input}
		if voiceID != "" {
			payload["speaker_id"] = voiceID
			payload["voice"] = voiceID
		}
		return postJSON(base, nil, payload)
	case formatName == "playht":
		user, key, _ := strings.Cut(apiKey, ":")
		return postJSON(base, map[string]string{
			"X-USER-ID": user, "Authorization": "Bearer " + key, "Accept": "audio/mpeg",
		}, map[string]any{"text": input, "voice": voiceID, "voice_engine": orDefault(modelID, "PlayDialog"),
			"output_format": "mp3", "speed": 1})
	}
	return nil, "", http.StatusBadRequest, fmt.Errorf("TTS format %q not implemented", formatName)
}

// geminiTTS asks generateContent for AUDIO and wraps the PCM in a WAV header.
func (g *Gateway) geminiTTS(base, apiKey, model, input, lang string) ([]byte, string, int, error) {
	modelID := model
	if i := strings.Index(model, "/"); i > 0 {
		modelID = model[:i]
	}
	url := strings.TrimRight(base, "/") + "/" + modelID + ":generateContent?key=" + urlQueryEscape(apiKey)
	prompt := input
	if !strings.Contains(input, ": ") {
		if lang != "" {
			prompt = "Say in " + lang + ": " + input
		} else {
			prompt = "Say: " + input
		}
	}
	out, format, status, err := postJSON(url, nil, map[string]any{
		"contents": []any{map[string]any{"parts": []any{map[string]any{"text": prompt}}}},
		"generationConfig": map[string]any{
			"responseModalities": []any{"AUDIO"},
			"speechConfig": map[string]any{"voiceConfig": map[string]any{
				"prebuiltVoiceConfig": map[string]any{"voiceName": geminiVoice(model)}}},
		},
	})
	if err != nil {
		return out, format, status, err
	}
	var m map[string]any
	if json.Unmarshal(out, &m) != nil {
		return out, format, status, fmt.Errorf("gemini tts: invalid JSON")
	}
	b64 := geminiAudioB64(m)
	if b64 == "" {
		return out, format, status, fmt.Errorf("Gemini TTS returned no audio")
	}
	pcm, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return out, format, status, err
	}
	return pcmToWAV(pcm), "wav", status, nil
}

func geminiVoice(model string) string {
	if i := strings.Index(model, "/"); i > 0 && i+1 < len(model) {
		return model[i+1:]
	}
	return "Kore"
}

func geminiAudioB64(m map[string]any) string {
	cands, _ := m["candidates"].([]any)
	if len(cands) == 0 {
		return ""
	}
	content, _ := cands[0].(map[string]any)["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	for _, p := range parts {
		inline, _ := p.(map[string]any)["inlineData"].(map[string]any)
		if d, ok := inline["data"].(string); ok && d != "" {
			return d
		}
	}
	return ""
}

// pcmToWAV wraps 24kHz mono 16-bit PCM in a RIFF/WAVE header.
func pcmToWAV(pcm []byte) []byte {
	const sampleRate, channels, bits = 24000, 1, 16
	dataSize := len(pcm)
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	putLE32(h[4:], uint32(36+dataSize))
	copy(h[8:], "WAVEfmt ")
	putLE32(h[16:], 16)
	putLE16(h[20:], 1)
	putLE16(h[22:], channels)
	putLE32(h[24:], sampleRate)
	putLE32(h[28:], sampleRate*channels*bits/8)
	putLE16(h[32:], channels*bits/8)
	putLE16(h[34:], bits)
	copy(h[36:], "data")
	putLE32(h[40:], uint32(dataSize))
	return append(h, pcm...)
}

func putLE16(b []byte, v uint16) { b[0], b[1] = byte(v), byte(v>>8) }
func putLE32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// ---------------------------------------------------------------- STT

var audioMIMEByExt = map[string]string{
	".mp3": "audio/mpeg", ".mp4": "audio/mp4", ".m4a": "audio/mp4", ".wav": "audio/wav",
	".ogg": "audio/ogg", ".flac": "audio/flac", ".webm": "audio/webm", ".aac": "audio/aac", ".opus": "audio/opus",
}

// audioForm is a parsed transcriptions request held in memory.
type audioForm struct {
	file     []byte
	filename string
	mimeType string
	fields   map[string]string
}

func (g *Gateway) handleTranscriptions(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid multipart body")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Missing required field: file")
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 64<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read file")
		return
	}
	modelStr := r.FormValue("model")
	if modelStr == "" {
		writeErr(w, http.StatusBadRequest, "Missing model")
		return
	}
	if _, ok := g.authorizeRequest(w, r, modelStr); !ok {
		return
	}
	targets, err := g.resolveTargets(modelStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t := targets[0]
	cfg := g.mediaConfig(t.Provider, "sttConfig")
	if cfg == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider '%s' does not support STT", t.Provider))
		return
	}
	form := &audioForm{file: raw, filename: hdr.Filename, fields: map[string]string{}}
	form.mimeType = hdr.Header.Get("Content-Type")
	if !strings.HasPrefix(form.mimeType, "audio/") {
		form.mimeType = audioMIMEByExt[strings.ToLower(filepath.Ext(hdr.Filename))]
		if form.mimeType == "" {
			form.mimeType = "application/octet-stream"
		}
	}
	for k, v := range r.MultipartForm.Value {
		if len(v) > 0 {
			form.fields[k] = v[0]
		}
	}

	g.serveAudio(w, r, t, cfg, func(conn []byte) ([]byte, string, int, error) {
		return g.sttOnce(r, t, cfg, conn, form)
	}, "json")
}

func (g *Gateway) sttOnce(r *http.Request, t target, cfg map[string]any, conn []byte, form *audioForm) ([]byte, string, int, error) {
	var creds map[string]any
	json.Unmarshal(conn, &creds)
	base, _ := cfg["baseUrl"].(string)
	formatName, _ := cfg["format"].(string)
	apiKey := strOf(creds, "apiKey", strOf(creds, "accessToken", ""))

	switch formatName {
	case "deepgram":
		url := strings.TrimRight(base, "/") + "?model=" + urlQueryEscape(t.Model) +
			"&smart_format=true&punctuate=true"
		if lang := form.fields["language"]; lang != "" {
			url += "&language=" + urlQueryEscape(lang)
		} else {
			url += "&detect_language=true"
		}
		out, _, status, err := rawPost(url, map[string]string{
			"Authorization": "Token " + apiKey, "Content-Type": form.mimeType}, form.file)
		if err != nil {
			return out, "json", status, err
		}
		var m map[string]any
		json.Unmarshal(out, &m)
		results, _ := m["results"].(map[string]any)
		chans, _ := results["channels"].([]any)
		text := ""
		if len(chans) > 0 {
			alts, _ := chans[0].(map[string]any)["alternatives"].([]any)
			if len(alts) > 0 {
				text, _ = alts[0].(map[string]any)["transcript"].(string)
			}
		}
		return mustJSON(map[string]any{"text": text}), "json", status, nil
	case "gemini-stt":
		b64 := base64.StdEncoding.EncodeToString(form.file)
		prompt := form.fields["prompt"]
		if prompt == "" {
			prompt = "Generate a transcript of the speech. Return only the transcribed text, no commentary."
		}
		if lang := form.fields["language"]; lang != "" {
			prompt += " Language: " + lang + "."
		}
		url := strings.TrimRight(base, "/") + "/" + t.Model + ":generateContent?key=" + urlQueryEscape(apiKey)
		out, _, status, err := postJSON(url, nil, map[string]any{
			"contents": []any{map[string]any{"parts": []any{
				map[string]any{"text": prompt},
				map[string]any{"inline_data": map[string]any{"mime_type": form.mimeType, "data": b64}},
			}}},
		})
		if err != nil {
			return out, "json", status, err
		}
		var m map[string]any
		json.Unmarshal(out, &m)
		text := ""
		if cands, _ := m["candidates"].([]any); len(cands) > 0 {
			content, _ := cands[0].(map[string]any)["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, p := range parts {
				if s, ok := p.(map[string]any)["text"].(string); ok {
					text += s
				}
			}
		}
		return mustJSON(map[string]any{"text": text}), "json", status, nil
	case "huggingface-asr":
		if strings.Contains(t.Model, "..") || strings.Contains(t.Model, "//") {
			return nil, "", http.StatusBadRequest, fmt.Errorf("invalid model ID")
		}
		return rawPost(strings.TrimRight(base, "/")+"/"+t.Model,
			map[string]string{"Authorization": "Bearer " + apiKey, "Content-Type": form.mimeType}, form.file)
	case "elevenlabs-stt":
		body, ctype := multipartUpload(map[string]string{"model_id": t.Model}, "file", form.filename, form.file)
		return rawPost(base, map[string]string{"xi-api-key": apiKey, "Content-Type": ctype}, body)
	default: // openai-compatible multipart
		fields := map[string]string{"model": t.Model}
		for _, k := range []string{"language", "prompt", "response_format", "temperature"} {
			if v := form.fields[k]; v != "" {
				fields[k] = v
			}
		}
		body, ctype := multipartUpload(fields, "file", form.filename, form.file)
		return rawPost(strings.TrimRight(base, "/"), map[string]string{
			"Authorization": "Bearer " + apiKey, "Content-Type": ctype}, body)
	}
}

// ---------------------------------------------------------------- HTTP helpers

// postJSON POSTs a JSON body and returns the raw response plus a format guess.
func postJSON(url string, headers map[string]string, payload map[string]any) ([]byte, string, int, error) {
	return rawPost(url, headers, mustJSON(payload))
}

func rawPost(url string, headers map[string]string, body []byte) ([]byte, string, int, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", http.StatusBadRequest, err
	}
	if _, ok := headers["Content-Type"]; !ok {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := mediaClient.Do(req)
	if err != nil {
		return nil, "", http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	format := "mp3"
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		switch {
		case strings.Contains(ct, "wav"):
			format = "wav"
		case strings.Contains(ct, "ogg"):
			format = "ogg"
		case strings.Contains(ct, "pcm"):
			format = "pcm"
		case strings.Contains(ct, "json"):
			format = "json"
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, format, resp.StatusCode, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	if len(out) == 0 {
		return out, format, resp.StatusCode, fmt.Errorf("upstream returned empty body")
	}
	return out, format, resp.StatusCode, nil
}

// mediaClient is the outbound client for media endpoints, which carry their own
// base URL and therefore never go through the registry executor.
var mediaClient = &http.Client{Timeout: 300 * time.Second}

func multipartUpload(fields map[string]string, fileField, filename string, file []byte) ([]byte, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	part, _ := mw.CreateFormFile(fileField, orDefault(filename, "audio.wav"))
	part.Write(file)
	mw.Close()
	return buf.Bytes(), mw.FormDataContentType()
}

func splitModelVoice(model string) (string, string) {
	if i := strings.LastIndex(model, "/"); i > 0 {
		return model[:i], model[i+1:]
	}
	return "", model
}

func strOf(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
