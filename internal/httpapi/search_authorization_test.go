package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/search"
)

func TestSearchDoesNotExposeUnpublishedContent(t *testing.T) {
	_, s, _ := setup(t)
	ctx := context.Background()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "List", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Secret report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	indexSearch(t, s)
	item, err = s.Get(ctx, "alice", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Unpublish(ctx, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetSurface(ctx, "bob", item.ID, "auto"); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("unpublished item: %v", err)
	}
	for _, surface := range []string{"auto", "published"} {
		out, err := s.Search(ctx, "bob", dms.SearchRequest{Q: "secret", Surface: surface, Facets: true})
		if err != nil || len(out.Hits) != 0 || out.Facets != nil || !out.TotalEstimated {
			t.Fatalf("unpublished %s: %+v %v", surface, out, err)
		}
	}
	// Draft readers retain access to the head while the index catches up.
	out, err := s.Search(ctx, "alice", dms.SearchRequest{Q: "secret"})
	if err != nil || len(out.Hits) != 1 || out.Hits[0].Surface != "head" {
		t.Fatalf("draft: %+v %v", out, err)
	}
}

func TestSearchFacetsVerifySourcesOutsideTheHitPage(t *testing.T) {
	_, s, _ := setup(t)
	ctx := context.Background()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "List"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Report", Tags: []string{"secret-merger"}})
	if err != nil {
		t.Fatal(err)
	}
	public, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Report public"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	indexSearch(t, s)
	if _, err = s.SetPermissions(ctx, "alice", private.ID, private.Version, dms.Permissions{Grants: []dms.Permission{{PrincipalType: "workspace_owner", PrincipalID: ws.ID, Action: "manage", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	// Filter-only results sort newest first: the stale source is off this page.
	out, err := s.Search(ctx, "bob", dms.SearchRequest{CollectionID: list.ID, Limit: 1, Facets: true})
	if err != nil || len(out.Hits) != 1 || out.Hits[0].ID != public.ID || out.Facets != nil || !out.TotalEstimated {
		t.Fatalf("unsafe facets: %+v %v", out, err)
	}
	indexSearch(t, s)
	out, err = s.Search(ctx, "bob", dms.SearchRequest{CollectionID: list.ID, Limit: 1, Facets: true})
	if err != nil || out.Facets == nil || len(out.Facets.Tag) != 0 || out.Total != 1 || out.TotalEstimated {
		t.Fatalf("settled facets: %+v %v", out, err)
	}
}

func TestSearchFileContentRequiresDocumentRead(t *testing.T) {
	_, s, _ := setup(t)
	ctx := context.Background()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "library", Name: "Library"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "File"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AttachBlob(ctx, "alice", item.ID, item.Version, dms.BlobInput{ObjectKey: "file.pdf", Filename: "file.pdf", ContentType: "application/pdf", Size: 1, SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	indexSearch(t, s)
	store := s.SearchStore()
	if err = store.StageContent(ctx, item.ID, b.ID, "generation", []search.Chunk{{Text: "Highly confidential file merger", Page: 1}}); err != nil {
		t.Fatal(err)
	}
	if err = store.PublishContent(ctx, item.ID, b.ID, "generation"); err != nil {
		t.Fatal(err)
	}
	limited := identity.WithRESTToken(ctx, identity.RESTTokenIdentity{Scopes: []string{"list.read"}})
	if _, err = s.GetBlob(limited, "alice", item.ID, ""); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("download: %v", err)
	}
	for _, tc := range []struct {
		q    string
		want int
	}{{"confidential", 0}, {"file confidential", 0}, {"file", 1}, {"file -confidential", 1}} {
		out, err := s.Search(limited, "alice", dms.SearchRequest{Q: tc.q, Facets: true})
		if err != nil || len(out.Hits) != tc.want || out.Total != tc.want {
			t.Fatalf("limited %q: %+v %v", tc.q, out, err)
		}
		for _, hit := range out.Hits {
			if hit.Page != 0 || strings.Contains(hit.Snippet, "confidential") {
				t.Fatalf("file snippet: %+v", hit)
			}
		}
	}
	for _, text := range []string{"confidential", "file"} {
		want := 0
		if text == "file" {
			want = 1
		}
		browse, err := s.Browse(limited, "alice", dms.Browse{ParentID: lib.ID, Search: text})
		if err != nil || len(browse.Data) != want {
			t.Fatalf("browse %s: %+v %v", text, browse, err)
		}
		query, err := s.Query(limited, "alice", lib.ID, dms.QueryRequest{Query: dms.QuerySpec{Search: text}})
		if err != nil || len(query.Data) != want {
			t.Fatalf("query %s: %+v %v", text, query, err)
		}
	}
	out, err := s.Search(ctx, "alice", dms.SearchRequest{Q: "confidential"})
	if err != nil || len(out.Hits) != 1 || out.Hits[0].Page != 1 || !strings.Contains(out.Hits[0].Snippet, "confidential") {
		t.Fatalf("authorized file text: %+v %v", out, err)
	}
}
