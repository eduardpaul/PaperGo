package httpapi

import (
	"encoding/json"
	"net/url"
	"papergo/internal/dms"
	"testing"
)

func TestRESTDeltaPagingAndExpiry(t *testing.T) {
	h, s, _ := setup(t)
	w, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "W"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(t.Context(), "alice", w.ID, dms.CreateResource{Kind: "list", Name: "L"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A", "B"} {
		if _, err = s.Create(t.Context(), "alice", l.ID, dms.CreateResource{Kind: "item", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/resources/" + l.ID + "/delta?$top=1"
	r := request(h, "GET", path, "", testToken, "", "")
	var page map[string]json.RawMessage
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil || r.Code != 200 {
		t.Fatal(r.Code, r.Body.String(), err)
	}
	var next string
	if err = json.Unmarshal(page["@odata.nextLink"], &next); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(next)
	if err != nil {
		t.Fatal(err)
	}
	r = request(h, "GET", u.RequestURI(), "", testToken, "", "")
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil || r.Code != 200 {
		t.Fatal(r.Code, r.Body.String(), err)
	}
	var delta string
	if err = json.Unmarshal(page["@odata.deltaLink"], &delta); err != nil {
		t.Fatal(err)
	}
	u, err = url.Parse(delta)
	if err != nil {
		t.Fatal(err)
	}
	r = request(h, "GET", u.RequestURI(), "", testToken, "", "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/resources/"+l.ID+"/delta?$deltatoken=00000000-0000-0000-0000-000000000000", "", testToken, "", "")
	if r.Code != 410 {
		t.Fatal(r.Code, r.Body.String())
	}
}
