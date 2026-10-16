package dms

import (
	"bytes"
	"image"
	"io"
	"strings"
	"testing"
	"time"

	"papergo/internal/imaging"
	"papergo/internal/storage"
)

func TestPreviewsTravelWithPackagesAndAreCollectedOnlyAfterSourcesDisappear(t *testing.T) {
	s, workspace, _ := fixture(t)
	ctx := t.Context()
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	library := create(t, s, workspace.ID, "library", "Files", nil)
	source, err := store.Put(ctx, strings.NewReader("%PDF-source"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	document, err := s.CreateDocument(ctx, "alice", library.ID, DocumentMetadata{Name: "Source"}, BlobInput{ObjectKey: source.Key, Filename: "source.pdf", ContentType: "application/pdf", Size: source.Size, SHA256: source.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := imaging.EncodeWebP(image.NewRGBA(image.Rect(0, 0, 20, 10)), 75)
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.Put(ctx, bytes.NewReader(encoded), 100000)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePreview(ctx, source.SHA256, object, 20, 10); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"workspace", "organization"} {
		t.Run(scope, func(t *testing.T) {
			options := ExportOptions{}
			if scope == "workspace" {
				options.WorkspaceID = workspace.ID
			}
			exported, manifest := exportWorkspaceForTest(t, s, store, options)
			if len(manifest.Tables["blob_previews"].Rows) != 1 {
				t.Fatal("preview metadata omitted")
			}
			target := emptyPackageTarget(t)
			name := ""
			if scope == "workspace" {
				name = "Preview copy"
			}
			result, err := importWorkspaceForTest(t, target, store, exported, false, name)
			if err != nil || !result.Valid {
				t.Fatal(result, err)
			}
			row, err := target.FindPreview(ctx, source.SHA256)
			if err != nil || row == nil || row.ObjectKey == object.Key {
				t.Fatal("preview object not remapped", row, err)
			}
			file, err := store.Open(ctx, row.ObjectKey)
			if err != nil {
				t.Fatal(err)
			}
			copied, err := io.ReadAll(file)
			file.Close()
			if err != nil || !bytes.Equal(copied, encoded) {
				t.Fatal("preview bytes not preserved", err)
			}
		})
	}
	if err = s.CollectObjects(ctx, store, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	kept, err := store.Open(ctx, object.Key)
	if err != nil {
		t.Fatal("live preview was garbage collected", err)
	}
	kept.Close()
	if err = s.Delete(ctx, "alice", document.Item.ID, latest(t, s, document.Item.ID).Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = s.PurgeDeleted(ctx, "alice", library.ID, document.Item.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupPurgedBlobs(ctx, store); err != nil {
		t.Fatal(err)
	}
	if row, err := s.FindPreview(ctx, source.SHA256); err != nil || row != nil {
		t.Fatal("orphan preview row retained", row, err)
	}
	if removed, err := store.Open(ctx, object.Key); err == nil {
		removed.Close()
		t.Fatal("orphan preview object retained")
	}
}
