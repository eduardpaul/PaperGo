package dms

import (
	"encoding/json"
	"strconv"
	"testing"

	eventstore "papergo/internal/dal/events"
)

func TestFileChangeEventsNameTheirSource(t *testing.T) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, false)
	events := func(id string) []eventstore.Event {
		t.Helper()
		rows, err := s.DAL.Events().List(testContext, eventstore.Filter{Types: []string{EventFileAdded, EventFileUpdated}, ResourceID: id})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	// A new item with a file, through WebDAV.
	f, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"a.pdf"}, PutFile{Blob: BlobInput{ObjectKey: "k1", Filename: "a.pdf", ContentType: "application/pdf", Size: 1, SHA256: hash('1'), Source: FileWebDAV}})
	if err != nil {
		t.Fatal(err)
	}
	// A replacement upload, then a restore of the first version.
	r, err := s.Get(testContext, "alice", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachBlob(testContext, "alice", f.ID, r.Version, BlobInput{ObjectKey: "k2", Filename: "a.pdf", ContentType: "application/pdf", Size: 2, SHA256: hash('2')}); err != nil {
		t.Fatal(err)
	}
	if r, err = s.Get(testContext, "alice", f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreBlob(testContext, "alice", f.ID, 1, r.Version); err != nil {
		t.Fatal(err)
	}
	// Metadata edits change no file.
	if r, err = s.Get(testContext, "alice", f.ID); err != nil {
		t.Fatal(err)
	}
	name := "b.pdf"
	if _, err = s.Update(testContext, "alice", f.ID, r.Version, UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	got := events(f.ID)
	want := []struct {
		source  string
		version int
	}{{FileWebDAV, 1}, {FileUpload, 2}, {FileRestore, 3}}
	if len(got) != len(want) {
		t.Fatalf("%d file.added events, want %d", len(got), len(want))
	}
	for i, e := range got {
		wantType := EventFileUpdated
		if i == 0 {
			wantType = EventFileAdded
		} else if e.Data["previous_blob_id"] != got[i-1].Data["blob_id"] {
			t.Fatal("replacement did not identify the previous file", e.Data)
		}
		if e.Type != wantType || e.Data["filename"] != "a.pdf" {
			t.Fatal("wrong file change event", e)
		}
		if e.Data["source"] != want[i].source || e.Data["file_version"] != json.Number(strconv.Itoa(want[i].version)) || e.Data["revision_id"] == "" || e.Data["blob_id"] == "" || e.Data["content_type"] != "application/pdf" {
			t.Errorf("event %d data %v, want source %s version %d", i, e.Data, want[i].source, want[i].version)
		}
		if e.CollectionID != lib.ID || e.Actor != "alice" {
			t.Errorf("event %d raised for %v by %s", i, e.CollectionID, e.Actor)
		}
	}
}

func hash(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
