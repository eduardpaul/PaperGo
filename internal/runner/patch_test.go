package runner

import (
	"testing"

	"papergo/internal/workflow"
)

func TestWorkflowItemPatchRemovesNullAndRetainsOmittedFields(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Clear note", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "clear", "nodes": {"clear": {"activity": "item.update", "inputs": {"values": {"note": null}}}}}}`)
	item := f.item(t, "alice", "Item", map[string]any{"status": "new", "note": "remove"})
	if _, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}}); err != nil {
		t.Fatal(err)
	}
	runs := f.runs(t, w.ID, 1)
	if runs[0].Status != "completed" {
		t.Fatal(runs)
	}
	values := f.head(t, item.ID).Values
	if values["status"] != "new" {
		t.Fatal("omitted field changed", values)
	}
	if _, present := values["note"]; present {
		t.Fatal("null did not remove value", values)
	}
}
