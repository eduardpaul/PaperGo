package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/model"
)

func TestStringLimitsAPIUTF16AndLongNote(t *testing.T) {
	h, s, _ := setup(t)
	w, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Text"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(t.Context(), "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []dms.CreateField{{Key: "short", Label: "Short", Type: "text"}, {Key: "note", Label: "Note", Type: "note", Options: model.FieldOptions{MaxLength: limitPtr(1000000)}}} {
		if _, err = s.CreateField(t.Context(), "alice", l.ID, field); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/resources/" + l.ID + "/children"
	for _, tc := range []struct {
		value  string
		status int
	}{{strings.Repeat("😀", 127) + "x", 201}, {strings.Repeat("😀", 128), 422}} {
		body, _ := json.Marshal(dms.CreateResource{Kind: "item", Name: "Boundary", Values: map[string]any{"short": tc.value}})
		res := request(h, "POST", path, string(body), testToken, "", "application/json")
		if res.Code != tc.status {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	text := strings.Repeat("漢", 1000000)
	body, _ := json.Marshal(dms.CreateResource{Kind: "item", Name: "Full note", Values: map[string]any{"note": text}})
	res := request(h, "POST", path, string(body), testToken, "", "application/json")
	if res.Code != 201 || !strings.Contains(res.Body.String(), text) {
		t.Fatal("full note", res.Code)
	}
}

func limitPtr(value int) *int { return &value }
