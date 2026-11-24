package gateway

// Console log feed for /dashboard/console-log, the Go counterpart of
// 9router-go's /api/translator/console-logs routes.
//
// The buffer itself lives in internal/logbuf and is fed by main() through
// log.SetOutput plus the leveled helpers in gateway.go, so every line arrives
// with the severity and tag it was emitted with.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/logbuf"
)

func (g *Gateway) registerConsoleLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/translator/console-logs", g.requireAuth(g.handleGetConsoleLogs))
	mux.HandleFunc("DELETE /api/translator/console-logs", g.requireAuth(g.handleClearConsoleLogs))
	mux.HandleFunc("GET /api/translator/console-logs/stream", g.requireAuth(g.handleConsoleLogStream))
	mux.HandleFunc("GET /api/translator/console-logs/level", g.requireAuth(g.handleGetConsoleLogLevel))
	mux.HandleFunc("PUT /api/translator/console-logs/level", g.requireAuth(g.handleSetConsoleLogLevel))
}

func (g *Gateway) handleGetConsoleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "logs": logbuf.Default().Entries()})
}

func (g *Gateway) handleClearConsoleLogs(w http.ResponseWriter, r *http.Request) {
	logbuf.Default().Clear()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (g *Gateway) handleGetConsoleLogLevel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "level": logbuf.Default().Level().String()})
}

// handleSetConsoleLogLevel changes the capture floor at runtime, no restart.
func (g *Gateway) handleSetConsoleLogLevel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil || body.Level == "" {
		writeErr(w, http.StatusBadRequest, "missing log level")
		return
	}
	level, ok := logbuf.ParseLevel(body.Level)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid log level (debug, info, warn, error)")
		return
	}
	logbuf.Default().SetLevel(level)
	logInfo("server", "log level changed level=%s", level.String())
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "level": level.String()})
}

// handleConsoleLogStream mirrors the reference: an init frame with the buffered
// entries, then one "line" frame per new entry, a clear frame on DELETE, and a
// 25s ping so proxies keep the connection open.
func (g *Gateway) handleConsoleLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func(v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if buffered := logbuf.Default().Entries(); len(buffered) > 0 {
		if !send(map[string]any{"type": "init", "entries": buffered}) {
			return
		}
	}

	ch, unsubscribe := logbuf.Default().Subscribe()
	defer unsubscribe()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Clear {
				if !send(map[string]any{"type": "clear"}) {
					return
				}
				continue
			}
			if !send(map[string]any{"type": "line", "entry": ev.Entry}) {
				return
			}
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
