package zvecstore

import (
	"strings"
	"testing"

	"papergo/internal/search"
)

func FuzzNativeFilter(f *testing.F) {
	for _, input := range []string{"", "scope", "' OR true --", "\\\x00é", strings.Repeat("a", 4096)} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 8192 {
			t.Skip()
		}
		out, err := nativeFilter(search.Filter{Access: search.Access{Draft: []string{input}, Read: []string{input}}, WorkspaceID: input, Keyword: input, TermIDs: []string{input}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(out, "\\\x00\r\n") {
			t.Fatalf("raw input in filter: %q", out)
		}
		// Every quoted literal is encoded hex, with optional fixed keyword/term prefix.
		pieces := strings.Split(out, "'")
		if len(pieces)%2 != 1 {
			t.Fatal("unbalanced quotes")
		}
		for i := 1; i < len(pieces); i += 2 {
			literal := strings.TrimPrefix(strings.TrimPrefix(pieces[i], "g"), "t")
			if !strings.HasPrefix(literal, "x") {
				t.Fatal(literal)
			}
			for _, r := range literal[1:] {
				if !strings.ContainsRune("0123456789abcdef", r) {
					t.Fatal(literal)
				}
			}
		}
	})
}

func FuzzNativeText(f *testing.F) {
	for _, input := range []string{"invoice", `"cafè pay*" -private`, `a OR b`, `a'\\*`, "é*"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		q, err := search.Parse(input)
		if err != nil || q == nil {
			return
		}
		stem, _ := search.NewStemmer([]string{"english"})
		s := &Store{stem: stem}
		for _, stems := range []bool{false, true} {
			out := s.render(q, stems)
			if strings.ContainsAny(out, "'\\\x00\r\n") {
				t.Fatalf("raw text in native query: %q", out)
			}
			// Rendering and matching must accept every validated syntax tree.
			s.matches(input, q, stems)
		}
	})
}
