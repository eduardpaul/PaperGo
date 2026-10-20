package dms

import (
	"bytes"
	"errors"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/daltest"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
	"testing"
	"time"

	eventstore "papergo/internal/dal/events"
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
			var sources []model.ArtifactSource
			for _, name := range []string{"primary.pdf", "first.pdf", "second.pdf"} {
				item, _, err := s.PutFile(testContext, "alice", lib.ID, []string{name}, PutFile{Blob: pdfContent(name)})
				if err != nil {
					t.Fatal(err)
				}
				b, err := s.CurrentFile(testContext, "alice", item.ID)
				if err != nil {
					t.Fatal(err)
				}
				items = append(items, item)
				sources = append(sources, model.ArtifactSource{ItemID: item.ID, BlobID: b.ID})
			}
			stagingRuns(t, s, w.ID, "conversion")
			f := stageArtifact(t, s, "bob", "conversion", ArtifactInput{ItemID: items[0].ID, Sources: sources,
				ObjectKey: "combined", Filename: "combined.pdf", ContentType: "application/pdf", Size: 12, SHA256: hash('a')})
			switch scenario {
			case "changed source":
				if _, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"second.pdf"}, PutFile{Blob: pdfContent("second.pdf")}); err != nil {
					t.Fatal(err)
				}
			case "cannot recycle source":
				r, err := s.DAL.Resources().Resource(testContext, items[2].ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.SetPermissions(testContext, "alice", r.ID, r.Version, Permissions{Grants: []Permission{
					{"user", "alice", "manage", "allow"}, {"user", "bob", "read_draft", "allow"},
				}}); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.PromoteStagedFile(testContext, "bob", f.ID, "conversion", true)
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
				if current.ObjectKey != f.Artifact.ObjectKey || current.Version != 2 {
					t.Fatalf("promoted file %+v", current)
				}
			} else if current.ID != sources[0].BlobID {
				t.Fatal("failed cleanup replaced the primary file")
			}
			for _, item := range items[1:] {
				r, err := s.DAL.Resources().Resource(testContext, item.ID)
				if err != nil {
					t.Fatal(err)
				}
				if (r.DeletedAt != nil) != (scenario == "promote") {
					t.Fatalf("source %s deleted at %v in %s", item.ID, r.DeletedAt, scenario)
				}
			}
			stored, err := s.DAL.Blobs().StagedFile(testContext, f.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantState := blobs.StagedPending
			if scenario == "promote" {
				wantState = blobs.StagedPromoted
			}
			if stored.State != wantState {
				t.Fatalf("staged file is %s, want %s", stored.State, wantState)
			}
			// A failed cleanup must roll back the file change event too.
			events, err := s.DAL.Events().List(testContext, eventstore.Filter{Types: []string{EventFileAdded, EventFileUpdated}, ResourceID: items[0].ID})
			if err != nil {
				t.Fatal(err)
			}
			wantEvents := 1
			if scenario == "promote" {
				wantEvents = 2
			}
			if len(events) != wantEvents {
				t.Fatalf("file change events: %d, want %d", len(events), wantEvents)
			}
		})
	}
}

func TestStagedFilesPromoteDiscardAndCollect(t *testing.T) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, false)
	stagingRuns(t, s, w.ID, "run-1", "run-3", "run-4", "run-5")
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
	stage := func(run, data string, sources []model.ArtifactSource) (string, storage.Object) {
		t.Helper()
		o := put(data)
		staged := stageArtifact(t, s, "alice", run, ArtifactInput{ItemID: f.ID, Sources: sources, ObjectKey: o.Key, Filename: "scan.pdf", ContentType: "application/pdf", Size: o.Size, SHA256: o.SHA256, Metrics: []byte(`{"pages":1}`)})
		return staged.ID, o
	}
	sources := []model.ArtifactSource{{ItemID: f.ID, BlobID: current.ID}}
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
	// A run's staged file answers only that run.
	for _, run := range []string{"run-2", ""} {
		if _, err = s.PromoteStagedFile(testContext, "alice", smaller, run, false); err == nil {
			t.Fatalf("%q promoted another run's staged file", run)
		}
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
	e, err := s.DAL.Events().List(testContext, eventstore.Filter{Types: []string{EventFileAdded, EventFileUpdated}, ResourceID: f.ID})
	if err != nil || len(e) != 2 || e[1].Data["source"] != FileWorkflow {
		t.Fatalf("file change events %v, %v", e, err)
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
	// A discarded file's artifact keeps its bytes for reuse until it
	// expires.
	if err = s.CollectObjects(testContext, store, later); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Open(testContext, discardedObject.Key); err != nil {
		t.Fatalf("an unexpired artifact lost its bytes: %v", err)
	}
	expired := time.Now().UTC().Add(ArtifactRetention + time.Hour)
	if err = s.CollectArtifacts(testContext, expired); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectObjects(testContext, store, expired); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Open(testContext, discardedObject.Key); err == nil {
		t.Fatal("an expired artifact kept its bytes")
	}
	if _, err = store.Open(testContext, smallerObject.Key); err != nil {
		t.Fatalf("the promoted file lost its bytes: %v", err)
	}
	// An artifact made from a file that is no longer current is refused.
	if _, err = s.AddArtifact(testContext, "alice", ArtifactInput{ItemID: f.ID, InputKey: hash('k'), Sources: sources, ObjectKey: "k", Filename: "x.pdf", ContentType: "application/pdf", Size: 1, SHA256: hash('a')}); !errors.Is(err, ErrConflict) {
		t.Fatalf("an artifact from an old file: %v", err)
	}
	// Finished runs discard their pending files.
	b2, _ := s.CurrentFile(testContext, "alice", f.ID)
	pending, _ := stage("run-4", "%PDF-pending", []model.ArtifactSource{{ItemID: f.ID, BlobID: b2.ID}})
	if err = s.Write(testContext, func(t *Service) error {
		return t.DiscardStagedFiles(testContext, blobs.StagedSelection{RunIDs: []string{"run-4"}})
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.DAL.Blobs().StagedFile(testContext, pending); got.State != blobs.StagedDiscarded {
		t.Fatalf("pending file of a finished run is %s", got.State)
	}
	// Without a run, anyone with write access stages and promotes.
	own := stageArtifact(t, s, "alice", "", ArtifactInput{ItemID: f.ID, Sources: []model.ArtifactSource{{ItemID: f.ID, BlobID: b2.ID}}, ObjectKey: put("%PDF-own").Key, Filename: "own.pdf", ContentType: "application/pdf", Size: 8, SHA256: hash('o')})
	if _, err = s.PromoteStagedFile(testContext, "alice", own.ID, "run-5", false); err == nil {
		t.Fatal("a run promoted someone's staged file")
	}
	if _, err = s.PromoteStagedFile(testContext, "alice", own.ID, "", false); err != nil {
		t.Fatal(err)
	}
	// Someone without write access can neither make nor stage an artifact.
	if _, err = s.AddArtifact(testContext, "mallory", ArtifactInput{ItemID: f.ID, InputKey: hash('k'), Sources: []model.ArtifactSource{{ItemID: f.ID, BlobID: b2.ID}}, ObjectKey: "k", Filename: "x.pdf", ContentType: "application/pdf", Size: 1, SHA256: hash('a')}); err == nil {
		t.Fatal("a stranger made an artifact")
	}
	if _, err = s.StageArtifact(testContext, "mallory", own.ArtifactID, ""); err == nil {
		t.Fatal("a stranger staged an artifact")
	}
}

// stageArtifact records an artifact as subject and stages it for run.
func stageArtifact(t *testing.T, s *Service, subject, run string, in ArtifactInput) *Staged {
	t.Helper()
	if in.InputKey == "" {
		in.InputKey = hash('i')
	}
	a, err := s.AddArtifact(testContext, subject, in)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.StageArtifact(testContext, subject, a.ID, run)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// stagingRuns records runs of a new workflow, which staged files belong to.
func stagingRuns(t *testing.T, s *Service, workspaceID string, ids ...string) {
	t.Helper()
	wf, err := s.DAL.Workflows().Create(testContext, workflows.CreateInput{WorkspaceID: workspaceID, Key: "staging", Name: "Staging", Content: daltest.Script, CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err = daltest.Run(testContext, s.DAL, runstore.CreateInput{ID: id, WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: workspaceID, EventID: id, EventType: "manual", Actor: "alice"}); err != nil {
			t.Fatal(err)
		}
	}
}
