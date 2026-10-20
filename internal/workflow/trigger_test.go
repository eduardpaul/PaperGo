package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"papergo/internal/dal/daltest"
	eventstore "papergo/internal/dal/events"
	"papergo/internal/dms"
)

func TestManualTriggerCapturesFormAndOrderedSelection(t *testing.T) {
	for _, mode := range []string{"standalone", "single", "selection", "test"} {
		t.Run(mode, func(t *testing.T) {
			s, ws, lib, _, item := policyFixture(t)
			ctx := t.Context()
			other, err := s.DMS.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Second"})
			if err != nil {
				t.Fatal(err)
			}
			trigger := `{type:"manual"}`
			ids := []string{}
			primary := ""
			switch mode {
			case "single":
				trigger = `{type:"manual",selection:"per_item"}`
				ids = []string{item.ID}
			case "selection", "test":
				trigger = `{type:"manual",selection:"selection"}`
				ids = []string{other.ID, item.ID}
				primary = item.ID
			}
			w, err := s.Create(ctx, "alice", ws.ID, Save{Name: "Form selection", Source: `import { workflow } from "@papergo/workflow";
export default workflow<{note: string}>({
  triggers: [` + trigger + `],
  input_schema: {type:"object",required:["note"],properties:{note:{type:"string"}}},
  run(ctx, input) { return input; }
});`})
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"note": "Submitted value"}
			var runIDs []string
			if mode == "test" {
				id, err := s.StartTest(ctx, "alice", w.ID, TestRun{Inputs: input, ItemIDs: ids, PrimaryItemID: primary})
				if err != nil {
					t.Fatal(err)
				}
				runIDs = []string{id}
			} else {
				runIDs, err = s.StartRuns(ctx, "alice", w.ID, Start{WorkflowVersion: 1, Inputs: input, ItemIDs: ids, PrimaryItemID: primary})
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(runIDs) != 1 {
				t.Fatal(runIDs)
			}
			event, err := s.DMS.DAL.Events().Find(ctx, strings.TrimPrefix(runIDs[0], "wf:"+w.ID+":"))
			if err != nil {
				t.Fatal(err)
			}
			started(t, s, []dms.Event{{ID: event.ID, Type: event.Type, WorkspaceID: event.WorkspaceID, ResourceID: event.ResourceID, CollectionID: event.CollectionID, Actor: event.Actor, Data: event.Data}})
			op, err := s.DMS.DAL.Operations().Find(ctx, runIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			var p RunPayload
			if err := json.Unmarshal(op.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if p.StartError != "" || p.Trigger == nil || p.Trigger.Kind != TriggerManual || !reflect.DeepEqual(p.Input, input) || len(p.Trigger.Items) != len(ids) {
				t.Fatalf("manual context lost form/selection: %+v", p)
			}
			if _, duplicated := p.Event.Data["inputs"]; duplicated {
				t.Fatal("form results duplicated in event metadata")
			}
			for i, selected := range p.Trigger.Items {
				if selected.ID != ids[i] || selected.Snapshot == nil || selected.Snapshot.ID != ids[i] {
					t.Fatalf("selection order/content changed: %+v", p.Trigger.Items)
				}
			}
			if primary != "" && p.ItemID != primary {
				t.Fatal("primary item changed", p.ItemID)
			}
		})
	}
}

func TestManualSelectionSharesSnapshotBudgetAndPreservesUnavailableIDs(t *testing.T) {
	s, ws, lib, _, item := policyFixture(t)
	ctx := t.Context()
	other, err := s.DMS.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"text": strings.Repeat("x", 40<<10)})
	for _, revision := range []string{*item.HeadRevisionID, *other.HeadRevisionID} {
		daltest.Update(t, s.DMS.DAL, "item_revisions", daltest.Row{"payload": string(payload)}, daltest.Row{"id": revision})
	}
	got, err := triggerObject(ctx, s.DMS, "alice", dms.Event{Type: TriggerManual, WorkspaceID: ws.ID, ResourceID: item.ID,
		Data: map[string]any{"items": []any{other.ID, "deleted-item", item.ID}}})
	if err != nil || got == nil || len(got.Items) != 3 {
		t.Fatal(got, err)
	}
	if got.Items[1].ID != "deleted-item" || got.Items[1].Snapshot != nil {
		t.Fatal("unavailable selection disappeared", got.Items)
	}
	for _, i := range []int{0, 2} {
		if got.Items[i].Snapshot == nil || got.Items[i].Snapshot.Values != nil || !got.Items[i].Snapshot.ValuesOmitted {
			t.Fatal("selection exceeded the shared values budget", got.Items)
		}
	}
	raw, _ := json.Marshal(got)
	if len(raw) > MaxTriggerBytes {
		t.Fatal("selection exceeds budget", len(raw))
	}
}

func triggerEvents(t *testing.T, s *Service, id, typ string) []dms.Event {
	t.Helper()
	rows, err := s.DMS.DAL.Events().List(t.Context(), eventstore.Filter{ResourceID: id, Types: []string{typ}})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]dms.Event, len(rows))
	for i, e := range rows {
		out[i] = dms.Event{ID: e.ID, Type: e.Type, WorkspaceID: e.WorkspaceID, CollectionID: e.CollectionID, ResourceID: e.ResourceID, Actor: e.Actor, Data: e.Data}
	}
	return out
}

func TestTriggerSnapshotPinsEventRevisionAndPersists(t *testing.T) {
	s, ws, lib, _, item := policyFixture(t)
	ctx := t.Context()
	_, err := s.Create(ctx, "alice", ws.ID, Save{Name: "Snapshot", AutomaticEnabled: new(true), Source: source(`[{type:"item.updated",collection_id:"`+lib.ID+`"}]`, "snapshot")})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"First", "Second"} {
		item, err = s.DMS.Update(ctx, "alice", item.ID, item.Version, dms.UpdateResource{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
	}
	events := triggerEvents(t, s, item.ID, dms.EventItemUpdated)
	runs := started(t, s, events)
	if len(runs) != 2 {
		t.Fatal(runs)
	}
	for i, run := range runs {
		op, err := s.DMS.DAL.Operations().Find(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		var p RunPayload
		if err = json.Unmarshal(op.Payload, &p); err != nil {
			t.Fatal(err)
		}
		want := []string{"First", "Second"}[i]
		if p.Trigger == nil || p.Trigger.Kind != "item" || p.Trigger.Item.Name != want || p.Trigger.Item.RevisionID != events[i].Data["revision_id"] {
			t.Fatalf("event %d lost its revision: %+v", i, p.Trigger)
		}
		if p.Context()["trigger"] != p.Trigger || string(p.Trigger.Item.Values) != "{}" {
			t.Fatal("snapshot not exposed as plain context content", p.Context())
		}
		// A resume uses this payload, even after the live head has changed.
		name := "After dispatch"
		item, err = s.DMS.Update(ctx, "alice", item.ID, item.Version, dms.UpdateResource{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
		again, err := s.DMS.DAL.Operations().Find(ctx, run.ID)
		if err != nil || string(again.Payload) != string(op.Payload) {
			t.Fatal("live updates changed the run's snapshot", err)
		}
	}
}

func TestFileTriggerSnapshotUsesEventFileVersion(t *testing.T) {
	s, _, _, _, item := policyFixture(t)
	ctx := t.Context()
	for i, filename := range []string{"first.pdf", "second.pdf"} {
		_, err := s.DMS.AttachBlob(ctx, "alice", item.ID, item.Version, dms.BlobInput{
			ObjectKey: filename, Filename: filename, ContentType: "application/pdf", Size: int64(i + 1), SHA256: strings.Repeat(string(rune('a'+i)), 64),
		})
		if err != nil {
			t.Fatal(err)
		}
		item, err = s.DMS.Get(ctx, "alice", item.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	e := triggerEvents(t, s, item.ID, dms.EventFileAdded)[0]
	got, err := triggerObject(ctx, s.DMS, "alice", e)
	if err != nil || got == nil || got.Kind != "file" || got.File == nil || got.File.Version != 1 || got.File.Filename != "first.pdf" || got.File.ID != e.Data["blob_id"] || got.Item.RevisionID != e.Data["revision_id"] {
		t.Fatalf("file event read current file instead: %+v %v", got, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "object_key") || strings.Contains(string(raw), "sha256") {
		t.Fatal("snapshot includes storage internals", string(raw))
	}
}

func TestTriggerSnapshotHonorsReadableSurface(t *testing.T) {
	s, ws, _, _, _ := policyFixture(t)
	ctx := t.Context()
	lib, err := s.DMS.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Published", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.DMS.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Photo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DMS.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DMS.Publish(ctx, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	item, _ = s.DMS.Get(ctx, "alice", item.ID)
	name := "Private draft"
	if _, err := s.DMS.Update(ctx, "alice", item.ID, item.Version, dms.UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	e := triggerEvents(t, s, item.ID, dms.EventItemUpdated)
	got, err := triggerObject(ctx, s.DMS, "reader", e[len(e)-1])
	if err != nil || got != nil {
		t.Fatalf("draft exposed to reader: %+v %v", got, err)
	}
	manual := dms.Event{Type: TriggerManual, WorkspaceID: ws.ID, ResourceID: item.ID}
	got, err = triggerObject(ctx, s.DMS, "reader", manual)
	if err != nil || got == nil || len(got.Items) != 1 || got.Items[0].Snapshot == nil || got.Items[0].Snapshot.Name != "Photo" {
		t.Fatalf("manual start did not select readable published content: %+v %v", got, err)
	}
	schedule := manual
	schedule.Type = TriggerSchedule
	schedule.Data = map[string]any{"surface": "published"}
	got, err = triggerObject(ctx, s.DMS, "alice", schedule)
	if err != nil || got == nil || got.Item.Name != "Photo" {
		t.Fatalf("published schedule read the author's draft: %+v %v", got, err)
	}
	got, err = triggerObject(ctx, s.DMS, "outsider", manual)
	if err != nil || got == nil || len(got.Items) != 1 || got.Items[0].ID != item.ID || got.Items[0].Snapshot != nil {
		t.Fatalf("inaccessible content exposed: %+v %v", got, err)
	}
	for _, typ := range []string{dms.EventItemDeleted, dms.EventItemPurged} {
		manual.Type = typ
		if got, err = triggerObject(ctx, s.DMS, "alice", manual); err != nil || got != nil {
			t.Fatal("deleted event has a snapshot", got, err)
		}
	}
}

func TestFolderTriggerSnapshot(t *testing.T) {
	s, ws, lib, _, _ := policyFixture(t)
	ctx := t.Context()
	if _, err := s.DMS.Update(ctx, "alice", lib.ID, lib.Version, dms.UpdateResource{AllowFolders: new(true)}); err != nil {
		t.Fatal(err)
	}
	folder, err := s.DMS.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "folder", Name: "Scans"})
	if err != nil {
		t.Fatal(err)
	}
	e := triggerEvents(t, s, folder.ID, dms.EventFolderCreated)[0]
	got, err := triggerObject(ctx, s.DMS, "alice", e)
	if err != nil || got == nil || got.Kind != "folder" || got.Folder.ID != folder.ID || got.Folder.Name != "Scans" || got.Item != nil || got.File != nil {
		t.Fatalf("folder snapshot: %+v %v", got, err)
	}
	if got, err := triggerObject(ctx, s.DMS, "alice", dms.Event{Type: TriggerManual, WorkspaceID: ws.ID}); err != nil || got == nil || got.Kind != TriggerManual || len(got.Items) != 0 {
		t.Fatal("standalone start lost its manual context", got, err)
	}
}

func TestTriggerSnapshotOmitsLargeValues(t *testing.T) {
	s, ws, _, _, item := policyFixture(t)
	// Revision payloads can legally reach 8 MiB; even one large field must
	// not be copied into every run that only needs identity or file metadata.
	payload, _ := json.Marshal(map[string]any{"text": strings.Repeat("x", MaxTriggerBytes)})
	daltest.Update(t, s.DMS.DAL, "item_revisions", daltest.Row{"payload": string(payload)}, daltest.Row{"id": *item.HeadRevisionID})
	got, err := triggerObject(t.Context(), s.DMS, "alice", dms.Event{Type: TriggerManual, WorkspaceID: ws.ID, ResourceID: item.ID})
	if err != nil || got == nil || len(got.Items) != 1 || got.Items[0].Snapshot == nil || got.Items[0].Snapshot.Values != nil || !got.Items[0].Snapshot.ValuesOmitted || got.Items[0].Snapshot.ID != item.ID {
		t.Fatalf("large values were not explicitly omitted: %+v %v", got, err)
	}
	raw, err := json.Marshal(got)
	if err != nil || len(raw) > MaxTriggerBytes {
		t.Fatal("snapshot exceeds budget", len(raw), err)
	}
}
