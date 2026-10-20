package httpapi

import (
	"encoding/json"
	"papergo/internal/dal/catalog"
	"papergo/internal/dms"
	"strings"
	"testing"
)

func TestCurrencyAPIExactPayloadAndMetadata(t *testing.T) {
	h, s, _ := setup(t)
	w, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Money"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(t.Context(), "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	fieldPath := "/v1/resources/" + l.ID + "/fields"
	res := request(h, "POST", fieldPath, `{"key":"price","label":"Price","type":"currency","indexed":true,"options":{"currency_code":"EUR","minimum":"0"}}`, testToken, "", "application/json")
	var field catalog.FieldDefinition
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &field) != nil || field.Options.CurrencyCode != "EUR" {
		t.Fatal(res.Code, res.Body.String())
	}
	path := "/v1/resources/" + l.ID + "/children"
	res = request(h, "POST", path, `{"kind":"item","name":"Exact","values":{"price":79228162514264337593543950335}}`, testToken, "", "application/json")
	if res.Code != 201 || !strings.Contains(res.Body.String(), `"price":79228162514264337593543950335`) {
		t.Fatal(res.Code, res.Body.String())
	}
	for _, literal := range []string{`79228162514264337593543950336`, `"12.3"`, `-1`} {
		res = request(h, "POST", path, `{"kind":"item","name":"Invalid","values":{"price":`+literal+`}}`, testToken, "", "application/json")
		if res.Code != 422 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	res = request(h, "POST", fieldPath, `{"key":"bad","label":"Bad","type":"currency","options":{"currency_code":"eur"}}`, testToken, "", "application/json")
	if res.Code != 422 {
		t.Fatal(res.Code, res.Body.String())
	}
}
