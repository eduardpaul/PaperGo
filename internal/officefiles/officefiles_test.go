package officefiles

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"papergo/internal/doctext"
)

func archiveOf(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range parts {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const w = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func TestWordReadsParagraphsTablesAndPageBreaks(t *testing.T) {
	data := archiveOf(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document ` + w + `><w:body>
<w:p><w:r><w:t>Supplier </w:t></w:r><w:r><w:t>agreement</w:t></w:r></w:p>
<w:p><w:r><w:t>Term:</w:t><w:tab/><w:t>12 months</w:t></w:r><w:r><w:delText>deleted words</w:delText></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Item</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Price</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:r><w:br w:type="page"/><w:t>Signatures</w:t></w:r></w:p>
</w:body></w:document>`,
		"word/footnotes.xml": `<w:footnotes ` + w + `><w:footnote><w:p><w:r><w:t>A footnote</w:t></w:r></w:p></w:footnote></w:footnotes>`,
	})
	r, err := ReadWord(context.Background(), data, doctext.Options{MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Pages) != 2 {
		t.Fatalf("%d pages: %+v", len(r.Pages), r.Pages)
	}
	first := r.Pages[0].Text
	for _, want := range []string{"Supplier agreement", "Term:\t12 months", "\nItem\tPrice"} {
		if !strings.Contains(first, want) {
			t.Errorf("page 1 lacks %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "deleted words") {
		t.Error("deleted text was read")
	}
	if !strings.Contains(r.Pages[1].Text, "Signatures") || !strings.Contains(r.Pages[1].Text, "A footnote") {
		t.Errorf("page 2: %q", r.Pages[1].Text)
	}
	if r, err = ReadWord(context.Background(), data, doctext.Options{MaxPages: 1}); err != nil || len(r.Pages) != 1 || !r.Truncated {
		t.Fatalf("bounded: %+v %v", r, err)
	}
}

func TestExcelReadsSheetsInOrderWithSharedStrings(t *testing.T) {
	data := archiveOf(t, map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Totals" r:id="rId2"/><sheet name="Lines" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="/xl/worksheets/sheet2.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst><si><t>Invoice</t></si><si><r><t>Grand </t></r><r><t>total</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData><row><c t="s"><v>0</v></c><c><v>4711</v></c><c t="inlineStr"><is><t>paid</t></is></c></row><row><c t="b"><v>1</v></c></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml":   `<worksheet><sheetData><row><c t="s"><v>1</v></c><c><f>SUM(A1)</f><v>1240</v></c></row></sheetData></worksheet>`,
	})
	r, err := ReadExcel(context.Background(), data, doctext.Options{MaxPages: 10})
	if err != nil || len(r.Pages) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Pages[0].Text != "Totals\nGrand total\t1240" {
		t.Errorf("first sheet: %q", r.Pages[0].Text)
	}
	if r.Pages[1].Text != "Lines\nInvoice\t4711\tpaid\nTRUE" {
		t.Errorf("second sheet: %q", r.Pages[1].Text)
	}
}

func TestPowerPointReadsSlidesAndNotes(t *testing.T) {
	data := archiveOf(t, map[string]string{
		"ppt/presentation.xml":                   `<p:presentation xmlns:p="p" xmlns:r="r"><p:sldIdLst><p:sldId r:id="rId3"/><p:sldId r:id="rId2"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels":        `<Relationships><Relationship Id="rId2" Target="slides/slide1.xml"/><Relationship Id="rId3" Target="slides/slide2.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":                  `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>Roadmap</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/slide2.xml":                  `<p:sld xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>Quarterly </a:t></a:r><a:r><a:t>results</a:t></a:r></a:p><a:p><a:r><a:t>Up 12%</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/_rels/slide2.xml.rels":       `<Relationships><Relationship Id="rId1" Target="../notesSlides/notesSlide1.xml"/></Relationships>`,
		"ppt/notesSlides/notesSlide1.xml":        `<p:notes xmlns:p="p" xmlns:a="a"><a:p><a:r><a:t>Mention the new office</a:t></a:r></a:p></p:notes>`,
		"ppt/notesSlides/_rels/notesSlide1.rels": ``,
	})
	r, err := ReadPowerPoint(context.Background(), data, doctext.Options{MaxPages: 10})
	if err != nil || len(r.Pages) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Pages[0].Text != "Quarterly results\nUp 12%\n\nMention the new office" {
		t.Errorf("first slide: %q", r.Pages[0].Text)
	}
	if r.Pages[1].Text != "Roadmap" {
		t.Errorf("second slide: %q", r.Pages[1].Text)
	}
}

func TestNotOfficeFilesAreUnsupported(t *testing.T) {
	for _, read := range []doctext.TextReader{ReadWord, ReadExcel, ReadPowerPoint} {
		if _, err := read(context.Background(), []byte("PK not a zip"), doctext.Options{MaxPages: 1}); !errors.Is(err, doctext.ErrUnsupported) {
			t.Error(err)
		}
		if _, err := read(context.Background(), archiveOf(t, map[string]string{"other.xml": "<x/>"}), doctext.Options{MaxPages: 1}); !errors.Is(err, doctext.ErrUnsupported) {
			t.Error(err)
		}
	}
}

func TestRegisterMakesOfficeTextReadable(t *testing.T) {
	Register()
	for _, media := range []string{Word, Excel, PowerPoint} {
		if !doctext.Supported(media) {
			t.Error(media)
		}
	}
}
