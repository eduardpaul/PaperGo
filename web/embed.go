// Package web serves the production frontend embedded in the PaperGo executable.
// Build web/dist with npm run build before compiling Go packages.
package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
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
	gzipped := &compressed{files: files}
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
				if gzipped.worth(name) {
					w.Header().Add("Vary", "Accept-Encoding")
					if body := gzippedBody(gzipped, name, r); body != nil {
						w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
						w.Header().Set("Content-Encoding", "gzip")
						http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
						return
					}
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

// compressed holds the gzip encodings of the embedded files worth
// compressing, made once on first request.
type compressed struct {
	files fs.FS
	cache sync.Map // name → func() []byte
}

// compressible are the text formats in the build, fonts included.
var compressible = map[string]bool{".js": true, ".mjs": true, ".css": true, ".svg": true, ".json": true, ".map": true, ".txt": true, ".ttf": true, ".wasm": true}

// worth reports whether name is a format worth compressing.
func (c *compressed) worth(name string) bool {
	return compressible[path.Ext(name)] && mime.TypeByExtension(path.Ext(name)) != ""
}

// get returns the gzip encoding of name, or nil when compressing it saves
// too little.
func (c *compressed) get(name string) []byte {
	once, _ := c.cache.LoadOrStore(name, sync.OnceValue(func() []byte {
		data, err := fs.ReadFile(c.files, name)
		if err != nil || len(data) < 1024 {
			return nil
		}
		var b bytes.Buffer
		z, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
		if _, err = z.Write(data); err != nil || z.Close() != nil || b.Len() >= len(data)*9/10 {
			return nil
		}
		return b.Bytes()
	}))
	return once.(func() []byte)()
}

// gzippedBody returns the gzip encoding of name when the request accepts
// it and the file has one.
func gzippedBody(c *compressed, name string, r *http.Request) []byte {
	if !acceptsGzip(r) {
		return nil
	}
	return c.get(name)
}

// acceptsGzip reports whether the request's Accept-Encoding allows gzip.
func acceptsGzip(r *http.Request) bool {
	for _, header := range r.Header.Values("Accept-Encoding") {
		for _, coding := range strings.Split(header, ",") {
			name, params, _ := strings.Cut(strings.TrimSpace(coding), ";")
			if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
				continue
			}
			q, found := strings.CutPrefix(strings.ReplaceAll(strings.TrimSpace(params), " ", ""), "q=")
			if !found {
				return true
			}
			if v, err := strconv.ParseFloat(q, 64); err == nil && v > 0 {
				return true
			}
		}
	}
	return false
}
