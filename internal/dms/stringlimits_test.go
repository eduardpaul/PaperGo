package dms

import (
	"encoding/json"
	"strings"
	"testing"

	"papergo/internal/dal/catalog"
	"papergo/internal/model"
)

func TestStringLimitsDefaultsCeilingsAndUTF16(t *testing.T) {
	for _, tc := range []struct {
		typ                   catalog.FieldType
		defaultLimit, ceiling int
	}{{catalog.FieldText, 255, 4000}, {catalog.FieldEmail, 255, 4000}, {catalog.FieldURL, 2048, 4000}, {catalog.FieldNote, 100000, 1000000}} {
		t.Run(string(tc.typ), func(t *testing.T) {
			field := fieldFromInput(CreateField{Key: "value", Label: "Value", Type: tc.typ})
			for _, value := range []string{strings.Repeat("a", tc.defaultLimit), strings.Repeat("😀", tc.defaultLimit/2) + strings.Repeat("a", tc.defaultLimit%2)} {
				if _, err := normalizeString(field, value); err != nil {
					t.Fatal("boundary rejected", err)
				}
				if _, err := normalizeString(field, value+"a"); err == nil {
					t.Fatal("default limit ignored")
				}
			}
			field.Options.MaxLength = ptr(tc.ceiling)
			if err := validateFieldDefinition(field); err != nil {
				t.Fatal("ceiling rejected", err)
			}
			if _, err := normalizeString(field, strings.Repeat("x", tc.ceiling)); err != nil {
				t.Fatal(err)
			}
			field.Options.MaxLength = ptr(tc.ceiling + 1)
			if err := validateFieldDefinition(field); err == nil {
				t.Fatal("ceiling ignored")
			}
			field.Options.MaxLength = ptr(0)
			if err := validateFieldDefinition(field); err == nil {
				t.Fatal("zero limit accepted")
			}
		})
	}
	field := fieldFromInput(CreateField{Key: "value", Label: "Value", Type: "text", Options: model.FieldOptions{MaxLength: ptr(3)}})
	if _, err := normalizeString(field, "😀a"); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeString(field, "😀😀"); err == nil {
		t.Fatal("non-BMP characters counted as one unit")
	}
	if _, err := normalizeString(field, string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	choice := fieldFromInput(CreateField{Key: "value", Label: "Value", Type: "choice", Choices: []string{"ok"}, Options: model.FieldOptions{MaxLength: ptr(100)}})
	if err := validateFieldDefinition(choice); err == nil {
		t.Fatal("choice accepted text-only option")
	}
}

func TestStringLimitsURLAndEmailBoundaries(t *testing.T) {
	url := fieldFromInput(CreateField{Key: "value", Label: "Value", Type: "url"})
	prefix := "HTTPS://user:password@example.com/"
	boundary := prefix + strings.Repeat("x", 2048-len(prefix))
	if _, err := normalizeURL(url, boundary); err != nil {
		t.Fatal("URL boundary/credentials/scheme rejected", err)
	}
	if _, err := normalizeURL(url, boundary+"x"); err == nil {
		t.Fatal("URL default ignored")
	}
	for _, value := range []string{"ftp://example.com/a", "relative/path", "http:///path"} {
		if _, err := normalizeURL(url, value); err == nil {
			t.Fatal("invalid URL accepted", value)
		}
	}
	email := fieldFromInput(CreateField{Key: "value", Label: "Value", Type: "email"})
	boundary = strings.Repeat("a", 243) + "@example.com"
	if _, err := normalizeEmail(email, boundary); err != nil {
		t.Fatal("email boundary rejected", err)
	}
	if _, err := normalizeEmail(email, "a"+boundary); err == nil {
		t.Fatal("email default ignored")
	}
}

func TestStringLimitsLongNoteAndFailedWriteRollback(t *testing.T) {
	s, _, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "short", Label: "Short", Type: "text"})
	note := fieldOK(t, s, l.ID, CreateField{Key: "note", Label: "Note", Type: "note"})
	item := create(t, s, l.ID, "item", "Long", map[string]any{"short": "valid", "note": strings.Repeat("a", 100000)})
	before := revisionCount(t, s, item.ID)
	if _, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &map[string]any{"short": strings.Repeat("😀", 128)}}); err == nil {
		t.Fatal("invalid patch accepted")
	}
	if latest(t, s, item.ID).Version != item.Version || revisionCount(t, s, item.ID) != before {
		t.Fatal("invalid patch committed")
	}
	options := note.Options
	options.MaxLength = ptr(1000000)
	if _, err := s.UpdateField(testContext, "alice", l.ID, note.ID, latest(t, s, l.ID).Version, UpdateField{Options: &options}); err != nil {
		t.Fatal(err)
	}
	// Three-byte BMP characters use one UTF-16 unit. The full configurable
	// note must survive the encoded-payload and HTTP budgets, too.
	text := strings.Repeat("漢", 1000000)
	if _, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &map[string]any{"note": text}}); err != nil {
		t.Fatal("full note rejected", err)
	}
	if got := latest(t, s, item.ID).Values["note"]; got != text {
		t.Fatal("long note changed")
	}
	for _, value := range []string{strings.Repeat("a", 256), strings.Repeat("😀", 128)} {
		raw, _ := json.Marshal(value)
		if _, err := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "bad", Label: "Bad", Type: "text", Options: model.FieldOptions{DefaultValue: raw}}); err == nil {
			t.Fatal("invalid default accepted")
		}
	}
}
