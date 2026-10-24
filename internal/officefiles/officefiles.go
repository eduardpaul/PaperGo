// Package officefiles reads the text of Word, Excel and PowerPoint files
// (Office Open XML) for search and workflows. It plugs into the doctext
// reader registry, so core knows these files only through that socket, as
// it knows Markdown through textfiles.
package officefiles

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"papergo/internal/doctext"
)

// Media types of the files this package reads.
const (
	Word       = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	Excel      = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	PowerPoint = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
)

// partBytes bounds one XML part as read, against archives that expand far
// beyond their size.
const partBytes = 64 << 20

// Bounds on what one file yields; tests lower them.
var (
	// textChars bounds the text read from one file.
	textChars = 8 << 20
	// sheetCells bounds the cells read from one workbook.
	sheetCells = 2_000_000
)

// ErrUnreadable reports a file that is not the Office document its type says.
var ErrUnreadable = errors.New("the Office file cannot be read")

var once sync.Once

// Register adds the readers. Calling it again does nothing.
func Register() {
	once.Do(func() {
		doctext.RegisterReader(Word, ReadWord)
		doctext.RegisterReader(Excel, ReadExcel)
		doctext.RegisterReader(PowerPoint, ReadPowerPoint)
	})
}

type archive struct {
	files map[string]*zip.File
}

func open(data []byte) (*archive, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %w: %w", doctext.ErrUnsupported, ErrUnreadable, err)
	}
	a := &archive{files: map[string]*zip.File{}}
	for _, f := range z.File {
		a.files[strings.TrimPrefix(f.Name, "/")] = f
	}
	return a, nil
}

// part opens an XML part, or nil when the archive has none by that name.
func (a *archive) part(name string) (*xml.Decoder, func(), error) {
	f := a.files[name]
	if f == nil {
		return nil, func() {}, nil
	}
	r, err := f.Open()
	if err != nil {
		return nil, func() {}, fmt.Errorf("%w: %s: %w", ErrUnreadable, name, err)
	}
	d := xml.NewDecoder(io.LimitReader(r, partBytes))
	d.Strict = false
	return d, func() { r.Close() }, nil
}

// relationships maps a part's relationship IDs to the parts they name.
func (a *archive) relationships(owner string) (map[string]string, error) {
	dir, name := path.Split(owner)
	d, done, err := a.part(dir + "_rels/" + name + ".rels")
	defer done()
	if err != nil || d == nil {
		return map[string]string{}, err
	}
	out := map[string]string{}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
		}
		if el, ok := tok.(xml.StartElement); ok && el.Name.Local == "Relationship" {
			target := attr(el, "Target")
			if !strings.HasPrefix(target, "/") {
				target = path.Join(dir, target)
			}
			out[attr(el, "Id")] = strings.TrimPrefix(target, "/")
		}
	}
}

func attr(el xml.StartElement, local string) string {
	for _, a := range el.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// pages collects text into pages, bounded by MaxPages and textChars. Text
// past a bound is left out and the result marked truncated; the text read
// up to it is kept.
type pages struct {
	max   int
	chars int
	r     doctext.Result
	text  bytes.Buffer
}

// end ends a table cell (with a tab) or row (with a line break) in place of
// the line break that ended its last paragraph.
func (p *pages) end(separator string) bool {
	if b := p.text.Bytes(); len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\t') {
		p.text.Truncate(len(b) - 1)
	}
	return p.write(separator)
}

func (p *pages) write(s string) bool {
	if p.chars+len(s) > textChars {
		p.r.Truncated = true
		return false
	}
	p.chars += len(s)
	p.text.WriteString(s)
	return true
}

// next ends the current page; it reports false when no more pages fit.
func (p *pages) next() bool {
	if len(p.r.Pages) == p.max {
		p.r.Truncated = true
		return false
	}
	p.r.Pages = append(p.r.Pages, doctext.Page{Number: len(p.r.Pages) + 1, Text: tidy(p.text.String())})
	p.text.Reset()
	return true
}

func (p *pages) result() doctext.Result {
	if len(p.r.Pages) == 0 {
		p.r.Pages = []doctext.Page{{Number: 1}}
	}
	return p.r
}

// tidy trims each line and keeps at most one empty line between paragraphs.
func tidy(text string) string {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l != "" || len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// ReadWord reads a Word document's body, then its footnotes and endnotes:
// paragraphs as lines, table cells separated by tabs, and a new page at
// each page break the document holds.
func ReadWord(ctx context.Context, data []byte, o doctext.Options) (doctext.Result, error) {
	a, err := open(data)
	if err != nil {
		return doctext.Result{}, err
	}
	if a.files["word/document.xml"] == nil {
		return doctext.Result{}, fmt.Errorf("%w: %w: no word/document.xml", doctext.ErrUnsupported, ErrUnreadable)
	}
	p := &pages{max: o.MaxPages}
	for _, name := range []string{"word/document.xml", "word/footnotes.xml", "word/endnotes.xml"} {
		if err := readWordPart(ctx, a, name, p); err != nil {
			return doctext.Result{}, err
		}
		if p.r.Truncated {
			break
		}
	}
	if p.text.Len() > 0 {
		p.next()
	}
	return p.result(), nil
}

func readWordPart(ctx context.Context, a *archive, name string, p *pages) error {
	d, done, err := a.part(name)
	defer done()
	if err != nil || d == nil {
		return err
	}
	inText := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUnreadable, name, err)
		}
		ok := true
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "t", "delText":
				inText = el.Name.Local == "t"
			case "tab":
				ok = p.write("\t")
			case "br", "cr":
				if attr(el, "type") == "page" {
					ok = p.next()
				} else {
					ok = p.write("\n")
				}
			}
		case xml.EndElement:
			switch el.Name.Local {
			case "t":
				inText = false
			case "p":
				ok = p.write("\n")
			case "tc":
				ok = p.end("\t")
			case "tr":
				ok = p.end("\n")
			}
		case xml.CharData:
			if inText {
				ok = p.write(string(el))
			}
		}
		if !ok {
			return nil
		}
	}
}

// ReadExcel reads each worksheet as a page, in the workbook's order: rows
// as lines and cells separated by tabs, with the sheet's name first.
func ReadExcel(ctx context.Context, data []byte, o doctext.Options) (doctext.Result, error) {
	a, err := open(data)
	if err != nil {
		return doctext.Result{}, err
	}
	sheets, err := workbookSheets(a)
	if err != nil {
		return doctext.Result{}, err
	}
	shared, err := sharedStrings(a)
	if err != nil {
		return doctext.Result{}, err
	}
	p := &pages{max: o.MaxPages}
	cells := 0
	for _, sheet := range sheets {
		if !p.write(sheet.name + "\n") {
			break
		}
		if err := readSheet(ctx, a, sheet.part, shared, p, &cells); err != nil {
			return doctext.Result{}, err
		}
		// A sheet cut short by a bound still ends its page.
		if !p.next() || p.r.Truncated {
			break
		}
	}
	return p.result(), nil
}

type sheet struct{ name, part string }

func workbookSheets(a *archive) ([]sheet, error) {
	rels, err := a.relationships("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	d, done, err := a.part("xl/workbook.xml")
	defer done()
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fmt.Errorf("%w: %w: no xl/workbook.xml", doctext.ErrUnsupported, ErrUnreadable)
	}
	var out []sheet
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
		}
		if el, ok := tok.(xml.StartElement); ok && el.Name.Local == "sheet" {
			if part := rels[attr(el, "id")]; part != "" {
				out = append(out, sheet{attr(el, "name"), part})
			}
		}
	}
}

func sharedStrings(a *archive) ([]string, error) {
	d, done, err := a.part("xl/sharedStrings.xml")
	defer done()
	if err != nil || d == nil {
		return nil, err
	}
	var out []string
	var current strings.Builder
	inText, chars := false, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			inText = el.Name.Local == "t"
		case xml.EndElement:
			if el.Name.Local == "t" {
				inText = false
			}
			if el.Name.Local == "si" {
				out = append(out, current.String())
				current.Reset()
			}
		case xml.CharData:
			if inText && chars < textChars {
				chars += len(el)
				current.Write(el)
			}
		}
	}
}

func readSheet(ctx context.Context, a *archive, part string, shared []string, p *pages, cells *int) error {
	d, done, err := a.part(part)
	defer done()
	if err != nil || d == nil {
		return err
	}
	var kind string
	var value strings.Builder
	inValue, first := false, true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUnreadable, part, err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "row":
				first = true
			case "c":
				kind = attr(el, "t")
				value.Reset()
			case "v", "t":
				inValue = true
			}
		case xml.EndElement:
			switch el.Name.Local {
			case "v", "t":
				inValue = false
			case "c":
				text := value.String()
				if kind == "s" {
					if i, err := strconv.Atoi(text); err == nil && i >= 0 && i < len(shared) {
						text = shared[i]
					}
				} else if kind == "b" {
					text = map[string]string{"0": "FALSE", "1": "TRUE"}[text]
				}
				if text == "" {
					continue
				}
				if *cells++; *cells > sheetCells {
					p.r.Truncated = true
					return nil
				}
				if !first && !p.write("\t") || !p.write(text) {
					return nil
				}
				first = false
			case "row":
				if !p.write("\n") {
					return nil
				}
			}
		case xml.CharData:
			if inValue {
				value.Write(el)
			}
		}
	}
}

// ReadPowerPoint reads each slide as a page, in the presentation's order,
// then its speaker notes.
func ReadPowerPoint(ctx context.Context, data []byte, o doctext.Options) (doctext.Result, error) {
	a, err := open(data)
	if err != nil {
		return doctext.Result{}, err
	}
	slides, err := presentationSlides(a)
	if err != nil {
		return doctext.Result{}, err
	}
	p := &pages{max: o.MaxPages}
	for _, slide := range slides {
		if err := readDrawingText(ctx, a, slide, p); err != nil {
			return doctext.Result{}, err
		}
		if rels, err := a.relationships(slide); err == nil {
			for _, target := range rels {
				if strings.HasPrefix(target, "ppt/notesSlides/") {
					if !p.write("\n") {
						break
					}
					if err := readDrawingText(ctx, a, target, p); err != nil {
						return doctext.Result{}, err
					}
				}
			}
		}
		// A slide cut short by a bound still ends its page.
		if !p.next() || p.r.Truncated {
			break
		}
	}
	return p.result(), nil
}

func presentationSlides(a *archive) ([]string, error) {
	rels, err := a.relationships("ppt/presentation.xml")
	if err != nil {
		return nil, err
	}
	d, done, err := a.part("ppt/presentation.xml")
	defer done()
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fmt.Errorf("%w: %w: no ppt/presentation.xml", doctext.ErrUnsupported, ErrUnreadable)
	}
	var out []string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
		}
		if el, ok := tok.(xml.StartElement); ok && el.Name.Local == "sldId" {
			if part := rels[attr(el, "id")]; part != "" && !slices.Contains(out, part) {
				out = append(out, part)
			}
		}
	}
	return out, nil
}

// readDrawingText reads DrawingML text: runs in paragraphs, as lines.
func readDrawingText(ctx context.Context, a *archive, part string, p *pages) error {
	d, done, err := a.part(part)
	defer done()
	if err != nil || d == nil {
		return err
	}
	inText := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUnreadable, part, err)
		}
		ok := true
		switch el := tok.(type) {
		case xml.StartElement:
			inText = el.Name.Local == "t"
			if el.Name.Local == "br" {
				ok = p.write("\n")
			}
		case xml.EndElement:
			if el.Name.Local == "t" {
				inText = false
			}
			if el.Name.Local == "p" {
				ok = p.write("\n")
			}
		case xml.CharData:
			if inText {
				ok = p.write(string(el))
			}
		}
		if !ok {
			return nil
		}
	}
}
