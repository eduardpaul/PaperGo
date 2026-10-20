package dms

import (
	"encoding/json"
	"fmt"
	eventstore "papergo/internal/dal/events"
	viewstore "papergo/internal/dal/views"
	"strings"
	"testing"
)

func TestSmartFolderPreviewIsReadOnlyScopedAndBounded(t *testing.T) {
	s, workspace, list := fixture(t)
	readers(t, s, workspace)
	for index := range 7 {
		item := create(t, s, list.ID, "item", fmt.Sprintf("Item %d", index), nil)
		publishItem(t, s, item)
	}
	library := create(t, s, workspace.ID, "library", "Documents", nil)
	file, err := s.CreateDocument(testContext, "alice", library.ID, DocumentMetadata{Name: "Excluded file"}, pdfContent("excluded.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	publishItem(t, s, file.Item)
	private := create(t, s, list.ID, "item", "Private", nil)
	publishItem(t, s, private)
	if _, err := s.SetPermissions(testContext, "alice", private.ID, latest(t, s, private.ID).Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.DAL.Views().CountSmartFolders(testContext, viewstore.FolderFilter{})
	events, _ := s.DAL.Events().Count(testContext, eventstore.Filter{})
	input := SmartFolderPreview{WorkspaceID: &workspace.ID, Definition: SmartFolderDefinition{Mode: "query", CollectionKind: "list"}}
	preview, err := s.PreviewSmartFolder(testContext, "reader", input)
	if err != nil || len(preview.Data) != 5 || counted(preview.Total) != 7 || preview.NextCursor != "" {
		t.Fatal(preview, err)
	}
	for _, entry := range preview.Data {
		if entry.CollectionID != list.ID || entry.CollectionKind != "list" {
			t.Fatal("wrong scope", entry)
		}
	}
	after, _ := s.DAL.Views().CountSmartFolders(testContext, viewstore.FolderFilter{})
	afterEvents, _ := s.DAL.Events().Count(testContext, eventstore.Filter{})
	if before != after || events != afterEvents {
		t.Fatal("preview wrote application state")
	}
	input.Definition.Mode = ""
	if _, err = s.PreviewSmartFolder(testContext, "reader", input); !isValidation(err) {
		t.Fatal("missing mode accepted", err)
	}
}

func TestSmartFolderFileTypeRulesUseTheVisibleRevision(t *testing.T) {
	s, workspace, _ := fixture(t)
	readers(t, s, workspace)
	library := create(t, s, workspace.ID, "library", "Documents", nil)
	item := create(t, s, library.ID, "item", "Scan", nil)
	attach := func(mime, key string) {
		current := latest(t, s, item.ID)
		if _, err := s.AttachBlob(testContext, "alice", item.ID, current.Version, BlobInput{ObjectKey: key, Filename: key, ContentType: mime, Size: 1, SHA256: strings.Repeat(fmt.Sprintf("%x", len(key)), 64)}); err != nil {
			t.Fatal(err)
		}
	}
	attach("application/pdf", "published.pdf")
	publishItem(t, s, latest(t, s, item.ID))
	attach("image/jpeg", "draft.jpg")
	input := SmartFolderPreview{WorkspaceID: &workspace.ID, Definition: SmartFolderDefinition{Mode: "query", CollectionKind: "library", Filter: &FilterExpr{Field: "$file_type", Value: json.RawMessage(`"application/pdf"`)}}}
	for _, tc := range []struct {
		subject string
		count   int
	}{{"reader", 1}, {"alice", 0}} {
		preview, err := s.PreviewSmartFolder(testContext, tc.subject, input)
		if err != nil || counted(preview.Total) != tc.count {
			t.Fatal("wrong visible file type", tc.subject, preview, err)
		}
	}
}

func TestSavedSearchModeAndDirectSortedResults(t *testing.T) {
	s, workspace, list := fixture(t)
	a := create(t, s, list.ID, "item", "Alpha", nil)
	create(t, s, list.ID, "item", "Beta", nil)
	folder := smartOK(t, s, SmartFolderInput{Name: "All", WorkspaceID: &workspace.ID, Definition: SmartFolderDefinition{Mode: "query", GroupBy: []SmartFolderGroupBy{{Field: "$modified_at", By: "year"}}}})
	if _, _, err := s.ClassifySmartFolder(testContext, "alice", folder.ID, SmartFolderDrop{FolderVersion: folder.Version, CollectionID: list.ID, ItemID: a.ID, Version: a.Version}); !isValidation(err) {
		t.Fatal("saved search classified an item", err)
	}
	if _, err := s.UnclassifySmartFolder(testContext, "alice", folder.ID, a.ID, folder.Version, a.Version); !isValidation(err) {
		t.Fatal("saved search removed classification", err)
	}
	order := &SortSpec{Field: "$name", Direction: "asc"}
	page, err := s.QuerySmartFolder(testContext, "alice", folder.ID, SmartFolderQueryRequest{Sort: order, Limit: 1, IncludeTotal: true})
	if err != nil || len(page.Data) != 1 || page.Data[0].Item.Name != "Alpha" || counted(page.Total) != 2 || page.NextCursor == "" {
		t.Fatal("grouping blocked direct results", page, err)
	}
	next, err := s.QuerySmartFolder(testContext, "alice", folder.ID, SmartFolderQueryRequest{Sort: order, Limit: 1, After: page.NextCursor})
	if err != nil || len(next.Data) != 1 || next.Data[0].Item.Name != "Beta" {
		t.Fatal(next, err)
	}
	capped, err := s.QuerySmartFolder(testContext, "alice", folder.ID, SmartFolderQueryRequest{IncludeTotal: true, TotalLimit: 1})
	pageBody, _ := capped.APIPage()
	if err != nil || counted(capped.Total) != 1 || pageBody["total_capped"] != true {
		t.Fatal("bounded counts must remain marked in API output", capped, pageBody, err)
	}
	order.Direction = "desc"
	if _, err = s.QuerySmartFolder(testContext, "alice", folder.ID, SmartFolderQueryRequest{Sort: order, Limit: 1, After: page.NextCursor}); err == nil {
		t.Fatal("cursor accepted a different ordering")
	}
}
