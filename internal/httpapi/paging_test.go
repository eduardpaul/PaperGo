package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/testutil"
)

func TestNextLinksPreserveFiltersAndTerminate(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Paging"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Doc A", "Doc B", "Other"} {
		if _, err = s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/resources/" + l.ID + "/children?$top=1&q=Doc&surface=head"
	seen := map[string]bool{}
	for page := 0; page < 2; page++ {
		res := request(h, "GET", path, "", testToken, "", "")
		var body struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
			NextLink *string `json:"@odata.nextLink"`
		}
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &body) != nil || len(body.Value) != 1 || seen[body.Value[0].ID] {
			t.Fatal(res.Code, res.Body.String())
		}
		seen[body.Value[0].ID] = true
		if strings.Contains(res.Body.String(), `"data":`) || strings.Contains(res.Body.String(), `"next_cursor":`) {
			t.Fatal("unexpected response fields", res.Body.String())
		}
		if page == 0 {
			if body.NextLink == nil {
				t.Fatal("missing continuation")
			}
			link, e := url.Parse(*body.NextLink)
			if e != nil {
				t.Fatal(e)
			}
			if link.Query().Get("q") != "Doc" || link.Query().Get("surface") != "head" || link.Query().Get("$top") != "1" || link.Query().Get("$skiptoken") == "" || !link.IsAbs() {
				t.Fatal(link)
			}
			path = *body.NextLink
		} else if body.NextLink != nil {
			t.Fatal("final page should have a null next link", *body.NextLink)
		}
	}
}

func TestHistoryEndpointsProvideNextLinks(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "History"})
	if err != nil {
		t.Fatal(err)
	}
	versioning := "major"
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records", PublishingEnabled: true, Versioning: &versioning})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", l.ID, dms.CreateField{Key: "note", Label: "Note", Type: "text"}); err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "First"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "alice", i.ID, i.Version); err != nil {
		t.Fatal(err)
	}
	i, err = s.Get(ctx, "alice", i.ID)
	if err != nil {
		t.Fatal(err)
	}
	name := "Second"
	i, err = s.Update(ctx, "alice", i.ID, i.Version, dms.UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "alice", i.ID, i.Version); err != nil {
		t.Fatal(err)
	}
	for _, initial := range []string{"/v1/items/" + i.ID + "/revisions", "/v1/items/" + i.ID + "/publications", "/v1/resources/" + l.ID + "/schemas"} {
		t.Run(initial, func(t *testing.T) {
			path := initial + "?$top=1"
			seen := map[string]bool{}
			for page := 0; page < 2; page++ {
				res := request(h, "GET", path, "", testToken, "", "")
				var body struct {
					Value []struct {
						ID string `json:"id"`
					} `json:"value"`
					NextLink *string `json:"@odata.nextLink"`
				}
				if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &body) != nil || len(body.Value) != 1 || seen[body.Value[0].ID] {
					t.Fatal(res.Code, res.Body.String())
				}
				seen[body.Value[0].ID] = true
				if page == 0 {
					if body.NextLink == nil {
						t.Fatal("missing next link", res.Body.String())
					}
					path = *body.NextLink
				} else if body.NextLink != nil {
					t.Fatal("unexpected extra page", res.Body.String())
				}
			}
		})
	}
}

func TestWeakETagsAndDistinctConflicts(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Conditional"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/resources/" + w.ID
	res := request(h, "PATCH", path, `{"description":"changed"}`, testToken, ` W/"1" `, "application/json")
	if res.Code != 200 || res.Header().Get("ETag") != `"2"` {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PATCH", path, `{"description":"stale"}`, testToken, `W/"1"`, "application/json")
	if res.Code != 412 || res.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(res.Body.String(), `"code":"precondition_failed"`) || !strings.Contains(res.Body.String(), res.Header().Get("X-Request-ID")) {
		t.Fatal(res.Code, res.Body.String())
	}
	for _, match := range []string{`"+2"`, `"\u0032"`, `*`, `"2", "3"`} {
		if res = request(h, "PATCH", path, `{}`, testToken, match, "application/json"); res.Code != 400 {
			t.Fatal(match, res.Code)
		}
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		res = request(h, "POST", "/v1/resources/"+l.ID+"/children", `{"kind":"folder","name":"Duplicate"}`, testToken, "", "application/json")
		want := 201
		if n == 1 {
			want = 409
		}
		if res.Code != want {
			t.Fatal(res.Code, res.Body.String())
		}
	}
}

func TestPageSizeContract(t *testing.T) {
	for raw, want := range map[string]int{"": 50, "invalid": 50, "0": 1, "-1": 1, "500": 500, "1000": 500} {
		r := httptest.NewRequest("GET", "/v1/workspaces?$top="+raw, nil)
		if got := queryInt(r, "$top"); got != want {
			t.Fatal(raw, got, want)
		}
	}
}

// Behind a TLS-terminating proxy the request is plain HTTP on an internal
// host; next links must still use the address clients know.
func TestNextLinksUsePublicURL(t *testing.T) {
	db := testutil.Database(t)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.SQL)
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewJSONHandler(testLog{t}, nil)), Ready: db.SQL.PingContext, MaxUpload: 32, PublicURL: &url.URL{Scheme: "https", Host: "docs.example.com"}}
	h := a.Handler()
	ctx := t.Context()
	for _, name := range []string{"One", "Two"} {
		if _, err = s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("GET", "http://papergo:8080/v1/workspaces?$top=1", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, r)
	var body struct {
		NextLink *string `json:"@odata.nextLink"`
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &body) != nil || body.NextLink == nil || !strings.HasPrefix(*body.NextLink, "https://docs.example.com/v1/workspaces?") {
		t.Fatal(res.Code, res.Body.String())
	}
	// Batch subrequests build their links the same way.
	r = httptest.NewRequest("POST", "http://papergo:8080/v1/$batch", strings.NewReader(`{"requests":[{"id":"a","method":"GET","url":"workspaces?$top=1"}]}`))
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	h.ServeHTTP(res, r)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"@odata.nextLink":"https://docs.example.com/v1/workspaces?`) {
		t.Fatal(res.Code, res.Body.String())
	}
}
