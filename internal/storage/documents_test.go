package storage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

type tinyDocumentReader struct{ io.Reader }

func (r tinyDocumentReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:min(1, len(p))]) }

func TestDocumentSignaturesAndStreamingIntake(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
		media  string
	}{
		{"PDF", []byte("%PDF-1.7\n"), "application/pdf"},
		{"TIFF little endian", []byte{'I', 'I', 42, 0}, "image/tiff"},
		{"TIFF big endian", []byte{'M', 'M', 0, 42}, "image/tiff"},
		{"JPEG", []byte{255, 216, 255}, "image/jpeg"},
		{"PNG", []byte{137, 80, 78, 71, 13, 10, 26, 10}, "image/png"},
		{"WebP", []byte("RIFF1234WEBP"), "image/webp"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			body := append(append([]byte{}, test.header...), []byte("image content")...)
			object, media, err := PutDocument(t.Context(), store, tinyDocumentReader{bytes.NewReader(body)}, 1000)
			if err != nil || media != test.media || object.Size != int64(len(body)) {
				t.Fatal(object, media, err)
			}
			file, err := store.Open(t.Context(), object.Key)
			if err != nil {
				t.Fatal(err)
			}
			actual, _ := io.ReadAll(file)
			file.Close()
			if !bytes.Equal(body, actual) {
				t.Fatal("signature read changed file bytes")
			}
		})
	}
	for _, body := range [][]byte{nil, []byte("plain text"), []byte("%PDF"), []byte("RIFF1234WAVE"), {137, 80, 78, 71}} {
		dir := t.TempDir()
		store, err := NewLocal(dir)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = PutDocument(t.Context(), store, bytes.NewReader(body), 32)
		if !errors.Is(err, ErrUnsupportedFile) {
			t.Fatal(body, err)
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Fatal("rejected bytes remained", files)
		}
	}
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = PutDocument(t.Context(), store, bytes.NewReader(bytes.Repeat([]byte("x"), 33)), 32); !errors.Is(err, ErrTooLarge) {
		t.Fatal("size limit must apply to unsupported files", err)
	}
}
