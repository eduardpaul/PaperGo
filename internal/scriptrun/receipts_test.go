package scriptrun

import (
	"bytes"
	"context"
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
	"papergo/internal/httpapi"
	"papergo/internal/storage"
	"papergo/internal/worker"
	"papergo/internal/workflow"
)

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
	lineType string
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
	if err = assistant.Register(s); err != nil {
		t.Fatal(err)
	}
	wf := &workflow.Service{DMS: s}
	api := &httpapi.API{DMS: s, Workflows: wf, Storage: blobs, Logger: log, Auth: auth.Development{Token: token, Subject: admin}, MaxUpload: 32 << 20,
		// Read receipts are kept as PDFs, which need OCR.
		Modules: []httpapi.Module{assistant.Module(), files(s, blobs)}}
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
	// One library holds the receipts and their lines.
	if len(collections) != 1 || collections[0].Name != "Receipts" {
		t.Fatalf("collections: %+v", collections)
	}
	r.library = collections[0].ID
	types, err := s.CollectionContentTypes(ctx, admin, r.library)
	if err != nil {
		t.Fatal(err)
	}
	for _, lt := range types {
		if lt.Key == "receipt_line" {
			r.lineType = *lt.ParentID
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
	Name string `json:"name"`
	File struct {
		ContentType string `json:"content_type"`
		Filename    string `json:"filename"`
	} `json:"file"`
	Version int            `json:"version"`
	Values  map[string]any `json:"values"`
}

func (r *receipts) item(t *testing.T, id string) receiptItem {
	t.Helper()
	var item receiptItem
	r.call(t, "GET", "/v1/resources/"+id, "", nil, 200, &item)
	return item
}

// links returns the links from a receipt to its lines.
func (r *receipts) links(t *testing.T, id string) []receiptLink {
	t.Helper()
	q := url.Values{"direction": {"outgoing"}, "type": {"contains receipt line"}, "$top": {"50"}}
	var page struct {
		Value []receiptLink `json:"value"`
	}
	r.call(t, "GET", "/v1/items/"+id+"/relationships?"+q.Encode(), "", nil, 200, &page)
	return page.Value
}

type receiptLink struct {
	ID       string `json:"id"`
	TargetID string `json:"target_id"`
	Related  struct {
		Name     string         `json:"name"`
		ParentID string         `json:"parent_id"`
		Values   map[string]any `json:"values"`
	} `json:"related_item"`
}

// lineValues returns the lines linked to a receipt, by description, as
// quantity, unit price and amount. Every line is in Receipt lines/<receipt
// ID>/ of the library.
func (r *receipts) lineValues(t *testing.T, id string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, link := range r.links(t, id) {
		var folder, root struct {
			Name     string `json:"name"`
			ParentID string `json:"parent_id"`
		}
		r.call(t, "GET", "/v1/resources/"+link.Related.ParentID, "", nil, 200, &folder)
		r.call(t, "GET", "/v1/resources/"+folder.ParentID, "", nil, 200, &root)
		if folder.Name != id || root.Name != "Receipt lines" || root.ParentID != r.library {
			t.Fatalf("line %q is in %q of %q", link.Related.Name, folder.Name, root.Name)
		}
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

// lineCount counts the items of the line type anywhere in the library.
func (r *receipts) lineCount(t *testing.T) int {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": map[string]any{"content_type_id": r.lineType}})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Value []struct {
			Kind string `json:"kind"`
		} `json:"value"`
	}
	r.call(t, "POST", "/v1/resources/"+r.library+"/query?$top=100", "application/json", body, 200, &page)
	n := 0
	for _, item := range page.Value {
		if item.Kind == "item" {
			n++
		}
	}
	return n
}

// The receipts package reads uploaded photos: each run reads the photo,
// waits for its batch answer and files the lines; a newer file replaces a
// waiting run, a repeated run replaces the lines, and a refused answer
// leaves the receipt for review.
func TestReceiptsPackage(t *testing.T) {
	r := installReceipts(t)

	large := r.upload(t, "market.png", receiptPhoto(t, 1200, 800))
	small := r.upload(t, "corner.png", receiptPhoto(t, 400, 300))
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
	// Once read, the photo became the receipt's PDF, and the receipt is
	// named after it.
	if item := r.item(t, large); item.Name != "market.pdf" || item.File.ContentType != "application/pdf" || item.File.Filename != "market.pdf" {
		t.Fatalf("the receipt's file: %+v", item)
	}
	if n := r.lineCount(t); n != 4 {
		t.Fatalf("%d lines, want 4", n)
	}

	// A repeated run replaces the receipt's lines with the cached answer,
	// including a line left without its link.
	unlinked := r.links(t, small)[0]
	r.call(t, "DELETE", "/v1/items/"+small+"/relationships/"+unlinked.ID, "", nil, 204, nil)
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

	// Lines share the library: a run started on one reads nothing.
	line := r.links(t, small)[0].TargetID
	if _, err = (&workflow.Service{DMS: r.s}).StartRuns(t.Context(), admin, r.workflow, workflow.Start{ItemIDs: []string{line}}); err != nil {
		t.Fatal(err)
	}
	if skipped := r.runOf(t, line, runstore.StatusCompleted); string(skipped.Result) != `{"status":"Not a receipt"}` {
		t.Fatalf("a run on a line: %s", skipped.Result)
	}
	if n := r.lineCount(t); n != 4 || r.batch.Creates() != 1 {
		t.Fatalf("%d lines and %d batches after a run on a line", n, r.batch.Creates())
	}

	// A newer file replaces the run still waiting for its answer. It takes
	// the name a renamed receipt had: lines are filed by receipt, not name.
	rename := httptest.NewRequest("PATCH", "/v1/resources/"+large, strings.NewReader(`{"name":"market-old.pdf"}`))
	rename.Header.Set("Authorization", "Bearer "+token)
	rename.Header.Set("Content-Type", "application/json")
	rename.Header.Set("If-Match", fmt.Sprintf(`"%d"`, r.item(t, large).Version))
	renamed := httptest.NewRecorder()
	r.handler.ServeHTTP(renamed, rename)
	if renamed.Code != 200 {
		t.Fatalf("rename: %d %s", renamed.Code, renamed.Body)
	}
	newer := r.upload(t, "market.png", receiptPhoto(t, 1100, 740))
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
	if item := r.item(t, newer); item.Name != "market.pdf" || item.File.ContentType != "application/pdf" {
		t.Fatalf("the newer receipt's file: %+v", item)
	}
	r.expectRead(t, large)
	// The receipts view lists the receipts, not the folders of their lines.
	var views struct {
		Value []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"value"`
	}
	r.call(t, "GET", "/v1/resources/"+r.library+"/views", "", nil, 200, &views)
	for _, v := range views.Value {
		if v.Name != "Receipts" {
			continue
		}
		var page struct {
			Value []struct {
				Kind string `json:"kind"`
			} `json:"value"`
		}
		r.call(t, "POST", "/v1/views/"+v.ID+"/query", "application/json", []byte(`{}`), 200, &page)
		items := 0
		for _, row := range page.Value {
			if row.Kind == "item" {
				items++
			}
		}
		if items != 3 || len(page.Value) != 3 {
			t.Fatalf("receipts view: %+v", page.Value)
		}
	}

	// An answer the provider refuses leaves the receipt for review.
	r.batch.SetReceiptMode("reject")
	rejected := r.upload(t, "reject.png", receiptPhoto(t, 900, 600))
	r.queued(t, 1)
	r.flush(t)
	reviewed := r.runOf(t, rejected, runstore.StatusCompleted)
	// The photo stays, to be read again.
	if item := r.item(t, rejected); item.Values["status"] != "Needs review" || item.File.ContentType != "image/png" || len(r.lineValues(t, rejected)) != 0 {
		t.Fatalf("a refused answer: %+v %s", item, dump(r, t, []workflow.Run{reviewed}))
	}
}
