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
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 256 256'%3E%3Cpath fill='%23e56a4a' d='M29.21 110.81L84.21 55.81Q89.16 50.86 96.16 50.86L204.52 50.86Q211.52 50.86 206.57 55.81L174.45 87.94Q169.5 92.89 162.5 92.89L112.66 92.89Q105.66 92.89 100.71 97.84L29.21 169.33Q24.26 174.28 24.26 167.28L24.26 122.76Q24.26 115.76 29.21 110.81Z'/%3E%3Cpath fill='%23e56a4a' d='M114.25 117.89L141.75 117.89Q148.75 117.89 153.7 122.84L231.05 200.19Q236 205.14 229 205.14L170.11 205.14Q163.11 205.14 158.16 200.19L96.51 138.54Q91.56 133.59 96.51 128.64L102.3 122.84Q107.25 117.89 114.25 117.89Z'/%3E%3Cpath fill='%23e56a4a' d='M83.74 151.3L105.49 173.06Q110.44 178.01 105.49 182.96L88.26 200.19Q83.31 205.14 76.31 205.14L27 205.14Q20 205.14 24.95 200.19L73.84 151.3Q78.79 146.35 83.74 151.3Z'/%3E%3C/svg%3E">
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
