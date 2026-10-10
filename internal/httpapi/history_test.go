package httpapi

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"testing"

	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
)

func TestNumberedRevisionReadAndRestoreAPI(t *testing.T) {
	h, s, _ := setup(t)
	w, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(t.Context(), "alice", w.ID, dms.CreateResource{Versioning: historyMode("major"), Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(t.Context(), "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	name := "Changed"
	item, err = s.Update(t.Context(), "alice", item.ID, item.Version, dms.UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/items/" + item.ID + "/revisions"
	response := request(h, "GET", path+"/1", "", testToken, "", "")
	var revision dms.RevisionView
	if err = json.Unmarshal(response.Body.Bytes(), &revision); err != nil || response.Code != 200 || revision.Name != "Original" || revision.IsCurrent || len(revision.ChangedFields) == 0 {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	if response = request(h, "POST", path+"/1/restore", "", testToken, "", ""); response.Code != 428 {
		t.Fatal(response.Code)
	}
	if response = request(h, "POST", path+"/1/restore", "", testToken, `"1"`, ""); response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "POST", path+"/1/restore", "", testToken, `"2"`, "")
	var restored ent.Resource
	if err = json.Unmarshal(response.Body.Bytes(), &restored); err != nil || response.Code != 200 || restored.Name != "Original" || restored.Version != 3 || response.Header().Get("ETag") != `"3"` {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	if response = request(h, "GET", path+"/4", "", testToken, "", ""); response.Code != 404 {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestOrdinaryReaderHistoryAPI(t *testing.T) {
	_, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "History"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Versioning: historyMode("major"), Kind: "list", Name: "Records", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, dms.WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "Published"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	item, err = s.Get(ctx, "alice", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	name := "Unpublished"
	item, err = s.Update(ctx, "alice", item.ID, item.Version, dms.UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	h := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "reader"}, Logger: slog.Default()}).Handler()
	path := "/v1/items/" + item.ID + "/revisions"
	res := request(h, "GET", path+"?after=1&limit=1", "", testToken, "", "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", path+"/2", "", testToken, "", "")
	var revision dms.RevisionView
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &revision) != nil || revision.Name != name || !revision.IsCurrent || revision.Edges.SchemaRevision == nil {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/resources/"+item.ID+"?surface=head", "", testToken, "", "")
	if res.Code != 403 {
		t.Fatal("head authorization", res.Code, res.Body.String())
	}
	res = request(h, "POST", path+"/1/restore", "", testToken, strconv.Quote(strconv.Itoa(item.Version)), "")
	if res.Code != 403 {
		t.Fatal("restore authorization", res.Code, res.Body.String())
	}
	if err = s.RemoveWorkspaceMember(ctx, "alice", w.ID, "reader"); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{path, path + "/1"} {
		res = request(h, "GET", endpoint, "", testToken, "", "")
		if res.Code != 403 {
			t.Fatal("revoked history access", res.Code, res.Body.String())
		}
	}
}
