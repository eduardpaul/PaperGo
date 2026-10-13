package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"papergo/internal/dms"
)

func TestChildrenFilterByKindPagesFoldersOnly(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Kinds"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Archive"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 120 {
		if _, err = s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: fmt.Sprintf("file-%03d.pdf", i)}); err != nil {
			t.Fatal(err)
		}
	}
	folders := map[string]bool{}
	for _, name := range []string{"Invoices", "Contracts", "Letters"} {
		f, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "folder", Name: name})
		if err != nil {
			t.Fatal(err)
		}
		folders[f.ID] = true
	}
	seen := map[string]bool{}
	next := "/v1/resources/" + lib.ID + "/children?kind=folder&$top=2"
	pages := 0
	for next != "" {
		r := request(h, "GET", next, "", testToken, "", "")
		var page struct {
			Value []struct{ ID, Kind string } `json:"value"`
			Next  *string                     `json:"@odata.nextLink"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
		for _, v := range page.Value {
			if v.Kind != "folder" || !folders[v.ID] {
				t.Fatal("non-folder listed", v)
			}
			seen[v.ID] = true
		}
		next = ""
		if page.Next != nil {
			next = strings.TrimPrefix(*page.Next, "http://example.com")
		}
		pages++
	}
	if len(seen) != 3 || pages != 2 {
		t.Fatal("folders across pages", len(seen), pages)
	}
	if r := request(h, "GET", "/v1/resources/"+lib.ID+"/children?kind=file", "", testToken, "", ""); r.Code != 422 {
		t.Fatal("unknown kind accepted", r.Code, r.Body.String())
	}
}
