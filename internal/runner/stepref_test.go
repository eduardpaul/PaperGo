package runner

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"papergo/internal/dal/daltest"
	"reflect"
	"strings"
	"testing"
)

// TestDurableStepReferences checks that a large workflow definition is stored
// on its version, each load record is a small shared reference, and every
// durable step output for the run is a step pointer.
func TestDurableStepReferences(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	const token = "DEFINITION-BODY"
	bulk := token + strings.Repeat("v", 8192)
	w := f.workflow(t, "Large version", `{
		"triggers": [{"type": "item.created", "collection_id": "$LIST"}],
		"variables": {"bulk": "`+bulk+`"},
		"flow": {"start": "stop", "nodes": {"stop": {"activity": "end"}}}}`)
	f.item(t, "alice", "One", nil)
	f.item(t, "alice", "Two", nil)
	runs := f.runs(t, w.ID, 2)

	definition := fmt.Sprint(only(t, daltest.Values(t, f.db.DAL, "workflow_versions", "definition", daltest.Row{"workflow_id": w.ID, "number": w.CurrentVersion})))
	if !strings.Contains(definition, token) || !strings.Contains(definition, `"flow"`) {
		t.Fatal("version definition missing the measured body")
	}

	var refs []string
	var markerBytes, dbosBytes, bodyBytes, dbosRows int
	for _, run := range runs {
		details, err := f.r.Run(ctx, "alice", run.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range details.Steps {
			body := []byte(fmt.Sprint(only(t, daltest.Values(t, f.db.DAL, "runner_step_results", "output", daltest.Row{"run_id": run.ID, "step_id": step.ID}))))
			if !reflect.DeepEqual(step.Output, decodeOutput(body)) {
				t.Fatalf("step %s exposes %+v instead of its body %s", step.Name, step.Output, body)
			}
		}
		load := fmt.Sprint(only(t, daltest.Values(t, f.db.DAL, "runner_step_results", "output", daltest.Row{"run_id": run.ID, "step": "load"})))
		var ref struct {
			WorkflowID string `json:"workflow_id"`
			Version    int    `json:"version"`
		}
		if err := json.Unmarshal([]byte(load), &ref); err != nil {
			t.Fatalf("load record: %s", load)
		}
		if ref.WorkflowID != w.ID || ref.Version != w.CurrentVersion || strings.Contains(load, token) || strings.Contains(load, `"flow"`) {
			t.Fatalf("load record %s", load)
		}
		if len(load) >= len(definition)/10 {
			t.Fatalf("load record is %d bytes and the definition is %d", len(load), len(definition))
		}
		refs = append(refs, load)
		markerBytes += len(load)

		for _, body := range daltest.Values(t, f.db.DAL, "runner_step_results", "output", daltest.Row{"run_id": run.ID}) {
			bodyBytes += len(fmt.Sprint(body))
		}

		outputs := daltest.Values(t, f.db.DAL, daltest.DBOS(f.db.DAL, "operation_outputs"), "output", daltest.Row{"workflow_uuid": run.ID})
		n := len(outputs)
		for _, value := range outputs {
			output, ok := value.(string)
			if !ok {
				t.Fatalf("step output %v", value)
			}
			decoded, err := decodeDurableOutput(output)
			if err != nil {
				t.Fatalf("step output: %v", err)
			}
			dbosBytes += len(output)
			var pointer stepPointer
			if err = json.Unmarshal(decoded, &pointer); err != nil || pointer.Kind != "step" || pointer.Step == nil || *pointer.Step < 0 {
				t.Fatalf("step stored %s", decoded)
			}
			if strings.Contains(string(decoded), token) || strings.Contains(output, token) {
				t.Fatal("step repeats the definition")
			}
		}
		if n < 3 {
			t.Fatalf("run %s recorded %d durable steps", run.ID, n)
		}
		dbosRows += n
	}
	if refs[0] != refs[1] {
		t.Fatalf("load records differ:\n%s\n%s", refs[0], refs[1])
	}
	t.Logf("definition %d bytes; two load markers %d bytes; step bodies %d bytes; dbos outputs %d bytes over %d rows", len(definition), markerBytes, bodyBytes, dbosBytes, dbosRows)
}

// decodeDurableOutput reads a DBOS payload. Current rows are base64 of JSON;
// a JSON payload is accepted first.
func decodeDurableOutput(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if json.Valid([]byte(raw)) {
		return []byte(raw), nil
	}
	return base64.StdEncoding.DecodeString(raw)
}

// only returns the single value of a column read.
func only(t *testing.T, values []any) any {
	t.Helper()
	if len(values) != 1 {
		t.Fatalf("read %d values, want one", len(values))
	}
	return values[0]
}
