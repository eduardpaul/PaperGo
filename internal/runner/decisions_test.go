package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"papergo/internal/decision"
	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/model"
	"papergo/internal/ocr/paddle"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
)

type runnerDecisionModel struct {
	calls       atomic.Int32
	mu          sync.Mutex
	states      []string
	probability float64
	during      func()
}

func (*runnerDecisionModel) ID() string { return "test-laya" }
func (m *runnerDecisionModel) Decide(_ context.Context, rows []decision.Request) ([]decision.Result, error) {
	m.calls.Add(1)
	m.mu.Lock()
	for _, r := range rows {
		m.states = append(m.states, r.State)
	}
	m.mu.Unlock()
	if m.during != nil {
		m.during()
	}
	out := make([]decision.Result, len(rows))
	for i, r := range rows {
		p := make([]float64, len(r.Options))
		p[0] = 1 - m.probability
		p[len(p)-1] = m.probability
		out[i].Probabilities = p
	}
	return out, nil
}
func TestDecisionRoutingSampleWithoutItem(t *testing.T) {
	m := &runnerDecisionModel{probability: .9}
	f := setup(t, func(tools *workflow.Tools) { tools.Decisions = m })
	w := f.sample(t, "decision-routing.json", "")
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{Inputs: map[string]any{"subject": "Refund", "body": "{input:customer_tier}", "customer_tier": "premium"}})
	if err != nil {
		t.Fatal(err)
	}
	run := f.status(t, ids[0], "completed")
	if run.ItemID != nil {
		t.Fatal("unexpected item")
	}
	m.mu.Lock()
	state := m.states[0]
	m.mu.Unlock()
	if !strings.Contains(state, `"body":"{input:customer_tier}"`) {
		t.Fatal("token data was expanded again", state)
	}
	again, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{Inputs: map[string]any{"subject": "Refund", "body": "{input:customer_tier}", "customer_tier": "premium"}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, again[0], "completed")
	if m.calls.Load() != 1 {
		t.Fatal("missed cache", m.calls.Load())
	}
}
func TestTaxonomyReviewSampleGuards(t *testing.T) {
	for _, scenario := range []string{"approve", "reject", "none", "metadata", "taxonomy", "file"} {
		t.Run(scenario, func(t *testing.T) {
			m := &runnerDecisionModel{probability: .9}
			if scenario == "none" {
				m.probability = .2
			}
			f := setup(t, func(tools *workflow.Tools) { tools.Decisions = m })
			lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Documents", WebDAVEnabled: true})
			if err != nil {
				t.Fatal(err)
			}
			group, err := f.dms.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Document topics"})
			if err != nil {
				t.Fatal(err)
			}
			set, err := f.dms.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics"})
			if err != nil {
				t.Fatal(err)
			}
			term, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Invoices", Description: "Invoices and payment requests"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.dms.CreateField(ctx, "alice", lib.ID, dms.CreateField{Key: "topics", Label: "Topics", Type: "term", Options: model.FieldOptions{TermSetID: set.ID, Multiple: true}}); err != nil {
				t.Fatal(err)
			}
			if _, err = f.dms.CreateField(ctx, "alice", lib.ID, dms.CreateField{Key: "note", Label: "Note", Type: "text"}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile("../../samples/workflows/taxonomy-review.json")
			if err != nil {
				t.Fatal(err)
			}
			text := strings.NewReplacer("$LIBRARY", lib.ID, "$TERM_SET", set.ID, "$FIELD", "topics", "role:owners", "bob").Replace(string(raw))
			var save workflow.Save
			if err = json.Unmarshal([]byte(text), &save); err != nil {
				t.Fatal(err)
			}
			w, err := f.wf.Create(ctx, "alice", f.ws.ID, save)
			if err != nil {
				t.Fatal(err)
			}
			item := f.upload(t, lib.ID, "invoice.pdf", "application/pdf", doctexttest.TextPDF("Invoice for coffee. Amount due 21 euros."))
			ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "none" {
				f.status(t, ids[0], "completed")
				mine, err := pageData(f.tasks().Mine(ctx, "bob", tasks.Filter{Status: "open"}))
				if err != nil || len(mine) != 0 {
					t.Fatal(mine, err)
				}
				return
			}
			f.status(t, ids[0], "waiting")
			mine, err := pageData(f.tasks().Mine(ctx, "bob", tasks.Filter{Status: "open"}))
			if err != nil || len(mine) != 1 {
				t.Fatal(mine, err)
			}
			switch scenario {
			case "metadata":
				current := f.head(t, item.ID)
				values := map[string]any{"note": "Concurrent change"}
				if _, err = f.dms.Update(ctx, "alice", item.ID, current.Version, dms.UpdateResource{Values: &values}); err != nil {
					t.Fatal(err)
				}
			case "taxonomy":
				if _, err = f.dms.UpdateTerm(ctx, "alice", term.ID, term.Version, dms.TermInput{Name: "Invoices updated", Description: term.Description}); err != nil {
					t.Fatal(err)
				}
			case "file":
				f.upload(t, lib.ID, "invoice.pdf", "application/pdf", doctexttest.TextPDF("New invoice"))
			}
			response := tasks.Response{Outcome: "approved", Input: map[string]any{"terms": []any{term.ID}}}
			if scenario == "reject" {
				response = tasks.Response{Outcome: "rejected", Comment: "Wrong category", Input: map[string]any{"terms": []any{term.ID}}}
			}
			if _, err = f.tasks().Respond(ctx, "bob", mine[0].ID, response); err != nil {
				t.Fatal(err)
			}
			f.status(t, ids[0], "completed")
			current := f.head(t, item.ID)
			value := current.Values["topics"]
			if scenario == "approve" {
				b, _ := json.Marshal(value)
				if string(b) != `["`+term.ID+`"]` {
					t.Fatal("approved value", value)
				}
			} else if value != nil {
				t.Fatal("unexpected write", scenario, value)
			}
			if scenario == "metadata" && current.Values["note"] != "Concurrent change" {
				t.Fatal("concurrent metadata overwritten")
			}
		})
	}
}

func TestTaxonomyReviewNativePDFAndOCR(t *testing.T) {
	models := os.Getenv("DECISION_MODELS_PATH")
	library := os.Getenv("ONNXRUNTIME_LIB")
	if models == "" || library == "" {
		if os.Getenv("PAPERGO_REQUIRE_DECISION") == "1" {
			t.Fatal("native decision bundle required")
		}
		t.Skip("native decision bundle not configured")
	}
	engine, err := decision.New(decision.Config{Models: models, Library: library})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ocrEngine, err := paddle.New(paddle.Config{Models: "../../models/paddleocr", Library: library, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer ocrEngine.Close()
	f := setup(t, func(tools *workflow.Tools) { tools.Decisions = engine; tools.OCR = ocrEngine })
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Invoices", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err := f.dms.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Document types"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := f.dms.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "types", Name: "Document types"})
	if err != nil {
		t.Fatal(err)
	}
	term, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Invoice", Description: "An invoice or bill requesting payment for goods or services"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.CreateField(ctx, "alice", lib.ID, dms.CreateField{Key: "topics", Label: "Topics", Type: "term", Options: model.FieldOptions{TermSetID: set.ID, Multiple: true}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../samples/workflows/taxonomy-review.json")
	if err != nil {
		t.Fatal(err)
	}
	var save workflow.Save
	if err = json.Unmarshal([]byte(strings.NewReplacer("$LIBRARY", lib.ID, "$TERM_SET", set.ID, "$FIELD", "topics", "role:owners", "bob").Replace(string(raw))), &save); err != nil {
		t.Fatal(err)
	}
	w, err := f.wf.Create(ctx, "alice", f.ws.ID, save)
	if err != nil {
		t.Fatal(err)
	}
	text := "ELECTRICITY INVOICE. Supplier City Energy. September electricity consumption 145 kWh. Amount due 42 euros. Please pay this bill by bank transfer."
	for _, format := range []string{"pdf", "png"} {
		t.Run(format, func(t *testing.T) {
			data, media := doctexttest.TextPDF(text), "application/pdf"
			if format == "png" {
				parsed, err := opentype.Parse(goregular.TTF)
				if err != nil {
					t.Fatal(err)
				}
				face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 32, DPI: 72, Hinting: font.HintingFull})
				if err != nil {
					t.Fatal(err)
				}
				defer face.Close()
				img := image.NewNRGBA(image.Rect(0, 0, 1200, 350))
				draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
				drawer := font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: face}
				for i, line := range []string{"ELECTRICITY INVOICE", "Supplier City Energy. Consumption 145 kWh.", "Amount due 42 euros. Please pay this bill.", "Payment by bank transfer."} {
					drawer.Dot = fixed.P(40, 60+i*65)
					drawer.DrawString(line)
				}
				var buffer bytes.Buffer
				if err = png.Encode(&buffer, img); err != nil {
					t.Fatal(err)
				}
				data, media = buffer.Bytes(), "image/png"
			}
			item := f.upload(t, lib.ID, "invoice."+format, media, data)
			ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(90 * time.Second)
			for {
				run, err := f.r.Run(ctx, "alice", ids[0])
				if err != nil {
					t.Fatal(err)
				}
				if run.Status == "waiting" {
					break
				}
				if run.Status == "failed" || run.Status == "completed" {
					t.Fatal("native review did not wait", run.Status, run.Error)
				}
				if time.Now().After(deadline) {
					t.Fatal("native review timeout")
				}
				time.Sleep(50 * time.Millisecond)
			}
			task := f.approvalOf(t, ids[0])
			if _, err = f.tasks().Respond(ctx, "bob", task.ID, tasks.Response{Outcome: "approved", Input: map[string]any{"terms": []any{term.ID}}}); err != nil {
				t.Fatal(err)
			}
			f.status(t, ids[0], "completed")
			stored, _ := json.Marshal(f.head(t, item.ID).Values["topics"])
			if string(stored) != `["`+term.ID+`"]` {
				t.Fatal("native review did not apply", string(stored))
			}
		})
	}
}
