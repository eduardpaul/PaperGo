package search

import (
	"slices"
	"testing"
)

func TestStemmer(t *testing.T) {
	s, err := NewStemmer([]string{"English", "german", "english", " "})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Languages(); !slices.Equal(got, []string{"english", "german"}) {
		t.Fatal(got)
	}
	if a, b := s.Stem("invoices"), s.Stem("invoice"); a[0] != b[0] {
		t.Fatalf("invoices %v, invoice %v", a, b)
	}
	if a, b := s.Stem("rechnungen"), s.Stem("rechnung"); a[1] != b[1] {
		t.Fatalf("rechnungen %v, rechnung %v", a, b)
	}
	if got := s.Text("Paid invoices"); got != "paid invoic\npaid invoic" {
		t.Fatalf("%q", got)
	}
	if _, err = NewStemmer([]string{"klingon"}); err == nil {
		t.Fatal("unknown language")
	}
	none, _ := NewStemmer(nil)
	if none.Text("invoices") != "" || len(none.Stem("invoices")) != 0 {
		t.Fatal("stemming without languages")
	}
}
