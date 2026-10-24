package dms

import (
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"testing"
)

// Each caller sees the file of the surface it reads: readers the published
// file, editors the draft, in single reads, listings, queries and summaries.
func TestFileSummaryFollowsTheVisibleSurface(t *testing.T) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, true)
	grantReader(t, s, w)
	first := pdfContent("first.pdf")
	put, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"contract.pdf"}, PutFile{Blob: first})
	if err != nil {
		t.Fatal(err)
	}
	publishItem(t, s, put.Resource)
	second := pdfContent("second.pdf")
	second.Size = 2
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"contract.pdf"}, PutFile{Blob: second}); err != nil {
		t.Fatal(err)
	}
	folder := create(t, s, lib.ID, "folder", "Empty", nil)

	want := map[string]resources.FileSummary{
		"reader": {ContentType: "application/pdf", Filename: "first.pdf", Size: 1, Version: 1},
		"alice":  {ContentType: "application/pdf", Filename: "second.pdf", Size: 2, Version: 2},
	}
	check := func(subject, where string, file *resources.FileSummary) {
		t.Helper()
		expected := want[subject]
		if file == nil || file.BlobID == "" {
			t.Fatalf("%s %s: no file", subject, where)
		}
		got := *file
		got.BlobID = ""
		if got != expected {
			t.Fatalf("%s %s: file %+v, want %+v", subject, where, got, expected)
		}
	}
	for subject := range want {
		item, err := s.Get(testContext, subject, put.ID)
		if err != nil {
			t.Fatal(err)
		}
		check(subject, "get", item.File)
		page, err := s.Browse(testContext, subject, Browse{ParentID: lib.ID})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Data {
			switch r.ID {
			case put.ID:
				check(subject, "browse", r.File)
			case folder.ID:
				if r.File != nil {
					t.Fatalf("folder has a file: %+v", r.File)
				}
			}
		}
		result, err := s.Query(testContext, subject, lib.ID, QueryRequest{})
		if err != nil || len(result.Data) == 0 {
			t.Fatal(result, err)
		}
		for _, r := range result.Data {
			if r.ID == put.ID {
				check(subject, "query", r.File)
			}
		}
	}
	surfaces := map[string]refs.Surface{put.ID: refs.SurfacePublished, folder.ID: refs.SurfaceHead}
	found, err := s.FileSummaries(testContext, surfaces)
	if err != nil || len(found) != 1 {
		t.Fatal(found, err)
	}
	check("reader", "published summary", found[put.ID])
	found, err = s.FileSummaries(testContext, map[string]refs.Surface{put.ID: refs.SurfaceHead})
	if err != nil {
		t.Fatal(err)
	}
	check("alice", "head summary", found[put.ID])
}

// A change returns the file its writer sees, the head's.
func TestUpdateReturnsTheHeadFile(t *testing.T) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, true)
	put, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"a.pdf"}, PutFile{Blob: pdfContent("a.pdf")})
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	out, err := s.Update(testContext, "alice", put.ID, latest(t, s, put.ID).Version, UpdateResource{Name: &name})
	if err != nil || out.File == nil || out.File.Version != 1 || out.File.ContentType != "application/pdf" {
		t.Fatalf("%+v %v", out, err)
	}
}
