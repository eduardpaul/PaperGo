package httpapi

import (
	"encoding/json"
	"net/url"
	"papergo/ent"
	"papergo/internal/dms"
	"testing"
)

func TestRESTOrganizationAuditTraceAndContinuation(t *testing.T) {
	h, s, _ := setup(t)
	w, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "W"})
	if err != nil {
		t.Fatal(err)
	}
	r := request(h, "PATCH", "/v1/resources/"+w.ID, `{"name":"Changed"}`, testToken, `"1"`, "application/json")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	trace := r.Header().Get("X-Request-ID")
	r = request(h, "GET", "/v1/audit?entity_id="+w.ID+"&$top=1", "", testToken, "", "")
	var page struct {
		Data []ent.AuditEvent `json:"value"`
		Next string           `json:"@odata.nextLink"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil || r.Code != 200 || len(page.Data) != 1 || page.Next == "" {
		t.Fatal(r.Code, r.Body.String(), err)
	}
	if trace == "" || page.Data[0].TraceID != trace || len(page.Data[0].Properties) != 1 || page.Data[0].Properties[0] != "name" {
		t.Fatal(trace, page)
	}
	u, err := url.Parse(page.Next)
	if err != nil {
		t.Fatal(err)
	}
	r = request(h, "GET", u.RequestURI(), "", testToken, "", "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/audit?from=not-a-date", "", testToken, "", "")
	if r.Code != 400 {
		t.Fatal(r.Code, r.Body.String())
	}
}
