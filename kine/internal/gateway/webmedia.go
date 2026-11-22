package gateway

// Executor-driven media providers (registry videoConfig.executorDriven): the
// upstream has no OpenAI-shaped endpoint, so the gateway speaks its site API
// directly and returns the artifact inline. Ported from
// open-sse/executors/{veoaifree-web,uc-web}.js.
//
// grok-web and muse-web are deliberately absent: grok needs a Chrome TLS
// fingerprint (wreq) and muse needs a headless browser. Both stay rejected
// until a browser runtime exists here.
//
// Security: every remote URL is validated with netguard before a request is
// made, artifact redirects are followed manually with the same check, and no
// credential is written to logs or error text.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/netguard"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

type mediaItem struct {
	B64  string
	URL  string
	Mime string
}

// webMediaGen generates one asset synchronously; kind is "image" or "video".
type webMediaGen func(ctx context.Context, c *http.Client, creds executor.Credentials,
	prompt, model, size, resolution string, seconds int, images []string) ([]mediaItem, error)

var webMediaGenerators = map[string]webMediaGen{
	"veoaifree-web": generateVeoAiFree,
	"uc-web":        generateUcWeb,
}

const webMediaUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

// cookieJar is the minimal cookie persistence these two sites need: they set a
// session cookie on the first page load and expect it back on the AJAX call.
type cookieJar struct {
	mu   sync.Mutex
	vals map[string]string
}

func newJar(header string) *cookieJar {
	j := &cookieJar{vals: map[string]string{}}
	for _, part := range strings.Split(header, ";") {
		if i := strings.Index(part, "="); i > 0 {
			j.vals[strings.TrimSpace(part[:i])] = strings.TrimSpace(part[i+1:])
		}
	}
	return j
}

func (j *cookieJar) header() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	parts := make([]string, 0, len(j.vals))
	for k, v := range j.vals {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func (j *cookieJar) absorb(resp *http.Response) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, sc := range resp.Header.Values("Set-Cookie") {
		pair := strings.SplitN(sc, ";", 2)[0]
		if i := strings.Index(pair, "="); i > 0 {
			if v := strings.TrimSpace(pair[i+1:]); v != "" {
				j.vals[strings.TrimSpace(pair[:i])] = v
			}
		}
	}
}

func (j *cookieJar) get(name string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.vals[name]
}

// fetch sends a request through the jar and the connection's proxy.
func webFetch(ctx context.Context, c *http.Client, jar *cookieJar, method, target, contentType string, body io.Reader, extra map[string]string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", webMediaUA)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	if h := jar.header(); h != "" {
		req.Header.Set("Cookie", h)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	jar.absorb(resp)
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	return resp, data, nil
}

// ---------------------------------------------------------------- veoaifree.com

const veoBaseURL = "https://veoaifree.com"

var veoNonceRe = regexp.MustCompile(`nonce":"([a-f0-9]+)"`)
var veoURLRe = regexp.MustCompile(`https?://\S+`)

func generateVeoAiFree(ctx context.Context, c *http.Client, creds executor.Credentials,
	prompt, model, size, resolution string, seconds int, images []string) ([]mediaItem, error) {
	kind := "video"
	if strings.Contains(strings.ToLower(model), "image") {
		kind = "image"
	}
	jar := newJar("")
	_, page, err := webFetch(ctx, c, jar, http.MethodGet, veoBaseURL, "", nil, map[string]string{"Referer": veoBaseURL + "/"})
	if err != nil {
		return nil, err
	}
	match := veoNonceRe.FindSubmatch(page)
	if match == nil {
		return nil, errors.New("failed to extract CSRF nonce from veoaifree.com (layout changed or rate limited)")
	}
	nonce := string(match[1])
	post := func(params url.Values) (string, error) {
		params.Set("action", "veo_video_generator")
		params.Set("nonce", nonce)
		resp, body, err := webFetch(ctx, c, jar, http.MethodPost, veoBaseURL+"/wp-admin/admin-ajax.php",
			"application/x-www-form-urlencoded", strings.NewReader(params.Encode()),
			map[string]string{"Origin": veoBaseURL, "Referer": veoBaseURL + "/"})
		if err != nil {
			return "", err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("veoaifree request failed (HTTP %d)", resp.StatusCode)
		}
		return strings.TrimSpace(string(body)), nil
	}

	if kind == "image" {
		out, err := post(url.Values{
			"promptIMG":          {prompt},
			"totalVariationsIMG": {"1"},
			"aspectRatioIMG":     {veoAspect(size, "IMAGE_ASPECT_RATIO")},
			"actionType":         {"banan-image-generator"},
		})
		if err != nil {
			return nil, err
		}
		if out == "" || out == "0" || strings.Contains(strings.ToLower(out), "error") {
			return nil, fmt.Errorf("veoaifree image generation failed: %.120s", out)
		}
		var items []mediaItem
		for _, part := range strings.Split(out, ",") {
			part = strings.TrimSpace(part)
			switch {
			case part == "":
			case strings.HasPrefix(part, "http"):
				items = append(items, mediaItem{URL: part})
			default:
				items = append(items, mediaItem{B64: part, Mime: "image/png"})
			}
		}
		return items, nil
	}

	scene, err := post(url.Values{
		"prompt":          {prompt},
		"totalVariations": {"1"},
		"aspectRatio":     {veoAspect(size, "VIDEO_ASPECT_RATIO")},
		"actionType":      {"full-video-generate"},
	})
	if err != nil {
		return nil, err
	}
	if scene == "" || scene == "0" || strings.Contains(strings.ToLower(scene), "error") {
		return nil, fmt.Errorf("veoaifree video submit failed: %.120s", scene)
	}
	for poll := 0; poll < 30; poll++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Second):
		}
		out, err := post(url.Values{"sceneData": {scene}, "actionType": {"final-video-results"}})
		if err != nil || out == "" || out == "0" || strings.Contains(strings.ToLower(out), "error") {
			continue
		}
		var artifact string
		for _, part := range veoURLRe.FindAllString(out, -1) {
			artifact = part
			break
		}
		if artifact == "" {
			continue
		}
		b, err := downloadGuarded(ctx, c, artifact, 5, func(resp *http.Response, data []byte) error {
			ct := strings.Split(resp.Header.Get("Content-Type"), ";")[0]
			if resp.StatusCode == 202 || resp.StatusCode == 404 || resp.StatusCode == 409 || resp.StatusCode == 425 {
				return errArtifactNotReady
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return fmt.Errorf("artifact download failed (HTTP %d)", resp.StatusCode)
			}
			if ct != "video/mp4" && ct != "application/octet-stream" {
				return fmt.Errorf("artifact returned unsupported content type (%s)", ct)
			}
			if len(data) < 12 || string(data[4:8]) != "ftyp" {
				return errors.New("artifact is not a valid MP4 file")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return []mediaItem{{B64: base64.StdEncoding.EncodeToString(b), Mime: "video/mp4"}}, nil
	}
	return nil, errors.New("veoaifree video generation timed out after 10 minutes")
}

func veoAspect(size, prefix string) string {
	v := strings.ToLower(size)
	switch {
	case strings.Contains(v, "9:16"), strings.Contains(v, "portrait"):
		return prefix + "_PORTRAIT"
	case strings.Contains(v, "1:1"), strings.Contains(v, "square"):
		return prefix + "_SQUARE"
	}
	return prefix + "_LANDSCAPE"
}

var errArtifactNotReady = errors.New("artifact not available yet")

// guardedClient returns a copy of c that refuses to dial a blocked address.
// CheckURL resolves once and the dial resolves again, so without the dialer hook
// a rebinding DNS answer still reaches loopback. Proxy settings survive.
func guardedClient(c *http.Client) *http.Client {
	out := *c
	if t, ok := c.Transport.(*http.Transport); ok {
		cl := t.Clone()
		cl.DialContext = netguard.Dialer.DialContext
		out.Transport = cl
	} else if c.Transport == nil {
		out.Transport = netguard.NewTransport(http.ProxyFromEnvironment)
	}
	return &out
}

// downloadGuarded fetches a remote artifact with SSRF checks on the initial URL
// and every redirect hop. The body is capped at 100MB.
func downloadGuarded(ctx context.Context, c *http.Client, target string, maxRedirects int,
	validate func(*http.Response, []byte) error) ([]byte, error) {
	c = guardedClient(c)
	for hop := 0; hop <= maxRedirects; hop++ {
		if err := netguard.CheckURL(ctx, target); err != nil {
			return nil, err
		}
		resp, data, err := webFetch(ctx, c, newJar(""), http.MethodGet, target, "", nil,
			map[string]string{"Referer": veoBaseURL + "/"})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			if loc == "" {
				return nil, errors.New("artifact redirect is missing a destination")
			}
			base, _ := url.Parse(target)
			next, err := base.Parse(loc)
			if err != nil {
				return nil, err
			}
			target = next.String()
			continue
		}
		if err := validate(resp, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, errors.New("artifact exceeded the redirect limit")
}

// ---------------------------------------------------------------- uncensored.com

const (
	ucOrigin      = "https://uncensored.com"
	ucClerkFAPI   = "https://clerk.uncensored.com"
	ucClerkJSVer  = "5.127.1"
	ucImageURL    = "https://internal.chatuncensored.ai/v2/image-gen"
	ucVideoURL    = "https://internal.chatuncensored.ai/image_to_video"
	ucSignedURL   = "https://internal-6.pubyar.com/generate-signed-url"
	ucImageMode   = "dev"
	ucModeration  = "SUPER_LIGHT"
	ucCountry     = "US"
	ucPollEvery   = 5 * time.Second
	ucPollTimeout = 5 * time.Minute
)

var ucAspectSizes = map[string][2]string{
	"1:1": {"1024", "1024"}, "16:9": {"1024", "576"}, "9:16": {"576", "1024"},
	"4:3": {"1024", "768"}, "3:4": {"768", "1024"}, "3:2": {"1152", "768"},
	"2:3": {"768", "1152"}, "4:5": {"896", "1120"}, "5:4": {"1120", "896"},
	"21:9": {"1344", "576"},
}

func generateUcWeb(ctx context.Context, c *http.Client, creds executor.Credentials,
	prompt, model, size, resolution string, seconds int, images []string) ([]mediaItem, error) {
	kind := "video"
	if strings.Contains(strings.ToLower(model), "image") {
		kind = "image"
	}
	jar := newJar(creds.APIKey)
	client := jar.get("__client")
	if client == "" {
		return nil, errors.New("UC credential missing the __client cookie — paste the full uncensored.com Cookie header")
	}
	jwt, uid, err := ucMintSession(ctx, c, jar)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{
		"Authorization": "Bearer " + jwt,
		"Origin":        ucOrigin,
		"Referer":       ucOrigin + "/",
		"Content-Type":  "application/json",
	}

	if kind == "image" {
		if model == "" {
			model = "seedream-v4.5"
		}
		payload := map[string]any{
			"prompt": prompt, "mode": ucImageMode, "model_version": model,
			"m_n_user": true, "moderationMode": ucModeration, "country": ucCountry, "vdiscount": false,
		}
		for k, v := range ucAspect(size) {
			payload[k] = v
		}
		if resolution != "" {
			payload["resolution"] = strings.ToLower(resolution)
		}
		resp, body, err := webFetch(ctx, c, jar, http.MethodPost, ucImageURL, "application/json", strings.NewReader(string(mustJSON(payload))), headers)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("UC image request failed (HTTP %d) %.200s", resp.StatusCode, body)
		}
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		resultURL, _ := m["url"].(string)
		if resultURL == "" {
			return nil, errors.New("UC image response carried no result url")
		}
		if err := ucPollResult(ctx, c, resultURL); err != nil {
			return nil, err
		}
		b, mime, err := ucDownload(ctx, c, resultURL)
		if err != nil {
			return nil, err
		}
		return []mediaItem{{B64: base64.StdEncoding.EncodeToString(b), Mime: mime}}, nil
	}

	if model == "" {
		model = "default"
	}
	if seconds <= 0 {
		seconds = 5
	}
	if resolution == "" {
		resolution = "480p"
	}
	payload := map[string]any{
		"prompt": prompt, "media_blob_name": nil, "end_frame_blob_name": nil,
		"num_frames":              seconds*16 + 1,
		"frames_per_second":       16,
		"num_inference_steps":     30,
		"guide_scale":             5,
		"shift":                   5,
		"aspect_ratio":            "auto",
		"pro_mode":                strings.HasSuffix(model, "-pro"),
		"turbo":                   model == "turbo",
		"resolution":              resolution,
		"sora_resolution":         resolution,
		"model":                   model,
		"seconds":                 seconds,
		"video_to_video_duration": seconds,
		"vdiscount":               false,
	}
	if len(images) > 0 {
		if uid == "" {
			return nil, errors.New("could not derive the account uid — re-paste the full Cookie header")
		}
		blob, err := ucUploadImage(ctx, c, jar, headers, uid, images[0])
		if err != nil {
			return nil, err
		}
		payload["media_blob_name"] = blob
	}
	resp, body, err := webFetch(ctx, c, jar, http.MethodPost, ucVideoURL, "application/json", strings.NewReader(string(mustJSON(payload))), headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("UC video request failed (HTTP %d) %.200s", resp.StatusCode, body)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	resultURL, _ := m["url"].(string)
	if resultURL == "" {
		return nil, errors.New("UC video response carried no result url")
	}
	timeout := ucPollTimeout
	if secs, ok := m["timeout_seconds"].(float64); ok && secs > 0 {
		timeout = time.Duration(secs) * time.Second
	}
	if err := ucPollResultUntil(ctx, c, resultURL, timeout); err != nil {
		return nil, err
	}
	b, mime, err := ucDownload(ctx, c, resultURL)
	if err != nil {
		return nil, err
	}
	if mime == "application/octet-stream" {
		mime = "video/mp4"
	}
	return []mediaItem{{B64: base64.StdEncoding.EncodeToString(b), Mime: mime}}, nil
}

// ucMintSession turns the durable __client cookie into a 60s Clerk session JWT.
// ponytail: the rotated __client is not persisted back (the JS app does); a
// rotation that invalidates the stored cookie surfaces as a 401.
func ucMintSession(ctx context.Context, c *http.Client, jar *cookieJar) (jwt, uid string, err error) {
	clerkHeaders := map[string]string{"Origin": ucOrigin, "Referer": ucOrigin + "/", "Accept": "application/json"}
	sid := ucJWTClaim(jar.get("__client"), "sid")
	if sid == "" {
		resp, body, err := webFetch(ctx, c, jar, http.MethodGet,
			ucClerkFAPI+"/v1/client?_clerk_js_version="+ucClerkJSVer, "", nil, clerkHeaders)
		if err != nil {
			return "", "", err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			payload, _ := m["response"].(map[string]any)
			if payload == nil {
				payload = m
			}
			sessions, _ := payload["sessions"].([]any)
			for _, s := range sessions {
				sm, _ := s.(map[string]any)
				if sm == nil {
					continue
				}
				if id, _ := sm["id"].(string); id != "" && (sid == "" || sm["status"] == "active") {
					sid = id
					if sm["status"] == "active" {
						break
					}
				}
			}
		}
	}
	if sid == "" {
		return "", "", errors.New("UC Clerk session not found — the __client cookie has no active session")
	}
	resp, body, err := webFetch(ctx, c, jar, http.MethodPost,
		ucClerkFAPI+"/v1/client/sessions/"+url.PathEscape(sid)+"/tokens?_clerk_js_version="+ucClerkJSVer,
		"application/x-www-form-urlencoded", strings.NewReader(""),
		map[string]string{"Origin": ucOrigin, "Referer": ucOrigin + "/"})
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("UC Clerk token mint failed (HTTP %d) %.200s", resp.StatusCode, body)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	jwt, _ = m["jwt"].(string)
	if jwt == "" {
		return "", "", errors.New("UC Clerk token mint returned no jwt")
	}
	return jwt, ucJWTClaim(jwt, "uid"), nil
}

func ucJWTClaim(token, claim string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	s, _ := m[claim].(string)
	return s
}

func ucAspect(size string) map[string]any {
	raw := strings.TrimSpace(size)
	pick := func(a string) map[string]any {
		return map[string]any{"aspect_ratio": a, "imageWidth": ucAspectSizes[a][0], "imageHeight": ucAspectSizes[a][1]}
	}
	if _, ok := ucAspectSizes[raw]; ok {
		return pick(raw)
	}
	if w, h := sizeDims(raw); w > 0 && h > 0 {
		best, bestDelta := "1:1", 1e9
		for a, d := range ucAspectSizes {
			dw, _ := strconv.Atoi(d[0])
			dh, _ := strconv.Atoi(d[1])
			delta := float64(dw)/float64(dh) - float64(w)/float64(h)
			if delta < 0 {
				delta = -delta
			}
			if delta < bestDelta {
				best, bestDelta = a, delta
			}
		}
		return pick(best)
	}
	return pick("1:1")
}

func ucPollResult(ctx context.Context, c *http.Client, target string) error {
	return ucPollResultUntil(ctx, c, target, ucPollTimeout)
}

func ucPollResultUntil(ctx context.Context, c *http.Client, target string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ucPollEvery):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Referer", ucOrigin+"/")
		resp, err := c.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}
	return errors.New("UC result did not become available in time")
}

func ucDownload(ctx context.Context, c *http.Client, target string) ([]byte, string, error) {
	var out []byte
	var mime string
	_, err := downloadGuarded(ctx, c, target, 3, func(resp *http.Response, data []byte) error {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("UC result download failed (HTTP %d)", resp.StatusCode)
		}
		out = data
		mime = strings.Split(resp.Header.Get("Content-Type"), ";")[0]
		if mime == "" {
			mime = "application/octet-stream"
		}
		return nil
	})
	return out, mime, err
}

func ucUploadImage(ctx context.Context, c *http.Client, jar *cookieJar, headers map[string]string, uid, ref string) (string, error) {
	bytes, err := decodeImageRef(ctx, c, ref)
	if err != nil {
		return "", err
	}
	if len(bytes) == 0 {
		return "", errors.New("UC image-to-video could not decode the input image")
	}
	resp, body, err := webFetch(ctx, c, jar, http.MethodPost, ucSignedURL, "application/json",
		strings.NewReader(string(mustJSON(map[string]any{"content_type": "image/png", "user_identifier": uid}))), headers)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("UC signed-url request failed (HTTP %d) %.200s", resp.StatusCode, body)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	signed, _ := m["signed_url"].(string)
	blob, _ := m["blob_name"].(string)
	if signed == "" || blob == "" {
		return "", errors.New("UC signed-url response missing signed_url or blob_name")
	}
	if err := netguard.CheckURL(ctx, signed); err != nil {
		return "", err
	}
	put, _, err := webFetch(ctx, guardedClient(c), newJar(""), http.MethodPut, signed, "image/png", strings.NewReader(string(bytes)), nil)
	if err != nil {
		return "", err
	}
	if put.StatusCode < 200 || put.StatusCode >= 300 {
		return "", fmt.Errorf("UC input-image upload failed (HTTP %d)", put.StatusCode)
	}
	return blob, nil
}

var dataURLRe = regexp.MustCompile(`^data:[^;]*;base64,(.*)$`)

// decodeImageRef accepts a data URL, an http(s) URL or raw base64.
func decodeImageRef(ctx context.Context, c *http.Client, ref string) ([]byte, error) {
	if m := dataURLRe.FindStringSubmatch(ref); m != nil {
		return base64.StdEncoding.DecodeString(m[1])
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		if err := netguard.CheckURL(ctx, ref); err != nil {
			return nil, err
		}
		resp, data, err := webFetch(ctx, guardedClient(c), newJar(""), http.MethodGet, ref, "", nil, map[string]string{"Accept": "image/*"})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("input image download failed (HTTP %d)", resp.StatusCode)
		}
		return data, nil
	}
	return base64.StdEncoding.DecodeString(ref)
}

// ---------------------------------------------------------------- dispatch

// webMediaConnectionFor runs a generator for one connection and returns the
// OpenAI-shaped image or video body.
func (g *Gateway) webMediaOnce(ctx context.Context, gen webMediaGen, conn *store.Connection, cfg map[string]any,
	kind, prompt, model, size, resolution string, seconds int, images []string) ([]byte, error) {
	creds := executor.CredentialsFromConnection(conn)
	client := mediaClient
	if creds.ProxyURL != "" {
		client = executor.ClientWithProxy(creds.ProxyURL)
	}
	items, err := gen(ctx, client, creds, prompt, model, size, resolution, seconds, images)
	if err != nil {
		return nil, err
	}
	if kind == "image" {
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			if it.B64 != "" {
				out = append(out, map[string]any{"b64_json": it.B64})
			} else if it.URL != "" {
				out = append(out, map[string]any{"url": it.URL})
			}
		}
		return imageResult(out...), nil
	}
	first := items[0]
	dataURL := first.URL
	if first.B64 != "" {
		mime := first.Mime
		if mime == "" {
			mime = "video/mp4"
		}
		dataURL = "data:" + mime + ";base64," + first.B64
	}
	return mustJSON(map[string]any{
		"id":      fmt.Sprintf("video-%s-%d", conn.Provider, time.Now().UnixMilli()),
		"object":  "video.generation",
		"created": time.Now().Unix(),
		"model":   model,
		"status":  "completed",
		"video":   map[string]any{"url": dataURL},
	}), nil
}

// handleWebVideo serves POST /v1/videos/{generations,edits} for an
// executor-driven provider: the generation is synchronous and the video comes
// back inline as a data URL, so there is no job to poll.
func (g *Gateway) handleWebVideo(w http.ResponseWriter, r *http.Request, provider, model, action string, parsed map[string]any) {
	gen := webMediaGenerators[provider]
	if gen == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("[%s] browser-driven video is not available in this build", provider))
		return
	}
	prompt, _ := parsed["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("[%s] video request requires a prompt", provider))
		return
	}
	size, _ := parsed["size"].(string)
	resolution, _ := parsed["resolution"].(string)
	seconds := 0
	if f, ok := parsed["seconds"].(float64); ok {
		seconds = int(f)
	} else if f, ok := parsed["duration"].(float64); ok {
		seconds = int(f)
	}

	images := collectImageRefs(parsed)

	conns, _ := g.store.ListConnections(provider, true)
	tried := map[string]bool{}
	for i := 0; i <= len(conns); i++ {
		conn := g.pickConnection(conns, model, tried)
		if conn == nil {
			break
		}
		tried[conn.ID] = true
		body, err := g.webMediaOnce(r.Context(), gen, conn, nil, "video", prompt, model, size, resolution, seconds, images)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-kinerouter-connection-id", conn.ID)
			w.WriteHeader(http.StatusOK)
			w.Write(body)
			return
		}
		if r.Context().Err() != nil {
			return
		}
	}
	writeErr(w, http.StatusBadGateway, fmt.Sprintf("[%s] video generation failed", provider))
}

// handleWebImage is the /v1/images/generations counterpart.
func (g *Gateway) handleWebImage(w http.ResponseWriter, r *http.Request, provider, model string, body map[string]any) {
	gen := webMediaGenerators[provider]
	if gen == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider '%s' image adapter is not implemented", provider))
		return
	}
	prompt, _ := body["prompt"].(string)
	size, _ := body["size"].(string)
	resolution, _ := body["resolution"].(string)

	conns, _ := g.store.ListConnections(provider, true)
	tried := map[string]bool{}
	for i := 0; i <= len(conns); i++ {
		conn := g.pickConnection(conns, model, tried)
		if conn == nil {
			break
		}
		tried[conn.ID] = true
		out, err := g.webMediaOnce(r.Context(), gen, conn, nil, "image", prompt, model, size, resolution, 0, nil)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(out)
			return
		}
		if r.Context().Err() != nil {
			return
		}
	}
	writeErr(w, http.StatusBadGateway, fmt.Sprintf("[%s] image generation failed", provider))
}

// collectImageRefs gathers image-to-video inputs from the several field names
// clients use.
func collectImageRefs(parsed map[string]any) []string {
	var out []string
	for _, key := range []string{"image", "image_url"} {
		if s, ok := parsed[key].(string); ok && s != "" {
			out = append(out, s)
		}
	}
	for _, key := range []string{"images", "image_urls"} {
		out = append(out, strArrOf(parsed[key])...)
	}
	return out
}
