package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"papergo/ent"
	"papergo/internal/dms"
)

func TestTaxonomyRoutes(t *testing.T) {
	h, s, _ := setup(t)
	ws, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	const json_ = "application/json"
	send := func(method, path, body, ifMatch, media string, status int, out any) {
		t.Helper()
		w := request(h, method, path, body, testToken, ifMatch, media)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		if out != nil {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
	}
	base := "/v1/workspaces/" + ws.ID
	var group ent.TermGroup
	send("POST", base+"/term-groups", `{"name":"Records"}`, "", json_, 201, &group)
	var set ent.TermSet
	send("POST", base+"/term-sets", `{"group_id":"`+group.ID+`","key":"topics","name":"Topics"}`, "", json_, 201, &set)
	var finance, money dms.TermView
	send("POST", "/v1/term-sets/"+set.ID+"/terms", `{"name":"Finance","color":"#336699"}`, "", json_, 201, &finance)
	send("POST", "/v1/term-sets/"+set.ID+"/terms", `{"name":"Money"}`, "", json_, 201, &money)
	var child dms.TermView
	send("POST", "/v1/term-sets/"+set.ID+"/terms", `{"name":"Tax","parent_id":"`+money.ID+`"}`, "", json_, 201, &child)
	send("POST", "/v1/terms/"+child.ID+"/move", `{"parent_id":"`+finance.ID+`"}`, "", json_, 428, nil)
	send("POST", "/v1/terms/"+child.ID+"/move", `{"parent_id":"`+finance.ID+`"}`, `"1"`, json_, 200, &child)
	if child.Path != finance.Path+child.ID+"/" {
		t.Fatalf("moved path: %s", child.Path)
	}
	var merged dms.TermView
	send("POST", "/v1/terms/"+money.ID+"/merge", `{"target_term_id":"`+finance.ID+`"}`, `"1"`, json_, 200, &merged)
	if merged.ID != finance.ID || strings.Join(merged.Synonyms, ",") != "Money" || !merged.HasChildren {
		t.Fatalf("merge: %+v", merged)
	}
	var roots dms.Page[dms.TermView]
	send("GET", "/v1/term-sets/"+set.ID+"/terms", "", "", "", 200, &roots)
	if len(roots.Data) != 1 || roots.Data[0].ID != finance.ID {
		t.Fatalf("roots: %+v", roots)
	}
	var byID struct{ Data []dms.TermView }
	send("GET", "/v1/terms?ids="+money.ID+","+finance.ID, "", "", "", 200, &byID)
	if len(byID.Data) != 2 || byID.Data[0].MergedIntoID == nil || *byID.Data[0].MergedIntoID != finance.ID {
		t.Fatalf("by id: %+v", byID)
	}

	var keyword dms.TermView
	send("POST", base+"/keywords", `{"name":"Budget"}`, "", json_, 201, &keyword)
	send("POST", base+"/keywords", `{"name":"budget"}`, "", json_, 200, nil)
	var suggestions struct{ Data []dms.TermView }
	send("GET", base+"/keywords?q=bud", "", "", "", 200, &suggestions)
	if len(suggestions.Data) != 1 || suggestions.Data[0].ID != keyword.ID {
		t.Fatalf("suggestions: %+v", suggestions)
	}
	send("GET", base+"/keywords/popular?top=5", "", "", "", 200, nil)
	var promoted dms.TermView
	send("POST", "/v1/keywords/"+keyword.ID+"/promote", `{"term_set_id":"`+set.ID+`"}`, `"1"`, json_, 200, &promoted)
	if promoted.TermSetID != set.ID || !promoted.AvailableAsKeyword {
		t.Fatalf("promoted: %+v", promoted)
	}

	csv := "Term Set Name,Level 1 Term,Level 2 Term\nRegions,Europe,Spain\n"
	send("POST", "/v1/term-groups/"+group.ID+"/import", csv, "", json_, 415, nil)
	var imported dms.TermImportResult
	send("POST", "/v1/term-groups/"+group.ID+"/import", csv, "", "text/csv", 200, &imported)
	if imported.SetsCreated != 1 || imported.TermsCreated != 2 {
		t.Fatalf("import: %+v", imported)
	}
	var empty ent.TermSet
	send("POST", base+"/term-sets", `{"group_id":"`+group.ID+`","key":"empty","name":"Empty"}`, "", json_, 201, &empty)
	send("DELETE", "/v1/term-sets/"+empty.ID, "", `"1"`, "", 204, nil)
	send("DELETE", "/v1/term-groups/"+group.ID, "", `"1"`, "", 422, nil)
	var pkg dms.TaxonomyPackage
	send("GET", base+"/taxonomy/export", "", "", "", 200, &pkg)
	other, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Copy"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pkg)
	var result dms.TaxonomyImportResult
	send("POST", "/v1/workspaces/"+other.ID+"/taxonomy/import", string(raw), "", json_, 200, &result)
	if result.GroupsCreated != 1 || result.SetsCreated != 2 || result.TermsCreated != 5 {
		t.Fatalf("taxonomy import: %+v", result)
	}
	var groups dms.Page[ent.TermGroup]
	send("GET", base+"/term-groups", "", "", "", 200, &groups)
	if len(groups.Data) != 2 {
		t.Fatalf("groups: %+v", groups)
	}
}
