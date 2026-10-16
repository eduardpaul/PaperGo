package runner

import (
	"strings"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/workflow"
)

func TestRelationshipActionsListCreateAndRemoveWithoutDuplicates(t *testing.T) {
	f := setup(t)
	source := f.item(t, "alice", "Receipt", nil)
	target := f.item(t, "alice", "Line", nil)
	def := strings.ReplaceAll(`{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "link", "nodes": {
			"link": {"activity": "relationship.create", "inputs": {"target_id": "$TARGET", "directed": true}, "next": {"done": "list"}},
			"list": {"activity": "relationship.list", "inputs": {"direction": "outgoing", "limit": 10}}
		}}
	}`, "$TARGET", target.ID)
	w := f.workflow(t, "Link", def)
	for range 2 {
		ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{source.ID}})
		if err != nil {
			t.Fatal(err)
		}
		run := f.status(t, ids[0], "completed")
		if run.Error != "" {
			t.Fatal(run.Error)
		}
	}
	page, err := f.dms.Relationships(ctx, "alice", source.ID, "outgoing", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].TargetID != target.ID || !page.Data[0].Directed {
		t.Fatalf("relationships %+v", page.Data)
	}
	id := page.Data[0].ID
	remove := strings.ReplaceAll(`{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "drop", "nodes": {
			"drop": {"activity": "relationship.remove", "inputs": {"relationship_id": "$ID"}}
		}}
	}`, "$ID", id)
	drop := f.workflow(t, "Unlink", remove)
	for range 2 {
		ids, err := f.wf.StartRuns(ctx, "alice", drop.ID, workflow.Start{ItemIDs: []string{source.ID}})
		if err != nil {
			t.Fatal(err)
		}
		f.status(t, ids[0], "completed")
	}
	page, err = f.dms.Relationships(ctx, "alice", source.ID, "both", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("relationships after removal %+v", page.Data)
	}
	// A reader who cannot write the items cannot link them.
	if _, err = f.dms.Link(ctx, "carol", source.ID, dms.CreateRelationship{TargetID: target.ID, Directed: &[]bool{true}[0]}); err == nil {
		t.Fatal("carol linked items")
	}
}
