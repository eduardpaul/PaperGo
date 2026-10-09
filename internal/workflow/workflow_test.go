package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDefinitionValidation(t *testing.T) {
	ok := `{"triggers": [{"type": "item.created", "collection_id": "c"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`
	for name, tc := range map[string]struct{ def, err string }{
		"valid":                {ok, ""},
		"unknown field":        {strings.Replace(ok, `"flow"`, `"flwo"`, 1), "unknown field"},
		"no triggers":          {`{"triggers": [], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "1 to 10 triggers"},
		"unknown trigger":      {strings.Replace(ok, "item.created", "item.renamed", 1), "unknown trigger type"},
		"workflow event":       {strings.Replace(ok, "item.created", "wf.other-flow.done", 1), ""},
		"bad cron":             {`{"triggers": [{"type": "schedule", "cron": "every day"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "invalid cron"},
		"bad zone":             {`{"triggers": [{"type": "schedule", "cron": "0 1 * * *", "time_zone": "Mars/Base"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "time_zone"},
		"condition scope":      {`{"triggers": [{"type": "item.created"}], "condition": {"field": "x", "op": "eq", "value": 1}, "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "needs collection_id"},
		"deleted condition":    {`{"triggers": [{"type": "item.deleted", "collection_id": "c"}], "condition": {"field": "x", "op": "eq", "value": 1}, "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "deleted items"},
		"inputs need manual":   {strings.Replace(ok, `"flow"`, `"inputs": {"n": {"type": "text"}}, "flow"`, 1), "manual trigger"},
		"missing start":        {strings.Replace(ok, `"start": "a"`, `"start": "b"`, 1), "start must name a node"},
		"unknown activity":     {strings.Replace(ok, `"end"`, `"teleport"`, 1), "unknown activity"},
		"unknown port":         {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "next": {"maybe": "b"}}, "b": {"activity": "end"}}}}`, "no port maybe"},
		"dangling port":        {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "next": {"done": "z"}}}}}`, "unknown node z"},
		"unreachable":          {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}, "b": {"activity": "end"}}}}`, "cannot be reached"},
		"terminal next":        {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end", "next": {"done": "a"}}}}}`, "ends the run"},
		"required input":       {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.create", "inputs": {"name": "x"}}}}}`, "collection_id is required"},
		"unknown input":        {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "inputs": {"id": "x"}}}}}`, "has no input id"},
		"if both":              {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "if", "inputs": {"filter": {}, "op": "eq"}}}}}`, "either filter"},
		"bad retry":            {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "retry": {"attempts": 0}}}}}`, "retry attempts"},
		"bad node name":        {`{"triggers": [{"type": "manual"}], "flow": {"start": "a.b", "nodes": {"a.b": {"activity": "end"}}}}`, "names use"},
		"reserved event":       {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "event.raise", "inputs": {"event": "completed"}}}}}`, "other than completed"},
		"selection":            {`{"triggers": [{"type": "manual", "collection_id": "c", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, ""},
		"selection needs list": {`{"triggers": [{"type": "manual", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "need collection_id"},
		"selection on items":   {`{"triggers": [{"type": "item.created", "collection_id": "c", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "on manual triggers"},
		"bad selection":        {`{"triggers": [{"type": "manual", "collection_id": "c", "selection": "all"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "per_item or selection"},
		"bad duration":         {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "delay", "inputs": {"duration": "soon"}}}}}`, "duration"},
	} {
		t.Run(name, func(t *testing.T) {
			def, err := ParseDefinition(json.RawMessage(tc.def))
			if err == nil {
				err = def.validate()
			}
			switch {
			case tc.err == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
				t.Fatalf("error %v, want %q", err, tc.err)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	s := &scope{ctx: context.Background(), now: time.Date(2026, 10, 9, 8, 30, 0, 0, time.UTC),
		trigger: map[string]any{"actor": "bob", "data": map[string]any{"revision_number": 3.0}},
		inputs:  map[string]any{"tags": []any{"a", "b"}},
		vars:    map[string]any{"limit": 100.0},
		steps:   map[string]any{"read item": map[string]any{"values": map[string]any{"total": 42.5}}},
		run:     map[string]any{"id": "run-1"},
		item: func(context.Context) (map[string]any, error) {
			return map[string]any{"name": "Lease", "values": map[string]any{"owner": "carol"}}, nil
		}}
	for in, want := range map[string]any{
		"{var:limit}":                   100.0,
		"{step:read item.values.total}": 42.5,
		"{input:tags}":                  []any{"a", "b"},
		"tags {input:tags}":             "tags a, b",
		"{item:name} for {item:values.owner} by {trigger:actor}": "Lease for carol by bob",
		"rev {trigger:data.revision_number} on {today}":          "rev 3 on 2026-10-09",
		"{{literal}} {missing:x}":                                "{literal} {missing:x}",
		"{var:absent}":                                           nil,
	} {
		got, err := s.resolve(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		if string(g) != string(w) {
			t.Fatalf("%s = %s, want %s", in, g, w)
		}
	}
	nested, err := s.resolve(map[string]any{"values": map[string]any{"limit": "{var:limit}"}, "list": []any{"{run:id}"}})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := json.Marshal(nested); string(g) != `{"list":["run-1"],"values":{"limit":100}}` {
		t.Fatalf("nested: %s", g)
	}
	s.item = nil
	if _, err = s.resolve("{item:name}"); err == nil {
		t.Fatal("item token without an item")
	}
}

func TestBuiltInFill(t *testing.T) {
	b := builtins["items.expire"]
	params, err := b.params(map[string]any{"field": "expires"}, "list-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := b.fill(params)
	if err != nil {
		t.Fatal(err)
	}
	def, err := ParseDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = def.validate(); err != nil {
		t.Fatal(err)
	}
	tr := def.Triggers[0]
	if tr.CollectionID != "list-1" || tr.Cron != "0 1 * * *" || tr.TimeZone != "UTC" || def.Condition.Field != "expires" {
		t.Fatalf("filled: %s", raw)
	}
	if _, err = b.params(map[string]any{"field": "expires", "colour": "red"}, "list-1"); err == nil {
		t.Fatal("unknown parameter accepted")
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		l, op, r any
		want     bool
	}{
		{"10", "gt", "9", true},
		{"10", "gt", "9a", false},
		{"Lease", "contains", "eas", true},
		{"", "empty", nil, true},
		{"x", "not_empty", nil, true},
		{2.0, "le", "2", true},
	} {
		if got := compare(tc.l, tc.op.(string), tc.r); got != tc.want {
			t.Fatalf("%v %v %v = %v", tc.l, tc.op, tc.r, got)
		}
	}
}
