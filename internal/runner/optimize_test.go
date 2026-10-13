package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"testing"
	"time"

	"papergo/ent"
	"papergo/internal/dms"
	"papergo/internal/imaging"
	"papergo/internal/ocr"
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
func (f *fixture) approvalOf(t *testing.T, runID string) workflow.Approval {
	t.Helper()
	f.status(t, runID, "waiting")
	all, err := f.wf.WorkspaceApprovals(ctx, "alice", f.ws.ID, workflow.ApprovalFilter{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range all {
		if a.RunID == runID {
			return a
		}
	}
	t.Fatalf("run %s has no open approval: %+v", runID, all)
	return workflow.Approval{}
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
	w, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "documents.optimize", workflow.SetBuiltIn{CollectionID: lib.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// An upload proposes a smaller copy and waits for the owners.
	file := f.upload(t, lib.ID, "receipt.png", "image/png", scan(t, 1200, 800))
	runs := f.runsOf(t, w.ID, 1)
	a := f.approvalOf(t, runs[0])
	if a.Title != "Review a smaller file for receipt.png" || len(a.Assignees) != 1 || a.Assignees[0] != "alice" || a.Review["type"] != workflow.ReviewCompare {
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
	if _, err = f.wf.Decide(ctx, "alice", a.ID, workflow.Decision{Decision: "approve"}); err != nil {
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
	closed, err := f.wf.Approval(ctx, "alice", f.approvalFor(t, first).ID)
	if err != nil || closed.Status != "cancelled" {
		t.Fatalf("replaced run's approval: %+v %v", closed, err)
	}
	// Rejecting keeps the file and discards the copy.
	a = f.approvalOf(t, all[0])
	if _, err = f.wf.Decide(ctx, "alice", a.ID, workflow.Decision{Decision: "reject"}); err != nil {
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
	if open, _ := f.wf.WorkspaceApprovals(ctx, "alice", f.ws.ID, workflow.ApprovalFilter{Status: "open"}); len(open) != 0 {
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
func (f *fixture) approvalFor(t *testing.T, runID string) workflow.Approval {
	t.Helper()
	all, err := f.wf.WorkspaceApprovals(ctx, "alice", f.ws.ID, workflow.ApprovalFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range all {
		if a.RunID == runID {
			return a
		}
	}
	t.Fatalf("run %s has no approval", runID)
	return workflow.Approval{}
}
