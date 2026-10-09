package webdav_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/model"
	"papergo/internal/storage"
	"papergo/internal/testutil"
	"papergo/internal/webdav"
	"regexp"
	"strings"
	"testing"
)

var ctx = context.Background()

// principals maps bearer tokens to subjects.
type principals map[string]string

func (p principals) Verify(_ context.Context, token string) (string, error) {
	if subject, ok := p[token]; ok {
		return subject, nil
	}
	return "", errors.New("invalid token")
}

type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

type env struct {
	t       *testing.T
	h       http.Handler
	s       *dms.Service
	blobs   string
	library string
	root    string
}

func setup(t *testing.T) *env {
	t.Helper()
	db := testutil.Database(t)
	blobs := t.TempDir()
	store, err := storage.NewLocal(blobs)
	if err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.SQL)
	verifier := principals{"alice-token": "alice", "bob-token": "bob"}
	logger := slog.New(slog.NewJSONHandler(testLog{t}, nil))
	a := &httpapi.API{DMS: s, Auth: verifier, Storage: store, Logger: logger, Ready: db.SQL.PingContext, MaxUpload: 64, MaxInFlight: 4, WebDAV: webdav.New(s, verifier, store, logger, 64)}
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(ctx, "alice", w.ID, w.Version, dms.Permissions{Grants: []dms.Permission{{Subject: "alice", Action: "manage"}, {Subject: "bob", Action: "read"}}}); err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Shared", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, h: a.Handler(), s: s, blobs: blobs, library: lib.ID, root: webdav.Prefix + lib.ID}
}

// do sends a request; headers alternate names and values.
func (e *env) do(method, path, token, body string, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}
func (e *env) expect(w *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	e.t.Helper()
	if w.Code != status {
		e.t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	return w
}
func (e *env) blobCount() int {
	e.t.Helper()
	entries, err := os.ReadDir(e.blobs)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(entries)
}

func TestDiscoveryAndAuthentication(t *testing.T) {
	e := setup(t)
	for _, path := range []string{e.root + "/", "/"} {
		w := e.expect(e.do("OPTIONS", path, "", ""), 200)
		if !strings.Contains(w.Header().Get("DAV"), "2") || !strings.Contains(w.Header().Get("Allow"), "PROPFIND") {
			t.Fatalf("OPTIONS %s headers: %v", path, w.Header())
		}
	}
	w := e.expect(e.do("PROPFIND", e.root+"/", "", "", "Depth", "0"), 401)
	if !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic") {
		t.Fatal("no Basic challenge", w.Header())
	}
	e.expect(e.do("PROPFIND", e.root+"/", "wrong", "", "Depth", "0"), 401)

	// Windows Explorer authenticates with an app password and any user name.
	c, err := e.s.CreateWebDAVCredential(ctx, "alice", dms.WebDAVCredentialInput{Label: "Explorer"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PROPFIND", e.root, nil)
	r.Header.Set("Depth", "0")
	r.SetBasicAuth("alice@example.com", c.Password)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	e.expect(rec, 207)
	if !strings.Contains(rec.Body.String(), "<D:href>"+e.root+"/</D:href>") || !strings.Contains(rec.Body.String(), "<D:collection/>") {
		t.Fatal("library root is not a collection", rec.Body.String())
	}
	r.SetBasicAuth("alice", c.Password+"x")
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	e.expect(rec, 401)

	// The mount root lets clients walk up from a library.
	for _, path := range []string{"/webdav/", "/webdav"} {
		e.expect(e.do("PROPFIND", path, "alice-token", "", "Depth", "1"), 207)
	}
	e.expect(e.do("PROPFIND", "/webdav/not-a-library/", "alice-token", "", "Depth", "0"), 400)
	// Libraries without WebDAV are not served.
	lib, err := e.s.LibraryFile(ctx, "alice", e.library, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Update(ctx, "alice", lib.ID, lib.Version, dms.UpdateResource{WebDAVEnabled: new(bool)}); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("PROPFIND", e.root+"/", "alice-token", "", "Depth", "0"), 404)
}

func TestFileLifecycle(t *testing.T) {
	e := setup(t)
	alice := "alice-token"
	e.expect(e.do("MKCOL", e.root+"/Docs", alice, ""), 201)
	e.expect(e.do("MKCOL", e.root+"/docs", alice, ""), 405)
	e.expect(e.do("MKCOL", e.root+"/Missing/Child", alice, ""), 409)
	e.expect(e.do("MKCOL", e.root+"/Body", alice, "<x/>"), 415)

	w := e.expect(e.do("PUT", e.root+"/Docs/hello.txt", alice, "hello"), 201)
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("PUT returned no ETag")
	}
	w = e.expect(e.do("GET", e.root+"/docs/HELLO.txt", alice, ""), 200)
	if w.Body.String() != "hello" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || w.Header().Get("ETag") != etag {
		t.Fatalf("GET: %q %v", w.Body.String(), w.Header())
	}
	if w = e.expect(e.do("GET", e.root+"/Docs/hello.txt", alice, "", "Range", "bytes=1-2"), 206); w.Body.String() != "el" {
		t.Fatalf("range: %q", w.Body.String())
	}
	e.expect(e.do("HEAD", e.root+"/Docs/hello.txt", alice, ""), 200)
	e.expect(e.do("GET", e.root+"/Docs", alice, ""), 405)
	e.expect(e.do("PUT", e.root+"/Docs/hello.txt", alice, "stale", "If-Match", `"stale"`), 412)
	e.expect(e.do("PUT", e.root+"/Docs/hello.txt", alice, "hello world", "If-Match", etag), 204)
	e.expect(e.do("PUT", e.root+"/Docs", alice, "x"), 405)
	e.expect(e.do("PUT", e.root+"/Nowhere/x.txt", alice, "x"), 409)
	e.expect(e.do("PUT", e.root+"/Docs/hello.txt", alice, "x", "Content-Range", "bytes 0-0/1"), 400)

	w = e.expect(e.do("PROPFIND", e.root+"/Docs/", alice, "", "Depth", "1"), 207)
	body := w.Body.String()
	for _, want := range []string{"<D:href>" + e.root + "/Docs/</D:href>", "<D:href>" + e.root + "/Docs/hello.txt</D:href>", "<D:getcontentlength>11</D:getcontentlength>", "<D:displayname>hello.txt</D:displayname>", "<D:getlastmodified>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("PROPFIND lacks %s: %s", want, body)
		}
	}
	w = e.expect(e.do("PROPFIND", e.root+"/", alice, "", "Depth", "infinity"), 403)
	if !strings.Contains(w.Body.String(), "propfind-finite-depth") {
		t.Fatal(w.Body.String())
	}
	w = e.expect(e.do("PROPFIND", e.root+"/Docs/hello.txt", alice, `<?xml version="1.0"?><D:propfind xmlns:D="DAV:" xmlns:Z="urn:x"><D:prop><D:getetag/><Z:color/></D:prop></D:propfind>`, "Depth", "0"), 207)
	if !strings.Contains(w.Body.String(), "404 Not Found") || !strings.Contains(w.Body.String(), `xmlns:P="urn:x"`) || strings.Contains(w.Body.String(), "getcontentlength") {
		t.Fatal("selected properties", w.Body.String())
	}
	w = e.expect(e.do("PROPPATCH", e.root+"/Docs/hello.txt", alice, `<?xml version="1.0"?><D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:"><D:set><D:prop><Z:Win32LastModifiedTime>Wed, 08 Oct 2026 10:00:00 GMT</Z:Win32LastModifiedTime></D:prop></D:set></D:propertyupdate>`), 207)
	if !strings.Contains(w.Body.String(), "200 OK") {
		t.Fatal("Win32 properties rejected", w.Body.String())
	}
	w = e.expect(e.do("PROPPATCH", e.root+"/Docs/hello.txt", alice, `<D:propertyupdate xmlns:D="DAV:"><D:set><D:prop><D:getetag>x</D:getetag></D:prop></D:set></D:propertyupdate>`), 207)
	if !strings.Contains(w.Body.String(), "403 Forbidden") {
		t.Fatal("live property changed", w.Body.String())
	}

	// Rename to a name that needs escaping, with an absolute Destination URL.
	e.expect(e.do("MOVE", e.root+"/Docs/hello.txt", alice, "", "Destination", "http://example.com"+e.root+"/Docs/Hi%20there.txt"), 201)
	e.expect(e.do("GET", e.root+"/Docs/hello.txt", alice, ""), 404)
	if w = e.expect(e.do("GET", e.root+"/Docs/Hi%20there.txt", alice, ""), 200); w.Body.String() != "hello world" {
		t.Fatal(w.Body.String())
	}
	e.expect(e.do("MOVE", e.root+"/Docs/Hi%20there.txt", alice, "", "Destination", "http://elsewhere.example"+e.root+"/x.txt"), 502)
	e.expect(e.do("MOVE", e.root+"/Docs/Hi%20there.txt", alice, "", "Destination", webdav.Prefix+"00000000-0000-0000-0000-000000000000/x.txt"), 502)

	e.expect(e.do("COPY", e.root+"/Docs", alice, "", "Destination", e.root+"/Copy"), 201)
	if w = e.expect(e.do("GET", e.root+"/Copy/Hi%20there.txt", alice, ""), 200); w.Body.String() != "hello world" {
		t.Fatal(w.Body.String())
	}
	e.expect(e.do("COPY", e.root+"/Docs", alice, "", "Destination", e.root+"/Docs/Inner"), 403)
	e.expect(e.do("MOVE", e.root+"/Copy/Hi%20there.txt", alice, "", "Destination", e.root+"/Docs/hi THERE.txt", "Overwrite", "F"), 412)
	e.expect(e.do("MOVE", e.root+"/Copy/Hi%20there.txt", alice, "", "Destination", e.root+"/Docs/hi THERE.txt"), 204)
	e.expect(e.do("MOVE", e.root+"/Copy", alice, "", "Destination", e.root+"/Docs/Copy"), 201)

	e.expect(e.do("DELETE", e.root+"/", alice, ""), 403)
	e.expect(e.do("DELETE", e.root+"/Docs", alice, ""), 204)
	e.expect(e.do("PROPFIND", e.root+"/Docs/", alice, "", "Depth", "0"), 404)
	w = e.expect(e.do("PROPFIND", e.root+"/", alice, "", "Depth", "1"), 207)
	if strings.Count(w.Body.String(), "<D:response>") != 1 || strings.Contains(w.Body.String(), "404 Not Found") {
		t.Fatal("deleted folder still listed", w.Body.String())
	}
}

func TestLocking(t *testing.T) {
	e := setup(t)
	alice := "alice-token"
	lockBody := `<?xml version="1.0"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner><D:href>alice</D:href></D:owner></D:lockinfo>`
	// Locking an unmapped URL creates an empty file.
	w := e.expect(e.do("LOCK", e.root+"/report.docx", alice, lockBody, "Timeout", "Infinite, Second-4100000000"), 201)
	token := w.Header().Get("Lock-Token")
	if !strings.HasPrefix(token, "<urn:uuid:") || !strings.Contains(w.Body.String(), "<D:timeout>Second-3600</D:timeout>") {
		t.Fatalf("lock: %q %s", token, w.Body.String())
	}
	if w = e.expect(e.do("GET", e.root+"/report.docx", alice, ""), 200); w.Body.Len() != 0 {
		t.Fatal("lock-created file is not empty")
	}
	e.expect(e.do("PUT", e.root+"/report.docx", alice, "v1"), 423)
	e.expect(e.do("DELETE", e.root+"/report.docx", alice, ""), 423)
	e.expect(e.do("LOCK", e.root+"/report.docx", alice, lockBody), 423)
	e.expect(e.do("PUT", e.root+"/report.docx", alice, "v1", "If", "("+token+")"), 204)
	w = e.expect(e.do("PROPFIND", e.root+"/report.docx", alice, `<D:propfind xmlns:D="DAV:"><D:prop><D:lockdiscovery/></D:prop></D:propfind>`, "Depth", "0"), 207)
	if !strings.Contains(w.Body.String(), strings.Trim(token, "<>")) || !strings.Contains(w.Body.String(), "<D:href>alice</D:href>") {
		t.Fatal("lock not discovered", w.Body.String())
	}
	e.expect(e.do("LOCK", e.root+"/report.docx", alice, "", "If", "("+token+")", "Timeout", "Second-60"), 200)
	e.expect(e.do("LOCK", e.root+"/report.docx", alice, "", "If", "(<urn:uuid:unknown>)"), 412)
	e.expect(e.do("UNLOCK", e.root+"/report.docx", alice, "", "Lock-Token", "<urn:uuid:unknown>"), 409)
	e.expect(e.do("UNLOCK", e.root+"/report.docx", "bob-token", "", "Lock-Token", token), 403)
	e.expect(e.do("UNLOCK", e.root+"/report.docx", alice, "", "Lock-Token", token), 204)
	// A token that no longer exists does not block its former holder.
	e.expect(e.do("PUT", e.root+"/report.docx", alice, "v2", "If", "("+token+")"), 204)

	// A depth-infinity folder lock guards everything below it.
	e.expect(e.do("MKCOL", e.root+"/Team", alice, ""), 201)
	w = e.expect(e.do("LOCK", e.root+"/Team", alice, lockBody), 200)
	folderToken := w.Header().Get("Lock-Token")
	e.expect(e.do("PUT", e.root+"/Team/a.txt", alice, "a"), 423)
	e.expect(e.do("PUT", e.root+"/Team/a.txt", alice, "a", "If", "("+folderToken+")"), 201)
	e.expect(e.do("MOVE", e.root+"/report.docx", alice, "", "Destination", e.root+"/Team/report.docx"), 423)
	e.expect(e.do("DELETE", e.root+"/Team", alice, "", "If", "("+folderToken+")"), 204)
	// Deleting the folder released its lock.
	e.expect(e.do("MKCOL", e.root+"/Team", alice, ""), 201)
}

func TestPermissionsLimitsAndValidation(t *testing.T) {
	e := setup(t)
	alice, bob := "alice-token", "bob-token"
	e.expect(e.do("PUT", e.root+"/a.txt", alice, "a"), 201)
	// Readers browse and download but cannot change anything.
	e.expect(e.do("PROPFIND", e.root+"/", bob, "", "Depth", "1"), 207)
	e.expect(e.do("GET", e.root+"/a.txt", bob, ""), 200)
	before := e.blobCount()
	e.expect(e.do("PUT", e.root+"/a.txt", bob, "b"), 403)
	e.expect(e.do("PUT", e.root+"/b.txt", bob, "b"), 403)
	e.expect(e.do("DELETE", e.root+"/a.txt", bob, ""), 403)
	e.expect(e.do("MKCOL", e.root+"/Bob", bob, ""), 403)
	e.expect(e.do("LOCK", e.root+"/a.txt", bob, `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`), 403)
	e.expect(e.do("PUT", e.root+"/big.bin", alice, strings.Repeat("x", 65)), 413)
	if e.blobCount() != before {
		t.Fatal("rejected uploads left blob files behind")
	}
	// Collection rules surface as refusals with their reason.
	lib, err := e.s.LibraryFile(ctx, "alice", e.library, nil)
	if err != nil {
		t.Fatal(err)
	}
	// New files take field defaults, as they would through the REST API.
	if _, err = e.s.CreateField(ctx, "alice", lib.ID, dms.CreateField{Key: "owner", Label: "Owner", Type: "text", Required: true, Options: model.FieldOptions{DefaultValue: json.RawMessage(`"unassigned"`)}}); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("PUT", e.root+"/c.txt", alice, "c"), 201)
	w := e.expect(e.do("PUT", e.root+"/bad%5Cname.txt", alice, "c"), 403)
	if !regexp.MustCompile(`names cannot contain`).MatchString(w.Body.String()) {
		t.Fatal(w.Body.String())
	}
	if e.blobCount() != before+1 {
		t.Fatal("refused upload left a blob file behind")
	}
	// Ordinary reads see what WebDAV wrote.
	f, err := e.s.LibraryFile(ctx, "bob", e.library, []string{"c.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if item, err := e.s.Get(ctx, "bob", f.ID); err != nil || item.Values["owner"] != "unassigned" {
		t.Fatal(item, err)
	}
	// A required field without a default refuses new files, with the reason.
	strict, err := e.s.Create(ctx, "alice", *lib.ParentID, dms.CreateResource{Kind: "library", Name: "Strict", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.CreateField(ctx, "alice", strict.ID, dms.CreateField{Key: "status", Label: "Status", Type: "text", Required: true}); err != nil {
		t.Fatal(err)
	}
	w = e.expect(e.do("PUT", webdav.Prefix+strict.ID+"/d.txt", alice, "d"), 403)
	if !strings.Contains(w.Body.String(), "required field missing: status") {
		t.Fatal(w.Body.String())
	}
}

func TestCopyAndMoveNeverDestroyTheirSource(t *testing.T) {
	e := setup(t)
	alice := "alice-token"
	e.expect(e.do("MKCOL", e.root+"/Docs", alice, ""), 201)
	e.expect(e.do("MKCOL", e.root+"/Docs/Inner", alice, ""), 201)
	e.expect(e.do("PUT", e.root+"/Docs/Inner/keep.txt", alice, "keep"), 201)
	for _, c := range []struct{ method, from, to string }{
		{"COPY", "/Docs", "/docs"},              // the source itself, ignoring case
		{"COPY", "/Docs/Inner", "/Docs"},        // an ancestor of the source
		{"MOVE", "/Docs/Inner", "/DOCS"},        // an ancestor of the source
		{"COPY", "/Docs", "/docs/Inner/Deeper"}, // below the source
	} {
		e.expect(e.do(c.method, e.root+c.from, alice, "", "Destination", e.root+c.to, "Overwrite", "T"), 403)
	}
	if w := e.expect(e.do("GET", e.root+"/Docs/Inner/keep.txt", alice, ""), 200); w.Body.String() != "keep" {
		t.Fatal("source damaged", w.Body.String())
	}
	// A case-only rename is still a MOVE onto the source's own name.
	e.expect(e.do("MOVE", e.root+"/Docs", alice, "", "Destination", e.root+"/docs"), 201)
	e.expect(e.do("GET", e.root+"/docs/Inner/keep.txt", alice, ""), 200)
}

func TestLockOwnersAndParentLocks(t *testing.T) {
	e := setup(t)
	alice := "alice-token"
	e.expect(e.do("MKCOL", e.root+"/Folder", alice, ""), 201)
	// rclone and others declare their own prefix; the owner must not leak it.
	w := e.expect(e.do("LOCK", e.root+"/Folder", alice, `<d:lockinfo xmlns:d="DAV:"><d:lockscope><d:exclusive/></d:lockscope><d:locktype><d:write/></d:locktype><d:owner><d:href>rclone &amp; co</d:href></d:owner></d:lockinfo>`, "Depth", "0"), 200)
	token := w.Header().Get("Lock-Token")
	w = e.expect(e.do("PROPFIND", e.root+"/Folder", alice, "", "Depth", "0"), 207)
	if err := xml.Unmarshal(w.Body.Bytes(), new(struct{})); err != nil || !strings.Contains(w.Body.String(), "<D:owner><D:href>rclone &amp; co</D:href></D:owner>") {
		t.Fatalf("lockdiscovery is not well-formed: %v %s", err, w.Body.String())
	}
	// Creating a file by LOCK adds a member to the locked folder.
	lockBody := `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`
	e.expect(e.do("LOCK", e.root+"/Folder/new.txt", alice, lockBody), 423)
	e.expect(e.do("PROPFIND", e.root+"/Folder/new.txt", alice, "", "Depth", "0"), 404)
	e.expect(e.do("LOCK", e.root+"/Folder/new.txt", alice, lockBody, "If", "("+token+")"), 201)
}
