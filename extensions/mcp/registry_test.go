package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"papergo/extensions/mcp"
	"papergo/extensions/mcp/builtin"
)

func tool(name string) mcp.Tool {
	return mcp.Tool{Name: name, InputSchema: json.RawMessage(`{"type": "object", "additionalProperties": false, "properties": {"n": {"type": "integer"}}}`),
		Scopes: []string{"item.read"},
		Call: func(context.Context, mcp.Caller, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}}
}

func TestBuiltinsRegister(t *testing.T) {
	r, err := mcp.NewRegistry(builtin.Tools(builtin.Options{}), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range r.Tools() {
		names = append(names, tool.Name)
	}
	if got := strings.Join(names, ","); got != "create_item,get_collection_schema,get_item,get_item_text,list_collections,list_terms,list_workspaces,query_items,search,update_item" {
		t.Fatalf("tools %s", got)
	}
}

func TestRegistryRefusesInvalidRegistrations(t *testing.T) {
	noScopes := tool("x_a")
	noScopes.Scopes = nil
	open := tool("x_a")
	open.InputSchema = json.RawMessage(`{"type": "object", "properties": {}}`)
	badOutput := tool("x_a")
	badOutput.OutputSchema = json.RawMessage(`{"type": 7}`)
	for name, c := range map[string]struct {
		builtins []mcp.Tool
		contribs []mcp.Contribution
		enabled  []string
		want     string
	}{
		"invalid name":             {builtins: []mcp.Tool{tool("bad name")}, want: "must be 1 to 64"},
		"long name":                {builtins: []mcp.Tool{tool(strings.Repeat("a", 65))}, want: "must be 1 to 64"},
		"duplicate built-in":       {builtins: []mcp.Tool{tool("a"), tool("a")}, want: "also built-in's"},
		"invalid contribution ID":  {contribs: []mcp.Contribution{{ID: "Acme", Tools: []mcp.Tool{tool("Acme_a")}}}, want: "contribution ID"},
		"normalized prefix":        {contribs: []mcp.Contribution{{ID: "acme.crm", Tools: []mcp.Tool{tool("acme_crm_a")}}, {ID: "acme-crm", Tools: []mcp.Tool{tool("acme_crm_b")}}}, want: "both name their tools acme_crm_"},
		"unprefixed contribution":  {contribs: []mcp.Contribution{{ID: "acme", Tools: []mcp.Tool{tool("get_item2")}}}, want: "must be named acme_"},
		"bare prefix":              {contribs: []mcp.Contribution{{ID: "acme", Tools: []mcp.Tool{tool("acme_")}}}, want: "must be named acme_"},
		"contribution vs built-in": {builtins: []mcp.Tool{tool("x_a")}, contribs: []mcp.Contribution{{ID: "x", Tools: []mcp.Tool{tool("x_a")}}}, want: "also built-in's"},
		"disabled still validated": {contribs: []mcp.Contribution{{ID: "x", Tools: []mcp.Tool{noScopes}}}, want: "required scope"},
		"open input schema":        {contribs: []mcp.Contribution{{ID: "x", Tools: []mcp.Tool{open}}}, want: "additionalProperties"},
		"invalid output schema":    {contribs: []mcp.Contribution{{ID: "x", Tools: []mcp.Tool{badOutput}}}, want: "output schema"},
		"unknown enabled":          {enabled: []string{"nope"}, want: `no compiled contribution is "nope"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := mcp.NewRegistry(c.builtins, c.contribs, c.enabled)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
		})
	}
}

func TestRegistryKeepsEnabledContributions(t *testing.T) {
	contribs := []mcp.Contribution{{ID: "acme.crm", Tools: []mcp.Tool{tool("acme_crm_find")}}, {ID: "other", Tools: []mcp.Tool{tool("other_x")}}}
	r, err := mcp.NewRegistry([]mcp.Tool{tool("b")}, contribs, []string{"acme.crm"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Tool("acme_crm_find") == nil || r.Tool("other_x") != nil || len(r.Tools()) != 2 || r.Tools()[0].Name != "acme_crm_find" {
		t.Fatalf("tools %v", r.Tools())
	}
}

func TestValidate(t *testing.T) {
	r, err := mcp.NewRegistry([]mcp.Tool{tool("a")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := r.Tool("a")
	for _, empty := range []string{"", "null", " "} {
		if got, err := reg.Validate(json.RawMessage(empty)); err != nil || string(got) != "{}" {
			t.Fatalf("%q: %s %v", empty, got, err)
		}
	}
	exact := json.RawMessage(`{"n": 9007199254740993}`)
	if got, err := reg.Validate(exact); err != nil || string(got) != string(exact) {
		t.Fatalf("exact: %s %v", got, err)
	}
	for _, bad := range []string{`{"m": 1}`, `{"n": "1"}`, `{"n": 1.5}`, `[]`, `{`} {
		var f *mcp.Failure
		if _, err := reg.Validate(json.RawMessage(bad)); !errors.As(err, &f) || f.Code != mcp.CodeInvalidArguments {
			t.Fatalf("%s: %v", bad, err)
		}
	}
}

func TestFailureOf(t *testing.T) {
	for status, want := range map[int]string{400: mcp.CodeInvalidArguments, 422: mcp.CodeInvalidArguments, 401: mcp.CodeUnauthorized, 403: mcp.CodeForbidden,
		404: mcp.CodeNotFound, 412: mcp.CodeVersionConflict, 409: mcp.CodeConflict, 413: mcp.CodeTooLarge, 429: mcp.CodeUnavailable, 503: mcp.CodeUnavailable} {
		if f := mcp.FailureOf(&mcp.APIError{Status: status, RequestID: "r"}); f.Code != want || f.RequestID != "r" {
			t.Errorf("%d: %+v", status, f)
		}
	}
	if f := mcp.FailureOf(errors.New("boom")); f.Code != mcp.CodeInternal || strings.Contains(f.Message, "boom") {
		t.Fatalf("internal %+v", f)
	}
}
