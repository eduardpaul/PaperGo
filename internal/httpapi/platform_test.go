package httpapi

import (
	"encoding/json"
	"fmt"
	"papergo/ent"
	"strings"
	"testing"
)

func TestRESTPlatformCatalogQueriesAndPreconditions(t *testing.T) {
	h, _, _ := setup(t)
	send := func(method, path, body, match string, expected int) []byte {
		t.Helper()
		media := ""
		if body != "" {
			media = "application/json"
		}
		res := request(h, method, path, body, testToken, match, media)
		if res.Code != expected {
			t.Fatalf("%s %s: %d %s", method, path, res.Code, res.Body.String())
		}
		return res.Body.Bytes()
	}
	wbody := send("POST", "/v1/workspaces", `{"name":"Platform"}`, "", 201)
	var w ent.Resource
	if e := json.Unmarshal(wbody, &w); e != nil {
		t.Fatal(e)
	}
	lbody := send("POST", "/v1/resources/"+w.ID+"/children", `{"kind":"list","name":"Records"}`, "", 201)
	var l ent.Resource
	json.Unmarshal(lbody, &l)
	base := "/v1/workspaces/" + w.ID
	groupBody := send("POST", base+"/term-groups", `{"name":"Subjects"}`, "", 201)
	var group ent.TermGroup
	json.Unmarshal(groupBody, &group)
	setBody := send("POST", base+"/term-sets", fmt.Sprintf(`{"group_id":%q,"key":"topics","name":"Topics"}`, group.ID), "", 201)
	var set ent.TermSet
	json.Unmarshal(setBody, &set)
	termBody := send("POST", "/v1/term-sets/"+set.ID+"/terms", `{"name":"Finance","labels":{"es":"Finanzas"},"synonyms":["Money"]}`, "", 201)
	var term ent.Term
	json.Unmarshal(termBody, &term)
	tplBody := send("POST", base+"/templates", fmt.Sprintf(`{"key":"record","name":"Record","fields":[{"key":"serial","label":"Serial","type":"integer","indexed":true,"options":{"default_value":9007199254740993}},{"key":"topic","label":"Topic","type":"term","options":{"term_set_id":%q}}]}`, set.ID), "", 201)
	var tpl ent.SchemaTemplate
	json.Unmarshal(tplBody, &tpl)
	send("GET", "/v1/templates/"+tpl.ID, "", "", 200)
	send("POST", "/v1/resources/"+l.ID+"/templates/"+tpl.ID+"/apply", `{"template_version":1}`, "", 428)
	applied := send("POST", "/v1/resources/"+l.ID+"/templates/"+tpl.ID+"/apply", `{"template_version":1}`, `"1"`, 200)
	if !strings.Contains(string(applied), `"version":2`) {
		t.Fatal("collection ETag/version")
	}
	itemBody := send("POST", "/v1/resources/"+l.ID+"/children", fmt.Sprintf(`{"kind":"item","name":"A","values":{"topic":%q}}`, term.ID), "", 201)
	var item ent.Resource
	json.Unmarshal(itemBody, &item)
	if !strings.Contains(string(itemBody), "9007199254740993") {
		t.Fatal("default precision lost")
	}
	otherBody := send("POST", "/v1/resources/"+l.ID+"/children", `{"kind":"item","name":"B"}`, "", 201)
	var other ent.Resource
	json.Unmarshal(otherBody, &other)
	query := `{"query":{"filter":{"field":"serial","op":"eq","value":9007199254740993},"sort":{"field":"serial"},"group_by":"serial"},"limit":1}`
	first := send("POST", "/v1/resources/"+l.ID+"/query", query, "", 200)
	if !strings.Contains(string(first), "9007199254740993") || strings.Contains(string(first), `"total"`) {
		t.Fatal("query contract", string(first))
	}
	counted := send("POST", "/v1/resources/"+l.ID+"/query", strings.Replace(query, `"limit":1`, `"limit":1,"include_total":true`, 1), "", 200)
	if !strings.Contains(string(counted), `"total":2`) {
		t.Fatal("requested total", string(counted))
	}
	groups := send("POST", "/v1/resources/"+l.ID+"/query/groups", query, "", 200)
	if !strings.Contains(string(groups), `"value":"9007199254740993"`) {
		t.Fatal("group precision", string(groups))
	}
	viewBody := send("POST", "/v1/resources/"+l.ID+"/views", `{"name":"All","columns":["$name","serial"],"query":{},"is_default":true}`, "", 201)
	var view ent.ListView
	json.Unmarshal(viewBody, &view)
	send("POST", "/v1/views/"+view.ID+"/query", `{}`, "", 200)
	send("PUT", "/v1/views/"+view.ID, `{"name":"All v2","columns":["serial"],"query":{}}`, `"1"`, 200)
	send("PUT", "/v1/views/"+view.ID, `{"name":"Stale","query":{}}`, `"1"`, 409)
	send("DELETE", "/v1/views/"+view.ID, "", `"1"`, 409)
	send("DELETE", "/v1/views/"+view.ID, "", `"2"`, 204)
	typeBody := send("POST", base+"/relationship-types", `{"key":"links","label":"Links","directed":true,"max_outgoing":1,"attributes":[{"key":"note","label":"Note","type":"text","options":{"max_length":20}}]}`, "", 201)
	var typ ent.RelationshipType
	json.Unmarshal(typeBody, &typ)
	linkBody := send("POST", "/v1/items/"+item.ID+"/relationships", fmt.Sprintf(`{"type_id":%q,"target_id":%q,"metadata":{"note":"Original"}}`, typ.ID, other.ID), "", 201)
	var edge ent.Relationship
	json.Unmarshal(linkBody, &edge)
	path := "/v1/items/" + item.ID + "/relationships/" + edge.ID
	send("PATCH", path, `{"metadata":{"note":"Changed"}}`, "", 428)
	send("PATCH", path, `{"metadata":{"note":"Changed"}}`, `"1"`, 200)
	send("DELETE", path, "", `"1"`, 409)
	send("DELETE", path, "", `"2"`, 204)
	send("PUT", "/v1/terms/"+term.ID, `{"name":"Renamed","deprecated":true}`, `"1"`, 200)
	send("GET", "/v1/terms/"+term.ID, "", "", 200)
	send("GET", base+"/term-sets", "", "", 200)
	send("GET", base+"/relationship-types", "", "", 200)
	send("POST", "/v1/resources/"+l.ID+"/query", `{"query":{"unknown":"bad"}}`, "", 400)
	send("PUT", "/v1/resources/"+l.ID+"/permissions", `{"inherit":true,"grants":[{"subject":"alice","action":"manage"}]}`, `"2"`, 422)
	send("PUT", "/v1/resources/"+l.ID+"/permissions", `{"inherit":false,"copy_inherited":true,"grants":[]}`, `"2"`, 200)
	acl := send("GET", "/v1/resources/"+item.ID+"/permissions", "", "", 200)
	if !strings.Contains(string(acl), l.ID) || !strings.Contains(string(acl), "effective_grants") {
		t.Fatal("effective ACL contract", string(acl))
	}
	// Indexing a populated field queues a tracked background build.
	var fields struct{ Data []ent.FieldDefinition }
	json.Unmarshal(send("GET", "/v1/resources/"+l.ID+"/fields", "", "", 200), &fields)
	var collection ent.Resource
	json.Unmarshal(send("GET", "/v1/resources/"+l.ID, "", "", 200), &collection)
	for _, f := range fields.Data {
		if f.Key == "topic" {
			patched := send("PATCH", "/v1/resources/"+l.ID+"/fields/"+f.ID, `{"indexed":true}`, fmt.Sprintf(`"%d"`, collection.Version), 200)
			if !strings.Contains(string(patched), `"index_status":"building"`) {
				t.Fatal("index build not reported", string(patched))
			}
		}
	}
	var operations struct{ Data []ent.Operation }
	json.Unmarshal(send("GET", "/v1/resources/"+l.ID+"/operations", "", "", 200), &operations)
	if len(operations.Data) != 1 || operations.Data[0].Status != "pending" {
		t.Fatal("operations", operations)
	}
	send("GET", "/v1/operations/"+operations.Data[0].ID, "", "", 200)
	send("GET", "/v1/operations/missing", "", "", 404)
}
