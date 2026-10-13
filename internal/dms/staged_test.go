package dms

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"papergo/ent/domainevent"
	"papergo/ent/stagedfile"
	"papergo/internal/model"
	"papergo/internal/storage"
)

func TestStagedFilesPromoteDiscardAndCollect(t *testing.T) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, false)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(data string) storage.Object {
		t.Helper()
		o, err := store.Put(testContext, bytes.NewReader([]byte(data)), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	original := put("%PDF-original")
	f, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"scan.pdf"}, PutFile{Blob: BlobInput{ObjectKey: original.Key, Filename: "scan.pdf", ContentType: "application/pdf", Size: original.Size, SHA256: original.SHA256, Languages: "eng"}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.CurrentFile(testContext, "alice", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	stage := func(run, data string, sources []model.StagedSource) (string, storage.Object) {
		t.Helper()
		o := put(data)
		staged, err := s.StageFile(testContext, "alice", StageInput{ItemID: f.ID, RunID: run, Sources: sources, ObjectKey: o.Key, Filename: "scan.pdf", ContentType: "application/pdf", Size: o.Size, SHA256: o.SHA256, Metrics: []byte(`{"pages":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		return staged.ID, o
	}
	sources := []model.StagedSource{{ItemID: f.ID, BlobID: current.ID}}
	smaller, smallerObject := stage("run-1", "%PDF-small", sources)
	discarded, discardedObject := stage("run-1", "%PDF-other", sources)

	// Pending files keep their bytes past the grace period.
	later := time.Now().UTC().Add(2 * ObjectGracePeriod)
	if err = s.CollectObjects(testContext, store, later); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Open(testContext, smallerObject.Key); err != nil {
		t.Fatalf("a pending staged file lost its bytes: %v", err)
	}
	if _, err = s.PromoteStagedFile(testContext, "alice", smaller, "run-2"); err == nil {
		t.Fatal("another run promoted the staged file")
	}
	b, err := s.PromoteStagedFile(testContext, "alice", smaller, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Version != 2 || b.Languages != "eng" || b.ObjectKey != smallerObject.Key {
		t.Fatalf("promoted file %+v", b)
	}
	if _, err = s.PromoteStagedFile(testContext, "alice", smaller, "run-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("promoting twice: %v", err)
	}
	e, err := s.Client.DomainEvent.Query().Where(domainevent.TypeEQ(EventFileAdded), domainevent.ResourceIDEQ(f.ID)).Order(domainevent.ByID()).All(testContext)
	if err != nil || len(e) != 2 || e[1].Data["source"] != FileWorkflow {
		t.Fatalf("file.added events %v, %v", e, err)
	}
	// The other candidate was made from the replaced file: stale.
	if _, err = s.PromoteStagedFile(testContext, "alice", discarded, "run-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a stale candidate was promoted: %v", err)
	}
	if err = s.DiscardStagedFile(testContext, "alice", discarded, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.DiscardStagedFile(testContext, "alice", discarded, "run-1"); err != nil {
		t.Fatalf("discarding twice: %v", err)
	}
	if err = s.CollectObjects(testContext, store, later); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Open(testContext, discardedObject.Key); err == nil {
		t.Fatal("a discarded staged file kept its bytes")
	}
	if _, err = store.Open(testContext, smallerObject.Key); err != nil {
		t.Fatalf("the promoted file lost its bytes: %v", err)
	}
	// Staging from a file that is no longer current is refused.
	if _, err = s.StageFile(testContext, "alice", StageInput{ItemID: f.ID, RunID: "run-3", Sources: sources, ObjectKey: "k", Filename: "x.pdf", ContentType: "application/pdf", Size: 1, SHA256: hash('a')}); !errors.Is(err, ErrConflict) {
		t.Fatalf("staged from an old file: %v", err)
	}
	// Finished runs discard their pending files.
	b2, _ := s.CurrentFile(testContext, "alice", f.ID)
	pending, _ := stage("run-4", "%PDF-pending", []model.StagedSource{{ItemID: f.ID, BlobID: b2.ID}})
	if err = s.Write(testContext, func(t *Service) error { return t.DiscardRunStagedFiles(testContext, "run-4") }); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Client.StagedFile.Get(testContext, pending); got.State != stagedfile.StateDiscarded {
		t.Fatalf("pending file of a finished run is %s", got.State)
	}
	// Someone without write access can neither stage nor promote.
	if _, err = s.StageFile(testContext, "mallory", StageInput{ItemID: f.ID, RunID: "run-5", Sources: []model.StagedSource{{ItemID: f.ID, BlobID: b2.ID}}, ObjectKey: "k", Filename: "x.pdf", ContentType: "application/pdf", Size: 1, SHA256: hash('a')}); err == nil {
		t.Fatal("a stranger staged a file")
	}
}
