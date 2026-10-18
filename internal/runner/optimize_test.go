package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"papergo/ent"
	"papergo/internal/dms"
	"papergo/internal/imaging"
	"papergo/internal/ocr"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
)

// textEngine is an OCR engine that finds one 24-pixel text line in the
// middle of every image, so the optimization halves it.
type textEngine struct{}

func (textEngine) Detect(_ context.Context, img image.Image, _ ocr.DetectOptions) (ocr.Detection, error) {
	b := img.Bounds()
	x, y := b.Dx()/4, b.Dy()/2
	q := ocr.Quad{{x, y}, {3 * x, y}, {3 * x, y + 24}, {x, y + 24}}
	return ocr.Detection{Regions: []ocr.Region{{Quad: q, Score: 0.9}}, Width: b.Dx(), Height: b.Dy()}, nil
}

func (textEngine) Recognize(_ context.Context, img image.Image) (ocr.Page, error) {
	b := img.Bounds()
	return ocr.Page{Width: b.Dx(), Height: b.Dy(), Lines: []ocr.Line{{Text: "Total 21.30", Confidence: 0.9, Quad: ocr.Quad{{10, 10}, {200, 10}, {200, 40}, {10, 40}}}}}, nil
}

func (textEngine) Info() ocr.Info { return ocr.Info{Engine: "test"} }
func (textEngine) Close() error   { return nil }

// scan is a w×h PNG that compresses badly, like a photographed page.
func scan(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(w)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// upload stores data and makes it the file at name in the library.
func (f *fixture) upload(t *testing.T, libraryID, name, contentType string, data []byte) *dms.File {
	t.Helper()
	o, err := f.store.Put(ctx, bytes.NewReader(data), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	file, _, err := f.dms.PutFile(ctx, "alice", libraryID, []string{name}, dms.PutFile{Blob: dms.BlobInput{ObjectKey: o.Key, Filename: name, ContentType: contentType, Size: o.Size, SHA256: o.SHA256}})
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// approvalOf waits for the run to wait and returns its open approval.
func (f *fixture) approvalOf(t *testing.T, runID string) tasks.View {
	t.Helper()
	f.status(t, runID, "waiting")
	all, err := pageData(f.tasks().Workspace(ctx, "alice", f.ws.ID, tasks.Filter{Status: "open"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range all {
		if a.RunID != nil && *a.RunID == runID {
			return a
		}
	}
	t.Fatalf("run %s has no open approval: %+v", runID, all)
	return tasks.View{}
}

func (f *fixture) current(t *testing.T, itemID string) *ent.Blob {
	t.Helper()
	b, err := f.dms.CurrentFile(ctx, "alice", itemID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOptimizeImageBuiltIn(t *testing.T) {
	f := setup(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Scans", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "documents.optimize", workflow.SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// An upload proposes a smaller copy and waits for the owners.
	file := f.upload(t, lib.ID, "receipt.png", "image/png", scan(t, 1200, 800))
	runs := f.runsOf(t, w.ID, 1)
	a := f.approvalOf(t, runs[0])
	if a.Title != "Review a smaller file for receipt.png" || len(a.Assignees) != 1 || a.Assignees[0] != "alice" || a.Review["type"] != tasks.ReviewCompare {
		t.Fatalf("approval %+v", a)
	}
	staged, err := f.dms.StagedFile(ctx, "alice", a.Review["staged_file_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(staged.Metrics, &m); err != nil {
		t.Fatal(err)
	}
	if staged.ContentType != "image/webp" || staged.Filename != "receipt.webp" || m["scale"] != 0.5 || m["smallest_text_height"] != 24.0 || m["width"] != 600.0 || m["detector"] != "test" {
		t.Fatalf("staged %s %s, metrics %v", staged.Filename, staged.ContentType, m)
	}
	if _, err = f.tasks().Respond(ctx, "alice", a.ID, tasks.Response{Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	run := f.status(t, runs[0], "completed")
	b := f.current(t, file.ID)
	if b.Version != 2 || b.ContentType != "image/webp" || b.Filename != "receipt.webp" {
		t.Fatalf("current file %+v; run %+v", b, run)
	}
	rc, err := f.store.Open(ctx, b.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	img, err := imaging.Decode(data, 1e8)
	if err != nil || img.Bounds().Dx() != 600 || img.Bounds().Dy() != 400 {
		t.Fatalf("promoted image %v, %v", img.Bounds(), err)
	}
	// The promoted file is a workflow file: it starts no run.
	f.runsOf(t, w.ID, 1)

	// A newer upload replaces the waiting run, which closes its review.
	second := f.upload(t, lib.ID, "invoice.png", "image/png", scan(t, 800, 600))
	first := f.runsOf(t, w.ID, 2)[0]
	f.approvalOf(t, first)
	f.upload(t, lib.ID, "invoice.png", "image/png", scan(t, 900, 600))
	all := f.runsOf(t, w.ID, 3)
	f.status(t, first, "cancelled")
	closed, err := f.tasks().Get(ctx, "alice", f.approvalFor(t, first).ID)
	if err != nil || closed.Status != "cancelled" {
		t.Fatalf("replaced run's approval: %+v %v", closed, err)
	}
	// Rejecting keeps the file and discards the copy.
	a = f.approvalOf(t, all[0])
	if _, err = f.tasks().Respond(ctx, "alice", a.ID, tasks.Response{Outcome: "rejected", Comment: "not now"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, all[0], "completed")
	if b = f.current(t, second.ID); b.Version != 2 || b.ContentType != "image/png" {
		t.Fatalf("rejected candidate replaced the file: %+v", b)
	}
	if s, _ := f.dms.StagedFile(ctx, "alice", a.Review["staged_file_id"].(string)); s == nil || s.State != "discarded" {
		t.Fatalf("rejected candidate %+v", s)
	}

	// Files it cannot optimize skip without asking anyone.
	f.upload(t, lib.ID, "contract.pdf", "application/pdf", []byte("%PDF-1.7 test"))
	pdfRun := f.runsOf(t, w.ID, 4)[0]
	done := f.status(t, pdfRun, "completed")
	if done.Outcome != "unchanged" {
		t.Fatalf("unsupported automatic input should report no changes: %+v", done)
	}
	if open, _ := pageData(f.tasks().Workspace(ctx, "alice", f.ws.ID, tasks.Filter{Status: "open"})); len(open) != 0 {
		t.Fatalf("a PDF asked for review: %+v (run %+v)", open, done)
	}
}

// runsOf waits until the workflow has want runs and returns their IDs,
// newest first.
func (f *fixture) runsOf(t *testing.T, workflowID string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		page, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{WorkflowID: workflowID, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(page.Data))
		for i, r := range page.Data {
			ids[i] = r.ID
		}
		if len(ids) == want {
			return ids
		}
		if len(ids) > want || time.Now().After(deadline) {
			t.Fatalf("workflow %s has %d runs, want %d: %+v", workflowID, len(ids), want, page.Data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// approvalFor returns the run's approval in any status.
func (f *fixture) approvalFor(t *testing.T, runID string) tasks.View {
	t.Helper()
	all, err := pageData(f.tasks().Workspace(ctx, "alice", f.ws.ID, tasks.Filter{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range all {
		if a.RunID != nil && *a.RunID == runID {
			return a
		}
	}
	t.Fatalf("run %s has no approval", runID)
	return tasks.View{}
}

func TestPhotosToPDFBuiltIn(t *testing.T) {
	f := setup(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Photos", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "documents.photos_to_pdf", workflow.SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	first := f.upload(t, lib.ID, "page-1.png", "image/png", scan(t, 400, 300))
	second := f.upload(t, lib.ID, "page-2.png", "image/png", scan(t, 300, 400))
	third := f.upload(t, lib.ID, "page-3.png", "image/png", scan(t, 200, 200))
	start := func(primary string, ids ...string) string {
		t.Helper()
		runs, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: ids, PrimaryItemID: primary})
		if err != nil || len(runs) != 1 {
			t.Fatalf("start: %v %v", runs, err)
		}
		// Dispatch can wait behind source-file processing. Complete it before
		// the bounded conversion and approval state assertions.
		if _, err = f.r.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
		return runs[0]
	}

	// Rejecting keeps every photo.
	run := start(second.ID, third.ID, second.ID, first.ID)
	a := f.approvalOf(t, run)
	if a.Review["type"] != tasks.ReviewPreview || a.ItemID == nil || *a.ItemID != second.ID {
		t.Fatalf("approval %+v", a)
	}
	staged, err := f.dms.StagedFile(ctx, "alice", a.Review["staged_file_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(staged.Metrics, &m)
	details, _ := m["page_details"].([]any)
	if staged.ContentType != "application/pdf" || staged.Filename != "page-2.pdf" || m["pages"] != 3.0 || len(details) != 3 || len(staged.Sources) != 3 {
		t.Fatalf("staged %+v, metrics %v", staged, m)
	}
	// Pages follow the run's order: the third photo first.
	if p := details[0].(map[string]any); p["source_width"] != 200.0 {
		t.Errorf("first page %v", p)
	}
	if _, err = f.tasks().Respond(ctx, "alice", a.ID, tasks.Response{Outcome: "rejected", Comment: "not now"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, run, "completed")
	if b := f.current(t, second.ID); b.ContentType != "image/png" || b.Version != 1 {
		t.Fatalf("rejected PDF replaced the file: %+v", b)
	}

	// While a conversion waits, another manual launch records a visible rejection.
	run = start(second.ID, third.ID, second.ID, first.ID)
	a = f.approvalOf(t, run)
	rejected, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{first.ID, second.ID}, PrimaryItemID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if result := f.wait(t, rejected[0], finished); result.Status != "failed" || !strings.Contains(result.Error, "another run is active") {
		t.Fatal("overlapping launch lost its inspectable rejection", result)
	}
	f.runsOf(t, w.ID, 3)

	// Approving gives the primary the PDF and recycles the other photos;
	// those deletions do not cancel the run that made them.
	if _, err = f.tasks().Respond(ctx, "alice", a.ID, tasks.Response{Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, run, "completed")
	if b := f.current(t, second.ID); b.ContentType != "application/pdf" || b.Filename != "page-2.pdf" || b.Version != 2 {
		t.Fatalf("primary file %+v", b)
	}
	for _, id := range []string{first.ID, third.ID} {
		if _, err = f.dms.GetSurface(ctx, "alice", id, "head"); err == nil {
			t.Errorf("photo %s was not recycled", id)
		}
	}
}

func TestPhotosToPDFRefusesChangedAndUnsupportedPhotos(t *testing.T) {
	f := setup(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Photos", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "documents.photos_to_pdf", workflow.SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	photo := f.upload(t, lib.ID, "page-1.png", "image/png", scan(t, 400, 300))
	other := f.upload(t, lib.ID, "page-2.png", "image/png", scan(t, 300, 400))
	pdf := f.upload(t, lib.ID, "contract.pdf", "application/pdf", []byte("%PDF-1.7 test"))

	_, err = f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{photo.ID, pdf.ID}, PrimaryItemID: photo.ID})
	if err == nil || !strings.Contains(err.Error(), "Requires a JPEG, PNG or WebP") {
		t.Fatalf("unsupported selections must be rejected before queueing: %v", err)
	}

	// A photo replaced while the review waits cannot be promoted.
	runs, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{photo.ID, other.ID}, PrimaryItemID: photo.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	a := f.approvalOf(t, runs[0])
	f.upload(t, lib.ID, "page-2.png", "image/png", scan(t, 310, 400))
	if _, err = f.tasks().Respond(ctx, "alice", a.ID, tasks.Response{Outcome: "approved"}); err == nil {
		t.Fatal("a review of a changed photo was approved")
	}
	if _, err = f.tasks().Respond(ctx, "alice", a.ID, tasks.Response{Outcome: "rejected", Comment: "not now"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, runs[0], "completed")
	if _, err = f.dms.GetSurface(ctx, "alice", other.ID, "head"); err != nil {
		t.Fatalf("the changed photo was deleted: %v", err)
	}
}

func TestOptimizeImageCanRunManuallyWithAutomationOff(t *testing.T) {
	f := setup(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Manual photos", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "documents.optimize", workflow.SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: false, ManualEnabled: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	photo := f.upload(t, lib.ID, "photo.png", "image/png", scan(t, 800, 600))
	f.runs(t, w.ID, 0)
	launch := workflow.Start{ItemIDs: []string{photo.ID}, WorkflowVersion: w.CurrentVersion}
	ready, err := f.wf.CheckStart(ctx, "alice", w.ID, launch, true)
	if err != nil || !ready.CanRun {
		t.Fatal(ready, err)
	}
	f.wf.Tools.OCR = nil
	blocked, err := f.wf.CheckStart(ctx, "alice", w.ID, launch, true)
	if err != nil || blocked.CanRun || blocked.Code != "needs_setup" {
		t.Fatal(blocked, err)
	}
	f.wf.Tools.OCR = textEngine{}
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, launch)
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	approval := f.approvalOf(t, ids[0])
	if _, err = f.tasks().Respond(ctx, "alice", approval.ID, tasks.Response{Outcome: "rejected", Comment: "Keep the original"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "completed")
	current, err := f.wf.Get(ctx, "alice", w.ID)
	if err != nil || current.AutomaticEnabled || current.Version != w.Version {
		t.Fatal("manual invocation changed automatic settings", current, err)
	}
	if b := f.current(t, photo.ID); b.Version != 1 {
		t.Fatal("rejected proposal changed original", b)
	}
	pdf := f.upload(t, lib.ID, "unsupported.pdf", "application/pdf", []byte("pdf"))
	unsupported, err := f.wf.CheckStart(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{pdf.ID}}, true)
	if err != nil || unsupported.CanRun || unsupported.Code != "unsupported_file" {
		t.Fatal(unsupported, err)
	}
	f.runs(t, w.ID, 1)
}
