package webdav_test

import (
	"papergo/internal/dms"
	"testing"
)

func TestCopyPreservesContentTypeAndPrunesRemovedFields(t *testing.T) {
	e := setup(t)
	lib, err := e.s.Get(ctx, "alice", e.library)
	if err != nil {
		t.Fatal(err)
	}
	typ, err := e.s.CreateContentType(ctx, "alice", lib.ID, lib.Version, dms.ContentTypeInput{Key: "document", Name: "Document"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.s.CreateField(ctx, "alice", lib.ID, dms.CreateField{ContentTypeID: typ.ID, Key: "caption", Label: "Caption", Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := e.s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "original.txt", ContentTypeID: typ.ID, Values: map[string]any{"caption": "Archived"}})
	if err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("PUT", e.root+"/original.txt", "alice-token", "data"), 204)
	lib, err = e.s.Get(ctx, "alice", e.library)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.s.DeleteField(ctx, "alice", lib.ID, d.ID, lib.Version); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("COPY", e.root+"/original.txt", "alice-token", "", "Destination", e.root+"/copy.txt"), 201)
	copy, err := e.s.LibraryFile(ctx, "alice", lib.ID, []string{"copy.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if copy.Resource.ContentTypeID == nil || *copy.Resource.ContentTypeID != typ.ID || len(copy.Values) != 0 {
		t.Fatal("copy type/schema", copy)
	}
	original, err := e.s.Get(ctx, "alice", item.ID)
	if err != nil || original.Values["caption"] != "Archived" {
		t.Fatal("copy changed source", original, err)
	}
	e.expect(e.do("GET", e.root+"/copy.txt", "alice-token", ""), 200)
}
