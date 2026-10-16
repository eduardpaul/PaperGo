package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"papergo/internal/decision"
	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/identity"
	"papergo/internal/resultcache"
	"papergo/internal/storage"
	"papergo/internal/testutil"
)

type testDecisionModel struct {
	calls atomic.Int32
	run   func([]decision.Request) ([]decision.Result, error)
}

func (*testDecisionModel) ID() string { return "test-laya-identity" }
func (m *testDecisionModel) Decide(_ context.Context, r []decision.Request) ([]decision.Result, error) {
	m.calls.Add(1)
	if m.run != nil {
		return m.run(r)
	}
	out := make([]decision.Result, len(r))
	for i, row := range r {
		p := make([]float64, len(row.Options))
		p[len(p)-1] = 1
		out[i].Probabilities = p
	}
	return out, nil
}
func decisionEnv(t *testing.T) (*env, *workEnv, *testDecisionModel) {
	t.Helper()
	model := &testDecisionModel{}
	tools := Tools{Decisions: model, Results: resultcache.New(testutil.Database(t).SQL), ResultTTL: time.Hour}
	ctx := context.Background()
	return &env{ctx: ctx, scope: &scope{ctx: ctx, inputs: map[string]any{}}, workspaceID: "workspace-a", tools: tools}, &workEnv{ctx: ctx, tools: tools}, model
}
func decisionInput() map[string]any {
	return map[string]any{"input": "An invoice", "questions": []any{map[string]any{"key": "route", "instructions": "Which category?", "options": []any{map[string]any{"key": "other", "text": "Other document"}, map[string]any{"key": "billing", "text": "Billing"}}}}}
}
func TestDecisionCustomDataTokensCacheAndIsolation(t *testing.T) {
	e, w, model := decisionEnv(t)
	e.scope.inputs = map[string]any{"payload": map[string]any{"literal": "{input:secret}", "amount": json.Number("9007199254740993"), "on": false}, "secret": "DO NOT EXPAND"}
	in := decisionInput()
	in["item_id"] = "{item:id}"
	delete(in, "input")
	in["data"] = map[string]any{"request": "{input:payload}", "zero": json.Number("0")}
	model.run = func(rows []decision.Request) ([]decision.Result, error) {
		if rows[0].State != `{"request":{"amount":9007199254740993,"literal":"{input:secret}","on":false},"zero":0}` {
			t.Fatal(rows[0].State)
		}
		return []decision.Result{{Probabilities: []float64{.1, .9}}}, nil
	}
	prep, err := runDecision(e, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		raw, err := workDecision(w, prep.Job)
		if err != nil {
			t.Fatal(err)
		}
		out, err := commitDecision(e, prep.Job, raw)
		if err != nil {
			t.Fatal(err)
		}
		got := out.Output.(map[string]any)
		if got["cached"] != cached || out.Port != "done" {
			t.Fatal(got)
		}
		if got["answers"].(map[string]decision.Answer)["route"].Selected != "billing" {
			t.Fatal(got)
		}
	}
	e.workspaceID = "workspace-b"
	prep, err = runDecision(e, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workDecision(w, prep.Job); err != nil {
		t.Fatal(err)
	}
	if model.calls.Load() != 2 {
		t.Fatal("workspace cache isolation", model.calls.Load())
	}
}
func TestDecisionLimitsAndMalformedEngineResults(t *testing.T) {
	e, w, model := decisionEnv(t)
	for _, in := range []map[string]any{{"data": map[string]any{}, "questions": decisionInput()["questions"]}, {"input": "x", "data": map[string]any{"x": false}, "questions": decisionInput()["questions"]}, {"input": "x", "context": map[string]any{}, "questions": decisionInput()["questions"]}, {"input": strings.Repeat("x", 16385), "questions": decisionInput()["questions"]}, {"text_id": "t", "questions": decisionInput()["questions"]}} {
		if _, err := runDecision(e, in); err == nil {
			t.Fatal("accepted invalid source")
		}
	}
	model.run = func([]decision.Request) ([]decision.Result, error) {
		return []decision.Result{{Probabilities: []float64{.1, .1}}}, nil
	}
	prep, err := runDecision(e, decisionInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workDecision(w, prep.Job); err == nil {
		t.Fatal("accepted invalid distribution")
	}
	model.run = nil
	raw, err := workDecision(w, prep.Job)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(e.ctx)
	cancel()
	e.ctx = canceled
	if _, err = commitDecision(e, prep.Job, raw); err == nil {
		t.Fatal("published canceled result")
	}
}

func TestDecisionJobModelAndCacheWinnerValidation(t *testing.T) {
	e, w, _ := decisionEnv(t)
	prep, err := runDecision(e, decisionInput())
	if err != nil {
		t.Fatal(err)
	}
	var job decisionJob
	if err = decisionDecode(prep.Job, &job); err != nil {
		t.Fatal(err)
	}
	// A conflicting writer's winning distribution is returned consistently.
	winner := decisionResult{InputHash: hashText(job.Input), Probabilities: [][]float64{{.7, .3}}}
	payload, _ := json.Marshal(winner)
	if _, err = w.tools.Results.PutIfAbsent(e.ctx, job.Namespace, job.Workspace, job.Key, payload, time.Hour); err != nil {
		t.Fatal(err)
	}
	raw, err := workDecision(w, prep.Job)
	if err != nil {
		t.Fatal(err)
	}
	out, err := commitDecision(e, prep.Job, raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Output.(map[string]any)["answers"].(map[string]decision.Answer)["route"].Selected != "other" {
		t.Fatal(out)
	}
	job.Model = "different-engine"
	changed, _ := json.Marshal(job)
	if _, err = workDecision(w, changed); err == nil {
		t.Fatal("used cache after model change")
	}
	if _, err = commitDecision(e, changed, raw); err == nil {
		t.Fatal("committed with changed model")
	}
}

func TestTaxonomyThresholdCacheAndSnapshotCommit(t *testing.T) {
	e, w, model := decisionEnv(t)
	service := dms.NewService(testutil.Database(t).SQL)
	e.t = service
	e.author = "alice"
	group, err := service.CreateTermGroup(e.ctx, "alice", dms.TermGroupInput{Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := service.CreateTermSet(e.ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	term, err := service.CreateTerm(e.ctx, "alice", set.ID, dms.TermInput{Name: "Invoices", Description: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"input": "An invoice", "term_set_id": set.ID, "threshold": .8}
	model.run = func([]decision.Request) ([]decision.Result, error) {
		return []decision.Result{{Probabilities: []float64{.2, .8}}}, nil
	}
	first, err := runTaxonomyDecision(e, in)
	if err != nil {
		t.Fatal(err)
	}
	result, err := workDecision(w, first.Job)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := commitDecision(e, first.Job, result)
	if err != nil || accepted.Port != "done" {
		t.Fatal(accepted, err)
	}
	in["threshold"] = .9
	second, err := runTaxonomyDecision(e, in)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := workDecision(w, second.Job)
	if err != nil {
		t.Fatal(err)
	}
	none, err := commitDecision(e, second.Job, cached)
	if err != nil || none.Port != "none" || none.Output.(map[string]any)["cached"] != true {
		t.Fatal(none, err)
	}
	if model.calls.Load() != 1 {
		t.Fatal("threshold changed inference key")
	}
	if _, err = service.UpdateTerm(e.ctx, "alice", term.ID, term.Version, dms.TermInput{Name: term.Name, Description: "Revised invoices"}); err != nil {
		t.Fatal(err)
	}
	if _, err = commitDecision(e, first.Job, result); !errors.Is(err, dms.ErrPreconditionFailed) {
		t.Fatal("published changed taxonomy", err)
	}
	third, err := runTaxonomyDecision(e, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workDecision(w, third.Job); err != nil {
		t.Fatal(err)
	}
	if model.calls.Load() != 2 {
		t.Fatal("taxonomy did not invalidate cache")
	}
	// Generic single category: unmatched is last and winning terms still use an array.
	in["selection"] = "single"
	in["threshold"] = .8
	model.run = func([]decision.Request) ([]decision.Result, error) {
		return []decision.Result{{Probabilities: []float64{.9, .1}}}, nil
	}
	single, err := runTaxonomyDecision(e, in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workDecision(w, single.Job)
	if err != nil {
		t.Fatal(err)
	}
	out, err := commitDecision(e, single.Job, raw)
	if err != nil {
		t.Fatal(err)
	}
	ids := out.Output.(map[string]any)["term_ids"].([]string)
	if len(ids) != 1 || ids[0] != term.ID || out.Output.(map[string]any)["other_probability"] != .1 {
		t.Fatal(out)
	}
}
func TestDecisionConcurrentCacheCheckAfterGate(t *testing.T) {
	e, w, model := decisionEnv(t)
	prep, err := runDecision(e, decisionInput())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan json.RawMessage, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { r, err := workDecision(w, prep.Job); results <- r; errs <- err })
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for raw := range results {
		out, err := commitDecision(e, prep.Job, raw)
		if err != nil {
			t.Fatal(err)
		}
		if out.Output.(map[string]any)["answers"].(map[string]decision.Answer)["route"].Selected != "billing" {
			t.Fatal(out)
		}
	}
	if model.calls.Load() != 1 {
		t.Fatal("gate did not recheck cache", model.calls.Load())
	}
}

func TestTaxonomyCommitReauthorizesCachedResults(t *testing.T) {
	e, w, _ := decisionEnv(t)
	service := dms.NewService(testutil.Database(t).SQL)
	e.t = service
	e.author = "bob"
	group, err := service.CreateTermGroup(e.ctx, "alice", dms.TermGroupInput{Name: "Shared"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := service.CreateTermSet(e.ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "shared", Name: "Shared"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateTerm(e.ctx, "alice", set.ID, dms.TermInput{Name: "Invoices"}); err != nil {
		t.Fatal(err)
	}
	prep, err := runTaxonomyDecision(e, map[string]any{"input": "invoice", "term_set_id": set.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workDecision(w, prep.Job); err != nil {
		t.Fatal(err)
	}
	cached, err := workDecision(w, prep.Job)
	if err != nil {
		t.Fatal(err)
	}
	directory := service.Directory()
	if err = directory.SetAssignment(e.ctx, "alice", identity.MemberRole, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = commitDecision(e, prep.Job, cached); !errors.Is(err, dms.ErrForbidden) {
		t.Fatal("cached result bypassed taxonomy scope", err)
	}
}
func TestDocumentDecisionSourceOwnershipAndStaleCache(t *testing.T) {
	e, w, model := decisionEnv(t)
	model.run = func(rows []decision.Request) ([]decision.Result, error) {
		if rows[0].State != `{"context":{},"document_text":"Invoice A {input:secret}"}` {
			t.Fatal("document/context serialization", rows[0].State)
		}
		return []decision.Result{{Probabilities: []float64{.1, .9}}}, nil
	}
	service := dms.NewService(testutil.Database(t).SQL)
	e.t = service
	e.author = "alice"
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.tools.Storage = store
	w.tools.Storage = store
	workspace, err := service.Create(e.ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	e.workspaceID = workspace.ID
	lib, err := service.Create(e.ctx, "alice", workspace.ID, dms.CreateResource{Kind: "library", Name: "Files", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	put := func(name, content string) *dms.File {
		t.Helper()
		obj, err := store.Put(e.ctx, strings.NewReader(content), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		f, _, err := service.PutFile(e.ctx, "alice", lib.ID, []string{name}, dms.PutFile{Blob: dms.BlobInput{ObjectKey: obj.Key, Filename: name, ContentType: "application/pdf", SHA256: obj.SHA256, Size: obj.Size}})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	// PutFile validates document formats. Use real PDF containers even though this
	// test supplies the immutable extracted-text object directly.
	first := put("first.pdf", string(doctexttest.TextPDF("Invoice A")))
	second := put("second.pdf", string(doctexttest.TextPDF("Invoice B")))
	text := `{"pages":[{"text":"Invoice A {input:secret}","page":1}]}`
	object, err := store.Put(e.ctx, strings.NewReader(text), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	row, err := service.SaveBlobText(e.ctx, dms.BlobTextInput{SHA256: first.Blob.Sha256, Settings: "test", ObjectKey: object.Key, ObjectSize: object.Size, ObjectSHA256: object.SHA256, Pages: 1, Chars: 26, Extractor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	in := decisionInput()
	delete(in, "input")
	in["context"] = map[string]any{}
	in["text_id"] = row.ID
	in["blob_id"] = first.Blob.ID
	in["item_id"] = first.ID
	prep, err := runDecision(e, in)
	if err != nil {
		t.Fatal(err)
	}
	in["blob_id"] = second.Blob.ID
	in["item_id"] = second.ID
	if _, err = runDecision(e, in); err == nil {
		t.Fatal("accepted text of another blob")
	}
	if _, err = workDecision(w, prep.Job); err != nil {
		t.Fatal(err)
	}
	cached, err := workDecision(w, prep.Job)
	if err != nil {
		t.Fatal(err)
	}
	put("first.pdf", string(doctexttest.TextPDF("Changed file")))
	if _, err = commitDecision(e, prep.Job, cached); !errors.Is(err, dms.ErrPreconditionFailed) {
		t.Fatal("published stale cached source", err)
	}
}

func TestDecisionStaticValidationWithDynamicSiblings(t *testing.T) {
	input := decisionInput()
	input["input"] = "{input:body}"
	questions := input["questions"].([]any)
	question := questions[0].(map[string]any)
	question["instructions"] = "{input:instructions}"
	if err := activities["ai.decide"].Validate(input); err != nil {
		t.Fatal(err)
	}
	question["unknown"] = true
	if err := activities["ai.decide"].Validate(input); err == nil {
		t.Fatal("static unknown property hidden by token")
	}
	delete(question, "unknown")
	delete(question, "key")
	if err := activities["ai.decide"].Validate(input); err == nil {
		t.Fatal("missing question key hidden by token")
	}
}
