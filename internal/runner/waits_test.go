package runner

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/workflow"
)

func init() {
	workflow.RegisterActivity(workflow.ActivitySpec{
		Key:         "test.wait",
		Description: "Parks on a durable wait so tests can complete it, claim it, or let it time out.",
		Kind:        "action",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"key"},
			"properties": map[string]any{
				"key":      map[string]any{"type": "string"},
				"kind":     map[string]any{"type": "string"},
				"deadline": map[string]any{"type": "string"},
				"complete": map[string]any{"type": "boolean"},
				"data":     map[string]any{"type": "object"},
			},
		},
		Prepare: prepareTestWait,
	})
}

func prepareTestWait(s workflow.Step, in map[string]any) (workflow.Prepared, error) {
	kind := "test.wait"
	if raw, ok := in["kind"]; ok && raw != nil {
		text, err := s.Text(raw)
		if err != nil {
			return workflow.Prepared{}, err
		}
		if text != "" {
			kind = text
		}
	}
	key, err := s.Text(in["key"])
	if err != nil {
		return workflow.Prepared{}, err
	}
	var deadline time.Time
	if raw, ok := in["deadline"]; ok && raw != nil {
		text, err := s.Text(raw)
		if err != nil {
			return workflow.Prepared{}, err
		}
		if text != "" {
			deadline, err = time.Parse(time.RFC3339, text)
			if err != nil {
				return workflow.Prepared{}, err
			}
		}
	}
	var data json.RawMessage
	if raw, ok := in["data"]; ok && raw != nil {
		resolved, err := s.Resolve(raw)
		if err != nil {
			return workflow.Prepared{}, err
		}
		if data, err = json.Marshal(resolved); err != nil {
			return workflow.Prepared{}, err
		}
	}
	row := workflow.Wait{
		Kind: kind, Key: key, WorkspaceID: s.WorkspaceID(), RunID: s.RunID(),
		Node: s.Node(), ExecutionID: s.ExecutionID(), Data: data,
	}
	if !deadline.IsZero() {
		row.Deadline = &deadline
	}
	if err = workflow.PutWait(s.Context(), s.DMS(), row); err != nil {
		return workflow.Prepared{}, err
	}
	if raw, ok := in["complete"]; ok && raw != nil {
		resolved, err := s.Resolve(raw)
		if err != nil {
			return workflow.Prepared{}, err
		}
		if resolved == true {
			if err = workflow.CompleteWait(s.Context(), s.DMS(), kind, key, json.RawMessage(`{"ready":true}`)); err != nil {
				return workflow.Prepared{}, err
			}
		}
	}
	return workflow.Prepared{Park: &workflow.Park{Kind: kind, Key: key, Data: data, Deadline: deadline}}, nil
}

func (f *fixture) waitRow(t *testing.T, kind, key string) workflow.Wait {
	t.Helper()
	row, err := dms.Read(ctx, f.dms, func(t *dms.Service) (workflow.Wait, error) {
		return workflow.GetWait(ctx, t, kind, key)
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func (f *fixture) startWait(t *testing.T, w workflow.Workflow, itemID, key string) string {
	t.Helper()
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{itemID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	return ids[0]
}

func TestWaitCompletesBeforeTheRunParks(t *testing.T) {
	f := setup(t)
	item := f.item(t, "alice", "Letter", nil)
	def := `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "park", "nodes": {
			"park": {"activity": "test.wait", "inputs": {"key": "early", "complete": true}, "next": {"done": "mark"}},
			"mark": {"activity": "item.update", "inputs": {"values": {"note": "ready"}}}
		}}
	}`
	w := f.workflow(t, "Early", def)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "completed")
	if got := f.head(t, item.ID).Values["note"]; got != "ready" {
		t.Fatalf("note %v", got)
	}
	row := f.waitRow(t, "test.wait", "early")
	if row.Status != "completed" || row.Parking || string(row.Completion) != `{"output":{"ready":true}}` {
		t.Fatalf("wait %+v", row)
	}
}

func TestWaitDeadlineAlreadyPassedFollowsTheErrorPort(t *testing.T) {
	f := setup(t)
	item := f.item(t, "alice", "Letter", nil)
	def := `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "park", "nodes": {
			"park": {"activity": "test.wait", "inputs": {"key": "past", "deadline": "2000-01-01T00:00:00Z"}, "next": {"error": "fail"}},
			"fail": {"activity": "item.update", "inputs": {"values": {"note": "timed out"}}}
		}}
	}`
	w := f.workflow(t, "Past", def)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "completed")
	if got := f.head(t, item.ID).Values["note"]; got != "timed out" {
		t.Fatalf("note %v", got)
	}
	if row := f.waitRow(t, "test.wait", "past"); row.Status != "completed" || row.Parking {
		t.Fatalf("wait %+v", row)
	}
}

func TestParkedWaitExpiresAndResumesOnTheErrorPort(t *testing.T) {
	f := setup(t)
	item := f.item(t, "alice", "Letter", nil)
	deadline := time.Now().Add(3 * time.Second).UTC().Format(time.RFC3339)
	def := `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "park", "nodes": {
			"park": {"activity": "test.wait", "inputs": {"key": "soon", "deadline": "` + deadline + `"}, "next": {"error": "fail"}},
			"fail": {"activity": "item.update", "inputs": {"values": {"note": "timed out"}}}
		}}
	}`
	w := f.workflow(t, "Soon", def)
	id := f.startWait(t, w, item.ID, "soon")
	f.status(t, id, "completed")
	if got := f.head(t, item.ID).Values["note"]; got != "timed out" {
		t.Fatalf("note %v", got)
	}
	if row := f.waitRow(t, "test.wait", "soon"); row.Status != "completed" || row.Parking {
		t.Fatalf("wait %+v completion %s", row, row.Completion)
	}
}

func TestCancellingAWaitingRunClosesItsWaitAndReleasesClaims(t *testing.T) {
	f := setup(t)
	item := f.item(t, "alice", "Letter", nil)
	other := f.item(t, "alice", "Other", nil)
	own := f.workflow(t, "Own", waitFlowWith("owned"))
	held := f.workflow(t, "Held", waitFlowWith("held"))
	ownerID := f.startWait(t, own, item.ID, "owned")
	heldID := f.startWait(t, held, other.ID, "held")
	claimed, token, err := f.wf.ClaimWaitsFor(ctx, "alice", workflow.WaitClaim{
		Kind: "test.wait", Key: "held", WorkspaceID: f.ws.ID, Holder: ownerID, Limit: 1,
	})
	if err != nil || len(claimed) != 1 || token == "" || claimed[0].ClaimRunID == nil || *claimed[0].ClaimRunID != ownerID {
		t.Fatalf("claim %+v token %q err %v", claimed, token, err)
	}
	if _, err = f.r.Cancel(ctx, "alice", ownerID); err != nil {
		t.Fatal(err)
	}
	f.status(t, ownerID, "cancelled")
	if row := f.waitRow(t, "test.wait", "owned"); row.Status != "closed" || row.ClaimRunID != nil || row.Parking {
		t.Fatalf("own wait %+v", row)
	}
	// The cancelled run held this claim; the other run's wait stays open.
	if row := f.waitRow(t, "test.wait", "held"); row.Status != "open" || row.ClaimRunID != nil || row.ClaimToken != nil {
		t.Fatalf("held wait %+v", row)
	}
	f.status(t, heldID, "waiting")
}

func waitFlowWith(key string) string {
	return `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "park", "nodes": {
			"park": {"activity": "test.wait", "inputs": {"key": "` + key + `"}}
		}}
	}`
}

func TestWaitSurvivesRestartUntilItIsCompleted(t *testing.T) {
	path := t.TempDir()
	f := open(t, path+"/papergo.db", "node-a")
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
	item := f.item(t, "alice", "Letter", nil)
	def := `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "park", "nodes": {
			"park": {"activity": "test.wait", "inputs": {"key": "restart"}, "next": {"done": "mark"}},
			"mark": {"activity": "item.update", "inputs": {"values": {"note": "ready"}}}
		}}
	}`
	w := f.workflow(t, "Park", def)
	id := f.startWait(t, w, item.ID, "restart")
	if err = f.r.Shutdown(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	g := open(t, path+"/papergo.db", "node-a")
	g.ws, g.list = f.ws, f.list
	if run, err := g.r.Run(ctx, "alice", id); err != nil || run.Status != "waiting" {
		t.Fatalf("after restart: %+v %v", run, err)
	}
	if err = g.wf.FinishWait(ctx, "alice", g.ws.ID, "test.wait", "restart", workflow.Completion{Output: map[string]any{"ready": true}}); err != nil {
		t.Fatal(err)
	}
	g.status(t, id, "completed")
	if got := g.head(t, item.ID).Values["note"]; got != "ready" {
		t.Fatalf("note %v", got)
	}
}

func TestConcurrentClaimsTakeEachWaitOnce(t *testing.T) {
	f := setup(t)
	var keys []string
	for i := range 8 {
		key := "pool-" + string(rune('a'+i))
		keys = append(keys, key)
		flow := f.workflow(t, "Park "+key, waitFlowWith(key))
		item := f.item(t, "alice", key, nil)
		f.startWait(t, flow, item.ID, key)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[string]string{}
	claim := func(holder string) {
		defer wg.Done()
		rows, _, err := f.wf.ClaimWaitsFor(ctx, "alice", workflow.WaitClaim{
			Kind: "test.wait", WorkspaceID: f.ws.ID, Holder: holder, Limit: 100,
		})
		if err != nil {
			t.Errorf("claim %s: %v", holder, err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, row := range rows {
			if prev, ok := got[row.Key]; ok {
				t.Errorf("wait %s claimed by %s and %s", row.Key, prev, holder)
			}
			got[row.Key] = holder
		}
	}
	wg.Add(2)
	go claim("holder-a")
	go claim("holder-b")
	wg.Wait()
	if len(got) != len(keys) {
		t.Fatalf("claimed %v, want %v", got, keys)
	}
	// A third holder finds nothing left.
	left, _, err := f.wf.ClaimWaitsFor(ctx, "alice", workflow.WaitClaim{
		Kind: "test.wait", WorkspaceID: f.ws.ID, Holder: "holder-c", Limit: 100,
	})
	if err != nil || len(left) != 0 {
		t.Fatalf("third claim %+v %v", left, err)
	}
	token := ""
	rows, token, err := f.wf.ClaimWaitsFor(ctx, "alice", workflow.WaitClaim{
		Kind: "test.wait", Key: keys[0], WorkspaceID: f.ws.ID, Holder: got[keys[0]], Limit: 1,
	})
	if err != nil || len(rows) != 1 {
		t.Fatalf("reclaim by holder: %+v %v", rows, err)
	}
	if err = f.wf.UpdateWait(ctx, "alice", f.ws.ID, workflow.WaitUpdate{
		Kind: "test.wait", Key: keys[0], Data: json.RawMessage(`{"n":1}`), Holder: got[keys[0]], ClaimToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	if row := f.waitRow(t, "test.wait", keys[0]); string(row.Data) != `{"n":1}` {
		t.Fatalf("data %s", row.Data)
	}
	if err = f.wf.UpdateWait(ctx, "bob", f.ws.ID, workflow.WaitUpdate{
		Kind: "test.wait", Key: keys[0], Data: json.RawMessage(`{"n":2}`), Holder: got[keys[0]], ClaimToken: token,
	}); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("member update: %v", err)
	}
	page, err := f.wf.ListWaits(ctx, "alice", workflow.WaitQuery{Kind: "test.wait", WorkspaceID: f.ws.ID, Limit: 100})
	if err != nil || len(page.Data) != 8 {
		t.Fatalf("list %d %v", len(page.Data), err)
	}
	// A member is not told the workspace exists when manage is required.
	if _, err = f.wf.ListWaits(ctx, "bob", workflow.WaitQuery{Kind: "test.wait", WorkspaceID: f.ws.ID}); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("member list: %v", err)
	}
	if _, err = f.wf.ListWaits(ctx, "alice", workflow.WaitQuery{Kind: "test.wait"}); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("installation list without organization.manage: %v", err)
	}
	f.organizationManager(t, "alice")
	all, err := f.wf.ListWaits(ctx, "alice", workflow.WaitQuery{Kind: "test.wait", Limit: 100})
	if err != nil || len(all.Data) < 8 {
		t.Fatalf("installation list %d %v", len(all.Data), err)
	}
}

func TestWaitInsideALoopHasAStableExecutionPerIteration(t *testing.T) {
	f := setup(t)
	def := `{
		"triggers": [{"type": "manual"}],
		"flow": {"start": "each", "nodes": {
			"each": {"activity": "forEach", "inputs": {"items": ["a", "b"], "as": "k"}, "next": {"body": "park"}},
			"park": {"activity": "test.wait", "inputs": {"key": "loop-{var:k}"}}
		}}
	}`
	w := f.workflow(t, "Loop", def)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	first := f.waitRow(t, "test.wait", "loop-a")
	if first.Status != "open" {
		t.Fatalf("first %+v", first)
	}
	if err = f.wf.FinishWait(ctx, "alice", f.ws.ID, "test.wait", "loop-a", workflow.Completion{Output: map[string]any{"ready": true}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var second workflow.Wait
	for {
		second, err = dms.Read(ctx, f.dms, func(t *dms.Service) (workflow.Wait, error) {
			return workflow.GetWait(ctx, t, "test.wait", "loop-b")
		})
		if err == nil && second.Status == "open" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("second wait: %+v %v", second, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if first.ExecutionID == "" || first.ExecutionID == second.ExecutionID {
		t.Fatalf("execution ids %s %s", first.ExecutionID, second.ExecutionID)
	}
	if err = f.wf.FinishWait(ctx, "alice", f.ws.ID, "test.wait", "loop-b", workflow.Completion{Output: map[string]any{"ready": true}}); err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "completed")
	// Replaying the recorded steps kept each iteration's execution id.
	if again := f.waitRow(t, "test.wait", "loop-a"); again.ExecutionID != first.ExecutionID {
		t.Fatalf("execution changed from %s to %s", first.ExecutionID, again.ExecutionID)
	}
}

func TestWaitingGenericRunsHoldNoWorker(t *testing.T) {
	f := setup(t)
	var waiting []string
	for i := range 6 {
		key := "worker-" + string(rune('a'+i))
		w := f.workflow(t, "Park "+key, waitFlowWith(key))
		item := f.item(t, "alice", key, nil)
		waiting = append(waiting, f.startWait(t, w, item.ID, key))
	}
	quick := f.workflow(t, "Quick", `{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`)
	ids, err := f.wf.StartRuns(ctx, "alice", quick.ID, workflow.Start{})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "completed")
	for _, id := range waiting {
		f.status(t, id, "waiting")
	}
}

func TestPurgeDeletesFinishedWaitsAndKeepsParkedOnes(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	f.r.cfg.Retention = time.Millisecond
	item := f.item(t, "alice", "Letter", nil)
	early := f.workflow(t, "Early", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "park", "nodes": {
			"park": {"activity": "test.wait", "inputs": {"key": "gone", "complete": true}}
		}}
	}`)
	ids, err := f.wf.StartRuns(ctx, "alice", early.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "completed")
	parked := f.workflow(t, "Parked", waitFlowWith("kept"))
	kept := f.startWait(t, parked, item.ID, "kept")
	time.Sleep(20 * time.Millisecond)
	if _, err = f.r.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	f.status(t, kept, "waiting")
	if _, err = f.r.Run(ctx, "alice", ids[0]); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("finished run kept: %v", err)
	}
	if _, err = dms.Read(ctx, f.dms, func(t *dms.Service) (workflow.Wait, error) {
		return workflow.GetWait(ctx, t, "test.wait", "gone")
	}); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("finished wait kept: %v", err)
	}
	if row := f.waitRow(t, "test.wait", "kept"); row.Status != "open" {
		t.Fatalf("parked wait %+v", row)
	}
}
