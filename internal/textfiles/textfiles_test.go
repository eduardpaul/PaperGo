package textfiles

import (
	"strings"
	"testing"

	"papergo/internal/doctext"
	"papergo/internal/storage"
)

func TestTextFilesAreNamedAndRead(t *testing.T) {
	Register()
	Register()
	for name, want := range map[string]string{"notes.md": Markdown, "README.markdown": Markdown, "run.log": Plain, "a.txt": Plain} {
		if got := storage.Identify([]byte("# Title\n"), name); got != want {
			t.Fatalf("%s: %s", name, got)
		}
	}
	if got := storage.Identify([]byte("%PDF-1.7"), "renamed.md"); got != "application/pdf" {
		t.Fatal("bytes win over the name", got)
	}
	if got := storage.Identify([]byte{0, 1, 2}, "binary.txt"); got != storage.OctetStream {
		t.Fatal("binary text names stay binary", got)
	}
	if !doctext.Supported(Markdown) || !doctext.Supported(Plain) || !doctext.Supported(CSV) {
		t.Fatal("readers registered")
	}
	read, err := doctext.Extract(t.Context(), []byte("\uFEFFHello\r\nwörld \xff"), Markdown, doctext.Options{}, nil, nil)
	if err != nil || len(read.Pages) != 1 || read.Pages[0].Text != "Hello\nwörld \uFFFD" || read.Chars == 0 {
		t.Fatalf("%+v %v", read, err)
	}
}

func TestLongTextReadsAsPages(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	text := strings.Repeat(line, 3*PageChars/100+5)
	read, err := Read(t.Context(), []byte(text), doctext.Options{MaxPages: 2})
	if err != nil || len(read.Pages) != 2 || !read.Truncated {
		t.Fatalf("%d pages, truncated %v, %v", len(read.Pages), read.Truncated, err)
	}
	for _, p := range read.Pages {
		if !strings.HasSuffix(p.Text, "\n") || len([]rune(p.Text)) > PageChars {
			t.Fatalf("page %d breaks inside a line or is too long: %d", p.Number, len(p.Text))
		}
	}
	empty, err := Read(t.Context(), nil, doctext.Options{MaxPages: 5})
	if err != nil || len(empty.Pages) != 1 || empty.Pages[0].Text != "" {
		t.Fatal(empty, err)
	}
}
