package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"papergo/internal/dal/blobs"
	"papergo/internal/worker"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"papergo/internal/model"
	"papergo/internal/storage"
	"papergo/internal/workflow"
)

const reclaimFlow = `{
	"triggers": [{"type": "manual", "collection_id": "$LIB"}],
	"input_schema": {"type": "object", "required": ["blob_id"], "properties": {"blob_id": {"type": "string"}}},
	"output_schema": {"type": "object", "properties": {
		"blob_id": {"type": "string"}, "availability": {"type": "string"}, "deferred": {"type": "boolean"},
		"reason": {"type": "string"}, "reclaim_after": {"type": "string"}}},
	"outputs": {
		"blob_id": "{step:reclaim.blob_id}", "availability": "{step:reclaim.availability}",
		"deferred": "{step:reclaim.deferred}", "reason": "{step:reclaim.reason}",
		"reclaim_after": "{step:reclaim.reclaim_after}"},
	"flow": {"start": "reclaim", "nodes": {"reclaim": {"activity": "file.reclaim", "inputs": {"blob_id": "{input:blob_id}"}}}}
}`

func (f *fixture) reclaimWorkflow(t *testing.T, name, libraryID string) workflow.Workflow {
	t.Helper()
	return f.workflow(t, name, strings.ReplaceAll(reclaimFlow, "$LIB", libraryID))
}

func (f *fixture) runReclaim(t *testing.T, w workflow.Workflow, itemID, blobID string, n int) map[string]any {
	t.Helper()
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{itemID}, Inputs: map[string]any{"blob_id": blobID}})
	if err != nil {
		t.Fatal(err)
	}
	finished := f.runs(t, w.ID, n)
	var status string
	for _, run := range finished {
		if run.ID == ids[0] {
			status = string(run.Status)
		}
	}
	detail, err := f.r.Run(ctx, "alice", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("reclaim run: %+v", detail)
	}
	result, _ := detail.Result.(map[string]any)
	outputs, _ := result["outputs"].(map[string]any)
	if outputs["blob_id"] != blobID {
		t.Fatalf("reclaim outputs: %+v", detail.Result)
	}
	return outputs
}

func (f *fixture) blobRow(t *testing.T, id string) *blobs.Blob {
	t.Helper()
	row, err := f.dms.DAL.Blobs().Blob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return &row
}

func (f *fixture) fileVersion(t *testing.T, itemID, blobID string) dms.FileVersion {
	t.Helper()
	page, err := f.dms.FileVersions(ctx, "alice", itemID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range page.Data {
		if version.ID == blobID {
			return version
		}
	}
	t.Fatalf("file version %s is not listed: %+v", blobID, page.Data)
	return dms.FileVersion{}
}

func objectPresent(t *testing.T, store storage.Store, key string) bool {
	t.Helper()
	file, err := store.Open(ctx, key)
	if err != nil {
		return false
	}
	defer file.Close()
	_, err = io.Copy(io.Discard, file)
	return err == nil
}

func TestFileReclaimFreesHistoricalBytesAfterGrace(t *testing.T) {
	f := setup(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Receipts", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w := f.reclaimWorkflow(t, "Reclaim receipts", lib.ID)
	first := f.upload(t, lib.ID, "ticket.png", "image/png", bytes.Repeat([]byte("a"), 80))
	source := first.Blob
	before := f.fileVersion(t, first.ID, source.ID)
	second := f.upload(t, lib.ID, "ticket.png", "image/png", bytes.Repeat([]byte("b"), 90))
	if second.ID != first.ID || second.Blob.ID == source.ID {
		t.Fatalf("replace did not keep the item and add a version: %+v", second)
	}

	out := f.runReclaim(t, w, first.ID, source.ID, 1)
	if out["availability"] != "pending" || out["deferred"] != false || out["reclaim_after"] == nil || out["reclaim_after"] == "" || out["reason"] != nil {
		t.Fatalf("first reclaim: %+v", out)
	}
	due := f.blobRow(t, source.ID)
	if due.Availability != "pending" || due.ReclaimAfter == nil {
		t.Fatalf("blob was not marked pending: %+v", due)
	}
	again := f.runReclaim(t, w, first.ID, source.ID, 2)
	kept := f.blobRow(t, source.ID)
	if kept.ReclaimAfter == nil || !kept.ReclaimAfter.Equal(*due.ReclaimAfter) || again["availability"] != "pending" || again["reclaim_after"] == nil {
		t.Fatalf("retry moved the deadline: first %v second %v outputs %+v", due.ReclaimAfter, kept.ReclaimAfter, again)
	}
	if _, err = f.dms.ReclaimBlob(ctx, "carol", first.ID, source.ID); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("carol reclaimed a file: %v", err)
	}
	if !kept.ReclaimAfter.Equal(*f.blobRow(t, source.ID).ReclaimAfter) {
		t.Fatal("a denied reclaim moved the deadline")
	}
	if !objectPresent(t, f.store, source.ObjectKey) {
		t.Fatal("bytes were freed before the grace period")
	}
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !objectPresent(t, f.store, source.ObjectKey) || f.blobRow(t, source.ID).Availability != "pending" {
		t.Fatal("a sweep before the deadline reclaimed the file")
	}
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC().Add(2*dms.ObjectGracePeriod)); err != nil {
		t.Fatal(err)
	}
	if objectPresent(t, f.store, source.ObjectKey) {
		t.Fatal("eligible bytes remained after the grace period")
	}
	got := f.fileVersion(t, first.ID, source.ID)
	if got.ID != before.ID || got.Version != before.Version || got.Filename != before.Filename || got.ContentType != before.ContentType || got.Size != before.Size || got.SHA256 != before.SHA256 || got.Availability != "reclaimed" {
		t.Fatalf("history metadata changed: before %+v after %+v", before, got)
	}
	if _, err = f.dms.GetBlob(ctx, "alice", first.ID, source.ID); !errors.Is(err, dms.ErrContentUnavailable) {
		t.Fatalf("reclaimed download: %v", err)
	}
	item, err := f.dms.Get(ctx, "alice", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.RestoreBlob(ctx, "alice", first.ID, source.Version, item.Version); !errors.Is(err, dms.ErrContentUnavailable) {
		t.Fatalf("reclaimed restore: %v", err)
	}
	current := f.current(t, first.ID)
	if current.ID != second.Blob.ID || current.Availability != "available" || !objectPresent(t, f.store, current.ObjectKey) {
		t.Fatalf("current file was reclaimed: %+v", current)
	}
	if listed := f.fileVersion(t, first.ID, current.ID); listed.Availability != "available" || listed.SHA256 != current.Sha256 {
		t.Fatalf("current history: %+v", listed)
	}
}

func TestFileReclaimDefersProtectedAndSharedBytes(t *testing.T) {
	f := setup(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Protected", WebDAVEnabled: true, PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w := f.reclaimWorkflow(t, "Reclaim protected", lib.ID)
	n := 0
	reclaim := func(itemID, blobID string) map[string]any {
		t.Helper()
		n++
		return f.runReclaim(t, w, itemID, blobID, n)
	}

	file := f.upload(t, lib.ID, "current.png", "image/png", bytes.Repeat([]byte("c"), 80))
	item, err := f.dms.Get(ctx, "alice", file.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.Publish(ctx, "alice", file.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	if out := reclaim(file.ID, file.Blob.ID); out["deferred"] != true || out["reason"] != "current" {
		t.Fatalf("current file: %+v", out)
	}
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC().Add(2*dms.ObjectGracePeriod)); err != nil {
		t.Fatal(err)
	}
	if !objectPresent(t, f.store, file.Blob.ObjectKey) || f.blobRow(t, file.Blob.ID).Availability == "reclaimed" {
		t.Fatal("current bytes were reclaimed")
	}

	next := f.upload(t, lib.ID, "current.png", "image/png", bytes.Repeat([]byte("d"), 85))
	if out := reclaim(file.ID, file.Blob.ID); out["deferred"] != true || out["reason"] != "published" {
		t.Fatalf("published file: %+v", out)
	}
	if out := reclaim(next.ID, next.Blob.ID); out["deferred"] != true || out["reason"] != "current" {
		t.Fatalf("replacement: %+v", out)
	}
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC().Add(2*dms.ObjectGracePeriod)); err != nil {
		t.Fatal(err)
	}
	if !objectPresent(t, f.store, file.Blob.ObjectKey) || !objectPresent(t, f.store, next.Blob.ObjectKey) {
		t.Fatal("published or current bytes were freed")
	}

	stagedItem := f.upload(t, lib.ID, "staged.png", "image/png", bytes.Repeat([]byte("e"), 80))
	candidate, err := f.store.Put(ctx, bytes.NewReader(bytes.Repeat([]byte("f"), 40)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.dms.Write(ctx, func(tx *dms.Service) error {
		stager, err := tx.DAL.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.ws.ID, Key: "stager", Name: "Stager", CreatedBy: "alice"})
		if err != nil {
			return err
		}
		return daltest.WorkflowRun(ctx, tx.DAL, "run-stage", stager.ID, f.ws.ID, "alice", 1)
	}); err != nil {
		t.Fatal(err)
	}
	staged, err := f.dms.StageFile(ctx, "alice", dms.StageInput{
		ItemID: stagedItem.ID, RunID: "run-stage",
		Sources:   []model.StagedSource{{ItemID: stagedItem.ID, BlobID: stagedItem.Blob.ID}},
		ObjectKey: candidate.Key, Filename: "candidate.png", ContentType: "image/png",
		Size: candidate.Size, SHA256: candidate.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.upload(t, lib.ID, "staged.png", "image/png", bytes.Repeat([]byte("g"), 88))
	if out := reclaim(stagedItem.ID, stagedItem.Blob.ID); out["deferred"] != true || out["reason"] != "staged" {
		t.Fatalf("staged source: %+v", out)
	}
	if err = f.dms.DiscardStagedFile(ctx, "alice", staged.ID, "run-stage"); err != nil {
		t.Fatal(err)
	}
	if out := reclaim(stagedItem.ID, stagedItem.Blob.ID); out["deferred"] != false || out["availability"] != "pending" {
		t.Fatalf("staged source after discard: %+v", out)
	}

	pinned := f.upload(t, lib.ID, "pinned.png", "image/png", bytes.Repeat([]byte("h"), 80))
	f.upload(t, lib.ID, "pinned.png", "image/png", bytes.Repeat([]byte("i"), 86))
	if err = f.dms.Write(ctx, func(tx *dms.Service) error {
		pinWorkflow, err := tx.DAL.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.ws.ID, Key: "pin-consumer", Name: "Pin consumer", CreatedBy: "alice"})
		if err != nil {
			return err
		}
		if err := daltest.WorkflowRun(ctx, tx.DAL, "run-pin", pinWorkflow.ID, f.ws.ID, "alice", 1); err != nil {
			return err
		}
		return workflow.PutWait(ctx, tx, workflow.Wait{
			BlobIDs: []string{pinned.Blob.ID}, Kind: "file.pin", Key: "one", WorkspaceID: f.ws.ID, RunID: "run-pin", Node: "hold", ExecutionID: "1",
			Data: json.RawMessage(fmt.Sprintf(`{"blobs":[{"item_id":%q,"blob_id":%q}]}`, pinned.ID, pinned.Blob.ID)),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if out := reclaim(pinned.ID, pinned.Blob.ID); out["deferred"] != true || out["reason"] != "in use" {
		t.Fatalf("in-flight consumer: %+v", out)
	}
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC().Add(2*dms.ObjectGracePeriod)); err != nil {
		t.Fatal(err)
	}
	if !objectPresent(t, f.store, pinned.Blob.ObjectKey) {
		t.Fatal("an open wait did not keep the bytes")
	}
	if err = f.dms.Write(ctx, func(tx *dms.Service) error {
		return workflow.CloseWait(ctx, tx, "file.pin", "one", "run-pin")
	}); err != nil {
		t.Fatal(err)
	}
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC().Add(2*dms.ObjectGracePeriod)); err != nil {
		t.Fatal(err)
	}

	if objectPresent(t, f.store, pinned.Blob.ObjectKey) {
		t.Fatal("bytes remained after the wait closed and the grace period passed")
	}

	shared := bytes.Repeat([]byte("s"), 70)
	left := f.upload(t, lib.ID, "left.png", "image/png", shared)
	right := f.upload(t, lib.ID, "right.png", "image/png", shared)
	if left.Blob.ObjectKey != right.Blob.ObjectKey {
		t.Fatalf("identical uploads kept separate objects %s %s", left.Blob.ObjectKey, right.Blob.ObjectKey)
	}
	replacement := f.upload(t, lib.ID, "left.png", "image/png", bytes.Repeat([]byte("z"), 75))
	if out := reclaim(left.ID, left.Blob.ID); out["deferred"] != false || out["availability"] != "pending" {
		t.Fatalf("shared historical version: %+v", out)
	}
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC().Add(2*dms.ObjectGracePeriod)); err != nil {
		t.Fatal(err)
	}
	if f.blobRow(t, left.Blob.ID).Availability != "reclaimed" {
		t.Fatal("shared historical version was not marked reclaimed")
	}
	if !objectPresent(t, f.store, right.Blob.ObjectKey) || !objectPresent(t, f.store, replacement.Blob.ObjectKey) {
		t.Fatal("shared or replacement bytes were deleted")
	}
	if _, err = f.dms.GetBlob(ctx, "alice", right.ID, ""); err != nil {
		t.Fatalf("the item that still uses the shared bytes: %v", err)
	}
	if _, err = f.dms.GetBlob(ctx, "alice", left.ID, left.Blob.ID); !errors.Is(err, dms.ErrContentUnavailable) {
		t.Fatalf("reclaimed copy of shared bytes: %v", err)
	}
}

func TestReclaimedContentIsOmittedFromPortabilityPackages(t *testing.T) {
	f := setup(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Archive", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w := f.reclaimWorkflow(t, "Reclaim archive", lib.ID)
	first := f.upload(t, lib.ID, "scan.png", "image/png", scan(t, 32, 24))
	source := first.Blob
	before := f.fileVersion(t, first.ID, source.ID)
	second := f.upload(t, lib.ID, "scan.png", "image/png", scan(t, 40, 24))
	f.runReclaim(t, w, first.ID, source.ID, 1)
	if err = f.dms.SweepReclaims(ctx, f.store, time.Now().UTC().Add(2*dms.ObjectGracePeriod)); err != nil {
		t.Fatal(err)
	}
	if objectPresent(t, f.store, source.ObjectKey) {
		t.Fatal("source bytes were not reclaimed")
	}
	f.organizationManager(t, "alice")
	if err = f.dms.RegisterPortability(f.store); err != nil {
		t.Fatal(err)
	}
	stop := f.operations(t)
	defer stop()
	export, err := f.dms.StartExport(ctx, "alice", dms.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var result dms.PackageResult
	deadline := time.Now().Add(30 * time.Second)
	for {
		result, err = f.dms.ExportPackage(ctx, "alice", export.ID)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			op, _ := f.dms.DAL.Operations().Find(ctx, export.ID)
			t.Fatalf("export: %v (%s %s)", err, op.Status, op.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	pkg, err := f.store.Open(ctx, result.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(pkg)
	pkg.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var manifest []byte
	sawCurrent := false
	for _, entry := range reader.File {
		switch entry.Name {
		case "manifest.json":
			body, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = io.ReadAll(body)
			body.Close()
			if err != nil {
				t.Fatal(err)
			}
		case "objects/" + source.ObjectKey:
			t.Fatal("the package contains reclaimed bytes")
		case "objects/" + second.Blob.ObjectKey:
			sawCurrent = true
		}
	}
	if !sawCurrent || manifest == nil {
		t.Fatal("the current file or the manifest is missing from the package")
	}
	var doc struct {
		Tables map[string]struct {
			Columns []string `json:"columns"`
			Rows    [][]any  `json:"rows"`
		} `json:"tables"`
		Objects []struct {
			Key string `json:"key"`
		} `json:"objects"`
	}
	if err = json.Unmarshal(manifest, &doc); err != nil {
		t.Fatal(err)
	}
	for _, obj := range doc.Objects {
		if obj.Key == source.ObjectKey {
			t.Fatal("the manifest lists a reclaimed object")
		}
	}
	blobs := doc.Tables["blobs"]
	col := map[string]int{}
	for i, name := range blobs.Columns {
		col[name] = i
	}
	found := false
	for _, row := range blobs.Rows {
		if fmt.Sprint(row[col["id"]]) != source.ID {
			continue
		}
		found = true
		if fmt.Sprint(row[col["availability"]]) != "reclaimed" || fmt.Sprint(row[col["filename"]]) != before.Filename || fmt.Sprint(row[col["content_type"]]) != before.ContentType || fmt.Sprint(row[col["sha256"]]) != before.SHA256 || fmt.Sprint(row[col["size"]]) != fmt.Sprint(before.Size) || fmt.Sprint(row[col["version"]]) != fmt.Sprint(before.Version) {
			t.Fatalf("packaged history: %+v", row)
		}
	}
	if !found {
		t.Fatal("reclaimed blob metadata is missing from the package")
	}

	target, targetStore := packageTarget(t)
	uploaded, err := targetStore.Put(ctx, bytes.NewReader(raw), dms.PackageLimit)
	if err != nil {
		t.Fatal(err)
	}
	stopTarget := runServiceOperations(t, target)
	defer stopTarget()
	imp, err := target.StartImport(ctx, "organization-admin", uploaded.Key, false, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(60 * time.Second)
	for {
		op, err := target.DAL.Operations().Find(ctx, imp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if op.Status == operations.StatusSucceeded {
			break
		}
		if op.Status == operations.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("import %s: %s", op.Status, op.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = target.GetBlob(ctx, "organization-admin", first.ID, source.ID); !errors.Is(err, dms.ErrContentUnavailable) {
		t.Fatalf("imported reclaimed download: %v", err)
	}
	imported, err := target.Get(ctx, "organization-admin", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.RestoreBlob(ctx, "organization-admin", first.ID, source.Version, imported.Version); !errors.Is(err, dms.ErrContentUnavailable) {
		t.Fatalf("imported reclaimed restore: %v", err)
	}
	current, err := target.GetBlob(ctx, "organization-admin", first.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !objectPresent(t, targetStore, current.ObjectKey) {
		t.Fatal("imported current bytes are missing")
	}
	page, err := target.FileVersions(ctx, "organization-admin", first.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range page.Data {
		if version.ID != source.ID {
			continue
		}
		if version.Availability != "reclaimed" || version.Filename != before.Filename || version.ContentType != before.ContentType || version.Size != before.Size || version.SHA256 != before.SHA256 || version.Version != before.Version {
			t.Fatalf("imported history: %+v", version)
		}
		return
	}
	t.Fatal("imported history omitted the reclaimed version")
}

func packageTarget(t *testing.T) (*dms.Service, storage.Store) {
	t.Helper()
	dir := t.TempDir()
	service := dms.NewService(daltest.EmptyDatabase(t).SQL)
	if err := service.Directory().Initialize(ctx, "organization-admin", ""); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocal(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	return service, store
}

func runServiceOperations(t *testing.T, service *dms.Service) func() {
	t.Helper()
	work, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		_ = worker.Single(service, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(work)
		close(stopped)
	}()
	return func() { cancel(); <-stopped }
}
