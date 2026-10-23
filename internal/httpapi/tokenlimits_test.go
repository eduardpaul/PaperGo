package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
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
	read := `"scopes":["workspace.read","item.read"]`
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
	code, writer, body := issue(`{"name":"Invoices writer","scopes":["workspace.read","item.read","item.write"],"resources":["` + invoices + `"]}`)
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
		"organization":   `{"name":"x","scopes":["item.read","workspace.create"],"resources":["` + invoices + `"]}`,
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

// A token without item.write changes nothing: not items, and not
// workspaces, lists or libraries themselves, whose settings, permissions,
// views and existence its holder could otherwise manage. Reading still works.
func TestReadOnlyTokensChangeNothing(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Operations"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Invoices"})
	library, _ := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Contracts"})
	item, _ := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Invoice"})
	read := []string{"workspace.read", "item.read", "contentType.read", "document.read", "taxonomy.read", "user.read"}
	// Refusals are 403, or 404 for workspaces, which do not reveal themselves.
	for _, limit := range [][]string{nil, {w.ID}} {
		tok, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Read only", Scopes: read, Resources: limit})
		if err != nil {
			t.Fatal(err)
		}
		version := func(id string) string {
			r, err := s.GetSurface(ctx, "alice", id, "auto")
			if err != nil {
				t.Fatal(err)
			}
			return `"` + strconv.Itoa(r.Version) + `"`
		}
		for _, id := range []string{w.ID, list.ID, library.ID, item.ID} {
			if r := request(h, "GET", "/v1/resources/"+id, "", tok.Secret, "", ""); r.Code != 200 {
				t.Fatalf("read %s: %d", id, r.Code)
			}
			if r := request(h, "PATCH", "/v1/resources/"+id, `{"name":"Renamed"}`, tok.Secret, version(id), "application/json"); r.Code != 403 && r.Code != 404 {
				t.Fatalf("limit %v renamed %s: %d %s", limit, id, r.Code, r.Body.String())
			}
			if r := request(h, "DELETE", "/v1/resources/"+id+"?recursive=true", "", tok.Secret, version(id), ""); r.Code != 403 && r.Code != 404 {
				t.Fatalf("limit %v deleted %s: %d %s", limit, id, r.Code, r.Body.String())
			}
		}
		for _, c := range []struct{ method, path, body string }{
			{"POST", "/v1/resources/" + w.ID + "/children", `{"kind":"list","name":"New"}`},
			{"POST", "/v1/resources/" + list.ID + "/children", `{"kind":"item","name":"New"}`},
			{"POST", "/v1/resources/" + list.ID + "/views", `{"name":"Mine"}`},
		} {
			if r := request(h, c.method, c.path, c.body, tok.Secret, "", "application/json"); r.Code != 403 && r.Code != 404 {
				t.Fatalf("limit %v %s %s: %d %s", limit, c.method, c.path, r.Code, r.Body.String())
			}
		}
		perms := request(h, "GET", "/v1/resources/"+list.ID+"/permissions", "", testToken, "", "")
		if r := request(h, "PUT", "/v1/resources/"+list.ID+"/permissions", perms.Body.String(), tok.Secret, version(list.ID), "application/json"); r.Code != 403 && r.Code != 404 {
			t.Fatalf("limit %v changed permissions: %d %s", limit, r.Code, r.Body.String())
		}
	}
}

// A read-and-write token, as an AI assistant connection holds, adds and
// edits items but leaves the workspaces, lists and libraries it reaches as
// they are: creating, configuring, sharing and deleting them needs
// list.manage, which members hold and such tokens do not.
func TestWriteTokensLeavePlacesAsTheyAre(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Operations"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Invoices"})
	library, _ := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Contracts"})
	if _, err = s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Invoice"}); err != nil {
		t.Fatal(err)
	}
	write := []string{"workspace.read", "item.read", "item.write", "contentType.read", "document.read", "taxonomy.read", "user.read"}
	version := func(id string) string {
		r, err := s.GetSurface(ctx, "alice", id, "auto")
		if err != nil {
			t.Fatal(err)
		}
		return `"` + strconv.Itoa(r.Version) + `"`
	}
	for _, limit := range [][]string{nil, {list.ID, library.ID}, {w.ID}} {
		tok, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Assistant", Scopes: write, Resources: limit})
		if err != nil {
			t.Fatal(err)
		}
		created := request(h, "POST", "/v1/resources/"+list.ID+"/children", `{"kind":"item","name":"New"}`, tok.Secret, "", "application/json")
		if created.Code != 201 {
			t.Fatalf("limit %v created no item: %d %s", limit, created.Code, created.Body.String())
		}
		for _, id := range []string{w.ID, list.ID, library.ID} {
			if r := request(h, "PATCH", "/v1/resources/"+id, `{"name":"Renamed"}`, tok.Secret, version(id), "application/json"); r.Code != 403 && r.Code != 404 {
				t.Fatalf("limit %v renamed %s: %d %s", limit, id, r.Code, r.Body.String())
			}
			if r := request(h, "DELETE", "/v1/resources/"+id+"?recursive=true", "", tok.Secret, version(id), ""); r.Code != 403 && r.Code != 404 {
				t.Fatalf("limit %v deleted %s: %d %s", limit, id, r.Code, r.Body.String())
			}
		}
		if r := request(h, "POST", "/v1/resources/"+w.ID+"/children", `{"kind":"list","name":"New"}`, tok.Secret, "", "application/json"); r.Code != 403 && r.Code != 404 {
			t.Fatalf("limit %v created a list: %d %s", limit, r.Code, r.Body.String())
		}
		perms := request(h, "GET", "/v1/resources/"+list.ID+"/permissions", "", testToken, "", "")
		if r := request(h, "PUT", "/v1/resources/"+list.ID+"/permissions", perms.Body.String(), tok.Secret, version(list.ID), "application/json"); r.Code != 403 && r.Code != 404 {
			t.Fatalf("limit %v changed permissions: %d %s", limit, r.Code, r.Body.String())
		}
	}
	// Holding list.manage is what lets a token manage, and only unlimited.
	tok, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Admin script", Scopes: append(write, "list.manage")})
	if err != nil {
		t.Fatal(err)
	}
	if r := request(h, "DELETE", "/v1/resources/"+library.ID, "", tok.Secret, version(library.ID), ""); r.Code != 204 {
		t.Fatalf("list.manage deleted nothing: %d %s", r.Code, r.Body.String())
	}
	if _, err = s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Limited", Scopes: append(write, "list.manage"), Resources: []string{list.ID}}); err == nil {
		t.Fatal("a limited token got list.manage")
	}
}

// The audit log names the access token an action was taken with, such as an
// AI assistant's connection, beside the person who holds it.
func TestAuditNamesTheCredential(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Operations"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Invoices"})
	tok, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Claude", Scopes: []string{"workspace.read", "item.read", "item.write"}})
	if err != nil {
		t.Fatal(err)
	}
	created := request(h, "POST", "/v1/resources/"+list.ID+"/children", `{"kind":"item","name":"By the assistant"}`, tok.Secret, "", "application/json")
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var item struct{ ID string }
	_ = json.Unmarshal(created.Body.Bytes(), &item)
	r := request(h, "GET", "/v1/audit?entity_id="+item.ID, "", testToken, "", "")
	var page struct {
		Value []dms.AuditEvent `json:"value"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Value) == 0 {
		t.Fatal(r.Code, r.Body.String())
	}
	if e := page.Value[0]; e.Subject != "alice" || e.Credential == nil || e.Credential.ID != tok.Token.ID || e.Credential.Name != "Claude" {
		t.Fatalf("event %+v %+v", e, e.Credential)
	}
	// An action without a token names none.
	byHand, _ := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "By hand"})
	r = request(h, "GET", "/v1/audit?entity_id="+byHand.ID, "", testToken, "", "")
	if r.Code != 200 || strings.Contains(r.Body.String(), `"credential"`) {
		t.Fatal(r.Code, r.Body.String())
	}
}
