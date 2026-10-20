package scriptrun

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/documenttext"
	"papergo/internal/httpapi"
	"papergo/internal/ocr"
	"papergo/internal/script"
)

// lineEngine recognizes one line in every image.
type lineEngine struct{}

func (lineEngine) Detect(_ context.Context, img image.Image, _ ocr.DetectOptions) (ocr.Detection, error) {
	b := img.Bounds()
	return ocr.Detection{Regions: []ocr.Region{{Quad: ocr.Quad{{10, 10}, {200, 10}, {200, 40}, {10, 40}}, Score: 0.9}}, Width: b.Dx(), Height: b.Dy()}, nil
}

func (lineEngine) Recognize(_ context.Context, img image.Image) (ocr.Page, error) {
	b := img.Bounds()
	return ocr.Page{Width: b.Dx(), Height: b.Dy(), Lines: []ocr.Line{{Text: "Total 21.30", Confidence: 0.9, Quad: ocr.Quad{{10, 10}, {200, 10}, {200, 40}, {10, 40}}}}}, nil
}

func (lineEngine) Info() ocr.Info { return ocr.Info{Engine: "test"} }
func (lineEngine) Close() error   { return nil }

const ocrSource = `
import { workflow, plain, OperationError } from "@papergo/workflow";

export default workflow({
  async run(ctx, input: { item: string }) {
    const started = await ctx.api.v1.items.byId(input.item).text.post({ ocr: "always" });
    try {
      const done = await ctx.wait(started!);
      const result = plain(done.result) as { ocr_pages: number; reused: boolean };
      const text = await ctx.api.v1.items.byId(input.item).text.get();
      return { ocr: result.ocr_pages, reused: result.reused, text: text?.pages?.[0]?.text };
    } catch (e) {
      if (e instanceof OperationError) return { failed: e.operation.errorEscaped };
      throw e;
    }
  },
});
`

// A script reads a photo's text through OCR as an operation, the same way an
// API client does, and a second run reuses it.
func TestScriptsReadTextThroughOperations(t *testing.T) {
	ctx := context.Background()
	var reader *documenttext.Reader
	f := setup(t, func(api *httpapi.API) {
		reader = &documenttext.Reader{Storage: api.Storage, PDF: doctexttest.PDF(t), OCR: lineEngine{}, Languages: []string{"english"}}
		api.Modules = append(api.Modules, reader.Module())
	})
	if err := reader.Register(f.s); err != nil {
		t.Fatal(err)
	}
	library, err := f.s.Create(ctx, "alice", f.workspaceID, dms.CreateResource{Kind: "library", Name: "Receipts", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var photo bytes.Buffer
	if err = png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 320, 240))); err != nil {
		t.Fatal(err)
	}
	upload := func(name, contentType string, data []byte) string {
		o, err := f.blobs.Put(ctx, bytes.NewReader(data), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		file, _, err := f.s.PutFile(ctx, "alice", library.ID, []string{name}, dms.PutFile{Blob: dms.BlobInput{ObjectKey: o.Key, Filename: name, ContentType: contentType, Size: o.Size, SHA256: o.SHA256}})
		if err != nil {
			t.Fatal(err)
		}
		return file.ID
	}
	receipt := upload("receipt.png", "image/png", photo.Bytes())
	broken := upload("broken.pdf", "application/pdf", []byte("%PDF-1.7 not really"))

	engine := script.New(&Effects{DMS: f.s, API: f.api}, 0)
	finish := func(run script.Run) script.Outcome {
		t.Helper()
		out, err := engine.Execute(ctx, run)
		if err != nil || out.Status != script.Suspended {
			t.Fatalf("first slice: %+v, %v", out, err)
		}
		f.work(t, func() bool {
			pending, err := f.s.DAL.Operations().List(ctx, operations.Filter{Statuses: []operations.Status{operations.StatusPending}})
			if err != nil {
				t.Fatal(err)
			}
			return len(pending) == 0
		})
		if out, err = engine.Execute(ctx, run); err != nil || out.Status != script.Completed {
			t.Fatalf("last slice: %+v, %v", out, err)
		}
		return out
	}

	first := finish(f.run(t, ocrSource, `{"item":"`+receipt+`"}`))
	if string(first.Output) != `{"ocr":1,"reused":false,"text":"Total 21.30"}` {
		t.Fatalf("first run: %s", first.Output)
	}
	second := finish(f.run(t, ocrSource, `{"item":"`+receipt+`"}`))
	if string(second.Output) != `{"ocr":1,"reused":true,"text":"Total 21.30"}` {
		t.Fatalf("second run: %s", second.Output)
	}
	unsupported := finish(f.run(t, ocrSource, `{"item":"`+broken+`"}`))
	if want := `{"failed":"read the text of `; len(unsupported.Output) < len(want) || string(unsupported.Output[:len(want)]) != want {
		t.Fatalf("unsupported file: %s", unsupported.Output)
	}
}
