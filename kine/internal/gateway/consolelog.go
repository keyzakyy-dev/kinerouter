package gateway

// Console log feed for /dashboard/console-log, the Go counterpart of
// src/app/api/translator/console-logs[/stream]/route.js.
//
// The buffer itself lives in internal/logbuf and is fed by main() through
// log.SetOutput, so every log.Printf in the process is visible here.

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
}

func (g *Gateway) handleGetConsoleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "logs": logbuf.Default().Lines()})
}

func (g *Gateway) handleClearConsoleLogs(w http.ResponseWriter, r *http.Request) {
	logbuf.Default().Clear()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleConsoleLogStream mirrors the JS route: an init frame with the buffered
// lines, then one frame per new line, a clear frame on DELETE, and a 25s ping
// so proxies keep the connection open.
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

	if buffered := logbuf.Default().Lines(); len(buffered) > 0 {
		if !send(map[string]any{"type": "init", "logs": buffered}) {
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
			if !send(map[string]any{"type": "line", "line": ev.Line}) {
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
