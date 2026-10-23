package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dms"
	"papergo/internal/identity"
)

// A token limited to places reaches their content, and a listed
// collection's workspace for reading, and nothing else: not through reads,
// listings, queries or writes, and not by issuing tokens or working across
// the organization.
func TestTokenResourceLimits(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	create := func(subject, parent, kind, name string) string {
		t.Helper()
		r, err := s.Create(ctx, subject, parent, dms.CreateResource{Kind: kind, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	ops := create("alice", "", "workspace", "Operations")
	invoices := create("alice", ops, "list", "Invoices")
	payroll := create("alice", ops, "list", "Payroll")
	invoice := create("alice", invoices, "item", "Invoice 1")
	salary := create("alice", payroll, "item", "Salary")
	sales := create("alice", "", "workspace", "Sales")
	leads := create("alice", sales, "list", "Leads")
	bobs := create("bob", "", "workspace", "Bob's")
	bobsList := create("bob", bobs, "list", "Private")

	issue := func(body string) (int, identity.CreatedAccessToken, string) {
		t.Helper()
		r := request(h, "POST", "/v1/identity/me/tokens", body, testToken, "", "application/json")
		var out identity.CreatedAccessToken
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		return r.Code, out, r.Body.String()
	}
	read := `"scopes":["workspace.read","list.read"]`
	code, invoicesOnly, body := issue(`{"name":"Invoices assistant",` + read + `,"resources":["` + invoices + `"]}`)
	if code != 201 || strings.Join(invoicesOnly.Token.Resources, ",") != invoices {
		t.Fatal(code, body)
	}
	get := func(token, path string) (int, string) {
		r := request(h, "GET", path, "", token, "", "")
		return r.Code, r.Body.String()
	}
	names := func(token, path string) string {
		t.Helper()
		code, body := get(token, path)
		var page struct {
			Value []struct {
				Name string `json:"name"`
			} `json:"value"`
		}
		if code != 200 || json.Unmarshal([]byte(body), &page) != nil {
			t.Fatal(path, code, body)
		}
		var out []string
		for _, v := range page.Value {
			out = append(out, v.Name)
		}
		return strings.Join(out, ",")
	}
	secret := invoicesOnly.Secret
	if got := names(secret, "/v1/workspaces"); got != "Operations" {
		t.Fatalf("workspaces %s", got)
	}
	if got := names(secret, "/v1/resources/"+ops+"/children"); got != "Invoices" {
		t.Fatalf("collections %s", got)
	}
	for _, id := range []string{ops, invoices, invoice} {
		if code, body := get(secret, "/v1/resources/"+id); code != 200 {
			t.Fatal("limited read", code, body)
		}
	}
	for _, id := range []string{payroll, salary, sales, leads} {
		if code, _ := get(secret, "/v1/resources/"+id); code != 403 && code != 404 {
			t.Fatalf("%s outside the limit: %d", id, code)
		}
	}
	if r := request(h, "POST", "/v1/resources/"+payroll+"/query", `{}`, secret, "", "application/json"); r.Code != 403 && r.Code != 404 {
		t.Fatal("query outside the limit", r.Code, r.Body.String())
	}
	var me identity.MeProfile
	if code, body := get(secret, "/v1/identity/me"); code != 200 || json.Unmarshal([]byte(body), &me) != nil || strings.Join(me.Resources, ",") != invoices {
		t.Fatal("me", code, body)
	}
	if code, body := get(testToken, "/v1/identity/me"); code != 200 || strings.Contains(body, `"resources"`) {
		t.Fatal("unlimited me", code, body)
	}

	// Writing stays inside the limit, and a listed collection's workspace is
	// readable but not writable.
	code, writer, body := issue(`{"name":"Invoices writer","scopes":["workspace.read","list.read","list.write"],"resources":["` + invoices + `"]}`)
	if code != 201 {
		t.Fatal(code, body)
	}
	if r := request(h, "POST", "/v1/resources/"+invoices+"/children", `{"kind":"item","name":"Invoice 2"}`, writer.Secret, "", "application/json"); r.Code != 201 {
		t.Fatal("limited write", r.Code, r.Body.String())
	}
	if r := request(h, "POST", "/v1/resources/"+payroll+"/children", `{"kind":"item","name":"Raise"}`, writer.Secret, "", "application/json"); r.Code != 403 && r.Code != 404 {
		t.Fatal("write outside the limit", r.Code, r.Body.String())
	}
	if r := request(h, "POST", "/v1/resources/"+ops+"/children", `{"kind":"list","name":"New"}`, writer.Secret, "", "application/json"); r.Code != 403 && r.Code != 404 {
		t.Fatal("collection created in a workspace outside the limit", r.Code, r.Body.String())
	}
	// A limited token issues no tokens.
	if r := request(h, "POST", "/v1/identity/me/tokens", `{"name":"Child",`+read+`,"resources":["`+invoices+`"]}`, writer.Secret, "", "application/json"); r.Code != 403 {
		t.Fatal("limited token issued a token", r.Code, r.Body.String())
	}

	// A workspace limit includes collections created there later.
	code, salesOnly, body := issue(`{"name":"Sales assistant",` + read + `,"resources":["` + sales + `"]}`)
	if code != 201 {
		t.Fatal(code, body)
	}
	create("alice", sales, "library", "Contracts")
	if got := names(salesOnly.Secret, "/v1/resources/"+sales+"/children"); got != "Contracts,Leads" && got != "Leads,Contracts" {
		t.Fatalf("workspace limit %s", got)
	}
	if got := names(salesOnly.Secret, "/v1/workspaces"); got != "Sales" {
		t.Fatalf("workspace limit workspaces %s", got)
	}

	// Limits name readable workspaces, lists and libraries, and leave out
	// scopes that act across the organization.
	for name, body := range map[string]string{
		"item":           `{"name":"x",` + read + `,"resources":["` + invoice + `"]}`,
		"unreadable":     `{"name":"x",` + read + `,"resources":["` + bobsList + `"]}`,
		"duplicate":      `{"name":"x",` + read + `,"resources":["` + invoices + `","` + invoices + `"]}`,
		"empty":          `{"name":"x",` + read + `,"resources":[]}`,
		"organization":   `{"name":"x","scopes":["list.read","workspace.create"],"resources":["` + invoices + `"]}`,
		"too many":       `{"name":"x",` + read + `,"resources":["` + strings.Repeat(invoices+`","`, identity.MaxTokenResources) + invoices + `"]}`,
		"unknown":        `{"name":"x",` + read + `,"resources":["00000000-0000-4000-8000-000000000000"]}`,
		"other's member": `{"name":"x",` + read + `,"resources":["` + bobs + `"]}`,
	} {
		if code, _, body := issue(body); code != 422 && code != 400 {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}

	// Queued work runs with the live token's limit.
	restored, err := identity.RestoreCredential(ctx, dal.NewStore(s.Directory().DB), "alice", invoicesOnly.Token.ID, invoicesOnly.Token.Scopes)
	if err != nil {
		t.Fatal(err)
	}
	if token, ok := identity.AccessTokenContext(restored); !ok || strings.Join(token.Resources, ",") != invoices {
		t.Fatalf("restored %+v", token)
	}
	if _, err := s.GetSurface(restored, "alice", salary, "auto"); err == nil {
		t.Fatal("queued work escaped the limit")
	}
}
