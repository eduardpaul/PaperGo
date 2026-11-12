package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"papergo/internal/appicon"
)

// What makes PaperGo and each of its apps installable on a phone lives under
// /pwa/: a web manifest and its icons. A browser's installer fetches them
// without the person's sign-in, so every answer is made from its own address
// alone: the page that names an app's manifest already knows the app, and
// the server reads nothing to answer.

// pwaBackground is the page's light background, shown while an installed app
// starts.
const pwaBackground = "#fcfcfd"

// pwaIcons are the icon files and how each is drawn. A phone cuts the shape
// of a maskable icon and of Apple's 180 pixel one itself, so those bleed.
var pwaIcons = map[string]struct {
	size  int
	bleed bool
}{
	"icon-180.png":          {180, true},
	"icon-192.png":          {192, false},
	"icon-512.png":          {512, false},
	"icon-maskable-512.png": {512, true},
}

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

type manifest struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	BackgroundColor string         `json:"background_color"`
	ThemeColor      string         `json:"theme_color"`
	Icons           []manifestIcon `json:"icons"`
}

func newManifest(id, name, start, scope, iconQuery string) manifest {
	icon := func(file, sizes, purpose string) manifestIcon {
		return manifestIcon{Src: "/pwa/" + file + iconQuery, Sizes: sizes, Type: "image/png", Purpose: purpose}
	}
	return manifest{
		ID: id, Name: name, ShortName: name, StartURL: start, Scope: scope, Display: "standalone",
		BackgroundColor: pwaBackground, ThemeColor: pwaBackground,
		Icons: []manifestIcon{icon("icon-192.png", "192x192", "any"), icon("icon-512.png", "512x512", "any"), icon("icon-maskable-512.png", "512x512", "maskable")},
	}
}

// appLook reads the name, glyph and colour an address gives an app, as the
// apps feature stores them.
func appLook(q url.Values) (name, glyph, colour string, ok bool) {
	name, glyph, colour = q.Get("name"), q.Get("icon"), q.Get("color")
	ok = name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 200 && strings.TrimSpace(name) == name &&
		(glyph == "" || appicon.ValidGlyph(glyph)) && (colour == "" || appicon.ValidColor(colour))
	return name, glyph, colour, ok
}

func iconQuery(name, glyph, colour string) string {
	q := url.Values{"name": {name}}
	if glyph != "" {
		q.Set("icon", glyph)
	}
	if colour != "" {
		q.Set("color", colour)
	}
	return "?" + q.Encode()
}

// pwaCache keeps the icons drawn lately; it empties when full, as any icon
// can be drawn again.
var pwaCache = struct {
	sync.Mutex
	icons map[string][]byte
}{icons: map[string][]byte{}}

const pwaCacheSize = 256

// pwaDrawing bounds the icons drawn at once. The addresses need no sign-in
// and every new one costs a drawing, so drawing never takes every processor.
var pwaDrawing = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/2))

func keptIcon(key string) ([]byte, bool) {
	pwaCache.Lock()
	defer pwaCache.Unlock()
	data, ok := pwaCache.icons[key]
	return data, ok
}

func cachedIcon(ctx context.Context, key string, draw func() ([]byte, error)) ([]byte, error) {
	if data, ok := keptIcon(key); ok {
		return data, nil
	}
	select {
	case pwaDrawing <- struct{}{}:
		defer func() { <-pwaDrawing }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Another request may have drawn it while this one waited.
	if data, ok := keptIcon(key); ok {
		return data, nil
	}
	data, err := draw()
	if err != nil {
		return nil, err
	}
	pwaCache.Lock()
	if len(pwaCache.icons) >= pwaCacheSize {
		clear(pwaCache.icons)
	}
	pwaCache.icons[key] = data
	pwaCache.Unlock()
	return data, nil
}

// servePWA answers the file name names under /pwa/.
func servePWA(w http.ResponseWriter, r *http.Request, name string) {
	q := r.URL.Query()
	if file, ok := pwaIcons[name]; ok {
		var data []byte
		var err error
		// PaperGo's own icon has one address, so it is revalidated daily.
		keep := "public, max-age=86400"
		if r.URL.RawQuery == "" {
			data, err = cachedIcon(r.Context(), name, func() ([]byte, error) { return appicon.Brand(file.size, file.bleed) })
		} else {
			app, glyph, colour, ok := appLook(q)
			if !ok {
				http.NotFound(w, r)
				return
			}
			// What is drawn depends on the name only through its initials and its default colour.
			if colour == "" {
				colour = appicon.DefaultColor(app)
			}
			initials := ""
			if glyph == "" {
				initials = appicon.Initials(app)
			}
			keep = "public, max-age=31536000, immutable"
			data, err = cachedIcon(r.Context(), strings.Join([]string{name, glyph, colour, initials}, "\x00"), func() ([]byte, error) {
				return appicon.App(glyph, app, colour, file.size, file.bleed)
			})
		}
		if err != nil {
			http.Error(w, "icon unavailable", http.StatusInternalServerError)
			return
		}
		// Only an icon that was drawn is worth keeping.
		w.Header().Set("Cache-Control", keep)
		w.Header().Set("Content-Type", "image/png")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
		return
	}
	var m manifest
	switch name {
	case "papergo.webmanifest":
		if r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		m = newManifest("/", "PaperGo", "/launcher", "/", "")
	case "app.webmanifest":
		app, glyph, colour, ok := appLook(q)
		workspace, appID := q.Get("workspace"), q.Get("app")
		if !ok || !canonicalUUID(workspace) || !canonicalUUID(appID) {
			http.NotFound(w, r)
			return
		}
		// The app's page is both where it starts and all it covers, so what
		// it opens elsewhere in PaperGo shows as a page outside the app.
		page := "/w/" + workspace + "/a/" + appID
		m = newManifest(page, app, page, page, iconQuery(app, glyph, colour))
	default:
		http.NotFound(w, r)
		return
	}
	body, err := json.Marshal(m)
	if err != nil {
		http.Error(w, "manifest unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}

func canonicalUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s
}
