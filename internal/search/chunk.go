package search

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Page is the text of one page of a file, numbered from 1; 0 is text not on
// a page, such as a summary.
type Page struct {
	Number int
	Text   string
}

// Chunk kinds: file text, and text workflow steps add about a file.
const (
	KindContent = "content"
	KindSummary = "summary"
	KindExtract = "extract"
)

// Chunk is a passage of a file's text, the unit of page hits and, later,
// of semantic search.
type Chunk struct {
	// Part names the step that staged the chunk, so staging it again
	// replaces its chunks only.
	Part    string
	Ordinal int
	// Page is the page the chunk is on, 0 when it is not on one.
	Page       int
	Kind       string
	Text       string
	Enrichment string
	// InputHash identifies the chunk's text, kind and enrichment, not the
	// file's name, so a rename embeds nothing again.
	InputHash string
	Language  string
}

// Hash returns the chunk's input hash.
func (c Chunk) Hash() string {
	h := sha256.Sum256([]byte(c.Kind + "\n" + c.Text + "\n" + c.Enrichment))
	return hex.EncodeToString(h[:])
}

// ChunkOptions tune a chunker.
type ChunkOptions struct {
	// MaxChars bounds a chunk's characters (runes); default 1,200.
	MaxChars int
	// Overlap is how many characters consecutive windows share; 0 is the
	// default, 150, and a negative value none.
	Overlap int
	// MaxChunks bounds the chunks of a text; default 400.
	MaxChunks int
}

// Chunk defaults, as PaperDotNet's Passages.
const (
	DefaultMaxChars  = 1200
	DefaultOverlap   = 150
	DefaultMaxChunks = 400
	// MaxWholeChars bounds the one chunk of the whole chunker.
	MaxWholeChars = 20_000
)

func (o ChunkOptions) withDefaults() ChunkOptions {
	if o.MaxChars <= 0 {
		o.MaxChars = DefaultMaxChars
	}
	switch {
	case o.Overlap == 0:
		o.Overlap = min(DefaultOverlap, o.MaxChars/4)
	case o.Overlap < 0:
		o.Overlap = 0
	case o.Overlap >= o.MaxChars/2:
		o.Overlap = o.MaxChars / 4
	}
	if o.MaxChunks <= 0 {
		o.MaxChunks = DefaultMaxChunks
	}
	return o
}

// Chunkers split text into chunks, by key.
var Chunkers = map[string]func([]Page, ChunkOptions) []Chunk{
	"window":     windowChunks,
	"pages":      pageChunks,
	"paragraphs": paragraphChunks,
	"headings":   headingChunks,
	"whole":      wholeChunk,
}

// ChunkerKeys lists the chunkers.
func ChunkerKeys() []string {
	keys := make([]string, 0, len(Chunkers))
	for k := range Chunkers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Split splits pages with the chunker key into at most o.MaxChunks chunks of
// kind, and reports whether chunks were left out.
func Split(key string, pages []Page, kind string, o ChunkOptions) ([]Chunk, bool, error) {
	split := Chunkers[key]
	if split == nil {
		return nil, false, fmt.Errorf("chunker must be one of %s", strings.Join(ChunkerKeys(), ", "))
	}
	o = o.withDefaults()
	chunks := split(pages, o)
	truncated := len(chunks) > o.MaxChunks
	if truncated {
		chunks = chunks[:o.MaxChunks]
	}
	for i := range chunks {
		chunks[i].Ordinal, chunks[i].Kind = i, kind
		chunks[i].InputHash = chunks[i].Hash()
	}
	return chunks, truncated, nil
}

// windowChunks cuts each page into overlapping windows, as PaperDotNet's
// Passages.Windows.
func windowChunks(pages []Page, o ChunkOptions) []Chunk {
	var out []Chunk
	for _, p := range pages {
		for _, w := range Windows(Normalize(p.Text), o.MaxChars, o.Overlap) {
			out = append(out, Chunk{Page: p.Number, Text: w})
		}
		if len(out) > o.MaxChunks {
			break
		}
	}
	return out
}

// pageChunks makes each page a chunk, cutting pages above MaxChars into
// windows.
func pageChunks(pages []Page, o ChunkOptions) []Chunk {
	return windowChunks(pages, ChunkOptions{MaxChars: o.MaxChars, Overlap: 0, MaxChunks: o.MaxChunks})
}

// paragraphChunks merges the paragraphs of each page up to MaxChars;
// paragraphs end at empty lines, or at lines ending a sentence.
func paragraphChunks(pages []Page, o ChunkOptions) []Chunk {
	var out []Chunk
	for _, p := range pages {
		out = append(out, merge(p.Number, paragraphs(p.Text), "", o)...)
		if len(out) > o.MaxChunks {
			break
		}
	}
	return out
}

func paragraphs(text string) []string {
	var out []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			out = append(out, Normalize(strings.Join(current, " ")))
			current = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		current = append(current, line)
		if strings.ContainsAny(line[len(line)-1:], ".!?:;") {
			flush()
		}
	}
	flush()
	return out
}

// merge joins parts into chunks of at most MaxChars, windowing a part that
// is longer; prefix (a heading) starts every chunk.
func merge(page int, parts []string, prefix string, o ChunkOptions) []Chunk {
	var out []Chunk
	var b strings.Builder
	limit := o.MaxChars - len([]rune(prefix))
	if limit < o.MaxChars/2 {
		prefix, limit = "", o.MaxChars
	}
	emit := func(text string) {
		if text = strings.TrimSpace(text); text != "" {
			if prefix != "" {
				text = prefix + "\n" + text
			}
			out = append(out, Chunk{Page: page, Text: text})
		}
	}
	for _, part := range parts {
		n := len([]rune(part))
		if n > limit {
			emit(b.String())
			b.Reset()
			for _, w := range Windows(part, limit, min(o.Overlap, limit/4)) {
				emit(w)
			}
			continue
		}
		if b.Len() > 0 && len([]rune(b.String()))+1+n > limit {
			emit(b.String())
			b.Reset()
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(part)
	}
	emit(b.String())
	return out
}

// heading matches numbered headings (1., 2.3, §4, Article 5, Section 2).
var heading = regexp.MustCompile(`^(\d+(\.\d+)*\.?|§\s*\d+|(article|section|chapter|artikel|abschnitt|kapitel)\s+\d+)\s`)

// isHeading reports whether a line looks like a heading: short, without a
// sentence's end, numbered or in capitals.
func isHeading(line string) bool {
	if line == "" || len([]rune(line)) > 80 || strings.ContainsAny(line[len(line)-1:], ".,;") && !heading.MatchString(strings.ToLower(line)) {
		return false
	}
	if heading.MatchString(strings.ToLower(line) + " ") {
		return true
	}
	letters, upper := 0, 0
	for _, r := range line {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return letters >= 3 && upper == letters
}

// headingChunks splits each page at headings and merges each section up to
// MaxChars, repeating the heading in every chunk of its section.
func headingChunks(pages []Page, o ChunkOptions) []Chunk {
	var out []Chunk
	title := ""
	for _, p := range pages {
		var section []string
		flush := func() {
			out = append(out, merge(p.Number, paragraphsOf(section), title, o)...)
			section = nil
		}
		for _, line := range strings.Split(p.Text, "\n") {
			line = strings.TrimSpace(line)
			if isHeading(line) {
				flush()
				title = line
				continue
			}
			section = append(section, line)
		}
		flush()
		if len(out) > o.MaxChunks {
			break
		}
	}
	return out
}

func paragraphsOf(lines []string) []string {
	return paragraphs(strings.Join(lines, "\n"))
}

// wholeChunk makes the whole text one chunk, on the first page, cut at
// MaxWholeChars.
func wholeChunk(pages []Page, _ ChunkOptions) []Chunk {
	var parts []string
	page := 0
	for _, p := range pages {
		if t := Normalize(p.Text); t != "" {
			parts = append(parts, t)
			if page == 0 {
				page = p.Number
			}
		}
	}
	text := []rune(strings.Join(parts, "\n"))
	if len(text) == 0 {
		return nil
	}
	if len(text) > MaxWholeChars {
		text = text[:MaxWholeChars]
	}
	return []Chunk{{Page: page, Text: string(text)}}
}

// Windows cuts normalized text into windows of at most maxChars runes that
// end at word boundaries and overlap by about overlap runes, so a sentence
// cut by one window is whole in the next. It is PaperDotNet's
// Passages.Windows.
func Windows(text string, maxChars, overlap int) []string {
	runes := []rune(text)
	var out []string
	start := 0
	for start < len(runes) {
		end := min(len(runes), start+maxChars)
		if end < len(runes) {
			// The last space in (start, end].
			space := -1
			for i := end; i > start; i-- {
				if runes[i] == ' ' {
					space = i
					break
				}
			}
			if space > start+maxChars/2 {
				end = space
			}
		}
		out = append(out, strings.TrimSpace(string(runes[start:end])))
		if end >= len(runes) {
			break
		}
		// Step back by the overlap, to the start of a word.
		next := max(start+1, end-overlap)
		boundary := -1
		for i := next; i < len(runes); i++ {
			if runes[i] == ' ' {
				boundary = i
				break
			}
		}
		if boundary >= 0 && boundary < end {
			start = boundary + 1
		} else {
			start = end
		}
	}
	return out
}

// Normalize turns whitespace runs into one space and trims the ends.
func Normalize(text string) string {
	var b strings.Builder
	space := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}
