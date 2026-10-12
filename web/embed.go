// Package web serves the production frontend embedded in the PaperGo executable.
// Build web/dist with npm run build before compiling Go packages.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// Vite's route chunks can start with underscores; all: includes those files.
//
//go:embed all:dist
var assets embed.FS

// Handler serves assets and the SPA entry point without a filesystem dependency.
// Missing assets stay 404s, so stale chunk URLs never receive HTML as JavaScript.
func Handler() http.Handler {
	files, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		panic(fmt.Errorf("embedded frontend: %w", err))
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(index))
	static := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.Trim(r.URL.Path, "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(files, name); err == nil {
				if info.IsDir() {
					http.NotFound(w, r)
					return
				}
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				static.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" || !fs.ValidPath(name) {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", etag)
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
