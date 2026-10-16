package runner

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/ai"
	"papergo/internal/ai/aitest"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// batchClock is the clock batch admission reads. Tests move it; activities
// call it from the runner's goroutines.
type batchClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *batchClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *batchClock) get() time.Time { return c.now() }

func (c *batchClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

func batchTools(t *testing.T, azure bool) (*aitest.BatchServer, *batchClock, func(*workflow.Tools)) {
	t.Helper()
	server := aitest.NewBatchServer(t)
	endpoint := server.Endpoint()
	if azure {
		endpoint = server.AzureEndpoint()
	}
	client, err := ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderOpenAI, Endpoint: endpoint, Model: "gpt-batch"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	clock := &batchClock{at: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	return server, clock, func(tools *workflow.Tools) {
		tools.Batch = client
		tools.Now = clock.now
		tools.BatchDeadline = 72 * time.Hour
	}
}

func (f *fixture) enableBatch(t *testing.T, params map[string]any) workflow.Workflow {
	t.Helper()
	f.organizationManager(t, "alice")
	w, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "ai.batch", workflow.SetBuiltIn{Enabled: true, Parameters: params})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func (f *fixture) coordinate(t *testing.T, id string) Run {
	t.Helper()
	ids, err := f.wf.StartRuns(ctx, "alice", id, workflow.Start{})
	if err != nil {
		t.Fatal(err)
	}
	run := f.wait(t, ids[0], finished)
	if run.Status != "completed" {
		t.Fatalf("coordinator %s: %s\nsteps %+v\nlog %s", run.Status, run.Error, run.Steps, f.logs.String())
	}
	return run
}

func (f *fixture) disableSearch(t *testing.T, workspaceID string) {
	t.Helper()
	if err := f.dms.Write(ctx, func(tx *dms.Service) error {
		return workflow.EnsureDefaults(ctx, tx, workspaceID)
	}); err != nil {
		t.Fatal(err)
	}
	all, err := f.wf.List(ctx, "alice", workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range all {
		if w.BuiltInKey != nil && *w.BuiltInKey == workflow.RoleSearchIndex {
			if _, err = f.wf.UpdateBuiltIn(ctx, "alice", workspaceID, *w.BuiltInKey, workflow.SetBuiltIn{Enabled: false, Version: w.Version}); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no search.index in %s", workspaceID)
}

func batchFlow(collection, prompt string, schema bool) string {
	value := "{step:ask.text}"
	extra := ""
	if schema {
		value = "{step:ask.json.total}"
		extra = `,"output_schema":{"type":"object","required":["total"],"properties":{"total":{"type":"number"}}}`
	}
	return fmt.Sprintf(`{"triggers":[{"type":"manual","collection_id":%q}],"input_schema":{"type":"object","required":["text"],"properties":{"text":{"type":"string"}}},"flow":{"start":"ask","nodes":{"ask":{"activity":"ai.complete","inputs":{"prompt":%q,"input":"{input:text}","execution":"batch","queue":"shared"%s},"next":{"done":"save","error":"bad"}},"save":{"activity":"set_variable","inputs":{"name":"answer","value":%q}},"bad":{"activity":"set_variable","inputs":{"name":"answer","value":"{step:ask.error}"}}}}}`, collection, prompt, extra, value)
}

func (f *fixture) startAsk(t *testing.T, w workflow.Workflow, itemID, text string) string {
	t.Helper()
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{itemID}, Inputs: map[string]any{"text": text}})
	if err != nil {
		t.Fatal(err)
	}
	return ids[0]
}

func settled(run Run) bool {
	return run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled"
}

func (f *fixture) answer(t *testing.T, id string) string {
	t.Helper()
	run := f.wait(t, id, settled)
	if run.Status != "completed" {
		t.Fatalf("run %s: %s steps %+v", run.Status, run.Error, run.Steps)
	}
	vars, _ := run.Result.(map[string]any)["variables"].(map[string]any)
	return shown(vars["answer"])
}

func shown(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return fmt.Sprintf("%v", t)
	case nil:
		return ""
	default:
		raw, _ := json.Marshal(t)
		return string(raw)
	}
}

func jsonlIDs(t *testing.T, raw []byte) []string {
	t.Helper()
	var ids []string
	seen := map[string]bool{}
	for _, row := range strings.Split(string(raw), "\n") {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		var line struct {
			CustomID string `json:"custom_id"`
		}
		if err := json.Unmarshal([]byte(row), &line); err != nil || line.CustomID == "" {
			t.Fatalf("batch line: %s", row)
		}
		if seen[line.CustomID] {
			t.Fatalf("duplicate custom_id %s", line.CustomID)
		}
		seen[line.CustomID] = true
		ids = append(ids, line.CustomID)
	}
	return ids
}

func (f *fixture) openRequests(t *testing.T, workspaceID string) []workflow.Wait {
	t.Helper()
	page, err := f.wf.ListWaits(ctx, "alice", workflow.WaitQuery{Kind: "ai.batch.request", WorkspaceID: workspaceID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return page.Data
}

func attemptsOf(t *testing.T, waits []workflow.Wait, runID string) int {
	t.Helper()
	for _, w := range waits {
		if w.RunID != runID {
			continue
		}
		var data struct {
			Attempts int `json:"attempts"`
		}
		if err := json.Unmarshal(w.Data, &data); err != nil {
			t.Fatal(err)
		}
		return data.Attempts
	}
	t.Fatalf("no open request for %s", runID)
	return 0
}

func (f *fixture) nextFlush(t *testing.T) time.Time {
	t.Helper()
	page, err := f.wf.ListWaits(ctx, "alice", workflow.WaitQuery{Kind: "ai.batch.state", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range page.Data {
		if w.Key != "shared" {
			continue
		}
		var data struct {
			NextFlush time.Time `json:"next_flush"`
		}
		if err = json.Unmarshal(w.Data, &data); err != nil {
			t.Fatal(err)
		}
		return data.NextFlush
	}
	t.Fatal("no coordinator state")
	return time.Time{}
}

func containsAny(raw string, parts ...string) string {
	for _, part := range parts {
		if strings.Contains(raw, part) {
			return part
		}
	}
	return ""
}

// Nineteen distinct uncached requests stay queued. A duplicate of one of
// them does not add a twentieth hash. The next distinct request submits one
// batch of 20, and both runs that share a hash complete from that one line.
func TestBatchNineteenStayQueuedUntilTheTwentieth(t *testing.T) {
	server, _, tools := batchTools(t, false)
	f := setup(t, tools)
	f.searchOff(t)
	coord := f.enableBatch(t, map[string]any{"policy": "threshold"})
	w := f.workflow(t, "Extract", batchFlow(f.list.ID, "Extract", false))
	var ids []string
	for i := range 19 {
		item := f.item(t, "alice", fmt.Sprintf("Receipt %02d", i), nil)
		ids = append(ids, f.startAsk(t, w, item.ID, fmt.Sprintf("ticket-%02d", i)))
	}
	dup := f.item(t, "alice", "Receipt 00 again", nil)
	ids = append(ids, f.startAsk(t, w, dup.ID, "ticket-00"))
	for _, id := range ids {
		f.status(t, id, "waiting")
	}
	f.coordinate(t, coord.ID)
	if server.Creates() != 0 || len(server.Uploads()) != 0 {
		t.Fatalf("19 hashes submitted: creates %d uploads %d", server.Creates(), len(server.Uploads()))
	}
	last := f.item(t, "alice", "Receipt 19", nil)
	ids = append(ids, f.startAsk(t, w, last.ID, "ticket-19"))
	f.status(t, ids[len(ids)-1], "waiting")
	f.coordinate(t, coord.ID)
	uploads := server.Uploads()
	if server.Creates() != 1 || len(uploads) != 1 || len(jsonlIDs(t, uploads[0])) != 20 {
		t.Fatalf("creates %d uploads %d", server.Creates(), len(uploads))
	}
	for i, id := range ids {
		text := "ticket-19"
		if i < 19 {
			text = fmt.Sprintf("ticket-%02d", i)
		} else if i == 19 {
			text = "ticket-00"
		}
		if got := f.answer(t, id); got != "answer:"+text {
			t.Fatalf("run %d answer %q", i, got)
		}
	}
	if ids[0] == ids[19] {
		t.Fatal("duplicate input reused one run")
	}
}

// The daily flush, in Europe/Madrid, sends a partial group and one missed
// flush after downtime. Requests from two workspaces share that provider
// batch. Neither workspace's run, nor the coordinator, carries the other
// workspace's prompt.
func TestBatchDailyFlushSendsAPartialAndKeepsWorkspacesApart(t *testing.T) {
	server, clock, tools := batchTools(t, false)
	f := setup(t, tools)
	f.searchOff(t)
	coord := f.enableBatch(t, map[string]any{"policy": "both", "flush_cron": "0 1 * * *", "time_zone": "Europe/Madrid"})
	askA := f.workflow(t, "Extract A", batchFlow(f.list.ID, "secret-a-plum", false))
	other, err := f.dms.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	f.disableSearch(t, other.ID)
	list, err := f.dms.Create(ctx, "alice", other.ID, dms.CreateResource{Kind: "list", Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	askB, err := f.wf.Create(ctx, "alice", other.ID, workflow.Save{Name: "Extract B", Definition: json.RawMessage(batchFlow(list.ID, "secret-b-cedar", false))})
	if err != nil {
		t.Fatal(err)
	}
	var aRuns, bRuns []string
	for _, text := range []string{"body-a1", "body-a2", "body-a3"} {
		item := f.item(t, "alice", text, nil)
		aRuns = append(aRuns, f.startAsk(t, askA, item.ID, text))
	}
	for _, text := range []string{"body-b1", "body-b2"} {
		item, err := f.dms.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: text})
		if err != nil {
			t.Fatal(err)
		}
		ids, err := f.wf.StartRuns(ctx, "alice", askB.ID, workflow.Start{ItemIDs: []string{item.ID}, Inputs: map[string]any{"text": text}})
		if err != nil {
			t.Fatal(err)
		}
		bRuns = append(bRuns, ids[0])
	}
	for _, id := range append(append([]string{}, aRuns...), bRuns...) {
		f.status(t, id, "waiting")
	}
	first := f.coordinate(t, coord.ID)
	if server.Creates() != 0 {
		t.Fatalf("a zero next flush submitted: %d", server.Creates())
	}
	if part := containsAny(runJSON(t, first), "secret-a-plum", "secret-b-cedar", "body-a1", "body-b1"); part != "" {
		t.Fatalf("coordinator exposed %s", part)
	}
	aWaits := f.openRequests(t, f.ws.ID)
	if len(aWaits) != len(aRuns) {
		t.Fatalf("workspace A waits: %+v", aWaits)
	}
	for _, w := range aWaits {
		for _, id := range bRuns {
			if w.RunID == id {
				t.Fatalf("workspace A listed %s", id)
			}
		}
		if part := containsAny(string(w.Data), "secret-a-plum", "secret-b-cedar", "body-a", "body-b"); part != "" {
			t.Fatalf("wait data exposed %s: %s", part, w.Data)
		}
	}
	if _, err = f.wf.ListWaits(ctx, "carol", workflow.WaitQuery{Kind: "ai.batch.request", Limit: 10}); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("installation list: %v", err)
	}
	due := f.nextFlush(t)
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 17, 1, 0, 0, 0, loc).UTC()
	if !due.Equal(want) {
		t.Fatalf("next flush %s, Madrid 01:00 is %s", due, want)
	}
	jumped := due.Add(26 * time.Hour)
	clock.set(jumped)
	second := f.coordinate(t, coord.ID)
	uploads := server.Uploads()
	lines := 0
	if len(uploads) == 1 {
		lines = len(jsonlIDs(t, uploads[0]))
	}
	if server.Creates() != 1 || lines != 5 {
		t.Fatalf("partial flush: creates %d uploads %d lines %d", server.Creates(), len(uploads), lines)
	}
	body := string(uploads[0])
	if !strings.Contains(body, "secret-a-plum") || !strings.Contains(body, "secret-b-cedar") {
		t.Fatal("the provider batch did not combine both workspaces")
	}
	if part := containsAny(runJSON(t, second), "secret-a-plum", "secret-b-cedar", "body-a1", "body-b1"); part != "" {
		t.Fatalf("coordinator exposed %s", part)
	}
	next := f.nextFlush(t)
	if !next.After(jumped) || next.Sub(jumped) > 24*time.Hour {
		t.Fatalf("missed flush caught up to %s from %s", next, jumped)
	}
	for i, id := range aRuns {
		got := f.answer(t, id)
		if got != "answer:"+[]string{"body-a1", "body-a2", "body-a3"}[i] || strings.Contains(got, "secret-b-cedar") || strings.Contains(got, "body-b") {
			t.Fatalf("A answer %q", got)
		}
	}
	for i, id := range bRuns {
		run := f.wait(t, id, settled)
		if run.Status != "completed" {
			t.Fatalf("B %s: %s", run.Status, run.Error)
		}
		got := shown(run.Result.(map[string]any)["variables"].(map[string]any)["answer"])
		if got != "answer:"+[]string{"body-b1", "body-b2"}[i] || strings.Contains(got, "secret-a-plum") || strings.Contains(got, "body-a") {
			t.Fatalf("B answer %q", got)
		}
	}
}

// Azure paths, an image, unordered lines, a retryable line failure, a schema
// rejection and an empty line go through one partial flush. Successful lines
// are not sent again. Exhausted retries follow the error path.
func TestBatchAzureImagesUnorderedPartialAndSchema(t *testing.T) {
	server, clock, tools := batchTools(t, true)
	f := setup(t, tools)
	f.searchOff(t)
	content := f.builtIn(t, "search.content", "")
	if _, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "search.content", workflow.SetBuiltIn{Enabled: false, Version: content.Version}); err != nil {
		t.Fatal(err)
	}
	coord := f.enableBatch(t, map[string]any{"policy": "both", "flush_cron": "* * * * *", "time_zone": "UTC"})
	textW := f.workflow(t, "Text", batchFlow(f.list.ID, "Extract", false))
	schemaW := f.workflow(t, "Schema", batchFlow(f.list.ID, "Extract", true))
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Scans", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	file := f.upload(t, lib.ID, "ticket.png", "image/png", png)
	imageW, err := f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{Name: "Image", Definition: json.RawMessage(fmt.Sprintf(`{"triggers":[{"type":"manual","collection_id":%q}],"input_schema":{"type":"object","required":["image"],"properties":{"image":{"type":"string"}}},"flow":{"start":"ask","nodes":{"ask":{"activity":"ai.complete","inputs":{"prompt":"Read the receipt","image":"{input:image}","execution":"batch","queue":"shared"},"next":{"done":"save","error":"bad"}},"save":{"activity":"set_variable","inputs":{"name":"answer","value":"{step:ask.text}"}},"bad":{"activity":"set_variable","inputs":{"name":"answer","value":"{step:ask.error}"}}}}}`, lib.ID))})
	if err != nil {
		t.Fatal(err)
	}
	alpha := f.startAsk(t, textW, f.item(t, "alice", "Alpha", nil).ID, "alpha-cedar")
	beta := f.startAsk(t, textW, f.item(t, "alice", "Beta", nil).ID, "beta-maple")
	failed := f.startAsk(t, textW, f.item(t, "alice", "Fail", nil).ID, "fail-line")
	empty := f.startAsk(t, textW, f.item(t, "alice", "Empty", nil).ID, "empty-line")
	rejected := f.startAsk(t, schemaW, f.item(t, "alice", "Reject", nil).ID, "reject-schema")
	total := f.startAsk(t, schemaW, f.item(t, "alice", "Total", nil).ID, "ok-total")
	imageIDs, err := f.wf.StartRuns(ctx, "alice", imageW.ID, workflow.Start{ItemIDs: []string{file.ID}, Inputs: map[string]any{"image": file.Blob.ID}})
	if err != nil {
		t.Fatal(err)
	}
	imageRun := imageIDs[0]
	for _, id := range []string{alpha, beta, failed, empty, rejected, total, imageRun} {
		f.status(t, id, "waiting")
	}
	f.coordinate(t, coord.ID)
	if server.Creates() != 0 {
		t.Fatalf("scheduled before the flush: %d", server.Creates())
	}
	clock.set(clock.get().Add(time.Minute))
	f.coordinate(t, coord.ID)
	uploads := server.Uploads()
	if server.Creates() != 1 || len(uploads) != 1 || len(jsonlIDs(t, uploads[0])) != 7 {
		t.Fatalf("flush: creates %d uploads %d", server.Creates(), len(uploads))
	}
	if !strings.Contains(string(uploads[0]), "image_url") || !strings.Contains(string(uploads[0]), "data:image/png;base64,") || !strings.Contains(string(uploads[0]), `"model":"gpt-batch"`) {
		t.Fatalf("image line: %s", uploads[0])
	}
	for _, call := range server.Calls() {
		if !strings.HasPrefix(call.Path, "/openai/v1/") || strings.Contains(call.Path, "/v1/v1/") {
			t.Fatalf("path %s", call.Path)
		}
	}
	if got := f.answer(t, alpha); got != "answer:alpha-cedar" {
		t.Fatalf("alpha %q", got)
	}
	if got := f.answer(t, beta); got != "answer:beta-maple" {
		t.Fatalf("beta %q", got)
	}
	if got := f.answer(t, total); got != "1.25" {
		t.Fatalf("total %q", got)
	}
	if got := f.answer(t, rejected); !strings.Contains(got, "output_schema") {
		t.Fatalf("schema rejection %q", got)
	}
	if got := f.answer(t, imageRun); !strings.Contains(got, "data:image/png;base64,") {
		t.Fatalf("image answer %q", got)
	}
	open := f.openRequests(t, "")
	if attemptsOf(t, open, failed) != 1 || attemptsOf(t, open, empty) != 1 {
		t.Fatalf("retries: %+v", open)
	}
	for n := 2; n <= 3; n++ {
		clock.set(clock.get().Add(time.Minute))
		f.coordinate(t, coord.ID)
		uploads = server.Uploads()
		last := string(uploads[len(uploads)-1])
		if strings.Contains(last, "alpha-cedar") || strings.Contains(last, "image_url") || strings.Contains(last, "reject-schema") {
			t.Fatalf("retry resent a finished line: %s", last)
		}
		if len(jsonlIDs(t, []byte(last))) != 2 {
			t.Fatalf("retry lines: %s", last)
		}
	}
	if server.Creates() != 3 {
		t.Fatalf("creates %d", server.Creates())
	}
	if got := f.answer(t, failed); !strings.Contains(got, "provider failed this line") {
		t.Fatalf("exhausted failure %q", got)
	}
	if got := f.answer(t, empty); !strings.Contains(got, "empty") {
		t.Fatalf("exhausted empty %q", got)
	}
	if len(f.openRequests(t, "")) != 0 {
		t.Fatal("requests still open")
	}
}

// An in-progress batch is polled by its stored id. The requests are not
// uploaded again.
func TestBatchPollDoesNotResubmit(t *testing.T) {
	server, clock, tools := batchTools(t, false)
	f := setup(t, tools)
	f.searchOff(t)
	coord := f.enableBatch(t, map[string]any{"policy": "both", "flush_cron": "* * * * *", "time_zone": "UTC"})
	w := f.workflow(t, "Extract", batchFlow(f.list.ID, "Extract", false))
	id := f.startAsk(t, w, f.item(t, "alice", "One", nil).ID, "held-ticket")
	f.status(t, id, "waiting")
	f.coordinate(t, coord.ID)
	server.Hold(1)
	clock.set(clock.get().Add(time.Minute))
	f.coordinate(t, coord.ID)
	if server.Creates() != 1 {
		t.Fatalf("creates %d", server.Creates())
	}
	f.status(t, id, "waiting")
	clock.set(clock.get().Add(6 * time.Minute))
	f.coordinate(t, coord.ID)
	if server.Creates() != 1 || len(server.Uploads()) != 1 {
		t.Fatalf("poll created again: creates %d uploads %d", server.Creates(), len(server.Uploads()))
	}
	if got := f.answer(t, id); got != "answer:held-ticket" {
		t.Fatalf("answer %q", got)
	}
}

// A create that does not confirm whether it landed is reconciled from
// provider metadata. A batch that did land is not created again. A batch
// that did not land is created on a later flush, not in the pass that
// observed its absence.
func TestBatchReconcilesAmbiguousCreate(t *testing.T) {
	t.Run("landed", func(t *testing.T) {
		reconcileAmbiguous(t, true, 1)
	})
	t.Run("missed", func(t *testing.T) {
		reconcileAmbiguous(t, false, 2)
	})
}

func reconcileAmbiguous(t *testing.T, land bool, wantCreates int) {
	t.Helper()
	server, clock, tools := batchTools(t, false)
	f := setup(t, tools)
	f.searchOff(t)
	coord := f.enableBatch(t, map[string]any{"policy": "both", "flush_cron": "* * * * *", "time_zone": "UTC"})
	w := f.workflow(t, "Extract", batchFlow(f.list.ID, "Extract", false))
	id := f.startAsk(t, w, f.item(t, "alice", "One", nil).ID, "ambiguous-ticket")
	f.status(t, id, "waiting")
	f.coordinate(t, coord.ID)
	server.FailCreates(1, land)
	clock.set(clock.get().Add(time.Minute))
	f.coordinate(t, coord.ID)
	if server.Creates() != 1 {
		t.Fatalf("creates %d", server.Creates())
	}
	f.status(t, id, "waiting")
	clock.set(clock.get().Add(time.Minute))
	f.coordinate(t, coord.ID)
	if server.Creates() != 1 {
		t.Fatalf("reconcile created again: %d", server.Creates())
	}
	if !land {
		f.status(t, id, "waiting")
		clock.set(clock.get().Add(time.Minute))
		f.coordinate(t, coord.ID)
	}
	if server.Creates() != wantCreates {
		t.Fatalf("creates %d, want %d", server.Creates(), wantCreates)
	}
	if got := f.answer(t, id); got != "answer:ambiguous-ticket" {
		t.Fatalf("answer %q", got)
	}
}

func TestBatchModeWithoutAProviderFollowsTheErrorPath(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	w := f.workflow(t, "Extract", batchFlow(f.list.ID, "Extract", false))
	id := f.startAsk(t, w, f.item(t, "alice", "One", nil).ID, "ticket")
	got := f.answer(t, id)
	if !strings.Contains(got, "batch execution needs a batch provider") {
		t.Fatalf("answer %q", got)
	}
}

func runJSON(t *testing.T, run Run) string {
	t.Helper()
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
