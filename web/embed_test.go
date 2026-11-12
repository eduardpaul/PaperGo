package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"image/png"
	"io"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"strconv"
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

func TestInstallableManifestsAndIcons(t *testing.T) {
	h := Handler()
	get := func(target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		return w
	}
	manifest := func(target string) map[string]any {
		w := get(target)
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/manifest+json" || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: %d %v", target, w.Code, w.Header())
		}
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	own := manifest("/pwa/papergo.webmanifest")
	if own["id"] != "/" || own["start_url"] != "/launcher" || own["scope"] != "/" || own["display"] != "standalone" || own["name"] != "PaperGo" {
		t.Fatalf("PaperGo's manifest: %v", own)
	}

	const workspace, app = "0b6f2a5e-1c3d-4e8f-9a7b-2d4c6e8f0a1b", "9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f"
	page := "/w/" + workspace + "/a/" + app
	q := url.Values{"workspace": {workspace}, "app": {app}, "name": {"Diesel & \"fuel\""}, "icon": {"fuel"}, "color": {"#b45309"}}
	m := manifest("/pwa/app.webmanifest?" + q.Encode())
	if m["id"] != page || m["start_url"] != page || m["scope"] != page || m["name"] != "Diesel & \"fuel\"" {
		t.Fatalf("an app's manifest: %v", m)
	}
	// Every icon a manifest names is served, in the size it promises.
	for _, each := range []map[string]any{own, m} {
		icons := each["icons"].([]any)
		if len(icons) != 3 {
			t.Fatalf("icons: %v", icons)
		}
		for _, entry := range icons {
			icon := entry.(map[string]any)
			w := get(icon["src"].(string))
			if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(w.Header().Get("Cache-Control"), "public, max-age=") {
				t.Fatalf("%v: %d %v", icon["src"], w.Code, w.Header())
			}
			img, err := png.Decode(w.Body)
			if err != nil {
				t.Fatal(err)
			}
			if size := strconv.Itoa(img.Bounds().Dx()); icon["sizes"] != size+"x"+size {
				t.Fatalf("%v is %s pixels wide", icon, size)
			}
			_, _, _, corner := img.At(0, 0).RGBA()
			if maskable := icon["purpose"] == "maskable"; maskable != (corner == 0xffff) {
				t.Fatalf("%v: corner alpha %x", icon, corner)
			}
		}
	}
	if w := get("/pwa/icon-180.png?name=Receipts"); w.Code != 200 || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("an app's Apple icon: %d %v", w.Code, w.Header())
	}

	// Nothing but a well-formed address is answered, and only to reads.
	for _, target := range []string{
		"/pwa/", "/pwa/other.webmanifest", "/pwa/icon-64.png", "/pwa/papergo.webmanifest?name=x",
		"/pwa/app.webmanifest", "/pwa/app.webmanifest?" + url.Values{"workspace": {workspace}, "app": {app}}.Encode(),
		"/pwa/app.webmanifest?" + url.Values{"workspace": {"../x"}, "app": {app}, "name": {"x"}}.Encode(),
		"/pwa/app.webmanifest?" + url.Values{"workspace": {strings.ToUpper(workspace)}, "app": {app}, "name": {"x"}}.Encode(),
		"/pwa/app.webmanifest?" + url.Values{"workspace": {workspace}, "app": {app}, "name": {strings.Repeat("x", 201)}}.Encode(),
		"/pwa/icon-192.png?name=x&icon=no-such-glyph", "/pwa/icon-192.png?name=x&color=red", "/pwa/icon-192.png?icon=fuel",
	} {
		if w := get(target); w.Code != 404 {
			t.Fatalf("%s: %d", target, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/pwa/papergo.webmanifest", nil))
	if w.Code != 405 {
		t.Fatalf("POST: %d", w.Code)
	}
}
