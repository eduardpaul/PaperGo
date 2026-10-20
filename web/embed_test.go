package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserRoutesAndCaching(t *testing.T) {
	h := Handler()
	index, err := assets.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	var etag string
	for _, route := range []string{"/", "/index.html", "/login", "/admin/users", "/w/workspace/l/collection?item=item", "/w/workspace/l/collection/"} {
		r := httptest.NewRequest("GET", route, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != string(index) || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("ETag") == "" {
			t.Fatalf("entry point must revalidate: %v", w.Header())
		}
		etag = w.Header().Get("ETag")
	}
	for _, tc := range []struct {
		method, match string
		status        int
	}{{"HEAD", "", 200}, {"GET", etag, 304}, {"HEAD", etag, 304}} {
		r := httptest.NewRequest(tc.method, "/admin", nil)
		r.Header.Set("If-None-Match", tc.match)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || w.Body.Len() != 0 {
			t.Fatalf("%s %s: %d %s", tc.method, tc.match, w.Code, w.Body.String())
		}
	}
}

func TestEmbeddedBuildAssets(t *testing.T) {
	h := Handler()
	underscoreChunks := 0
	err := fs.WalkDir(assets, "dist", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || name == "dist/index.html" {
			return err
		}
		data, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/"+strings.TrimPrefix(name, "dist/"), nil))
		if w.Code != 200 || w.Body.String() != string(data) {
			t.Fatalf("%s: %d", name, w.Code)
		}
		if strings.HasPrefix(name, "dist/assets/") {
			if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatalf("hashed asset must be immutable: %s %v", name, w.Header())
			}
		} else if w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("unhashed asset must revalidate: %s", name)
		}
		if strings.HasSuffix(name, ".js") && !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
			t.Fatalf("JavaScript MIME: %s %v", name, w.Header())
		}
		if strings.HasSuffix(name, ".css") && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
			t.Fatalf("CSS MIME: %s %v", name, w.Header())
		}
		if strings.HasPrefix(entry.Name(), "_") {
			underscoreChunks++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if underscoreChunks == 0 {
		t.Fatal("expected Vite's underscore-prefixed route chunks to be embedded")
	}
}

func TestCompressedAssets(t *testing.T) {
	h := Handler()
	var name string
	var data []byte
	err := fs.WalkDir(assets, "dist/assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !strings.HasSuffix(path, ".js") {
			return err
		}
		if b, err := assets.ReadFile(path); err != nil || len(b) > len(data) {
			name, data = path, b
			return err
		}
		return nil
	})
	if err != nil || name == "" {
		t.Fatal(name, err)
	}
	route := "/" + strings.TrimPrefix(name, "dist/")
	for _, tc := range []struct {
		accept string
		gzip   bool
	}{{"gzip, deflate, br", true}, {"br;q=1.0, GZIP;q=0.5", true}, {"", false}, {"gzip;q=0", false}, {"identity", false}} {
		r := httptest.NewRequest("GET", route, nil)
		if tc.accept != "" {
			r.Header.Set("Accept-Encoding", tc.accept)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		header := w.Header()
		if w.Code != 200 || header.Get("Vary") != "Accept-Encoding" || !strings.Contains(header.Get("Content-Type"), "javascript") || header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("%q: %d %v", tc.accept, w.Code, header)
		}
		body := w.Body.Bytes()
		if tc.gzip {
			if header.Get("Content-Encoding") != "gzip" || len(body) >= len(data) {
				t.Fatalf("%q must be gzip: %v %d", tc.accept, header, len(body))
			}
			z, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if body, err = io.ReadAll(z); err != nil {
				t.Fatal(err)
			}
		} else if header.Get("Content-Encoding") != "" {
			t.Fatalf("%q must be identity: %v", tc.accept, header)
		}
		if !bytes.Equal(body, data) {
			t.Fatalf("%q: content differs", tc.accept)
		}
	}
}

func TestMissingAssetsAndUnsupportedMethods(t *testing.T) {
	h := Handler()
	for _, route := range []string{"/assets", "/assets/", "/assets/missing", "/assets/missing.js", "/missing.css", "/src/main.tsx", "/../package.json"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		if w.Code != 404 || strings.Contains(w.Body.String(), "<html") || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("missing asset %s must remain a noncacheable 404: %d %v", route, w.Code, w.Header())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/admin", nil))
	if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("unsupported method: %d %v", w.Code, w.Header())
	}
}
