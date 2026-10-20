package dms

import (
	"fmt"
	"papergo/internal/dal/surfaces"
	"papergo/internal/model"
	"strings"
	"testing"
)

func TestValidationTitlesUTF16AndHistory(t *testing.T) {
	s, w, list := fixture(t)
	library := create(t, s, w.ID, "library", "Files", nil)
	for _, title := range []string{strings.Repeat("a", 1024), strings.Repeat("漢", 1024), strings.Repeat("😀", 512)} {
		item := create(t, s, list.ID, "item", title, nil)
		history, err := s.RevisionHistory(testContext, "alice", item.ID, 0, 10)
		if err != nil || len(history.Data) != 1 || history.Data[0].Name != title {
			t.Fatal("title/history", history, err)
		}
		folder := create(t, s, library.ID, "folder", title, nil)
		changed := " " + string([]rune(title)[:len([]rune(title))-3]) + " "
		if _, err = s.Update(testContext, "alice", folder.ID, folder.Version, UpdateResource{Name: &changed}); err != nil {
			t.Fatal(err)
		}
		if !filenameValid(title) {
			t.Fatal("Unicode filename rejected")
		}
	}
	for _, title := range []string{strings.Repeat("a", 1025), strings.Repeat("😀", 513), " " + strings.Repeat("a", 1024), string([]byte{0xff})} {
		if _, err := s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: title}); err == nil {
			t.Fatal("overlong/invalid title accepted")
		}
	}
	item := create(t, s, list.ID, "item", "  Trimmed  ", nil)
	if item.Name != "Trimmed" {
		t.Fatal("title not trimmed", item.Name)
	}
}

func TestValidationLargeArrayPersistsAndDeduplicates(t *testing.T) {
	s, _, list := fixture(t)
	choices := []string{}
	values := []any{}
	for i := 0; i < 150; i++ {
		value := fmt.Sprintf("value-%d", i)
		choices = append(choices, value)
		values = append(values, value)
	}
	fieldOK(t, s, list.ID, CreateField{Key: "selected", Label: "Selected", Type: "choice", Choices: choices, Indexed: true, Options: model.FieldOptions{Multiple: true}})
	values = append(values, values...)
	item := create(t, s, list.ID, "item", "Many selections", map[string]any{"selected": values})
	if len(item.Values["selected"].([]any)) != 150 {
		t.Fatal("array normalization", item.Values)
	}
	n, err := countFieldValues(s, surfaces.ValueFilter{ItemID: item.ID})
	if err != nil || n != 150 {
		t.Fatal("indexed values", n, err)
	}
	publishItem(t, s, item)
	if _, err = s.RestoreRevision(testContext, "alice", item.ID, 1, latest(t, s, item.ID).Version); err != nil {
		t.Fatal("history restore", err)
	}
}

func TestValidationFolderDepthAndMoveBoundary(t *testing.T) {
	s, _, list := fixture(t)
	parent := list
	for i := 0; i < MaxFolderDepth; i++ {
		parent = create(t, s, parent.ID, "folder", fmt.Sprintf("level-%d", i), nil)
	}
	if _, err := s.Create(testContext, "alice", parent.ID, CreateResource{Kind: "folder", Name: "Too deep"}); err == nil {
		t.Fatal("65th folder accepted")
	}
	item := create(t, s, parent.ID, "item", "Leaf", nil)
	if _, err := s.Get(testContext, "alice", item.ID); err != nil {
		t.Fatal("deep inherited permission", err)
	}
	folder := create(t, s, list.ID, "folder", "Moving", nil)
	create(t, s, folder.ID, "folder", "Child", nil)
	if _, err := s.Update(testContext, "alice", folder.ID, folder.Version, UpdateResource{ParentID: &parent.ID}); err == nil {
		t.Fatal("overdeep move accepted")
	}
	if *latest(t, s, folder.ID).ParentID != list.ID {
		t.Fatal("failed move committed")
	}
	shallow := create(t, s, list.ID, "item", "Moving item", nil)
	if _, err := s.Update(testContext, "alice", shallow.ID, shallow.Version, UpdateResource{ParentID: &parent.ID}); err != nil {
		t.Fatal("item at 64 folders rejected", err)
	}
}
