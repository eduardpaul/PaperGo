package runner

import (
	"io"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/imaging"
)

func TestDefaultPreviewWorkflowIsAsynchronousReusableAndDoesNotChangeFiles(t *testing.T) {
	f := setup(t)
	library, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Previews", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// Occupied OCR/workflow slots must not starve the bounded preview worker.
	f.r.workSlots <- struct{}{}
	defer func() { <-f.r.workSlots }()
	file := f.upload(t, library.ID, "scan.png", "image/png", scan(t, 1600, 1200))
	wait := func(hash string) string {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			row, err := f.dms.FindPreview(ctx, hash)
			if err != nil {
				t.Fatal(err)
			}
			if row != nil {
				if row.Status != "ready" || row.ObjectSize >= 100000 || max(row.Width, row.Height) > 800 {
					t.Fatal(row)
				}
				return row.ObjectKey
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("preview workflow did not finish", f.logs.String())
		return ""
	}
	key := wait(file.Blob.Sha256)
	bytes, err := f.store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(bytes)
	bytes.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = imaging.Decode(data, 800*800); err != nil {
		t.Fatal(err)
	}
	current := f.current(t, file.ID)
	if current.ID != file.Blob.ID {
		t.Fatal("preview replaced original")
	}
	item := f.head(t, file.ID)
	if item.Version != file.Version {
		t.Fatal("preview changed item metadata", item.Version, file.Version)
	}
	again := f.upload(t, library.ID, "copy.png", "image/png", scan(t, 1600, 1200))
	if reused := wait(again.Blob.Sha256); reused != key {
		t.Fatal("identical bytes did not share preview")
	}
	replacement := f.upload(t, library.ID, "scan.png", "image/png", scan(t, 1000, 800))
	if replacement.Blob.ID == file.Blob.ID {
		t.Fatal("replacement fixture failed")
	}
	wait(replacement.Blob.Sha256)
	if old, err := f.dms.FindPreview(ctx, file.Blob.Sha256); err != nil || old.ObjectKey != key {
		t.Fatal("replacement overwrote prior rendition", err)
	}
}
