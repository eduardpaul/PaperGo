package scriptrun

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// diesel sets up the diesel sample in the runtime's workspace: its fields,
// content types and list from samples/diesel/resources.json, and its four
// workflows bound to them.
func diesel(t *testing.T, r *runtime) (list string, types map[string]string, workflows map[string]workflow.Workflow) {
	t.Helper()
	ctx := t.Context()
	var spec struct {
		Fields []dms.CreateField `json:"fields"`
		Types  []struct {
			Key, Name string
			Fields    []string
		} `json:"content_types"`
	}
	raw, err := os.ReadFile("../../samples/diesel/resources.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	fields, types := map[string]string{}, map[string]string{}
	for _, in := range spec.Fields {
		f, err := r.s.CreateCatalogField(ctx, "organization-admin", in)
		if err != nil {
			t.Fatal(in.Key, err)
		}
		fields[in.Key] = f.ID
	}
	ids := []string{}
	for _, in := range spec.Types {
		var own []string
		for _, key := range in.Fields {
			own = append(own, fields[key])
		}
		ct, err := r.s.CreateContentType(ctx, "organization-admin", dms.ContentTypeInput{Key: in.Key, Name: in.Name, FieldIds: own})
		if err != nil {
			t.Fatal(in.Key, err)
		}
		types[in.Key] = ct.ID
		ids = append(ids, ct.ID)
	}
	l, err := r.s.Create(ctx, "alice", r.ws, dms.CreateResource{Kind: "list", Name: "Diesel", ContentTypeIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	bindings := strings.NewReplacer("$LIST", l.ID, "$READING_TYPE", types["diesel_reading"], "$PURCHASE_TYPE", types["diesel_purchase"], "$HEATING_TYPE", types["diesel_heating"])
	workflows = map[string]workflow.Workflow{}
	for _, name := range []string{"diesel-levels", "diesel-measure-reminder", "diesel-refill", "diesel-sample-data"} {
		raw, err := os.ReadFile(filepath.Join("../../samples/diesel", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var sample struct {
			workflow.Save
			Script string `json:"script"`
		}
		if err = json.Unmarshal([]byte(bindings.Replace(string(raw))), &sample); err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(filepath.Join("../../samples/diesel", sample.Script))
		if err != nil {
			t.Fatal(err)
		}
		sample.Save.Source = string(source)
		w, err := r.wf.Create(ctx, "alice", r.ws, sample.Save)
		if err != nil {
			t.Fatal(name, err)
		}
		workflows[name] = w
	}
	return l.ID, types, workflows
}

type dieselItem struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Version int            `json:"version"`
	Values  map[string]any `json:"values"`
}

// dieselItems reads the list's items of a type.
func dieselItems(t *testing.T, r *runtime, list, typ string) []dieselItem {
	t.Helper()
	var page struct {
		Value []dieselItem `json:"value"`
	}
	r.call(t, "POST", "/v1/resources/"+list+"/query?$top=200", `{"query":{"content_type_id":"`+typ+`"}}`, 200, &page)
	return page.Value
}

// settle waits until check holds.
func settle(t *testing.T, what string, check func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !check(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
	}
}

type dieselTask struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func dieselTasks(t *testing.T, r *runtime, key, status string) []dieselTask {
	t.Helper()
	var page struct {
		Value []dieselTask `json:"value"`
	}
	r.call(t, "GET", "/v1/workspaces/"+r.ws+"/tasks?key="+key+"&status="+status, "", 200, &page)
	return page.Value
}

// The diesel sample's workflows work out a real tank's history, ask for a
// refill once and keep it current, and close it with the purchase.
func TestDieselSample(t *testing.T) {
	r := run(t)
	list, types, workflows := diesel(t, r)
	reading, purchase := types["diesel_reading"], types["diesel_purchase"]

	loaded := r.await(t, r.start(t, workflows["diesel-sample-data"], nil), "completed")
	if !strings.Contains(string(loaded.Result), `"readings":19`) {
		t.Fatalf("sample data: %s", loaded.Result)
	}
	settle(t, "converting the sample readings", func() bool {
		for _, item := range dieselItems(t, r, list, reading) {
			if item.Values["diesel_level_liters"] == nil || item.Values["diesel_flag"] == nil {
				return false
			}
		}
		return true
	})
	byName := map[string]dieselItem{}
	for _, item := range dieselItems(t, r, list, reading) {
		byName[item.Name] = item
		if item.Values["diesel_flag"] != "ok" {
			t.Errorf("%s is marked %v", item.Name, item.Values["diesel_flag"])
		}
	}
	// (200 - 86.8) / 125 * 750 = 679.2 L, 90.6 %.
	if got := byName["Reading 2025-11-24 · 86.8 cm"].Values; fmt.Sprint(got["diesel_level_liters"]) != "679.2" || fmt.Sprint(got["diesel_fill_percent"]) != "90.6" {
		t.Fatalf("a reading's litres: %v", got)
	}
	settle(t, "pricing the sample purchases", func() bool {
		for _, item := range dieselItems(t, r, list, purchase) {
			if item.Values["diesel_price_per_liter"] == nil {
				return false
			}
		}
		return true
	})
	for _, item := range dieselItems(t, r, list, purchase) {
		if item.Name == "Purchase 2026-09-03 · 535.0 L" && fmt.Sprint(item.Values["diesel_price_per_liter"]) != "1.594" {
			t.Fatalf("price per litre: %v", item.Values)
		}
	}
	if again := r.await(t, r.start(t, workflows["diesel-sample-data"], nil), "completed"); string(again.Result) != `"the list has readings already"` {
		t.Fatalf("loading twice: %s", again.Result)
	}

	add := func(name string, values string) string {
		t.Helper()
		var created dieselItem
		r.call(t, "POST", "/v1/resources/"+list+"/children", `{"kind":"item","name":"`+name+`","content_type_id":"`+reading+`","values":`+values+`}`, 201, &created)
		return created.ID
	}
	// 170 cm from the mouth leaves 30 cm: 180 L, below the 200 L threshold.
	add("Low", `{"diesel_measured_at":"2026-12-20T10:00:00Z","diesel_stick_cm":"170.0"}`)
	settle(t, "asking for a refill", func() bool { return len(dieselTasks(t, r, "diesel_refill", "open")) == 1 })
	if open := dieselTasks(t, r, "diesel_refill", "open"); open[0].Title != "Fill the diesel tank: 180.0 L left" {
		t.Fatalf("refill task: %+v", open)
	}
	add("Lower", `{"diesel_measured_at":"2026-12-24T10:00:00Z","diesel_stick_cm":"175.0"}`)
	settle(t, "updating the refill task", func() bool {
		open := dieselTasks(t, r, "diesel_refill", "open")
		return len(open) == 1 && open[0].Title == "Fill the diesel tank: 150.0 L left"
	})
	var bought dieselItem
	r.call(t, "POST", "/v1/resources/"+list+"/children", `{"kind":"item","name":"Bought","content_type_id":"`+purchase+`","values":{"diesel_purchased_at":"2026-12-26T10:00:00Z","diesel_bought_liters":"550.0","diesel_total_price":880}}`, 201, &bought)
	settle(t, "closing the refill task", func() bool { return len(dieselTasks(t, r, "diesel_refill", "open")) == 0 })
	if cancelled := dieselTasks(t, r, "diesel_refill", "cancelled"); len(cancelled) != 1 {
		t.Fatalf("the refill task was not cancelled: %+v", cancelled)
	}

	// Full without a purchase since the last reading: marked check.
	odd := add("Odd", `{"diesel_measured_at":"2027-01-05T10:00:00Z","diesel_stick_cm":"75.0"}`)
	flag := func() any {
		var item dieselItem
		r.call(t, "GET", "/v1/resources/"+odd, "", 200, &item)
		return item.Values["diesel_flag"]
	}
	settle(t, "marking an odd reading", func() bool { return flag() == "check" })
	var item dieselItem
	r.call(t, "GET", "/v1/resources/"+odd, "", 200, &item)
	r.callWith(t, "PATCH", "/v1/resources/"+odd, `{"values":{"diesel_flag":"reviewed"}}`, fmt.Sprintf(`"%d"`, item.Version), 200)
	recalculated := r.await(t, r.start(t, workflows["diesel-levels"], nil), "completed")
	if !strings.Contains(string(recalculated.Result), `"readings":22`) || flag() != "reviewed" {
		t.Fatalf("recalculating: %s, the reviewed reading is %v", recalculated.Result, flag())
	}
}

// callWith makes an API request as alice at the version match names.
func (r *runtime) callWith(t *testing.T, method, path, body, match string, want int) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", match)
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
	}
}
