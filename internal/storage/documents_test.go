package storage

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type tinyDocumentReader struct{ io.Reader }

func (r tinyDocumentReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:min(1, len(p))]) }

func TestIdentifyNamesEveryFile(t *testing.T) {
	cases := []struct {
		name, filename string
		head           []byte
		media          string
	}{
		{"PDF", "a.pdf", []byte("%PDF-1.7\n"), "application/pdf"},
		{"PDF named as text", "notes.txt", []byte("%PDF-1.7\n"), "application/pdf"},
		{"TIFF little endian", "scan", []byte{'I', 'I', 42, 0}, "image/tiff"},
		{"TIFF big endian", "scan", []byte{'M', 'M', 0, 42}, "image/tiff"},
		{"JPEG", "a.jpg", []byte{255, 216, 255}, "image/jpeg"},
		{"PNG", "a.png", []byte{137, 80, 78, 71, 13, 10, 26, 10}, "image/png"},
		{"WebP", "a.webp", []byte("RIFF1234WEBP"), "image/webp"},
		{"GIF", "a.gif", []byte("GIF89a"), "image/gif"},
		{"Word", "Contract.DOCX", []byte("PK\x03\x04rest"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"ZIP", "export.zip", []byte("PK\x03\x04rest"), "application/zip"},
		{"ZIP under another name", "plugin.jar", []byte("PK\x03\x04rest"), "application/zip"},
		{"Word named docx without ZIP bytes", "fake.docx", []byte("not a zip"), OctetStream},
		{"legacy Excel", "book.xls", []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}, "application/vnd.ms-excel"},
		{"CSV", "rows.csv", []byte("a,b\n1,2\n"), "text/csv"},
		{"CSV that is not text", "rows.csv", []byte("a,b\x00"), OctetStream},
		{"HTML is stored and named", "page.html", []byte("<script>alert(1)</script>"), "text/html"},
		{"executable", "setup.exe", []byte("MZ\x90\x00"), OctetStream},
		{"no name", "", []byte("hello"), OctetStream},
		{"empty", "empty.csv", nil, "text/csv"},
		{"MP4 by name", "clip.mp4", []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p'}, "video/mp4"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if media := Identify(test.head, test.filename); media != test.media {
				t.Fatalf("%q: %s, want %s", test.filename, media, test.media)
			}
		})
	}
}

func TestTextChecksUTF8(t *testing.T) {
	if !isText([]byte("Grüße\n")) || isText([]byte{0xff, 0xfe, 'a'}) || isText([]byte("a\x00b")) {
		t.Fatal("text detection")
	}
	// A rune cut at the end of a full head is still text; in a whole file it is not.
	head := append(bytes.Repeat([]byte("a"), HeadSize-1), 0xc3)
	if !isText(head) || isText([]byte{'a', 0xc3}) {
		t.Fatal("cut rune")
	}
}

func TestFitsChecksSignaturesAndText(t *testing.T) {
	if !Fits("application/pdf", []byte("%PDF-1.4")) || Fits("application/pdf", []byte("GIF89a")) {
		t.Fatal("signature")
	}
	if Fits("text/csv", []byte{0}) || !Fits("text/csv", []byte("a,b")) {
		t.Fatal("text")
	}
	if !Fits("application/x-newer", []byte{0}) || Fits("nonsense", nil) {
		t.Fatal("unregistered types fit any bytes; malformed ones nothing")
	}
}

func TestRegisteredTypesComeAfterBuiltIns(t *testing.T) {
	RegisterFileType(FileType{Media: "text/x-test", Label: "Test", Extensions: []string{".pdf"}, Text: true})
	defer func() {
		typesMu.Lock()
		types = types[:len(types)-1]
		typesMu.Unlock()
	}()
	if Identify([]byte("%PDF-1.7"), "a.pdf") != "application/pdf" || Identify([]byte("plain"), "a.pdf") != "text/x-test" {
		t.Fatal("registration order")
	}
}

func TestPutDocumentStoresAnyFileUnchanged(t *testing.T) {
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 2*HeadSize)...)
	object, media, err := PutDocument(t.Context(), store, tinyDocumentReader{bytes.NewReader(body)}, 1<<20, "a.bin")
	if err != nil || media != "application/pdf" || object.Size != int64(len(body)) {
		t.Fatal(object, media, err)
	}
	file, err := store.Open(t.Context(), object.Key)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(file)
	file.Close()
	if !bytes.Equal(body, actual) {
		t.Fatal("identification changed file bytes")
	}
	if _, media, err = PutDocument(t.Context(), store, bytes.NewReader(nil), 32, "empty.bin"); err != nil || media != OctetStream {
		t.Fatal("empty files are stored", media, err)
	}
	if _, _, err = PutDocument(t.Context(), store, bytes.NewReader(bytes.Repeat([]byte("x"), 33)), 32, "big.txt"); !errors.Is(err, ErrTooLarge) {
		t.Fatal("the size limit applies", err)
	}
}
