package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func nativeEngine(t *testing.T) *Engine {
	t.Helper()
	path := os.Getenv("DECISION_MODELS_PATH")
	lib := os.Getenv("ONNXRUNTIME_LIB")
	if path == "" || lib == "" {
		if os.Getenv("PAPERGO_REQUIRE_DECISION") == "1" {
			t.Fatal("DECISION_MODELS_PATH and ONNXRUNTIME_LIB required")
		}
		t.Skip("native Laya bundle not configured")
	}
	e, err := New(Config{Models: path, Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}
func TestLayaReferenceCorpus(t *testing.T) {
	checkReferenceCorpus(t, "../../docs/performance-artifacts/laya/references.json", 475)
}
func TestLayaTypedReferenceCorpus(t *testing.T) {
	checkReferenceCorpus(t, "testdata/typed-references.json", 8)
}
func checkReferenceCorpus(t *testing.T, path string, want int) {
	e := nativeEngine(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// act_logits uses a snake-case key in the independent Python references.
	var parsed struct {
		Revision string `json:"model_revision"`
		Groups   []struct {
			Name string `json:"name"`
			Rows []struct {
				Request   Request   `json:"request"`
				IDs       []int64   `json:"ids"`
				Markers   []int64   `json:"markers"`
				QType     int64     `json:"qtype"`
				Logits    []float64 `json:"logits"`
				ActLogits []float64 `json:"act_logits"`
				Expected  []float64 `json:"expected"`
			}
		} `json:"groups"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Revision != Revision {
		t.Fatal("reference revision mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	count := 0
	quality := map[string]map[string]int{}
	var fixture struct {
		Tags []struct {
			Tag string `json:"tag"`
		}
		Documents []struct {
			ID       string
			Expected []string
		}
	}
	if want == 475 {
		raw, err := os.ReadFile("../../scripts/decision-benchmark/fixtures.json")
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &fixture); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range parsed.Groups {
		rows := make([]Request, len(g.Rows))
		for i, r := range g.Rows {
			enc, err := e.Encode(ctx, r.Request)
			if err != nil {
				t.Fatal(g.Name, i, err)
			}
			if !slices.Equal(enc.IDs, r.IDs) || !slices.Equal(enc.Markers, r.Markers) || enc.QType != r.QType {
				t.Fatal("encoding differs", g.Name, i)
			}
			rows[i] = r.Request
		}
		results, err := e.Decide(ctx, rows)
		if err != nil {
			t.Fatal(g.Name, err)
		}
		for i, r := range results {
			reference := g.Rows[i]
			if len(r.Probabilities) != len(reference.Expected) || len(r.ActLogits) != 2 {
				t.Fatal("invalid result shape")
			}
			for j, p := range r.Probabilities {
				if math.Abs(p-reference.Expected[j]) > 1e-4 || math.Abs(float64(r.Logits[j])-reference.Logits[j]) > 1e-3 {
					t.Fatalf("parity %s row %d option %d: %g vs %g", g.Name, i, j, p, reference.Expected[j])
				}
			}
			for j, v := range r.ActLogits {
				if math.Abs(float64(v)-reference.ActLogits[j])/max(1, math.Abs(reference.ActLogits[j])) > 1e-5 {
					t.Fatal("action parity", g.Name, i)
				}
			}
			if r.Index != slices.Index(reference.Expected, slices.Max(reference.Expected)) {
				t.Fatal("winner parity", g.Name, i)
			}
		}
		if strings.HasPrefix(g.Name, "matched_binary:") {
			docID := strings.TrimPrefix(g.Name, "matched_binary:")
			expected := map[string]bool{}
			for _, d := range fixture.Documents {
				if d.ID == docID {
					for _, tag := range d.Expected {
						expected[tag] = true
					}
				}
			}
			for _, threshold := range []float64{.5, .6, .7, .8, .9} {
				key := fmt.Sprintf("%.1f", threshold)
				if quality[key] == nil {
					quality[key] = map[string]int{"tp": 0, "fp": 0, "fn": 0, "exact_sets": 0, "documents": 0}
				}
				counts := quality[key]
				exact := true
				for i, r := range results {
					accepted := r.Probabilities[1] >= threshold
					if accepted != (g.Rows[i].Expected[1] >= threshold) {
						t.Fatal("threshold selection changed", g.Name, i, threshold)
					}
					truth := expected[fixture.Tags[i].Tag]
					if accepted && truth {
						counts["tp"]++
					}
					if accepted && !truth {
						counts["fp"]++
						exact = false
					}
					if !accepted && truth {
						counts["fn"]++
						exact = false
					}
				}
				counts["documents"]++
				if exact {
					counts["exact_sets"]++
				}
			}
		}
		if want == 8 {
			for i, row := range rows {
				single, err := e.Decide(ctx, []Request{row})
				if err != nil {
					t.Fatal(err)
				}
				for j, p := range single[0].Probabilities {
					if math.Abs(p-results[i].Probabilities[j]) > 1e-4 {
						t.Fatal("batch versus individual", g.Name, i, j)
					}
				}
			}
		}
		count += len(results)
	}
	if count != want {
		t.Fatal("reference question count", count, want)
	}
	if want == 475 {
		raw, _ := json.Marshal(quality)
		t.Log("production fixture thresholds", string(raw))
		if path := os.Getenv("DECISION_QUALITY_REPORT"); path != "" {
			if err = os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestLayaValidationLifecycle(t *testing.T) {
	e := nativeEngine(t)
	ctx := context.Background()
	valid := Request{State: "Factura de electricidad", Question: "Which category?", Options: []string{"Other document", "Electricity invoice"}}
	for _, r := range []Request{{State: "", Question: "x", Options: valid.Options}, {State: "x", Question: "x", Options: []string{"x"}}, {State: "<mask>", Question: "x", Options: valid.Options}, {State: "x\x00", Question: "x", Options: valid.Options}, {State: "x", Question: "x", Options: []string{"x", ""}}} {
		if _, err := e.Decide(ctx, []Request{r}); err == nil {
			t.Fatal("accepted invalid input", r)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.Decide(canceled, []Request{valid}); err == nil {
		t.Fatal("ignored cancellation")
	}
	r, err := e.Decide(ctx, []Request{valid})
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckResults([]Request{valid}, r); err != nil {
		t.Fatal(err)
	}
	e.gate <- struct{}{}
	waiting, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	defer stop()
	if _, err = e.Decide(waiting, []Request{valid}); err == nil {
		t.Fatal("waiting cancellation")
	}
	<-e.gate
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Decide(ctx, []Request{valid}); err == nil {
		t.Fatal("used closed engine")
	}
}
