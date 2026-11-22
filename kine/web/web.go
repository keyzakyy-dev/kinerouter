// Package web serves the built React dashboard from an embedded filesystem.
// The Vite build output is copied to web/dist before `go build`; when that
// directory holds no build (fresh clone, `go test ./...`), a placeholder page
// is served instead so the binary still starts.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// placeholder is served when web/dist carries no Vite build.
const placeholder = `<!doctype html><meta charset="utf-8"><title>KineRouter</title>
<body style="font:14px system-ui;padding:2rem;max-width:40rem">
<h1 style="font-size:1.1rem">KineRouter is running</h1>
<p>The dashboard was not built into this binary. Build it with:</p>
<pre style="background:#f4f4f5;padding:.75rem;border-radius:6px">cd web-app &amp;&amp; npm ci &amp;&amp; npm run build</pre>
<p>then rebuild the Go binary. The API is available at <code>/v1</code>.</p>
</body>`

// Handler returns an http.Handler that serves static assets and falls back to
// index.html for client-side routes (SPA).
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		if clean == "/" {
			serveIndex(w, r, sub)
			return
		}
		if f, err := sub.Open(strings.TrimPrefix(clean, "/")); err == nil {
			f.Close()
			// Hashed assets are immutable; everything else gets a short cache.
			if strings.HasPrefix(clean, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, sub)
	}), nil
}

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte(placeholder))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}
