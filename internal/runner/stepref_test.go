package runner

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
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

	var definition string
	if err := f.db.SQL.QueryRow(`SELECT definition FROM workflow_versions WHERE workflow_id=? AND number=?`, w.ID, w.CurrentVersion).Scan(&definition); err != nil {
		t.Fatal(err)
	}
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
			var body []byte
			if err := f.db.SQL.QueryRow(`SELECT output FROM runner_step_results WHERE run_id=? AND step_id=?`, run.ID, step.ID).Scan(&body); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(step.Output, decodeOutput(body)) {
				t.Fatalf("step %s exposes %+v instead of its body %s", step.Name, step.Output, body)
			}
		}
		var load string
		if err := f.db.SQL.QueryRow(`SELECT output FROM runner_step_results WHERE run_id=? AND step='load'`, run.ID).Scan(&load); err != nil {
			t.Fatal(err)
		}
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

		var bodies int
		if err := f.db.SQL.QueryRow(`SELECT coalesce(sum(length(output)),0) FROM runner_step_results WHERE run_id=?`, run.ID).Scan(&bodies); err != nil {
			t.Fatal(err)
		}
		bodyBytes += bodies

		rows, err := f.db.SQL.Query(`SELECT function_name, output FROM operation_outputs WHERE workflow_uuid=?`, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var name string
			var output sql.NullString
			if err = rows.Scan(&name, &output); err != nil {
				t.Fatal(err)
			}
			n++
			if !output.Valid {
				t.Fatalf("step %s has no output", name)
			}
			decoded, err := decodeDurableOutput(output.String)
			if err != nil {
				t.Fatalf("step %s output: %v", name, err)
			}
			dbosBytes += len(output.String)
			var pointer stepPointer
			if err = json.Unmarshal(decoded, &pointer); err != nil || pointer.Kind != "step" || pointer.Step == nil || *pointer.Step < 0 {
				t.Fatalf("step %s stored %s", name, decoded)
			}
			if strings.Contains(string(decoded), token) || strings.Contains(output.String, token) {
				t.Fatalf("step %s repeats the definition", name)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
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
