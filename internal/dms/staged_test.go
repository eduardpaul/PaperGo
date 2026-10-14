package dms

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"papergo/ent"
	"papergo/ent/domainevent"
	"papergo/ent/stagedfile"
	"papergo/internal/model"
	"papergo/internal/storage"
)

func TestPromoteAndRecycleSourcesIsAtomic(t *testing.T) {
	for _, scenario := range []string{"promote", "changed source", "cannot recycle source"} {
		t.Run(scenario, func(t *testing.T) {
			s, w, _ := fixture(t)
			lib := davLibrary(t, s, w, false)
			if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
				t.Fatal(err)
			}
			var items []*File
			var sources []model.StagedSource
			for _, name := range []string{"primary.pdf", "first.pdf", "second.pdf"} {
				item, _, err := s.PutFile(testContext, "alice", lib.ID, []string{name}, PutFile{Blob: content(name)})
				if err != nil {
					t.Fatal(err)
				}
				b, err := s.CurrentFile(testContext, "alice", item.ID)
				if err != nil {
					t.Fatal(err)
				}
				items = append(items, item)
				sources = append(sources, model.StagedSource{ItemID: item.ID, BlobID: b.ID})
			}
			f, err := s.StageFile(testContext, "bob", StageInput{ItemID: items[0].ID, RunID: "conversion", Sources: sources,
				ObjectKey: "combined", Filename: "combined.pdf", ContentType: "application/pdf", Size: 12, SHA256: hash('a')})
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "changed source":
				if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"second.pdf"}, PutFile{Blob: content("second.pdf")}); err != nil {
					t.Fatal(err)
				}
			case "cannot recycle source":
				r, err := s.Client.Resource.Get(testContext, items[2].ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.SetPermissions(testContext, "alice", r.ID, r.Version, Permissions{Grants: []Permission{
					{"user", "alice", "manage", "allow"}, {"user", "bob", "read_draft", "allow"},
				}}); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.PromoteStagedFile(testContext, "bob", f.ID, "conversion", true)
			if scenario == "promote" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				want := ErrConflict
				if scenario == "cannot recycle source" {
					want = ErrForbidden
				}
				if !errors.Is(err, want) {
					t.Fatalf("promotion: %v, want %v", err, want)
				}
			}
			current, err := s.CurrentFile(testContext, "alice", items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "promote" {
				if current.ObjectKey != f.ObjectKey || current.Version != 2 {
					t.Fatalf("promoted file %+v", current)
				}
			} else if current.ID != sources[0].BlobID {
				t.Fatal("failed cleanup replaced the primary file")
			}
			for _, item := range items[1:] {
				r, err := s.Client.Resource.Get(testContext, item.ID)
				if err != nil {
					t.Fatal(err)
				}
				if (r.DeletedAt != nil) != (scenario == "promote") {
					t.Fatalf("source %s deleted at %v in %s", item.ID, r.DeletedAt, scenario)
				}
			}
			f, err = s.Client.StagedFile.Get(testContext, f.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantState := stagedfile.StatePending
			if scenario == "promote" {
				wantState = stagedfile.StatePromoted
			}
			if f.State != wantState {
				t.Fatalf("staged file is %s, want %s", f.State, wantState)
			}
			// A failed cleanup must roll back the file.added event too.
			events, err := s.Client.DomainEvent.Query().Where(domainevent.TypeEQ(EventFileAdded), domainevent.ResourceIDEQ(items[0].ID)).Order(ent.Asc(domainevent.FieldID)).All(testContext)
			if err != nil {
				t.Fatal(err)
			}
			wantEvents := 1
			if scenario == "promote" {
				wantEvents = 2
			}
			if len(events) != wantEvents {
				t.Fatalf("file.added events: %d, want %d", len(events), wantEvents)
			}
		})
	}
}

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
	if _, err = s.PromoteStagedFile(testContext, "alice", smaller, "run-2", false); err == nil {
		t.Fatal("another run promoted the staged file")
	}
	b, err := s.PromoteStagedFile(testContext, "alice", smaller, "run-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if b.Version != 2 || b.Languages != "eng" || b.ObjectKey != smallerObject.Key {
		t.Fatalf("promoted file %+v", b)
	}
	if _, err = s.PromoteStagedFile(testContext, "alice", smaller, "run-1", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("promoting twice: %v", err)
	}
	e, err := s.Client.DomainEvent.Query().Where(domainevent.TypeEQ(EventFileAdded), domainevent.ResourceIDEQ(f.ID)).Order(domainevent.ByID()).All(testContext)
	if err != nil || len(e) != 2 || e[1].Data["source"] != FileWorkflow {
		t.Fatalf("file.added events %v, %v", e, err)
	}
	// The other candidate was made from the replaced file: stale.
	if _, err = s.PromoteStagedFile(testContext, "alice", discarded, "run-1", false); !errors.Is(err, ErrConflict) {
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
