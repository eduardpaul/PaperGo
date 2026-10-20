package documentsearch

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
	"papergo/internal/testutil/searchstore"
)

const testToken = "0123456789abcdef0123456789abcdef"

// searches are the search extensions of the services setup made.
var searches sync.Map

// setup is the API with search, acting as alice, its service and its file
// storage directory.
func setup(t *testing.T) (http.Handler, *dms.Service, string) {
	t.Helper()
	db := daltest.Database(t)
	path := t.TempDir()
	files, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.SQL)
	x := &Search{Storage: files, Store: searchstore.Open(t, filepath.Join(t.TempDir(), "search.db"), []string{"english"})}
	if err = x.Register(s); err != nil {
		t.Fatal(err)
	}
	searches.Store(s, x)
	return handlerAs(t, s, "alice"), s, path
}

// handlerAs is the API with search acting as subject.
func handlerAs(t *testing.T, s *dms.Service, subject string) http.Handler {
	t.Helper()
	x := extension(t, s)
	a := &httpapi.API{DMS: s, Auth: auth.Development{Token: testToken, Subject: subject}, Storage: x.Storage, Logger: slog.Default(), MaxUpload: 32, Modules: []httpapi.Module{x.Module()}}
	return a.Handler()
}

func extension(t *testing.T, s *dms.Service) *Search {
	t.Helper()
	x, ok := searches.Load(s)
	if !ok {
		t.Fatal("no search for the service")
	}
	return x.(*Search)
}

// indexSearch brings the search store up to date with every live item and
// folder, as the search.index workflow does in a running PaperGo.
func indexSearch(t *testing.T, s *dms.Service) {
	t.Helper()
	indexAll(t, s, extension(t, s))
}

func request(h http.Handler, method, path, body, token, match, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(context.Background()))
	return w
}
