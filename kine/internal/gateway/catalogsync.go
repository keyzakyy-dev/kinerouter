package gateway

// Model catalog sync. Ported from src/lib/modelCatalog/sync.js: once a day,
// download models.dev/api.json and write a trimmed catalog next to the
// database, so a deployment shares the same file the reference catalog reader
// consumes.
//
// Scope note: the JS build() also diffs upstream capabilities against the
// hand-written tables to emit per-provider model deltas. this build has no
// runtime capability reader yet, so this port writes the catalog data (models +
// trimmed raw) and omits the provider-delta layer — there is nothing to consume
// it. When a capability reader lands, add the delta builder here.
//
// Fail-open, like the JS: a failed sync leaves the previous file in place and
// records lastError; it never blocks a request.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	catalogURL        = "https://models.dev/api.json"
	catalogVersion    = 2
	catalogSyncEvery  = 24 * time.Hour
	catalogRetryEvery = 30 * time.Minute
	catalogFetchLimit = 64 << 20 // models.dev api.json is ~4.3MB; cap far above
)

// catalogState is what GET/POST /api/models/catalog-sync report.
type catalogState struct {
	Running    bool   `json:"running"`
	LastSync   int64  `json:"lastSync"`
	LastError  string `json:"lastError"`
	LastResult any    `json:"lastResult"`
	Etag       string `json:"etag"`
	File       string `json:"file"`
	URL        string `json:"url"`
	IntervalMs int64  `json:"intervalMs"`
}

var (
	catMu       sync.Mutex
	catState    = catalogState{URL: catalogURL, IntervalMs: int64(catalogSyncEvery / time.Millisecond)}
	catStop     chan struct{}
	catStopOnce sync.Once
)

func (g *Gateway) catalogFile() string { return filepath.Join(g.cfg.DataDir, "model-catalog.json") }
func (g *Gateway) catalogRawFile() string {
	return filepath.Join(g.cfg.DataDir, "model-catalog-raw.json")
}

// StartCatalogSync launches the daily timer. Call once from main after boot;
// MODEL_CATALOG_SYNC=off disables it.
func (g *Gateway) StartCatalogSync() {
	if strings.EqualFold(os.Getenv("MODEL_CATALOG_SYNC"), "off") {
		return
	}
	catStopOnce.Do(func() {
		g.restoreCatalogEtag()
		catStop = make(chan struct{})
		go func() {
			// Let the server serve first requests before the first download.
			delay := 60 * time.Second
			for {
				select {
				case <-catStop:
					return
				case <-time.After(delay):
				}
				if g.syncCatalog() {
					delay = catalogSyncEvery
				} else {
					delay = catalogRetryEvery
				}
			}
		}()
	})
}

func (g *Gateway) StopCatalogSync() {
	if catStop != nil {
		close(catStop)
	}
}

// restoreCatalogEtag resumes from the file we wrote, so a restart can send
// If-None-Match instead of re-downloading to be told nothing changed.
func (g *Gateway) restoreCatalogEtag() {
	b, err := os.ReadFile(g.catalogFile())
	if err != nil {
		return
	}
	var f struct {
		Etag     string `json:"etag"`
		SyncedAt int64  `json:"syncedAt"`
		Version  int    `json:"v"`
	}
	if json.Unmarshal(b, &f) != nil {
		return
	}
	catMu.Lock()
	catState.Etag, catState.LastSync = f.Etag, f.SyncedAt
	if f.Version != catalogVersion {
		catState.Etag = "" // force a rebuild on the next run
	}
	catMu.Unlock()
}

type catalogModel struct {
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Limit struct {
		Context int64 `json:"context"`
		Output  int64 `json:"output"`
	} `json:"limit"`
	Reasoning bool `json:"reasoning"`
}

// buildCatalog emits the same two objects build() does, minus the provider
// delta layer (see the file header). models keys are "provider:baseModelId".
func buildCatalog(raw map[string]struct {
	Models map[string]catalogModel `json:"models"`
}) (models map[string]map[string]bool, slim map[string]map[string]map[string]any) {
	models = map[string]map[string]bool{}
	slim = map[string]map[string]map[string]any{}
	for providerID, provider := range raw {
		pm := map[string]map[string]any{}
		for modelID, m := range provider.Models {
			id := baseModelID(modelID)
			declared := map[string]bool{}
			for _, in := range m.Modalities.Input {
				switch in {
				case "image":
					declared["vision"] = true
				case "pdf":
					declared["pdf"] = true
				case "audio":
					declared["audioInput"] = true
				case "video":
					declared["videoInput"] = true
				}
			}
			if len(declared) > 0 {
				models[providerID+":"+id] = declared
			}
			inputs := []string{}
			for _, in := range m.Modalities.Input {
				if in != "text" {
					inputs = append(inputs, in)
				}
			}
			entry := map[string]any{"i": inputs, "c": m.Limit.Context, "o": m.Limit.Output}
			if m.Reasoning {
				entry["r"] = true
			}
			pm[modelID] = entry
		}
		slim[providerID] = pm
	}
	return models, slim
}

// baseModelID mirrors baseId(): "zai-org/GLM-4.6V:free" -> "glm-4.6v".
func baseModelID(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.IndexByte(id, ':'); i >= 0 {
		id = id[:i]
	}
	return strings.ToLower(id)
}

func writeAtomic(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// syncCatalog runs one sync and reports whether it completed (updated or
// unchanged), so the timer can pick its next delay.
func (g *Gateway) syncCatalog() bool {
	catMu.Lock()
	if catState.Running {
		catMu.Unlock()
		return false
	}
	catState.Running = true
	etag := catState.Etag
	catMu.Unlock()
	defer func() {
		catMu.Lock()
		catState.Running = false
		catMu.Unlock()
	}()

	req, err := http.NewRequest(http.MethodGet, catalogURL, nil)
	if err != nil {
		g.catalogFail(err)
		return false
	}
	req.Header.Set("Accept", "application/json")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		g.catalogFail(err)
		return false
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		catMu.Lock()
		catState.LastSync = time.Now().UnixMilli()
		catState.LastError = ""
		catState.LastResult = map[string]any{"status": "unchanged"}
		catMu.Unlock()
		return true
	case http.StatusOK:
	default:
		g.catalogFail(errHTTP(resp.StatusCode))
		return false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, catalogFetchLimit))
	if err != nil {
		g.catalogFail(err)
		return false
	}
	var raw map[string]struct {
		Models map[string]catalogModel `json:"models"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		g.catalogFail(err)
		return false
	}

	models, slim := buildCatalog(raw)
	newEtag := resp.Header.Get("Etag")
	payload, _ := json.Marshal(map[string]any{
		"v": catalogVersion, "etag": newEtag, "syncedAt": time.Now().UnixMilli(),
		"models": models, "providers": map[string]any{},
	})
	rawPayload, _ := json.Marshal(slim)
	if err := writeAtomic(g.catalogFile(), payload); err != nil {
		g.catalogFail(err)
		return false
	}
	if err := writeAtomic(g.catalogRawFile(), rawPayload); err != nil {
		g.catalogFail(err)
		return false
	}

	catMu.Lock()
	catState.Etag = newEtag
	catState.LastSync = time.Now().UnixMilli()
	catState.LastError = ""
	catState.LastResult = map[string]any{
		"status": "updated", "etag": newEtag, "bytes": len(payload),
		"models": len(models), "providers": len(slim),
	}
	catMu.Unlock()
	return true
}

func (g *Gateway) catalogFail(err error) {
	catMu.Lock()
	catState.LastError = err.Error()
	catMu.Unlock()
}

type errHTTP int

func (e errHTTP) Error() string { return "HTTP " + itoa(int64(e)) }

// ---------------------------------------------------------------- routes

func (g *Gateway) registerCatalogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/models/catalog-sync", g.requireAuth(g.handleCatalogStatus))
	mux.HandleFunc("POST /api/models/catalog-sync", g.requireAuth(g.handleCatalogSync))
}

func (g *Gateway) handleCatalogStatus(w http.ResponseWriter, r *http.Request) {
	catMu.Lock()
	st := catState
	catMu.Unlock()
	st.File = g.catalogFile()
	var catalog any
	if b, err := os.ReadFile(g.catalogFile()); err == nil {
		var f struct {
			SyncedAt  int64          `json:"syncedAt"`
			Models    map[string]any `json:"models"`
			Providers map[string]any `json:"providers"`
		}
		if json.Unmarshal(b, &f) == nil {
			catalog = map[string]any{
				"syncedAt": f.SyncedAt, "models": len(f.Models),
				"providers": len(f.Providers), "bytes": len(b),
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running": st.Running, "lastSync": st.LastSync, "lastError": st.LastError,
		"lastResult": st.LastResult, "etag": st.Etag, "file": st.File,
		"url": st.URL, "intervalMs": st.IntervalMs, "catalog": catalog,
	})
}

func (g *Gateway) handleCatalogSync(w http.ResponseWriter, r *http.Request) {
	if !g.syncCatalog() {
		catMu.Lock()
		msg := catState.LastError
		catMu.Unlock()
		if msg == "" {
			msg = "sync in progress"
		}
		writeErr(w, http.StatusServiceUnavailable, msg)
		return
	}
	catMu.Lock()
	res := catState.LastResult
	catMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "result": res})
}
