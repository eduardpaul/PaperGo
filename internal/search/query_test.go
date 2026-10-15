package search

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	term := func(prefix bool, tokens ...string) Term { return Term{Tokens: tokens, Prefix: prefix} }
	for text, want := range map[string]*FullTextQuery{
		"invoice acme":           {Groups: [][]Term{{term(false, "invoice")}, {term(false, "acme")}}},
		`"late fee" penalty`:     {Groups: [][]Term{{term(false, "late", "fee")}, {term(false, "penalty")}}},
		"red OR blue car":        {Groups: [][]Term{{term(false, "red"), term(false, "blue")}, {term(false, "car")}}},
		"invoice -draft NOT old": {Groups: [][]Term{{term(false, "invoice")}}, Excluded: []Term{term(false, "draft"), term(false, "old")}},
		"inv* INV-2026":          {Groups: [][]Term{{term(true, "inv")}, {term(false, "inv", "2026")}}},
		`"acme corp"* Ünïcode`:   {Groups: [][]Term{{term(true, "acme", "corp")}, {term(false, "ünïcode")}}},
		"OR leading":             {Groups: [][]Term{{term(false, "leading")}}},
		`"unclosed phrase`:       {Groups: [][]Term{{term(false, "unclosed", "phrase")}}},
	} {
		got, err := Parse(text)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", text, got, err, want)
		}
	}
	for _, text := range []string{"", "-only", "NOT this", "!!! ---", strings.Repeat("a", 257)} {
		if q, err := Parse(text); err == nil {
			t.Errorf("Parse(%q) = %+v, want an error", text, q)
		}
	}
	many := strings.Repeat("w ", 40)
	if q, err := Parse(many); err != nil || len(q.Groups) != MaxTerms {
		t.Errorf("terms beyond the limit: %v %v", q, err)
	}
}

func TestLiteral(t *testing.T) {
	if got := Literal(`acme "OR" -x*`); !reflect.DeepEqual(got, &FullTextQuery{Groups: [][]Term{{{Tokens: []string{"acme", "or", "x"}, Prefix: true}}}}) {
		t.Fatalf("Literal: %+v", got)
	}
	if Literal(" !! ") != nil {
		t.Fatal("a literal without words matches nothing")
	}
}

func TestSemanticText(t *testing.T) {
	for q, want := range map[string]string{
		`unpaid "electricity bill" OR invoice -draft NOT copy pay*`: "unpaid electricity bill invoice pay",
		"  which bill was paid?  ":                                  "which bill was paid?",
		"-draft":                                                    "-draft",
	} {
		if got := SemanticText(q); got != want {
			t.Errorf("SemanticText(%q) = %q, want %q", q, got, want)
		}
	}
}
