package httpapi

import (
	"encoding/json"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/identity"
)

func TestFileVersionsListRetainedFilesNewestFirst(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Documents", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Contract"})
	if err != nil {
		t.Fatal(err)
	}
	type versions struct {
		Value []dms.FileVersion `json:"value"`
		Next  *string           `json:"@odata.nextLink"`
	}
	list := func(token, query string) versions {
		t.Helper()
		r := request(h, "GET", "/v1/items/"+item.ID+"/content/versions"+query, "", token, "", "")
		var out versions
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
		return out
	}
	if got := list(testToken, ""); len(got.Value) != 0 {
		t.Fatal("item without a file listed versions", got)
	}
	endpoint := "/v1/items/" + item.ID + "/content"
	if r := request(h, "PUT", endpoint, "%PDF-first", testToken, `"1"`, "text/plain"); r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	if _, err = s.Publish(ctx, "alice", item.ID, 2); err != nil {
		t.Fatal(err)
	}
	if r := request(h, "PUT", endpoint, "%PDF-second", testToken, `"3"`, "text/plain"); r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := request(h, "POST", endpoint+"/versions/1/restore", "", testToken, `"4"`, ""); r.Code != 200 && r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	all := list(testToken, "")
	if len(all.Value) != 3 || all.Value[0].Version != 3 || all.Value[2].Version != 1 || !all.Value[0].IsCurrent || all.Value[1].IsCurrent {
		t.Fatal("draft reader versions", all.Value)
	}
	for _, v := range all.Value {
		if v.CreatedBy != "alice" || v.Size == 0 || len(v.SHA256) != 64 || v.ContentType != "application/pdf" {
			t.Fatal("file version metadata", v)
		}
	}
	if all.Value[0].SHA256 != all.Value[2].SHA256 {
		t.Fatal("restored version does not carry the first file")
	}
	page := list(testToken, "?$top=2")
	if len(page.Value) != 2 || page.Next == nil {
		t.Fatal("paging", page)
	}
	// Ordinary readers see only the published file.
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, dms.WorkspaceMember{Subject: "visitor", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	reader, err := s.Directory().CreateAccessToken(ctx, "visitor", identity.AccessTokenInput{Name: "Reader", Scopes: []string{"workspace.read", "list.read", "document.read"}})
	if err != nil {
		t.Fatal(err)
	}
	visible := list(reader.Secret, "")
	if len(visible.Value) != 1 || visible.Value[0].Version != 1 || !visible.Value[0].IsCurrent {
		t.Fatal("reader versions", visible.Value)
	}
}
