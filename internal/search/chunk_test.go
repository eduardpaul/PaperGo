package search

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func words(n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = fmt.Sprintf("w%d", i+1)
	}
	return strings.Join(w, " ")
}

// The window chunker matches PaperDotNet's Passages: overlapping windows at
// word boundaries, each page on its own.
func TestWindowChunkerMatchesPaperDotNet(t *testing.T) {
	chunks, truncated, err := Split("window", []Page{{1, "Page  one\n"}, {2, words(600)}}, KindContent, ChunkOptions{})
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	if chunks[0].Text != "Page one" || chunks[0].Page != 1 {
		t.Fatalf("%+v", chunks[0])
	}
	var second []Chunk
	for _, c := range chunks {
		if c.Page == 2 {
			second = append(second, c)
		}
	}
	if len(second) < 2 {
		t.Fatalf("%d windows", len(second))
	}
	pattern := regexp.MustCompile(`^w\d+( w\d+)*$`)
	for _, c := range second {
		if len([]rune(c.Text)) > DefaultMaxChars || !pattern.MatchString(c.Text) {
			t.Fatalf("window %q", c.Text)
		}
	}
	end := strings.Fields(second[0].Text)
	if !slices.Contains(strings.Fields(second[1].Text), end[len(end)-1]) {
		t.Fatal("windows do not overlap")
	}
	// Golden windows of PaperDotNet's algorithm, at a small size.
	got := Windows("aaa bbb ccc ddd eee fff", 10, 4)
	if want := []string{"aaa bbb", "bbb ccc", "ccc ddd", "ddd eee", "eee fff"}; !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
	if got = Windows("abcdefghijklmnop", 5, 2); !slices.Equal(got, []string{"abcde", "fghij", "klmno", "p"}) {
		t.Fatalf("no spaces: %q", got)
	}
	if Normalize("  a \t\n b  ") != "a b" {
		t.Fatal(Normalize("  a \t\n b  "))
	}
	for i, c := range chunks {
		if c.Ordinal != i || c.Kind != KindContent || c.InputHash != c.Hash() || len(c.InputHash) != 64 {
			t.Fatalf("%+v", c)
		}
	}
}

func TestOtherChunkers(t *testing.T) {
	pages := []Page{
		{1, "1. INTRODUCTION\nThis agreement starts today.\nIt binds both parties.\n\n2. PAYMENT\nInvoices are due in 30 days."},
		{2, "Second page text."},
	}
	byPage, _, _ := Split("pages", pages, KindContent, ChunkOptions{})
	if len(byPage) != 2 || byPage[1].Text != "Second page text." {
		t.Fatalf("pages: %+v", byPage)
	}
	paragraphs, _, _ := Split("paragraphs", pages, KindContent, ChunkOptions{MaxChars: 40})
	if len(paragraphs) < 3 || paragraphs[0].Page != 1 {
		t.Fatalf("paragraphs: %+v", paragraphs)
	}
	headings, _, _ := Split("headings", pages, KindContent, ChunkOptions{})
	if len(headings) != 3 || !strings.HasPrefix(headings[0].Text, "1. INTRODUCTION\nThis agreement") || !strings.HasPrefix(headings[1].Text, "2. PAYMENT\nInvoices") ||
		!strings.HasPrefix(headings[2].Text, "2. PAYMENT\nSecond page") {
		t.Fatalf("headings: %+v", headings)
	}
	whole, _, _ := Split("whole", pages, KindSummary, ChunkOptions{})
	if len(whole) != 1 || whole[0].Kind != KindSummary || !strings.Contains(whole[0].Text, "Second page") {
		t.Fatalf("whole: %+v", whole)
	}
	capped, truncated, _ := Split("window", []Page{{1, words(3000)}}, KindContent, ChunkOptions{MaxChunks: 3})
	if len(capped) != 3 || !truncated {
		t.Fatalf("max chunks: %d %v", len(capped), truncated)
	}
	if _, _, err := Split("sentences", pages, KindContent, ChunkOptions{}); err == nil {
		t.Fatal("unknown chunker")
	}
	// Equal text has an equal hash, whatever the page.
	a, _, _ := Split("whole", []Page{{1, "Same text"}}, KindContent, ChunkOptions{})
	b, _, _ := Split("whole", []Page{{7, "Same  text"}}, KindContent, ChunkOptions{})
	if a[0].InputHash != b[0].InputHash {
		t.Fatal("hash depends on more than the text")
	}
}
