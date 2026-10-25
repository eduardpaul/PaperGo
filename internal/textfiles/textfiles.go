// Package textfiles makes Markdown and plain text files first-class: it
// names them when they are stored and reads their text, and that of CSV
// files, for search and workflows. It plugs into the storage file type registry and the doctext
// reader registry, so core knows text files only through those sockets.
package textfiles

import (
	"context"
	"strings"
	"sync"
	"unicode/utf8"

	"papergo/internal/doctext"
	"papergo/internal/storage"
)

// Media types of the files this package handles.
const (
	Markdown = "text/markdown"
	Plain    = "text/plain"
	// CSV is named by storage; this package reads its text.
	CSV = "text/csv"
)

// PageChars bounds a page of text, so long files read as several pages and
// search shows where in the file a match is.
const PageChars = 20_000

var once sync.Once

// Register adds the text types and their reader. Calling it again does
// nothing.
func Register() {
	once.Do(func() {
		storage.RegisterFileType(storage.FileType{Media: Markdown, Label: "Markdown document", Family: "text", Extensions: []string{".md", ".markdown"}, Text: true})
		storage.RegisterFileType(storage.FileType{Media: Plain, Label: "Text file", Family: "text", Extensions: []string{".txt", ".log"}, Text: true})
		doctext.RegisterReader(Markdown, Read)
		doctext.RegisterReader(Plain, Read)
		doctext.RegisterReader(CSV, Read)
	})
}

// Read reads a text file as pages of at most PageChars characters, broken at
// line ends where one is near. Invalid UTF-8 reads as replacement characters.
func Read(ctx context.Context, data []byte, o doctext.Options) (doctext.Result, error) {
	text := strings.TrimPrefix(strings.ToValidUTF8(string(data), "\uFFFD"), "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var r doctext.Result
	for len(text) > 0 {
		if err := ctx.Err(); err != nil {
			return doctext.Result{}, err
		}
		if len(r.Pages) == o.MaxPages {
			r.Truncated = true
			break
		}
		page := cut(text)
		r.Pages = append(r.Pages, doctext.Page{Number: len(r.Pages) + 1, Text: page})
		text = text[len(page):]
	}
	if len(r.Pages) == 0 {
		r.Pages = []doctext.Page{{Number: 1}}
	}
	return r, nil
}

// cut returns the first page of text: up to PageChars characters, ending at
// the last line break in the page's second half when there is one.
func cut(text string) string {
	if utf8.RuneCountInString(text) <= PageChars {
		return text
	}
	end, count := 0, 0
	for end < len(text) && count < PageChars {
		_, size := utf8.DecodeRuneInString(text[end:])
		end += size
		count++
	}
	if line := strings.LastIndexByte(text[:end], '\n'); line >= end/2 {
		return text[:line+1]
	}
	return text[:end]
}
