package httpapi

import (
	"bytes"
	"context"
	"database/sql"
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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/ent/resource"
	"papergo/internal/ai"
	"papergo/internal/ai/aitest"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/ocr"
	"papergo/internal/resultcache"
	"papergo/internal/runner"
	"papergo/internal/storage"
	"papergo/internal/testutil"
	"papergo/internal/testutil/searchstore"
	"papergo/internal/workflow"
	"papergo/internal/workflowai"
)

const bobToken = "fedcba9876543210fedcba9876543210"

// receiptAnimatedPNG is the two-frame image imaging tests keep unchanged.
const receiptAnimatedPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACGFjVEwAAAACAAAAAPONk3AAAAAaZmNUTAAAAAAAAAABAAAAAQAAAAAAAAAAAAEACgAAWn8w0AAAAA1JREFUeJxj+M/A8B8ABQAB/4mZPR0AAAAaZmNUTAAAAAEAAAABAAAAAQAAAAAAAAAAAAEACgAAwQzaBAAAABFmZEFUAAAAAnicY2Bg+P8fAAMCAf/1e6XXAAAAAElFTkSuQmCC"

// receiptTextEngine finds one 24-pixel line so a photographed page can be halved.
type receiptTextEngine struct{}

func (receiptTextEngine) Detect(_ context.Context, img image.Image, _ ocr.DetectOptions) (ocr.Detection, error) {
	b := img.Bounds()
	// A tall photo has 24px text, so the first pass halves it. A copy already
	// near the target reports 12px text, which is the default target height,
	// so a later pass keeps that WebP and reuses its cached answer.
	line := 24
	if b.Dy() < 600 {
		line = 12
	}
	x, y := b.Dx()/4, b.Dy()/2
	q := ocr.Quad{{x, y}, {3 * x, y}, {3 * x, y + line}, {x, y + line}}
	return ocr.Detection{Regions: []ocr.Region{{Quad: q, Score: 0.9}}, Width: b.Dx(), Height: b.Dy()}, nil
}

func (receiptTextEngine) Recognize(_ context.Context, img image.Image) (ocr.Page, error) {
	b := img.Bounds()
	return ocr.Page{Width: b.Dx(), Height: b.Dy(), Lines: []ocr.Line{{Text: "Total 21.30", Confidence: 0.9, Quad: ocr.Quad{{10, 10}, {200, 10}, {200, 40}, {10, 40}}}}}, nil
}

func (receiptTextEngine) Info() ocr.Info { return ocr.Info{Engine: "test"} }
func (receiptTextEngine) Close() error   { return nil }

// receiptScan is a w×h PNG that compresses badly, like a photographed page.
func receiptScan(t *testing.T, w, h int) []byte {
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

type receiptClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *receiptClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *receiptClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

type receiptLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *receiptLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *receiptLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type tokenAuth map[string]string

func (tokenAuth) Kind() auth.Kind { return auth.Bearer }

func (a tokenAuth) Authenticate(r *http.Request) (auth.Identity, error) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	subject, ok := a[parts[1]]
	if !ok {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	return auth.Identity{Subject: subject}, nil
}

type receiptEnv struct {
	t         *testing.T
	handler   http.Handler
	api       *API
	pool      *sql.DB
	dms       *dms.Service
	wf        *workflow.Service
	batch     *aitest.BatchServer
	clock     *receiptClock
	logs      *receiptLog
	runner    *runner.Runner
	cfg       runner.Config
	workspace string
	library   string
	lines     string
	workflow  string
	coord     string
}

func (h *receiptEnv) fail(t *testing.T, format string, args ...any) {
	t.Helper()
	log := h.logs.String()
	if len(log) > 8000 {
		log = log[len(log)-8000:]
	}
	t.Fatalf("%s\nrunner log:\n%s", fmt.Sprintf(format, args...), log)
}

func (h *receiptEnv) call(t *testing.T, method, path string, body []byte, token, match, contentType, filename string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if match != "" {
		req.Header.Set("If-Match", match)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if filename != "" {
		req.Header.Set("X-Filename", filename)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func (h *receiptEnv) send(t *testing.T, method, path string, payload any, token, match string, code int) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			t.Fatal(err)
		}
	}
	rec := h.call(t, method, path, body, token, match, "application/json", "")
	if rec.Code != code {
		h.fail(t, "%s %s: %d %s", method, path, rec.Code, rec.Body.String())
	}
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

func textOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		raw, _ := json.Marshal(t)
		return string(raw)
	}
}

func (h *receiptEnv) listRuns(t *testing.T, itemID, token string) []runner.Run {
	t.Helper()
	path := "/v1/workspaces/" + h.workspace + "/workflow-runs?workflow_id=" + url.QueryEscape(h.workflow) + "&$top=50"
	if itemID != "" {
		path += "&item_id=" + url.QueryEscape(itemID)
	}
	rec := h.call(t, http.MethodGet, path, nil, token, "", "", "")
	if rec.Code != 200 {
		h.fail(t, "runs: %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Value []runner.Run `json:"value"`
	}
	decodeJSON(t, rec, &page)
	return page.Value
}

func stepDump(run runner.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status=%s error=%s", run.Status, run.Error)
	for _, step := range run.Steps {
		fmt.Fprintf(&b, "\n  %s err=%s", step.Name, step.Error)
	}
	return b.String()
}

func (h *receiptEnv) awaitID(t *testing.T, id, token, until string) runner.Run {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var run runner.Run
	for time.Now().Before(deadline) {
		rec := h.call(t, http.MethodGet, "/v1/workflow-runs/"+id, nil, token, "", "", "")
		if rec.Code != 200 {
			h.fail(t, "run %s: %d %s", id, rec.Code, rec.Body.String())
		}
		decodeJSON(t, rec, &run)
		switch until {
		case "waiting":
			if run.Status == "waiting" {
				return run
			}
			if run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled" {
				h.fail(t, "run settled before it parked: %s", stepDump(run))
			}
		default:
			if run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled" {
				return run
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.fail(t, "timed out waiting for %s to be %s: %s", id, until, stepDump(run))
	return run
}

func (h *receiptEnv) awaitItem(t *testing.T, itemID, token, until string) runner.Run {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last runner.Run
	for time.Now().Before(deadline) {
		runs := h.listRuns(t, itemID, token)
		if len(runs) == 0 {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return h.awaitID(t, runs[0].ID, token, until)
	}
	h.fail(t, "no run for %s: %s", itemID, stepDump(last))
	return last
}

func (h *receiptEnv) coordinate(t *testing.T) {
	t.Helper()
	rec := h.send(t, http.MethodPost, "/v1/workflows/"+h.coord+"/runs", map[string]any{}, bobToken, "", http.StatusAccepted)
	var started struct {
		IDs []string `json:"run_ids"`
	}
	decodeJSON(t, rec, &started)
	if len(started.IDs) != 1 {
		h.fail(t, "coordinator start: %s", rec.Body.String())
	}
	run := h.awaitID(t, started.IDs[0], bobToken, "settled")
	if run.Status != "completed" {
		h.fail(t, "coordinator: %s", stepDump(run))
	}
}

func (h *receiptEnv) flush(t *testing.T) {
	t.Helper()
	before := h.batch.Creates()
	h.clock.advance(26 * time.Hour)
	h.coordinate(t)
	if got := h.batch.Creates(); got != before+1 {
		h.fail(t, "provider creates %d, want %d", got, before+1)
	}
}

func (h *receiptEnv) upload(t *testing.T, name string, data []byte) string {
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
	rec := h.call(t, http.MethodPost, "/v1/resources/"+h.library+"/documents", body.Bytes(), testToken, "", writer.FormDataContentType(), "")
	if rec.Code != http.StatusCreated {
		h.fail(t, "upload %s: %d %s", name, rec.Code, rec.Body.String())
	}
	var created struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	decodeJSON(t, rec, &created)
	if created.Item.ID == "" {
		h.fail(t, "upload %s has no item: %s", name, rec.Body.String())
	}
	return created.Item.ID
}

type receiptItem struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Values map[string]any `json:"values"`
}

func (h *receiptEnv) item(t *testing.T, id, token string) receiptItem {
	t.Helper()
	rec := h.call(t, http.MethodGet, "/v1/resources/"+id, nil, token, "", "", "")
	if rec.Code != 200 {
		h.fail(t, "item %s: %d %s", id, rec.Code, rec.Body.String())
	}
	var item receiptItem
	decodeJSON(t, rec, &item)
	item.ID = id
	return item
}

func (h *receiptEnv) etag(t *testing.T, id, token string) string {
	t.Helper()
	rec := h.call(t, http.MethodGet, "/v1/resources/"+id, nil, token, "", "", "")
	if rec.Code != 200 || rec.Header().Get("ETag") == "" {
		h.fail(t, "etag %s: %d %s", id, rec.Code, rec.Header().Get("ETag"))
	}
	return rec.Header().Get("ETag")
}

type receiptLink struct {
	ID      string `json:"id"`
	Related struct {
		ID     string         `json:"id"`
		Name   string         `json:"name"`
		Values map[string]any `json:"values"`
	} `json:"related_item"`
}

func (h *receiptEnv) links(t *testing.T, itemID, token string) []receiptLink {
	t.Helper()
	q := url.Values{}
	q.Set("direction", "outgoing")
	q.Set("type", "contains receipt line")
	q.Set("$top", "50")
	rec := h.call(t, http.MethodGet, "/v1/items/"+itemID+"/relationships?"+q.Encode(), nil, token, "", "", "")
	if rec.Code != 200 {
		h.fail(t, "links %s: %d %s", itemID, rec.Code, rec.Body.String())
	}
	var page struct {
		Value []receiptLink `json:"value"`
	}
	decodeJSON(t, rec, &page)
	return page.Value
}

func (h *receiptEnv) childIDs(t *testing.T, token string) []string {
	t.Helper()
	rec := h.call(t, http.MethodGet, "/v1/resources/"+h.lines+"/children?kind=item&$top=100", nil, token, "", "", "")
	if rec.Code != 200 {
		h.fail(t, "lines: %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	decodeJSON(t, rec, &page)
	ids := make([]string, len(page.Value))
	for i, row := range page.Value {
		ids[i] = row.ID
	}
	return ids
}

func (h *receiptEnv) versions(t *testing.T, itemID, token string) []dms.FileVersion {
	t.Helper()
	rec := h.call(t, http.MethodGet, "/v1/items/"+itemID+"/content/versions?$top=20", nil, token, "", "", "")
	if rec.Code != 200 {
		h.fail(t, "versions %s: %d %s", itemID, rec.Code, rec.Body.String())
	}
	var page struct {
		Value []dms.FileVersion `json:"value"`
	}
	decodeJSON(t, rec, &page)
	return page.Value
}

func (h *receiptEnv) expectPurchase(t *testing.T, itemID, token string) []string {
	t.Helper()
	item := h.item(t, itemID, token)
	want := map[string]string{
		"store": "North Market", "purchase_date": "2026-10-15", "currency": "EUR", "total": "12.50", "status": "Read",
	}
	for key, value := range want {
		if got := textOf(item.Values[key]); got != value {
			h.fail(t, "item %s %s = %q, want %q; values %#v", itemID, key, got, value, item.Values)
		}
	}
	links := h.links(t, itemID, token)
	if len(links) != 2 {
		h.fail(t, "item %s has %d lines", itemID, len(links))
	}
	byDesc := map[string]receiptLink{}
	var ids []string
	for _, link := range links {
		desc := textOf(link.Related.Values["description"])
		byDesc[desc] = link
		ids = append(ids, link.Related.ID)
		if link.Related.Name != desc || link.Related.ID == "" {
			h.fail(t, "line %+v", link)
		}
	}
	checks := map[string][3]string{
		"Plums": {"2.000", "4.00", "8.00"},
		"Bread": {"1.000", "4.50", "4.50"},
	}
	for desc, numbers := range checks {
		link, ok := byDesc[desc]
		if !ok {
			h.fail(t, "missing %s in %+v", desc, links)
		}
		got := [3]string{
			textOf(link.Related.Values["quantity"]),
			textOf(link.Related.Values["unit_price"]),
			textOf(link.Related.Values["amount"]),
		}
		if got != numbers {
			h.fail(t, "%s numbers %v, want %v", desc, got, numbers)
		}
	}
	return ids
}

func (h *receiptEnv) restart(t *testing.T) {
	t.Helper()
	if err := h.runner.Shutdown(15 * time.Second); err != nil {
		h.fail(t, "shutdown: %v", err)
	}
	next, err := runner.New(h.cfg, h.pool, h.dms, h.wf)
	if err != nil {
		h.fail(t, "restart runner: %v", err)
	}
	if err = next.Launch(context.Background()); err != nil {
		_ = next.Shutdown(5 * time.Second)
		h.fail(t, "relaunch: %v", err)
	}
	h.runner = next
	h.api.Runner = next
}

func TestReceiptsPackage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "papergo.db")
	db := testutil.DatabaseAt(t, path)
	batch := aitest.NewBatchServer(t)
	client, err := ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderOpenAI, Endpoint: batch.Endpoint(), Model: "gpt-batch"}, batch.Client())
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocal(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	logs := &receiptLog{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dmsService := dms.NewService(db.SQL)
	index := searchstore.Open(t, filepath.Join(t.TempDir(), "search.db"), []string{"english"})
	dmsService.SetSearch(index)
	clock := &receiptClock{at: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	wf := &workflow.Service{DMS: dmsService, Tools: workflow.Tools{
		Storage: store, OCR: receiptTextEngine{}, Search: index,
		Results: resultcache.New(db.SQL), ResultTTL: 30 * 24 * time.Hour,
		Now: clock.now,
	}}
	workflowai.Configure(&wf.Tools, workflowai.Runtime{Batch: client, BatchDeadline: 72 * time.Hour})
	cfg := runner.Config{
		DatabasePath: path, NodeID: "receipts", PollInterval: 20 * time.Millisecond,
		DispatchInterval: 20 * time.Millisecond, TickSchedule: "0 0 0 1 1 *", Logger: logger,
	}
	run, err := runner.New(cfg, db.SQL, dmsService, wf)
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Launch(ctx); err != nil {
		_ = run.Shutdown(5 * time.Second)
		t.Fatal(err)
	}
	api := &API{
		DMS: dmsService, Workflows: wf, Runner: run,
		Auth:    tokenAuth{testToken: "alice", bobToken: "bob"},
		Storage: store, Logger: logger, Ready: db.SQL.PingContext, MaxUpload: 32 << 20,
	}
	handler := api.Handler()
	h := &receiptEnv{
		t: t, handler: handler, api: api, dms: dmsService, wf: wf,
		batch: batch, clock: clock, logs: logs, runner: run, cfg: cfg,
	}
	h.pool = db.SQL
	t.Cleanup(func() { _ = h.runner.Shutdown(15 * time.Second) })

	// The same ZIP a user selects in Export and import provisions the sample.
	// Drop the temporary organization role before running it, so ACL tests still
	// exercise the importing author's ordinary workspace permissions.
	if err = dmsService.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	sampleOperations(t, dmsService, store)
	result := importSampleHTTP(t, handler, dmsService, "alice", "../../samples/receipts-package/receipts.zip", "", false)
	if err = dmsService.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: "user", ID: "alice"}, true); err != nil {
		t.Fatal(err)
	}
	h.workspace = result.WorkspaceID
	collections, err := dmsService.Client.Resource.Query().Where(resource.WorkspaceIDEQ(h.workspace), resource.KindIn(resource.KindLibrary, resource.KindList)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, collection := range collections {
		switch collection.Name {
		case "Receipts":
			h.library = collection.ID
		case "Receipt lines":
			h.lines = collection.ID
		}
	}
	flows, err := wf.List(ctx, "alice", h.workspace)
	if err != nil || len(flows) != 2 {
		t.Fatal(flows, err)
	}
	for _, flow := range flows {
		if flow.Name == "Read receipts" {
			h.workflow = flow.ID
		}
	}

	t.Logf("installed workspace %s library %s lines %s workflow %s", h.workspace, h.library, h.lines, h.workflow)
	h.editWorkflow(t)
	h.disableSearch(t)
	h.grantBob(t)
	h.enableCoordinator(t)
	if batch.Creates() != 0 {
		t.Fatalf("creates before the schedule %d", batch.Creates())
	}
	h.coordinate(t)
	if batch.Creates() != 0 {
		t.Fatalf("the first pass submitted %d batches", batch.Creates())
	}
	t.Log("coordinator scheduled the Madrid flush and submitted nothing")

	good := h.upload(t, "market.png", receiptScan(t, 1200, 800))
	animated := h.upload(t, "animated.png", mustAnimated(t))
	goodRun := h.awaitItem(t, good, testToken, "waiting")
	animatedRun := h.awaitItem(t, animated, testToken, "waiting")
	if textOf(h.item(t, good, testToken).Values["status"]) != "New" || textOf(h.item(t, animated, testToken).Values["status"]) != "New" {
		t.Fatalf("status changed before the answer: good %#v animated %#v", h.item(t, good, testToken).Values, h.item(t, animated, testToken).Values)
	}
	if batch.Creates() != 0 {
		t.Fatalf("parked receipts submitted %d batches", batch.Creates())
	}
	t.Log("both receipts are waiting on the batch")
	h.restart(t)
	if again := h.awaitID(t, goodRun.ID, testToken, "waiting"); again.ID != goodRun.ID {
		t.Fatalf("restart lost %s", goodRun.ID)
	}
	if again := h.awaitID(t, animatedRun.ID, testToken, "waiting"); again.ID != animatedRun.ID {
		t.Fatalf("restart lost %s", animatedRun.ID)
	}
	t.Log("restart kept both parked runs")
	h.flush(t)
	if run := h.awaitID(t, goodRun.ID, testToken, "settled"); run.Status != "completed" {
		h.fail(t, "good receipt: %s", stepDump(run))
	}
	if run := h.awaitID(t, animatedRun.ID, testToken, "settled"); run.Status != "completed" {
		h.fail(t, "animated receipt: %s", stepDump(run))
	}
	goodLines := h.expectPurchase(t, good, testToken)
	animatedLines := h.expectPurchase(t, animated, testToken)
	h.expectPromoted(t, good, "market.png")
	h.expectKept(t, animated)
	if kids := h.childIDs(t, testToken); len(kids) != 4 {
		t.Fatalf("line items %d, want 4: %v", len(kids), kids)
	}
	t.Logf("flush filed both receipts, creates %d", batch.Creates())

	beforeKids := len(h.childIDs(t, testToken))
	repeat := h.send(t, http.MethodPost, "/v1/workflows/"+h.workflow+"/runs", map[string]any{"item_ids": []string{good}}, testToken, "", http.StatusAccepted)
	var started struct {
		IDs []string `json:"run_ids"`
	}
	decodeJSON(t, repeat, &started)
	if len(started.IDs) != 1 {
		t.Fatalf("repeat start: %s", repeat.Body.String())
	}
	if run := h.awaitID(t, started.IDs[0], testToken, "settled"); run.Status != "completed" {
		h.fail(t, "repeat: %s", stepDump(run))
	}
	if batch.Creates() != 1 {
		t.Fatalf("repeat extraction created batch %d", batch.Creates())
	}
	again := h.expectPurchase(t, good, testToken)
	if len(h.childIDs(t, testToken)) != beforeKids {
		t.Fatalf("repeat changed the line-list count from %d to %d", beforeKids, len(h.childIDs(t, testToken)))
	}
	for _, id := range goodLines {
		rec := h.call(t, http.MethodGet, "/v1/resources/"+id, nil, testToken, "", "", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("old line %s still %d", id, rec.Code)
		}
		for _, kept := range again {
			if kept == id {
				t.Fatalf("repeat reused line %s", id)
			}
		}
	}
	t.Log("repeat extraction replaced the lines from the cached answer")

	newer := h.upload(t, "newer.png", receiptScan(t, 1100, 740))
	parked := h.awaitItem(t, newer, testToken, "waiting")
	match := h.etag(t, newer, testToken)
	put := h.call(t, http.MethodPut, "/v1/items/"+newer+"/content", receiptScan(t, 1000, 680), testToken, match, "image/png", "replaced.png")
	if put.Code != http.StatusCreated {
		h.fail(t, "replace: %d %s", put.Code, put.Body.String())
	}
	replaced := h.awaitReplacement(t, newer, parked.ID)
	h.flush(t)
	if run := h.awaitID(t, replaced, testToken, "settled"); run.Status != "completed" {
		h.fail(t, "replaced upload: %s", stepDump(run))
	}
	if run := h.awaitID(t, parked.ID, testToken, "settled"); run.Status != "cancelled" {
		h.fail(t, "old upload: %s", stepDump(run))
	}
	h.expectPurchase(t, newer, testToken)
	t.Log("a newer upload cancelled the parked run and filed one set of lines")

	h.batch.SetReceiptMode("reject")
	rejected := h.upload(t, "reject.png", receiptScan(t, 900, 600))
	rejectedRun := h.awaitItem(t, rejected, testToken, "waiting")
	h.flush(t)
	if run := h.awaitID(t, rejectedRun.ID, testToken, "settled"); run.Status != "completed" {
		h.fail(t, "reject: %s", stepDump(run))
	}
	if status := textOf(h.item(t, rejected, testToken).Values["status"]); status != "Needs review" {
		t.Fatalf("rejected status %q", status)
	}
	if n := len(h.links(t, rejected, testToken)); n != 0 {
		t.Fatalf("rejected receipt kept %d lines", n)
	}
	t.Log("a schema rejection completed through Needs review")

	h.batch.SetReceiptMode("bad-amount")
	bad := h.upload(t, "bad.png", receiptScan(t, 800, 520))
	badRun := h.awaitItem(t, bad, testToken, "waiting")
	h.flush(t)
	if run := h.awaitID(t, badRun.ID, testToken, "settled"); run.Status != "completed" {
		h.fail(t, "bad amount: %s", stepDump(run))
	}
	badItem := h.item(t, bad, testToken)
	if textOf(badItem.Values["status"]) != "Needs review" || textOf(badItem.Values["store"]) != "North Market" || textOf(badItem.Values["total"]) != "12.50" {
		t.Fatalf("bad amount values %#v", badItem.Values)
	}
	if n := len(h.links(t, bad, testToken)); n != 0 {
		t.Fatalf("bad amount kept %d lines", n)
	}
	t.Log("a line that cannot be stored marked the receipt Needs review")

	h.batch.SetReceiptMode("")
	locked := h.upload(t, "locked.png", receiptScan(t, 700, 480))
	lockedRun := h.awaitItem(t, locked, testToken, "waiting")
	h.send(t, http.MethodPost, "/v1/workspaces/"+h.workspace+"/members", map[string]string{"subject": "alice", "role": "visitor"}, testToken, "", http.StatusNoContent)
	h.flush(t)
	if run := h.awaitID(t, lockedRun.ID, bobToken, "settled"); run.Status != "failed" {
		h.fail(t, "revoked run: %s", stepDump(run))
	}
	lockedItem := h.item(t, locked, bobToken)
	if status := textOf(lockedItem.Values["status"]); status != "New" {
		t.Fatalf("revoked receipt status %q values %#v", status, lockedItem.Values)
	}
	if n := len(h.links(t, locked, bobToken)); n != 0 {
		t.Fatalf("revoked receipt kept %d lines", n)
	}
	if batch.Creates() != 5 {
		t.Fatalf("creates %d, want 5", batch.Creates())
	}
	for _, id := range animatedLines {
		for _, other := range goodLines {
			if id == other {
				t.Fatalf("receipts share line %s", id)
			}
		}
	}
	t.Log("revoking the workflow author failed the parked run and left the receipt New")
}

func (h *receiptEnv) editWorkflow(t *testing.T) {
	t.Helper()
	rec := h.call(t, http.MethodGet, "/v1/workflows/"+h.workflow, nil, testToken, "", "", "")
	if rec.Code != 200 {
		h.fail(t, "get workflow: %d %s", rec.Code, rec.Body.String())
	}
	var doc map[string]json.RawMessage
	decodeJSON(t, rec, &doc)
	var name string
	if err := json.Unmarshal(doc["name"], &name); err != nil || name == "" || doc["definition"] == nil {
		h.fail(t, "workflow document: %s", rec.Body.String())
	}
	body, err := json.Marshal(map[string]any{
		"name": name, "description": "Edited through the workflow API",
		"definition": json.RawMessage(doc["definition"]), "automatic_enabled": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := h.call(t, http.MethodPut, "/v1/workflows/"+h.workflow, body, testToken, rec.Header().Get("ETag"), "application/json", "")
	if updated.Code != 200 {
		h.fail(t, "edit workflow: %d %s", updated.Code, updated.Body.String())
	}
	again := h.call(t, http.MethodGet, "/v1/workflows/"+h.workflow, nil, testToken, "", "", "")
	if again.Code != 200 {
		h.fail(t, "reread workflow: %d %s", again.Code, again.Body.String())
	}
	var saved workflow.Workflow
	decodeJSON(t, again, &saved)
	if saved.Description != "Edited through the workflow API" || saved.Definition.Concurrency != "replace" {
		h.fail(t, "edited workflow %+v", saved.Description)
	}
	sawFile, sawComplete := false, false
	for _, trigger := range saved.Definition.Triggers {
		if trigger.Type == "file.added" {
			sawFile = true
		}
	}
	for _, node := range saved.Definition.Flow.Nodes {
		if node.Activity == "ai.complete" && node.Inputs["execution"] == "batch" {
			sawComplete = true
		}
	}
	if !sawFile || !sawComplete {
		h.fail(t, "edited definition lost the receipt flow")
	}
	versions := h.call(t, http.MethodGet, "/v1/workflows/"+h.workflow+"/versions", nil, testToken, "", "", "")
	if versions.Code != 200 {
		h.fail(t, "versions: %d %s", versions.Code, versions.Body.String())
	}
	var page struct {
		Value []struct {
			CreatedBy string `json:"created_by"`
		} `json:"value"`
	}
	decodeJSON(t, versions, &page)
	if len(page.Value) == 0 {
		h.fail(t, "no workflow versions")
	}
	for _, version := range page.Value {
		if version.CreatedBy != "alice" {
			h.fail(t, "version author %s", version.CreatedBy)
		}
	}
	t.Log("edited the installed workflow through the workflow API")
}

func (h *receiptEnv) disableSearch(t *testing.T) {
	t.Helper()
	for _, key := range []string{"search.index", "search.content"} {
		h.turnOff(t, key)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		active := false
		for _, id := range h.searchWorkflows(t) {
			for _, run := range h.runsOf(t, id) {
				if run.Status == "queued" || run.Status == "running" || run.Status == "waiting" {
					active = true
				}
			}
		}
		if !active {
			t.Log("search built-ins are off")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.fail(t, "search runs did not settle")
}

func (h *receiptEnv) turnOff(t *testing.T, key string) {
	t.Helper()
	for range 5 {
		rows := h.builtins(t)
		var found *builtinRow
		for i := range rows {
			if rows[i].Key == key {
				found = &rows[i]
			}
		}
		if found == nil {
			h.fail(t, "missing built-in %s", key)
		}
		if len(found.Workflows) == 0 {
			rec := h.call(t, http.MethodPut, "/v1/workspaces/"+h.workspace+"/workflow-builtins/"+key, []byte(`{"automatic_enabled":false}`), testToken, "", "application/json", "")
			if rec.Code == http.StatusPreconditionFailed {
				continue
			}
			if rec.Code != 200 {
				h.fail(t, "disable %s: %d %s", key, rec.Code, rec.Body.String())
			}
			return
		}
		done := true
		for _, w := range found.Workflows {
			if !w.AutomaticEnabled {
				continue
			}
			done = false
			body := map[string]any{"automatic_enabled": false}
			if w.CollectionID != nil {
				body["collection_id"] = *w.CollectionID
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rec := h.call(t, http.MethodPut, "/v1/workspaces/"+h.workspace+"/workflow-builtins/"+key, raw, testToken, strconv.Quote(strconv.Itoa(w.Version)), "application/json", "")
			if rec.Code == http.StatusPreconditionFailed {
				done = false
				break
			}
			if rec.Code != 200 {
				h.fail(t, "disable %s: %d %s", key, rec.Code, rec.Body.String())
			}
		}
		if done {
			return
		}
	}
	h.fail(t, "could not turn off %s", key)
}

type builtinRow struct {
	Key       string `json:"key"`
	Workflows []struct {
		ID               string  `json:"id"`
		Version          int     `json:"version"`
		AutomaticEnabled bool    `json:"automatic_enabled"`
		CollectionID     *string `json:"collection_id"`
	} `json:"workflows"`
}

func (h *receiptEnv) builtins(t *testing.T) []builtinRow {
	t.Helper()
	rec := h.call(t, http.MethodGet, "/v1/workspaces/"+h.workspace+"/workflow-builtins", nil, testToken, "", "", "")
	if rec.Code != 200 {
		h.fail(t, "builtins: %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Value []builtinRow `json:"value"`
	}
	decodeJSON(t, rec, &page)
	return page.Value
}

func (h *receiptEnv) searchWorkflows(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, row := range h.builtins(t) {
		if row.Key != "search.index" && row.Key != "search.content" {
			continue
		}
		for _, w := range row.Workflows {
			ids = append(ids, w.ID)
		}
	}
	return ids
}

func (h *receiptEnv) runsOf(t *testing.T, workflowID string) []runner.Run {
	t.Helper()
	rec := h.call(t, http.MethodGet, "/v1/workspaces/"+h.workspace+"/workflow-runs?workflow_id="+url.QueryEscape(workflowID)+"&$top=50", nil, testToken, "", "", "")
	if rec.Code != 200 {
		h.fail(t, "runs %s: %d %s", workflowID, rec.Code, rec.Body.String())
	}
	var page struct {
		Value []runner.Run `json:"value"`
	}
	decodeJSON(t, rec, &page)
	return page.Value
}

func (h *receiptEnv) grantBob(t *testing.T) {
	t.Helper()
	h.send(t, http.MethodPost, "/v1/workspaces/"+h.workspace+"/members", map[string]string{"subject": "bob", "role": "owner"}, testToken, "", http.StatusNoContent)
	role := h.send(t, http.MethodPost, "/v1/identity/roles", map[string]any{
		"name": "Batch administrator", "description": "Runs the installation batch coordinator", "scopes": []string{"organization.manage"},
	}, testToken, "", http.StatusCreated)
	var saved struct {
		ID string `json:"id"`
	}
	decodeJSON(t, role, &saved)
	if saved.ID == "" {
		h.fail(t, "role: %s", role.Body.String())
	}
	rec := h.call(t, http.MethodPut, "/v1/identity/roles/"+saved.ID+"/assignments/user/bob", nil, testToken, "", "", "")
	if rec.Code != http.StatusNoContent {
		h.fail(t, "assign bob: %d %s", rec.Code, rec.Body.String())
	}
}

func (h *receiptEnv) enableCoordinator(t *testing.T) {
	t.Helper()
	var version int
	for _, row := range h.builtins(t) {
		if row.Key == "ai.batch" && len(row.Workflows) == 1 {
			version = row.Workflows[0].Version
		}
	}
	if version == 0 {
		t.Fatal("sample did not import its batch coordinator")
	}
	// The imported coordinator is enabled already. Revise it as Bob because this
	// test removes Alice's organization role to exercise source-author revocation.
	rec := h.send(t, http.MethodPut, "/v1/workspaces/"+h.workspace+"/workflow-builtins/ai.batch", map[string]any{"automatic_enabled": true}, bobToken, strconv.Quote(strconv.Itoa(version)), 200)
	var saved workflow.Workflow
	decodeJSON(t, rec, &saved)
	if !saved.AutomaticEnabled || saved.ID == "" {
		h.fail(t, "coordinator: %s", rec.Body.String())
	}
	if saved.Parameters["queue"] != "shared" || saved.Parameters["policy"] != "both" || saved.Parameters["flush_cron"] != "0 1 * * *" || saved.Parameters["time_zone"] != "Europe/Madrid" {
		h.fail(t, "coordinator parameters %#v", saved.Parameters)
	}
	h.coord = saved.ID
}

func (h *receiptEnv) expectPromoted(t *testing.T, itemID, filename string) {
	t.Helper()
	versions := h.versions(t, itemID, testToken)
	if len(versions) != 2 {
		h.fail(t, "promoted history %+v", versions)
	}
	var original, current *dms.FileVersion
	for i := range versions {
		switch versions[i].Version {
		case 1:
			original = &versions[i]
		case 2:
			current = &versions[i]
		}
	}
	if original == nil || current == nil || original.ID == "" || current.ID == "" {
		h.fail(t, "history ids %+v", versions)
	}
	if original.Filename != filename || original.ContentType != "image/png" || original.Availability != "pending" || original.IsCurrent || original.Size <= 0 || original.SHA256 == "" {
		h.fail(t, "original %+v", original)
	}
	if current.ContentType != "image/webp" || current.Availability != "available" || !current.IsCurrent || current.Size <= 0 || current.SHA256 == "" || !strings.HasSuffix(current.Filename, ".webp") {
		h.fail(t, "current %+v", current)
	}
}

func (h *receiptEnv) expectKept(t *testing.T, itemID string) {
	t.Helper()
	versions := h.versions(t, itemID, testToken)
	if len(versions) != 1 {
		h.fail(t, "animated history %+v", versions)
	}
	file := versions[0]
	if file.Version != 1 || file.Availability != "available" || !file.IsCurrent || file.ContentType != "image/png" || file.Size <= 0 || file.SHA256 == "" || file.ID == "" {
		h.fail(t, "kept file %+v", file)
	}
}

func (h *receiptEnv) awaitReplacement(t *testing.T, itemID, oldID string) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		runs := h.listRuns(t, itemID, testToken)
		var old *runner.Run
		var next string
		for i := range runs {
			if runs[i].ID == oldID {
				old = &runs[i]
			}
			if runs[i].ID != oldID && runs[i].Status == "waiting" {
				next = runs[i].ID
			}
		}
		if old != nil && old.Status == "cancelled" && next != "" {
			return next
		}
		if old != nil && (old.Status == "completed" || old.Status == "failed") {
			h.fail(t, "old upload was not replaced: %s", stepDump(*old))
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.fail(t, "replacement for %s did not park", itemID)
	return ""
}

func mustAnimated(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(receiptAnimatedPNG)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
