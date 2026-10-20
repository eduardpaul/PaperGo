package dms

import (
	"encoding/json"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/model"
	"papergo/internal/storage"
	"strings"
	"testing"
)

func TestKeywordVocabularyFollowsAccessAndSurfaces(t *testing.T) {
	s, w, list := fixture(t)
	other := create(t, s, w.ID, "list", "Other", nil)
	if err := setWorkspaceMembers(testContext, s, "alice", w.ID, WorkspaceMember{Subject: "rita", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	tagged := func(parent, kind, name string, keywords ...string) {
		t.Helper()
		r, err := s.Create(testContext, "alice", parent, CreateResource{Kind: kind, Name: name, Keywords: keywords})
		if err != nil {
			t.Fatal(err)
		}
		if kind == "item" && name != "Draft" {
			if _, err = s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
				t.Fatal(err)
			}
		}
	}
	tagged(list.ID, "item", "Invoice 1", "finance", "legal")
	tagged(list.ID, "item", "Invoice 2", "finance")
	tagged(list.ID, "item", "Draft", "secret")
	tagged(list.ID, "folder", "2026", "archive")
	tagged(other.ID, "item", "Memo", "elsewhere")

	vocab := func(subject string, q WorkspaceKeywordsQuery) string {
		t.Helper()
		page, err := s.WorkspaceKeywords(testContext, subject, w.ID, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tc := range page.Data {
			out = append(out, tc.Name+":"+string(rune('0'+tc.Count)))
		}
		return strings.Join(out, ",")
	}
	if got := vocab("alice", WorkspaceKeywordsQuery{}); got != "archive:1,elsewhere:1,finance:2,legal:1,secret:1" {
		t.Fatalf("manager vocabulary: %s", got)
	}
	// rita reads published content only, so the draft's keyword is not hers to see.
	if got := vocab("rita", WorkspaceKeywordsQuery{}); got != "archive:1,elsewhere:1,finance:2,legal:1" {
		t.Fatalf("reader vocabulary: %s", got)
	}
	if got := vocab("alice", WorkspaceKeywordsQuery{CollectionID: list.ID, Prefix: "f"}); got != "finance:2" {
		t.Fatalf("prefix in collection: %s", got)
	}
	page, err := s.WorkspaceKeywords(testContext, "alice", w.ID, WorkspaceKeywordsQuery{Limit: 2})
	if err != nil || len(page.Data) != 2 || !strings.HasPrefix(page.NextCursor, "elsewhere\x1f") {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if got := vocab("alice", WorkspaceKeywordsQuery{After: page.NextCursor, Limit: 2}); got != "finance:2,legal:1" {
		t.Fatalf("second page: %s", got)
	}
	if _, err = s.WorkspaceKeywords(testContext, "mallory", w.ID, WorkspaceKeywordsQuery{}); err == nil {
		t.Fatal("a stranger read the vocabulary")
	}
	if _, err = s.WorkspaceKeywords(testContext, "alice", w.ID, WorkspaceKeywordsQuery{CollectionID: w.ID}); err == nil {
		t.Fatal("the workspace accepted as a collection")
	}
}

func TestSharedKeywordFolksonomyAcrossIntrinsicAndCustomFields(t *testing.T) {
	s, w, list := fixture(t)
	library := create(t, s, w.ID, "library", "Documents", nil)
	fieldOK(t, s, list.ID, CreateField{Key: "topics", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}})
	intrinsic, err := s.Create(testContext, "alice", library.ID, CreateResource{Kind: "item", Name: "Invoice", Keywords: []string{"Finance", "finance"}})
	if err != nil || len(intrinsic.Keywords) != 1 {
		t.Fatal(intrinsic, err)
	}
	custom := create(t, s, list.ID, "item", "Budget", map[string]any{"topics": []any{"FINANCE", "Review"}})
	finance := intrinsic.Keywords[0]
	if custom.Values["topics"].([]any)[0] != finance {
		t.Fatal("keyword controls must share a term ID", custom.Values, intrinsic.Keywords)
	}
	set, err := s.DAL.Taxonomy().KeywordsSet(testContext)
	if err != nil {
		t.Fatal(err)
	}
	terms, err := s.Terms(testContext, "alice", set.ID, TermsQuery{})
	if err != nil || len(terms.Data) != 2 {
		t.Fatal("all additions must be visible in the Keywords term set", terms, err)
	}
	vocabulary, err := s.WorkspaceKeywords(testContext, "alice", w.ID, WorkspaceKeywordsQuery{Prefix: "fin"})
	if err != nil || len(vocabulary.Data) != 1 || vocabulary.Data[0].Keyword != finance || vocabulary.Data[0].Count != 2 {
		t.Fatal(vocabulary, err)
	}
	rows, err := s.Query(testContext, "alice", list.ID, QueryRequest{Query: QuerySpec{Keyword: finance}})
	if err != nil || len(rows.Data) != 1 || rows.Data[0].ID != custom.ID {
		t.Fatal("keyword filtering must include custom fields", rows, err)
	}
	folder := smartOK(t, s, SmartFolderInput{Name: "Finance", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Mode: "classification", Terms: []string{finance}}})
	matches, err := s.QuerySmartFolder(testContext, "alice", folder.ID, SmartFolderQueryRequest{})
	if err != nil || len(matches.Data) != 2 {
		t.Fatal("smart folders must see both keyword assignments", matches, err)
	}
	added, _, err := s.ClassifySmartFolder(testContext, "alice", folder.ID, SmartFolderDrop{FolderVersion: folder.Version, CollectionID: library.ID, Create: &BulkCreate{Name: "Receipt"}})
	if err != nil || len(added.Keywords) != 1 || added.Keywords[0] != finance {
		t.Fatal("classification keeps intrinsic keyword functionality", added, err)
	}
	if _, err = s.UnclassifySmartFolder(testContext, "alice", folder.ID, added.ID, folder.Version, added.Version); err != nil {
		t.Fatal(err)
	}
	indexSearch(t, s)
	found, err := s.Search(testContext, "alice", SearchRequest{Keyword: finance, Facets: true})
	if err != nil || len(found.Hits) != 2 || found.Facets == nil || len(found.Facets.Keyword) != 2 {
		t.Fatal("search must index the shared vocabulary", found, err)
	}
	popular, err := s.PopularKeywords(testContext, "alice", 10)
	if err != nil || len(popular) < 1 || popular[0].Term.ID != finance || popular[0].Count != 2 {
		t.Fatal("popularity must include both assignment paths", popular, err)
	}
}

func TestWorkspaceKeywordReferencesArePortable(t *testing.T) {
	s, w, list := fixture(t)
	item, err := s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: "Invoice", Keywords: []string{"Finance"}})
	if err != nil {
		t.Fatal(err)
	}
	fieldOK(t, s, list.ID, CreateField{Key: "topics", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}})
	values := map[string]any{"topics": []any{item.Keywords[0]}}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &values})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	object, _ := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: w.ID})
	target := emptyPackageTarget(t)
	result, err := importWorkspaceForTest(t, target, store, object, false, "Keyword copy")
	if err != nil || !result.Valid {
		t.Fatal(result, err)
	}
	copied, err := target.DAL.Resources().One(testContext, resources.Filter{WorkspaceID: result.WorkspaceID, Kinds: []refs.ResourceKind{refs.KindItem}})
	if err != nil || len(copied.Keywords) != 1 || copied.Keywords[0] == item.Keywords[0] || copied.Values["topics"].([]any)[0] != copied.Keywords[0] {
		t.Fatal("keyword references must be remapped together", copied, err)
	}
	term, err := target.Term(testContext, "organization-admin", copied.Keywords[0])
	if err != nil || term.Name != "Finance" {
		t.Fatal(term, err)
	}
}

func TestMergedIntrinsicKeywordsKeepFilteringAndCanonicalizeOnWrite(t *testing.T) {
	s, w, list := fixture(t)
	item, err := s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: "Invoice", Keywords: []string{"Finance"}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.Term(testContext, "alice", item.Keywords[0])
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := s.AddKeyword(testContext, "alice", KeywordInput{Name: "Accounts"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeTerm(testContext, "alice", source.ID, source.Version, MergeTermInput{TargetTermID: target.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Query(testContext, "alice", list.ID, QueryRequest{Query: QuerySpec{Keyword: target.ID}})
	if err != nil || len(rows.Data) != 1 {
		t.Fatal(rows, err)
	}
	vocabulary, err := s.WorkspaceKeywords(testContext, "alice", w.ID, WorkspaceKeywordsQuery{})
	if err != nil || len(vocabulary.Data) != 1 || vocabulary.Data[0].Keyword != target.ID {
		t.Fatal(vocabulary, err)
	}
	indexSearch(t, s)
	found, err := s.Search(testContext, "alice", SearchRequest{Keyword: target.ID})
	if err != nil || len(found.Hits) != 1 {
		t.Fatal(found, err)
	}
	item = latest(t, s, item.ID)
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Renamed")})
	if err != nil || item.Keywords[0] != target.ID {
		t.Fatal("a normal write must use the canonical keyword ID", item, err)
	}
}

func TestKeywordFieldDefaultsUseTheSharedVocabulary(t *testing.T) {
	s, _, list := fixture(t)
	field := fieldOK(t, s, list.ID, CreateField{Key: "topics", Label: "Topics", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true, DefaultValue: []byte(`["Finance"]`)}})
	var ids []string
	if err := json.Unmarshal(field.Options.DefaultValue, &ids); err != nil || len(ids) != 1 {
		t.Fatal(field, err)
	}
	keyword, err := s.Term(testContext, "alice", ids[0])
	if err != nil || keyword.Name != "Finance" {
		t.Fatal(keyword, err)
	}
	item := create(t, s, list.ID, "item", "Invoice", nil)
	if !sameJSON(item.Values["topics"], []any{keyword.ID}) {
		t.Fatal(item.Values)
	}
	options := field.Options
	options.DefaultValue = []byte(`["Review"]`)
	field, err = s.UpdateCatalogField(testContext, "alice", field.ID, field.Version, UpdateField{Options: &options})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(field.Options.DefaultValue, &ids); err != nil || len(ids) != 1 || ids[0] == keyword.ID {
		t.Fatal(field, err)
	}
	keyword, err = s.Term(testContext, "alice", ids[0])
	if err != nil || keyword.Name != "Review" {
		t.Fatal(keyword, err)
	}
}
