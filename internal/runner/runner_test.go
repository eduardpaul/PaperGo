package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"papergo/ent"
	"papergo/ent/domainevent"
	"papergo/ent/workflowrun"
	"papergo/ent/workflowtrigger"
	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/model"
	"papergo/internal/testutil"
	"papergo/internal/workflow"
)

type fixture struct {
	path string
	db   *database.Database
	dms  *dms.Service
	wf   *workflow.Service
	r    *Runner
	ws   *ent.Resource
	list *ent.Resource
}

var ctx = context.Background()

// open starts PaperGo with a launched runner on the database at path,
// creating the schema when the file is new.
func open(t testing.TB, path, node string) *fixture {
	t.Helper()
	var db *database.Database
	if _, err := os.Stat(path); err == nil {
		if db, err = database.Open(ctx, path); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
	} else {
		db = testutil.DatabaseAt(t, path)
	}
	f := &fixture{path: path, db: db, dms: dms.NewService(db.SQL)}
	f.wf = &workflow.Service{DMS: f.dms}
	var err error
	f.r, err = New(Config{DatabasePath: path, NodeID: node, PollInterval: 20 * time.Millisecond, TickSchedule: "0 0 0 1 1 *"}, db.SQL, f.dms, f.wf)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.r.Launch(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.r.Shutdown(10 * time.Second); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return f
}

func setup(t testing.TB) *fixture {
	t.Helper()
	f := open(t, filepath.Join(t.TempDir(), "papergo.db"), "node-a")
	var err error
	if f.ws, err = f.dms.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"}); err != nil {
		t.Fatal(err)
	}
	if f.list, err = f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "list", Name: "Contracts", PublishingEnabled: true}); err != nil {
		t.Fatal(err)
	}
	// bob may write content but does not manage the workspace.
	if f.ws, err = f.dms.SetPermissions(ctx, "alice", f.ws.ID, f.ws.Version, dms.Permissions{Grants: []dms.Permission{{Subject: "alice", Action: "manage", Effect: "allow"}, {Subject: "bob", Action: "write", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []dms.CreateField{
		{Key: "status", Label: "Status", Type: "text", Indexed: true},
		{Key: "note", Label: "Note", Type: "text"},
		{Key: "expires", Label: "Expires", Type: "date", Indexed: true},
		{Key: "total", Label: "Total", Type: "integer", Options: model.FieldOptions{}},
	} {
		if _, err = f.dms.CreateField(ctx, "alice", f.list.ID, field); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) workflow(t testing.TB, name string, def string) workflow.Workflow {
	t.Helper()
	def = strings.ReplaceAll(def, "$LIST", f.list.ID)
	w, err := f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{Name: name, Definition: json.RawMessage(def)})
	if err != nil {
		t.Fatalf("create workflow %s: %v", name, err)
	}
	return w
}

func (f *fixture) item(t testing.TB, subject, name string, values map[string]any) *ent.Resource {
	t.Helper()
	r, err := f.dms.Create(ctx, subject, f.list.ID, dms.CreateResource{Kind: "item", Name: name, Values: values})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// runs waits until the workflow has want finished runs and returns them.
func (f *fixture) runs(t testing.TB, workflowID string, want int) []Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		page, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{WorkflowID: workflowID, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		done := 0
		for _, run := range page.Data {
			if run.Status != "queued" && run.Status != "running" {
				done++
			}
		}
		if done >= want && len(page.Data) == want {
			return page.Data
		}
		if len(page.Data) > want || time.Now().After(deadline) {
			t.Fatalf("workflow %s: %d runs (%d finished), want %d: %+v", workflowID, len(page.Data), done, want, page.Data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *fixture) head(t testing.TB, id string) *ent.Resource {
	t.Helper()
	r, err := f.dms.GetSurface(ctx, "alice", id, "head")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestItemTriggerConditionAndChainedWorkflow(t *testing.T) {
	f := setup(t)
	mark := f.workflow(t, "Mark new contracts", `{
		"triggers": [{"type": "item.created", "collection_id": "$LIST"}],
		"condition": {"field": "status", "op": "eq", "value": "new"},
		"flow": {"start": "note", "nodes": {
			"note": {"activity": "item.update", "inputs": {"values": {"note": "seen {item:name} by {trigger:actor}"}}, "next": {"done": "tell"}},
			"tell": {"activity": "event.raise", "inputs": {"event": "marked", "data": {"by": "{trigger:actor}"}}}
		}}}`)
	chained := f.workflow(t, "Tag marked contracts", `{
		"triggers": [{"type": "wf.`+mark.Key+`.marked"}],
		"flow": {"start": "tag", "nodes": {"tag": {"activity": "item.update", "inputs": {"tags": ["marked", "{trigger:data.by}"]}}}}}`)

	fresh := f.item(t, "bob", "Lease", map[string]any{"status": "new"})
	f.item(t, "bob", "Archive", map[string]any{"status": "old"})
	runs := f.runs(t, mark.ID, 1)
	if runs[0].Status != "completed" || runs[0].ItemID == nil || *runs[0].ItemID != fresh.ID || runs[0].Actor != "bob" {
		t.Fatalf("run: %+v", runs[0])
	}
	f.runs(t, chained.ID, 1)
	item := f.head(t, fresh.ID)
	if item.Values["note"] != "seen Lease by bob" || strings.Join(item.Tags, ",") != "marked,bob" {
		t.Fatalf("item after workflows: %v %v", item.Values, item.Tags)
	}
	detail, err := f.r.Run(ctx, "alice", runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range detail.Steps {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "load,note,tell,finish" {
		t.Fatalf("steps: %v", names)
	}

	// A write that rolls back starts nothing.
	err = f.dms.Write(ctx, func(t *dms.Service) error {
		if _, err := t.Create(ctx, "bob", f.list.ID, dms.CreateResource{Kind: "item", Name: "Draft", Values: map[string]any{"status": "new"}}); err != nil {
			return err
		}
		return errors.New("abandon")
	})
	if err == nil {
		t.Fatal("write did not fail")
	}
	n, err := f.db.Client.WorkflowRun.Query().Where(workflowrun.WorkflowIDEQ(mark.ID)).Count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("rolled back write started runs: %d %v", n, err)
	}
	if n, err = f.db.Client.DomainEvent.Query().Where(domainevent.DispatchedAtIsNil()).Count(ctx); err != nil || n != 0 {
		t.Fatalf("rolled back write logged %d undispatched events: %v", n, err)
	}
	// Runs are for workspace managers.
	if _, err = f.r.Runs(ctx, "bob", f.ws.ID, RunFilter{}); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("bob listed runs: %v", err)
	}
}

func TestManualStartInputsAndBranches(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Set total", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"inputs": {"total": {"type": "integer", "required": true}, "label": {"type": "text", "default": "checked"}},
		"variables": {"limit": 100},
		"flow": {"start": "check", "nodes": {
			"check": {"activity": "if", "inputs": {"left": "{input:total}", "op": "gt", "right": "{var:limit}"}, "next": {"true": "big", "false": "small"}},
			"big": {"activity": "item.update", "inputs": {"values": {"total": "{input:total}", "note": "big {input:label}"}}},
			"small": {"activity": "set_variable", "inputs": {"name": "size", "value": "small"}, "next": {"done": "stop"}},
			"stop": {"activity": "end"}
		}}}`)
	a := f.item(t, "alice", "A", nil)
	b := f.item(t, "alice", "B", nil)
	if _, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{a.ID}}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing input: %v", err)
	}
	if _, err := f.wf.StartRuns(ctx, "carol", w.ID, workflow.Start{ItemIDs: []string{a.ID}, Inputs: map[string]any{"total": 1}}); err == nil {
		t.Fatal("a stranger started a run")
	}
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{a.ID, b.ID}, Inputs: map[string]any{"total": json.Number("250")}})
	if err != nil || len(ids) != 2 {
		t.Fatalf("start: %v %v", ids, err)
	}
	for _, run := range f.runs(t, w.ID, 2) {
		if run.Status != "completed" {
			t.Fatalf("big run: %+v", run)
		}
	}
	if v := f.head(t, a.ID).Values; fmt.Sprint(v["total"]) != "250" || v["note"] != "big checked" {
		t.Fatalf("big branch: %v", v)
	}
	ids, err = f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{a.ID}, Inputs: map[string]any{"total": 5}})
	if err != nil {
		t.Fatal(err)
	}
	f.runs(t, w.ID, 3)
	run, err := f.r.Run(ctx, "alice", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(run.Result)
	if run.Status != "completed" || !strings.Contains(string(raw), `"size":"small"`) || !strings.Contains(string(raw), `"node":"stop"`) {
		t.Fatalf("small branch: %s %s", run.Status, raw)
	}
}

func TestBuiltInExpiryOnSchedule(t *testing.T) {
	f := setup(t)
	old := f.item(t, "alice", "Old", map[string]any{"expires": "2020-01-01"})
	current := f.item(t, "alice", "Current", map[string]any{"expires": "2999-01-01"})
	draft := f.item(t, "alice", "Draft", map[string]any{"expires": "2020-01-01"})
	for _, r := range []*ent.Resource{old, current} {
		if _, err := f.dms.Publish(ctx, "alice", r.ID, r.Version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "items.expire", workflow.SetBuiltIn{CollectionID: f.list.ID, Enabled: true}); err == nil || !strings.Contains(err.Error(), "field") {
		t.Fatalf("missing parameter: %v", err)
	}
	w, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "items.expire", workflow.SetBuiltIn{CollectionID: f.list.ID, Enabled: true, Parameters: map[string]any{"field": "expires"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.wf.Update(ctx, "alice", w.ID, w.Version, workflow.Save{Name: "Mine", Definition: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("built-in definition was edited in place")
	}
	// Make the schedule due and tick.
	if _, err = f.db.Client.WorkflowTrigger.Update().Where(workflowtrigger.WorkflowIDEQ(w.ID)).SetNextAt(time.Now().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	raised, err := f.r.Tick(ctx)
	if err != nil || raised != 1 {
		t.Fatalf("tick raised %d: %v", raised, err)
	}
	if raised, err = f.r.Tick(ctx); err != nil || raised != 0 {
		t.Fatalf("second tick raised %d: %v", raised, err)
	}
	runs := f.runs(t, w.ID, 1)
	if runs[0].Status != "completed" || *runs[0].ItemID != old.ID {
		t.Fatalf("expiry run: %+v", runs[0])
	}
	if f.head(t, old.ID).PublishedRevisionID != nil || f.head(t, current.ID).PublishedRevisionID == nil || f.head(t, draft.ID).PublishedRevisionID != nil {
		t.Fatal("expiry unpublished the wrong items")
	}

	// Copying the built-in turns it off and gives an editable workflow.
	copied, err := f.wf.CopyBuiltIn(ctx, "alice", f.ws.ID, "items.expire", workflow.CopyBuiltIn{CollectionID: f.list.ID, Name: "Expire (custom)", Parameters: map[string]any{"field": "expires"}})
	if err != nil || copied.BuiltInKey != nil {
		t.Fatalf("copy: %+v %v", copied, err)
	}
	states, err := f.wf.ListBuiltIns(ctx, "alice", f.ws.ID)
	if err != nil || len(states) == 0 || len(states[0].Workflows) != 1 || states[0].Workflows[0].Enabled {
		t.Fatalf("built-in after copy: %+v %v", states, err)
	}
}

func TestLoopsStopAtMaxDepth(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Echo", `{
		"triggers": [{"type": "item.updated", "collection_id": "$LIST"}],
		"flow": {"start": "touch", "nodes": {"touch": {"activity": "item.update", "inputs": {"values": {"note": "{item:values.note}+"}}}}}}`)
	it := f.item(t, "alice", "Loop", nil)
	if _, err := f.dms.Update(ctx, "alice", it.ID, it.Version, dms.UpdateResource{Values: &map[string]any{"note": "x"}}); err != nil {
		t.Fatal(err)
	}
	runs := f.runs(t, w.ID, workflow.MaxDepth)
	for _, run := range runs {
		if run.Status != "completed" {
			t.Fatalf("run %+v", run)
		}
	}
	if note := f.head(t, it.ID).Values["note"]; note != "x"+strings.Repeat("+", workflow.MaxDepth) {
		t.Fatalf("note %v", note)
	}
}

func TestFailedRunRetriesFromFailedStep(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Stamp", `{
		"triggers": [{"type": "item.created", "collection_id": "$LIST"}],
		"flow": {"start": "read", "nodes": {
			"read": {"activity": "item.get", "next": {"done": "stamp"}},
			"stamp": {"activity": "item.update", "inputs": {"values": {"note": "stamped {step:read.name}"}}}
		}}}`)
	// bob manages the workspace and takes the list away from alice, the author.
	if _, err := f.dms.SetPermissions(ctx, "alice", f.ws.ID, f.ws.Version, dms.Permissions{Grants: []dms.Permission{{Subject: "alice", Action: "manage", Effect: "allow"}, {Subject: "bob", Action: "manage", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	list, err := f.dms.Get(ctx, "bob", f.list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if list, err = f.dms.SetPermissions(ctx, "bob", f.list.ID, list.Version, dms.Permissions{Grants: []dms.Permission{{Subject: "bob", Action: "manage", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	it := f.item(t, "bob", "Lease", nil)
	runs := f.runs(t, w.ID, 1)
	if runs[0].Status != "failed" || !strings.Contains(runs[0].Error, "read") {
		t.Fatalf("run without access: %+v", runs[0])
	}
	if _, err = f.r.Retry(ctx, "alice", runs[0].ID); err == nil {
		t.Log("retry before access returns a new failing run")
	}
	if _, err = f.dms.SetPermissions(ctx, "bob", f.list.ID, list.Version, dms.Permissions{Inherit: true, Grants: []dms.Permission{}}); err != nil {
		t.Fatal(err)
	}
	retried, err := f.r.Retry(ctx, "alice", runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for retried.Status == "queued" || retried.Status == "running" {
		if time.Now().After(deadline) {
			t.Fatal("retry did not finish")
		}
		time.Sleep(20 * time.Millisecond)
		if retried, err = f.r.Run(ctx, "alice", retried.ID); err != nil {
			t.Fatal(err)
		}
	}
	if retried.Status != "completed" || retried.RetryOf == nil || *retried.RetryOf != runs[0].ID {
		t.Fatalf("retried run: %+v", retried)
	}
	if note := f.head(t, it.ID).Values["note"]; note != "stamped Lease" {
		t.Fatalf("note %v", note)
	}
}

const crashEnv = "PAPERGO_RUNNER_CRASH"

// TestCrashChild is the process that dies in the middle of a run; the parent
// test starts it.
func TestCrashChild(t *testing.T) {
	spec := os.Getenv(crashEnv)
	if spec == "" {
		t.Skip("run by TestCrashRecovery")
	}
	parts := strings.Split(spec, "|")
	f := open(t, parts[0], "node-a")
	f.list = &ent.Resource{ID: parts[1]}
	f.ws = &ent.Resource{ID: parts[2]}
	f.item(t, "alice", "Crash", nil)
	// Die once the first node committed, while the run sleeps.
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var n int
		if err := f.db.SQL.QueryRow(`SELECT count(*) FROM runner_step_results WHERE step='stamp'`).Scan(&n); err == nil && n == 1 {
			os.Exit(3)
		}
	}
	t.Fatal("the first node never committed")
}

func TestCrashRecovery(t *testing.T) {
	if os.Getenv(crashEnv) != "" {
		t.Skip("child process")
	}
	path := filepath.Join(t.TempDir(), "papergo.db")
	f := open(t, path, "setup")
	var err error
	if f.ws, err = f.dms.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"}); err != nil {
		t.Fatal(err)
	}
	if f.list, err = f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "list", Name: "Contracts"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.CreateField(ctx, "alice", f.list.ID, dms.CreateField{Key: "note", Label: "Note", Type: "text"}); err != nil {
		t.Fatal(err)
	}
	w := f.workflow(t, "Stamp and wait", `{
		"triggers": [{"type": "item.created", "collection_id": "$LIST"}],
		"flow": {"start": "stamp", "nodes": {
			"stamp": {"activity": "item.update", "inputs": {"values": {"note": "stamped"}}, "next": {"done": "wait"}},
			"wait": {"activity": "delay", "inputs": {"duration": "2s"}, "next": {"done": "done"}},
			"done": {"activity": "item.update", "inputs": {"tags": ["recovered"]}}
		}}}`)
	if err = f.r.Shutdown(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	f.db.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s|%s|%s", crashEnv, path, f.list.ID, f.ws.ID))
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("child did not crash mid-run: %v\n%s", err, out)
	}

	// The same node relaunches and finishes the run without repeating the
	// committed node.
	g := open(t, path, "node-a")
	g.ws, g.list = f.ws, f.list
	runs := g.runs(t, w.ID, 1)
	if runs[0].Status != "completed" {
		t.Fatalf("recovered run: %+v", runs[0])
	}
	revisions, err := g.dms.Revisions(ctx, "alice", *runs[0].ItemID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	// create, stamp, tag: the stamp was not written twice.
	if len(revisions) != 3 {
		t.Fatalf("revisions after recovery: %d", len(revisions))
	}
}

// TestEventsLoggedWithoutRunnerStartLater shows that a write only logs its
// event: a process without a runner (another server) commits the change,
// and a runner that launches later dispatches it and runs the workflow.
func TestEventsLoggedWithoutRunnerStartLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	db := testutil.DatabaseAt(t, path)
	writer := dms.NewService(db.SQL)
	wf := &workflow.Service{DMS: writer}
	ws, err := writer.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := writer.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Inbox"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := wf.Create(ctx, "alice", ws.ID, workflow.Save{Name: "Tag", Definition: json.RawMessage(`{
		"triggers": [{"type": "item.created", "collection_id": "` + list.ID + `"}],
		"flow": {"start": "tag", "nodes": {"tag": {"activity": "item.update", "inputs": {"tags": ["seen"]}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	it, err := writer.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Letter"})
	if err != nil {
		t.Fatal(err)
	}
	// item.created, and item.published: the list publishes automatically.
	if n, err := db.Client.DomainEvent.Query().Where(domainevent.DispatchedAtIsNil()).Count(ctx); err != nil || n != 2 {
		t.Fatalf("logged events: %d %v", n, err)
	}
	f := open(t, path, "worker")
	f.ws, f.list = ws, list
	if runs := f.runs(t, w.ID, 1); runs[0].Status != "completed" {
		t.Fatalf("run: %+v", runs[0])
	}
	if tags := f.head(t, it.ID).Tags; strings.Join(tags, ",") != "seen" {
		t.Fatalf("tags %v", tags)
	}
}

// TestConditionErrorsFailVisibly: a condition that cannot be evaluated does
// not block dispatch; the run fails with the reason.
func TestConditionErrorsFailVisibly(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Needs status", `{
		"triggers": [{"type": "item.created", "collection_id": "$LIST"}],
		"condition": {"field": "status", "op": "eq", "value": "new"},
		"flow": {"start": "stop", "nodes": {"stop": {"activity": "end"}}}}`)
	fields, err := f.dms.Fields(ctx, "alice", f.list.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range fields {
		if d.Key == "status" {
			off := false
			list, err := f.dms.Get(ctx, "alice", f.list.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.dms.UpdateField(ctx, "alice", f.list.ID, d.ID, list.Version, dms.UpdateField{Indexed: &off}); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.item(t, "alice", "Odd", map[string]any{"status": "new"})
	runs := f.runs(t, w.ID, 1)
	if runs[0].Status != "failed" || !strings.Contains(runs[0].Error, "condition") {
		t.Fatalf("run: %+v", runs[0])
	}
	// Later events still dispatch.
	other := f.workflow(t, "Any item", `{"triggers": [{"type": "item.created", "collection_id": "$LIST"}], "flow": {"start": "stop", "nodes": {"stop": {"activity": "end"}}}}`)
	f.item(t, "alice", "Next", nil)
	f.runs(t, other.ID, 1)
}

func (f *fixture) wait(t testing.TB, id string, done func(Run) bool) Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		run, err := f.r.Run(ctx, "alice", id)
		if err != nil {
			t.Fatal(err)
		}
		if done(run) {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s: %+v", id, run)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func finished(run Run) bool { return run.Status != "queued" && run.Status != "running" }

func TestSelectionRuns(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Combine", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST", "selection": "selection"}],
		"flow": {"start": "primary", "nodes": {
			"primary": {"activity": "item.update", "inputs": {"tags": ["primary"]}, "next": {"done": "first"}},
			"first": {"activity": "item.update", "inputs": {"item_id": "{run:items.0}", "values": {"note": "first of {run:items}"}}}
		}}}`)
	a, b, c := f.item(t, "alice", "A", nil), f.item(t, "alice", "B", nil), f.item(t, "alice", "C", nil)
	other, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "list", Name: "Elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	stray, err := f.dms.Create(ctx, "alice", other.ID, dms.CreateResource{Kind: "item", Name: "Stray"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{a.ID, stray.ID}}); err == nil {
		t.Fatal("selection across collections")
	}
	if _, err = f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{a.ID}, PrimaryItemID: c.ID}); err == nil {
		t.Fatal("primary outside the selection")
	}
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{a.ID, b.ID, a.ID, c.ID}, PrimaryItemID: b.ID})
	if err != nil || len(ids) != 1 {
		t.Fatalf("start: %v %v", ids, err)
	}
	run := f.wait(t, ids[0], finished)
	if run.Status != "completed" || *run.ItemID != b.ID || strings.Join(run.Items, ",") != strings.Join([]string{a.ID, b.ID, c.ID}, ",") {
		t.Fatalf("selection run: %+v", run)
	}
	if tags := f.head(t, b.ID).Tags; strings.Join(tags, ",") != "primary" {
		t.Fatalf("primary tags %v", tags)
	}
	if note := f.head(t, a.ID).Values["note"]; note != "first of "+a.ID+", "+b.ID+", "+c.ID {
		t.Fatalf("note %v", note)
	}
	page, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{ItemID: c.ID})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != run.ID {
		t.Fatalf("runs of a member: %+v %v", page.Data, err)
	}

	// Per-item workflows take no primary item.
	each := f.workflow(t, "Each", `{"triggers": [{"type": "manual", "collection_id": "$LIST"}], "flow": {"start": "stop", "nodes": {"stop": {"activity": "end"}}}}`)
	if _, err = f.wf.StartRuns(ctx, "alice", each.ID, workflow.Start{ItemIDs: []string{a.ID}, PrimaryItemID: a.ID}); err == nil {
		t.Fatal("primary on a per-item workflow")
	}
	if ids, err = f.wf.StartRuns(ctx, "alice", each.ID, workflow.Start{ItemIDs: []string{a.ID, b.ID}}); err != nil || len(ids) != 2 {
		t.Fatalf("per-item start: %v %v", ids, err)
	}
}

func TestDeletingAMemberCancelsSelectionRuns(t *testing.T) {
	f := setup(t)
	slow := f.workflow(t, "Slow", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST", "selection": "selection"}],
		"flow": {"start": "wait", "nodes": {"wait": {"activity": "delay", "inputs": {"duration": "1h"}}}}}`)
	consume := f.workflow(t, "Consume", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST", "selection": "selection"}],
		"flow": {"start": "drop", "nodes": {
			"drop": {"activity": "item.delete", "inputs": {"item_id": "{run:items.1}"}, "next": {"done": "pause"}},
			"pause": {"activity": "delay", "inputs": {"duration": "500ms"}}
		}}}`)
	a, b, c := f.item(t, "alice", "A", nil), f.item(t, "alice", "B", nil), f.item(t, "alice", "C", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", slow.ID, workflow.Start{ItemIDs: []string{a.ID, b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, ids[0], func(r Run) bool { return r.Status == "running" })
	if err = f.dms.Delete(ctx, "alice", b.ID, f.head(t, b.ID).Version); err != nil {
		t.Fatal(err)
	}
	if run := f.wait(t, ids[0], finished); run.Status != "cancelled" {
		t.Fatalf("selection run after a member was deleted: %+v", run)
	}

	// A run that deletes its own member is not cancelled by it.
	ids, err = f.wf.StartRuns(ctx, "alice", consume.ID, workflow.Start{ItemIDs: []string{a.ID, c.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if run := f.wait(t, ids[0], finished); run.Status != "completed" {
		t.Fatalf("run that consumed its member: %+v", run)
	}
	if _, err = f.dms.Get(ctx, "alice", c.ID); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("consumed member: %v", err)
	}
}
