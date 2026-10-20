package scriptrun

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"papergo/internal/ai"
	"papergo/internal/ai/aitest"
	"papergo/internal/aiops"
	"papergo/internal/auth"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/resultcache"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/documentfiles"
	"papergo/internal/httpapi"
	"papergo/internal/ocr"
	"papergo/internal/storage"
	"papergo/internal/worker"
	"papergo/internal/workflow"
)

// receiptText finds one line of text in a receipt photo: 24 pixels high in
// a large photo, so the optimizer halves it, and 12 in a small one, which
// it keeps.
type receiptText struct{}

func (receiptText) Detect(_ context.Context, img image.Image, _ ocr.DetectOptions) (ocr.Detection, error) {
	b := img.Bounds()
	line := 24
	if b.Dy() < 600 {
		line = 12
	}
	x, y := b.Dx()/4, b.Dy()/2
	q := ocr.Quad{{x, y}, {3 * x, y}, {3 * x, y + line}, {x, y + line}}
	return ocr.Detection{Regions: []ocr.Region{{Quad: q, Score: 0.9}}, Width: b.Dx(), Height: b.Dy()}, nil
}

func (receiptText) Recognize(_ context.Context, img image.Image) (ocr.Page, error) {
	b := img.Bounds()
	return ocr.Page{Width: b.Dx(), Height: b.Dy(), Lines: []ocr.Line{{Text: "Total 12.50", Confidence: 0.9, Quad: ocr.Quad{{10, 10}, {200, 10}, {200, 40}, {10, 40}}}}}, nil
}

func (receiptText) Info() ocr.Info { return ocr.Info{Engine: "test"} }
func (receiptText) Close() error   { return nil }

// animatedPNG is a two-frame image the optimizer keeps as it is.
const animatedPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACGFjVEwAAAACAAAAAPONk3AAAAAaZmNUTAAAAAAAAAABAAAAAQAAAAAAAAAAAAEACgAAWn8w0AAAAA1JREFUeJxj+M/A8B8ABQAB/4mZPR0AAAAaZmNUTAAAAAEAAAABAAAAAQAAAAAAAAAAAAEACgAAwQzaBAAAABFmZEFUAAAAAnicY2Bg+P8fAAMCAf/1e6XXAAAAAElFTkSuQmCC"

// receiptPhoto is a w×h PNG that compresses badly, like a photographed page.
func receiptPhoto(t *testing.T, w, h int) []byte {
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

// admin installs the receipts package and uploads the receipts.
const admin = "organization-admin"

// receipts is the receipts package installed on a running PaperGo whose
// batch provider is a test server.
type receipts struct {
	s        *dms.Service
	handler  http.Handler
	ai       *aiops.Assistant
	batch    *aitest.BatchServer
	library  string
	lines    string
	workflow string
}

func installReceipts(t *testing.T) *receipts {
	t.Helper()
	ctx := context.Background()
	db := daltest.Database(t)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	batch := aitest.NewBatchServer(t)
	client, err := ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderOpenAI, Endpoint: batch.Endpoint(), Model: "gpt-batch"}, batch.Client())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := dms.NewService(db.SQL)
	if err = s.RegisterPortability(blobs); err != nil {
		t.Fatal(err)
	}
	assistant := &aiops.Assistant{Storage: blobs, Batch: client, Results: resultcache.New(db.SQL), ResultTTL: time.Hour, Records: activitystore.New(db.SQL), BatchDeadline: 72 * time.Hour}
	files := &documentfiles.Maker{Storage: blobs, OCR: receiptText{}, MaxFileBytes: 32 << 20}
	for _, register := range []func(*dms.Service) error{assistant.Register, files.Register} {
		if err = register(s); err != nil {
			t.Fatal(err)
		}
	}
	wf := &workflow.Service{DMS: s, OCR: true}
	api := &httpapi.API{DMS: s, Workflows: wf, Storage: blobs, Logger: log, Auth: auth.Development{Token: token, Subject: admin}, MaxUpload: 32 << 20,
		Modules: []httpapi.Module{assistant.Module(), files.Module()}}
	runs := &Runner{DMS: s, API: api, Log: log}
	if err = runs.Register(s); err != nil {
		t.Fatal(err)
	}
	s.OnOperationsFinished(ReleaseWaits(s, log))
	if err = api.Validate(); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, kind := range s.OperationKinds() {
		if kind != aiops.KindBatch {
			kinds = append(kinds, kind)
		}
	}
	rt := worker.New(s, log)
	rt.Add(worker.Definition{Name: "all", Kinds: kinds, Concurrency: 4})
	// Batch requests wait for the test to flush them.
	rt.Add(worker.Definition{Name: "ai-batch", Kinds: []string{aiops.KindBatch}, Admission: worker.Admission{Policy: worker.Threshold, Threshold: 1000, Batch: 20}})
	rt.Maintain(worker.Maintenance{Name: "dispatch", Every: 50 * time.Millisecond, Run: runs.Dispatch, Wake: runs.Woken()})
	rt.Maintain(worker.Maintenance{Name: "waits", Every: 50 * time.Millisecond, Run: func(ctx context.Context, _ time.Time) error { return ReconcileWaits(ctx, s) }})
	working, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		if err := rt.Run(working); err != nil {
			t.Error(err)
		}
		close(stopped)
	}()
	t.Cleanup(func() { stop(); <-stopped })

	r := &receipts{s: s, handler: api.Handler(), ai: assistant, batch: batch}
	raw, err := os.ReadFile("../../samples/receipts-package/receipts.zip")
	if err != nil {
		t.Fatal(err)
	}
	var imported struct {
		ID string `json:"id"`
	}
	r.call(t, "POST", "/v1/portability/imports?dry_run=false", "application/zip", raw, 202, &imported)
	var result dms.PackageResult
	for deadline := time.Now().Add(20 * time.Second); result.WorkspaceID == ""; time.Sleep(20 * time.Millisecond) {
		op, err := s.Operation(ctx, admin, imported.ID)
		if err != nil || op.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("import: %+v %v", op, err)
		}
		if op.Status == "succeeded" {
			if err = json.Unmarshal(op.Result, &result); err != nil {
				t.Fatal(err)
			}
		}
	}
	collections, err := s.DAL.Resources().Resources(ctx, resources.Filter{WorkspaceID: result.WorkspaceID, Kinds: []refs.ResourceKind{refs.KindLibrary, refs.KindList}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range collections {
		switch c.Name {
		case "Receipts":
			r.library = c.ID
		case "Receipt lines":
			r.lines = c.ID
		}
	}
	flows, err := wf.List(ctx, admin, result.WorkspaceID)
	if err != nil || len(flows) != 1 || flows[0].Name != "Read receipts" || !flows[0].AutomaticEnabled {
		t.Fatalf("workflows: %+v %v", flows, err)
	}
	r.workflow = flows[0].ID
	return r
}

func (r *receipts) call(t *testing.T, method, path, contentType string, body []byte, want int, out any) http.Header {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Header()
}

// upload uploads a photo to the library and returns its item.
func (r *receipts) upload(t *testing.T, name string, data []byte) string {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": name}))
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	var created struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	r.call(t, "POST", "/v1/resources/"+r.library+"/documents", writer.FormDataContentType(), body.Bytes(), 201, &created)
	return created.Item.ID
}

// runOf waits for a run of the receipt workflow on item, other than those
// in skip, to be in status, and returns it.
func (r *receipts) runOf(t *testing.T, item string, status runstore.Status, skip ...string) workflow.Run {
	t.Helper()
	var last []workflow.Run
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var page struct {
			Value []workflow.Run `json:"value"`
		}
		r.call(t, "GET", "/v1/workspaces/"+r.workspace(t)+"/workflow-runs?workflow_id="+r.workflow+"&item_id="+item, "", nil, 200, &page)
		last = page.Value
		for _, run := range page.Value {
			if run.Status == status && !contains(skip, run.ID) {
				var full workflow.Run
				r.call(t, "GET", "/v1/workflow-runs/"+run.ID, "", nil, 200, &full)
				return full
			}
		}
	}
	t.Fatalf("no %s run on %s: %s", status, item, dump(r, t, last))
	return workflow.Run{}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func dump(r *receipts, t *testing.T, runs []workflow.Run) string {
	var out strings.Builder
	for _, run := range runs {
		var full workflow.Run
		r.call(t, "GET", "/v1/workflow-runs/"+run.ID, "", nil, 200, &full)
		fmt.Fprintf(&out, "\n%s %s %s", full.ID, full.Status, full.Error)
		for _, step := range full.Steps {
			fmt.Fprintf(&out, "\n  %d %s %s %s %s", step.Seq, step.Kind, step.Name, step.Status, step.Error)
		}
		for _, l := range full.Logs {
			fmt.Fprintf(&out, "\n  log %s %s", l.Level, l.Message)
		}
	}
	return out.String()
}

func (r *receipts) workspace(t *testing.T) string {
	t.Helper()
	var library struct {
		WorkspaceID string `json:"workspace_id"`
	}
	r.call(t, "GET", "/v1/resources/"+r.library, "", nil, 200, &library)
	return library.WorkspaceID
}

// queued waits until n batch requests wait for the provider.
func (r *receipts) queued(t *testing.T, n int) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		groups, err := r.s.ReadyOperationGroups(t.Context(), []string{aiops.KindBatch})
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) == 1 && groups[0].Inputs >= n {
			return
		}
		if time.Now().After(deadline) {
			ops, _ := r.s.DAL.Operations().List(t.Context(), operations.Filter{ExcludeKinds: []string{dms.OperationKindRun}})
			for _, op := range ops {
				t.Logf("%s %s %s %s", op.ID, op.Kind, op.Status, op.Error)
			}
			var page struct {
				Value []workflow.Run `json:"value"`
			}
			r.call(t, "GET", "/v1/workspaces/"+r.workspace(t)+"/workflow-runs?workflow_id="+r.workflow, "", nil, 200, &page)
			t.Fatalf("batch requests: %+v %s", groups, dump(r, t, page.Value))
		}
	}
}

// flush sends the parked batch requests to the provider and polls until it
// answered them.
func (r *receipts) flush(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	before := r.batch.Creates()
	if _, err := r.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err != nil {
		t.Fatal(err)
	}
	if r.batch.Creates() != before+1 {
		t.Fatalf("provider batches %d, want %d", r.batch.Creates(), before+1)
	}
	if err := r.ai.Poll(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

type receiptItem struct {
	Name    string         `json:"name"`
	Version int            `json:"version"`
	Values  map[string]any `json:"values"`
}

func (r *receipts) item(t *testing.T, id string) receiptItem {
	t.Helper()
	var item receiptItem
	r.call(t, "GET", "/v1/resources/"+id, "", nil, 200, &item)
	return item
}

// lineValues returns the lines linked to a receipt, by description, as
// quantity, unit price and amount.
func (r *receipts) lineValues(t *testing.T, id string) map[string]string {
	t.Helper()
	q := url.Values{"direction": {"outgoing"}, "type": {"contains receipt line"}, "$top": {"50"}}
	var page struct {
		Value []struct {
			Related struct {
				Values map[string]any `json:"values"`
			} `json:"related_item"`
		} `json:"value"`
	}
	r.call(t, "GET", "/v1/items/"+id+"/relationships?"+q.Encode(), "", nil, 200, &page)
	out := map[string]string{}
	for _, link := range page.Value {
		v := link.Related.Values
		out[fmt.Sprint(v["description"])] = fmt.Sprintf("%v %v %v", v["quantity"], v["unit_price"], v["amount"])
	}
	return out
}

func (r *receipts) expectRead(t *testing.T, id string) {
	t.Helper()
	item := r.item(t, id)
	for key, want := range map[string]string{"store": "North Market", "purchase_date": "2026-10-15", "currency": "EUR", "total": "12.50", "status": "Read"} {
		if got := fmt.Sprint(item.Values[key]); got != want {
			t.Fatalf("%s = %q, want %q: %+v", key, got, want, item.Values)
		}
	}
	lines := r.lineValues(t, id)
	if len(lines) != 2 || lines["Plums"] != "2.000 4.00 8.00" || lines["Bread"] != "1.000 4.50 4.50" {
		t.Fatalf("lines: %+v", lines)
	}
}

func (r *receipts) lineCount(t *testing.T) int {
	t.Helper()
	var page struct {
		Value []json.RawMessage `json:"value"`
	}
	r.call(t, "GET", "/v1/resources/"+r.lines+"/children?kind=item&$top=100", "", nil, 200, &page)
	return len(page.Value)
}

// The receipts package reads uploaded photos: each run optimizes the photo,
// waits for its batch answer and files the lines; a newer file replaces a
// waiting run, a repeated run replaces the lines, and a refused answer
// leaves the receipt for review.
func TestReceiptsPackage(t *testing.T) {
	r := installReceipts(t)

	large := r.upload(t, "market.png", receiptPhoto(t, 1200, 800))
	animated, err := base64.StdEncoding.DecodeString(animatedPNG)
	if err != nil {
		t.Fatal(err)
	}
	small := r.upload(t, "animated.png", animated)
	r.runOf(t, large, runstore.StatusWaiting)
	r.queued(t, 2)
	if r.batch.Creates() != 0 {
		t.Fatalf("waiting receipts submitted %d batches", r.batch.Creates())
	}
	r.flush(t)
	done := r.runOf(t, large, runstore.StatusCompleted)
	if string(done.Result) != `{"lines":2,"status":"Read"}` && string(done.Result) != `{"status":"Read","lines":2}` {
		t.Fatalf("output: %s", done.Result)
	}
	r.runOf(t, small, runstore.StatusCompleted)
	r.expectRead(t, large)
	r.expectRead(t, small)
	var versions struct {
		Value []dms.FileVersion `json:"value"`
	}
	r.call(t, "GET", "/v1/items/"+large+"/content/versions", "", nil, 200, &versions)
	if len(versions.Value) < 1 || versions.Value[0].ContentType != "image/webp" {
		t.Fatalf("the large photo was not replaced by its smaller copy: %+v", versions.Value)
	}
	r.call(t, "GET", "/v1/items/"+small+"/content/versions", "", nil, 200, &versions)
	if len(versions.Value) != 1 || versions.Value[0].ContentType != "image/png" {
		t.Fatalf("the animated photo was replaced: %+v", versions.Value)
	}
	if n := r.lineCount(t); n != 4 {
		t.Fatalf("%d lines, want 4", n)
	}

	// A repeated run replaces the receipt's lines with the cached answer.
	ids, err := (&workflow.Service{DMS: r.s}).StartRuns(t.Context(), admin, r.workflow, workflow.Start{ItemIDs: []string{small}})
	if err != nil {
		t.Fatal(err)
	}
	repeat := r.runOf(t, small, runstore.StatusCompleted, done.ID)
	for repeat.ID != ids[0] {
		repeat = r.runOf(t, small, runstore.StatusCompleted, done.ID, repeat.ID)
	}
	r.expectRead(t, small)
	if n := r.lineCount(t); n != 4 || r.batch.Creates() != 1 {
		t.Fatalf("%d lines and %d batches after the repeat", n, r.batch.Creates())
	}

	// A newer file replaces the run still waiting for its answer.
	newer := r.upload(t, "newer.png", receiptPhoto(t, 1100, 740))
	r.queued(t, 1)
	parked := r.runOf(t, newer, runstore.StatusWaiting)
	item := r.item(t, newer)
	var body bytes.Buffer
	body.Write(receiptPhoto(t, 1000, 680))
	req := httptest.NewRequest("PUT", "/v1/items/"+newer+"/content", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("X-Filename", "replaced.png")
	req.Header.Set("If-Match", fmt.Sprintf(`"%d"`, item.Version))
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body)
	}
	r.runOf(t, newer, runstore.StatusCancelled)
	r.runOf(t, newer, runstore.StatusWaiting, parked.ID)
	r.queued(t, 1)
	r.flush(t)
	r.runOf(t, newer, runstore.StatusCompleted)
	r.expectRead(t, newer)

	// An answer the provider refuses leaves the receipt for review.
	r.batch.SetReceiptMode("reject")
	rejected := r.upload(t, "reject.png", receiptPhoto(t, 900, 600))
	r.queued(t, 1)
	r.flush(t)
	reviewed := r.runOf(t, rejected, runstore.StatusCompleted)
	if status := r.item(t, rejected).Values["status"]; status != "Needs review" || len(r.lineValues(t, rejected)) != 0 {
		t.Fatalf("a refused answer: %v %s", status, dump(r, t, []workflow.Run{reviewed}))
	}
}
