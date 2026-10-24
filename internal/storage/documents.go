package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// ErrUnsupportedFile refuses a request that processes a file, such as
// reading its text, whose kind the processor does not handle. Storage itself
// accepts every file.
var ErrUnsupportedFile = errors.New("this kind of file cannot be processed by this request")

// HeadSize is how much of a file Identify reads.
const HeadSize = 8192

// OctetStream is the media type of a file no registered type describes.
const OctetStream = "application/octet-stream"

// FileType describes a kind of file PaperGo can name. Storage accepts every
// file; types only name them, so processors, viewers and people can tell
// what a file is. A type matches when its signature (if any) matches the
// first bytes, its extensions (if any) include the file's, and, for a text
// type, the first bytes are UTF-8 without NUL bytes. Types are tried in the
// order they were registered, so signatures that prove a format come first.
type FileType struct {
	Media string
	// Label names the type for people: "PDF document".
	Label string
	// Family groups types for icons: document, spreadsheet, image, text...
	Family     string
	Extensions []string
	Signature  func(head []byte) bool
	Text       bool
}

var (
	typesMu sync.RWMutex
	types   []FileType
)

// RegisterFileType adds a type after the registered ones. A type with the
// media type of a registered one replaces it in place.
func RegisterFileType(t FileType) {
	if !ValidMediaType(t.Media) || t.Signature == nil && len(t.Extensions) == 0 {
		panic("storage: a file type needs a media type and a signature or extensions")
	}
	typesMu.Lock()
	defer typesMu.Unlock()
	if i := slices.IndexFunc(types, func(e FileType) bool { return e.Media == t.Media }); i >= 0 {
		types[i] = t
		return
	}
	types = append(types, t)
}

// FileTypes returns the registered types in matching order.
func FileTypes() []FileType {
	typesMu.RLock()
	defer typesMu.RUnlock()
	return slices.Clone(types)
}

// Identify names the media type of a file from its first HeadSize bytes and
// its name. Bytes that prove a format win over the name: a PDF named
// notes.txt is a PDF. A file no type describes is OctetStream.
func Identify(head []byte, filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	text := isText(head)
	for _, t := range FileTypes() {
		if t.Signature != nil && !t.Signature(head) {
			continue
		}
		if len(t.Extensions) > 0 && !slices.Contains(t.Extensions, ext) {
			continue
		}
		if t.Text && !text {
			continue
		}
		return t.Media
	}
	return OctetStream
}

// Signature names the media type the first bytes alone prove, or "" when no
// registered signature without extensions matches them.
func Signature(head []byte) string {
	for _, t := range FileTypes() {
		if t.Signature != nil && len(t.Extensions) == 0 && t.Signature(head) {
			return t.Media
		}
	}
	return ""
}

// Fits reports whether bytes starting with head can be a file of media: a
// registered type's signature or text check must hold. Media no type
// registers, as from a newer PaperGo, fits any bytes.
func Fits(media string, head []byte) bool {
	for _, t := range FileTypes() {
		if t.Media == media {
			return (t.Signature == nil || t.Signature(head)) && (!t.Text || isText(head))
		}
	}
	return ValidMediaType(media)
}

// ValidMediaType reports whether media has the shape of a media type.
func ValidMediaType(media string) bool {
	return len(media) >= 3 && len(media) <= 127 && strings.Count(media, "/") == 1 && !strings.HasPrefix(media, "/") && !strings.HasSuffix(media, "/") &&
		!strings.ContainsFunc(media, func(r rune) bool { return r <= ' ' || r >= 0x7f || r == ';' || r == ',' })
}

// isText reports whether head reads as the start of UTF-8 text: no NUL
// bytes, and valid UTF-8 except for a rune the head cut off.
func isText(head []byte) bool {
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	for i := 0; i < len(head); {
		r, size := utf8.DecodeRune(head[i:])
		if r == utf8.RuneError && size <= 1 {
			return len(head)-i < utf8.UTFMax && len(head) == HeadSize && !utf8.FullRune(head[i:])
		}
		i += size
	}
	return true
}

type headReader struct {
	io.Reader
	head []byte
}

func (r *headReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if len(r.head) < HeadSize {
		r.head = append(r.head, p[:min(n, HeadSize-len(r.head))]...)
	}
	return n, err
}

// PutDocument stores a file of any type under the streaming limits and
// names its media type from its first bytes and filename.
func PutDocument(ctx context.Context, store Store, input io.Reader, max int64, filename string) (Object, string, error) {
	reader := &headReader{Reader: input}
	object, err := store.Put(ctx, reader, max)
	if err != nil {
		return Object{}, "", err
	}
	return object, Identify(reader.head, filename), nil
}

func prefix(signature ...[]byte) func([]byte) bool {
	return func(head []byte) bool {
		return slices.ContainsFunc(signature, func(s []byte) bool { return bytes.HasPrefix(head, s) })
	}
}

var (
	zip = prefix([]byte("PK\x03\x04"), []byte("PK\x05\x06"))
	ole = prefix([]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
)

// The types PaperGo names. Signatures first, then container formats told
// apart by extension, then types known only by extension.
func init() {
	for _, t := range []FileType{
		{Media: "application/pdf", Label: "PDF document", Family: "pdf", Signature: prefix([]byte("%PDF-"))},
		{Media: "image/tiff", Label: "TIFF image", Family: "image", Signature: prefix([]byte{'I', 'I', 42, 0}, []byte{'M', 'M', 0, 42})},
		{Media: "image/jpeg", Label: "JPEG image", Family: "image", Signature: prefix([]byte{255, 216, 255})},
		{Media: "image/png", Label: "PNG image", Family: "image", Signature: prefix([]byte{137, 80, 78, 71, 13, 10, 26, 10})},
		{Media: "image/webp", Label: "WebP image", Family: "image", Signature: func(h []byte) bool {
			return len(h) >= 12 && string(h[:4]) == "RIFF" && string(h[8:12]) == "WEBP"
		}},
		{Media: "image/gif", Label: "GIF image", Family: "image", Signature: prefix([]byte("GIF87a"), []byte("GIF89a"))},
		{Media: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Label: "Word document", Family: "document", Signature: zip, Extensions: []string{".docx"}},
		{Media: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Label: "Excel workbook", Family: "spreadsheet", Signature: zip, Extensions: []string{".xlsx"}},
		{Media: "application/vnd.openxmlformats-officedocument.presentationml.presentation", Label: "PowerPoint presentation", Family: "presentation", Signature: zip, Extensions: []string{".pptx"}},
		{Media: "application/vnd.oasis.opendocument.text", Label: "OpenDocument text", Family: "document", Signature: zip, Extensions: []string{".odt"}},
		{Media: "application/vnd.oasis.opendocument.spreadsheet", Label: "OpenDocument spreadsheet", Family: "spreadsheet", Signature: zip, Extensions: []string{".ods"}},
		{Media: "application/vnd.oasis.opendocument.presentation", Label: "OpenDocument presentation", Family: "presentation", Signature: zip, Extensions: []string{".odp"}},
		{Media: "application/epub+zip", Label: "EPUB book", Family: "document", Signature: zip, Extensions: []string{".epub"}},
		{Media: "application/zip", Label: "ZIP archive", Family: "archive", Signature: zip},
		{Media: "application/msword", Label: "Word document", Family: "document", Signature: ole, Extensions: []string{".doc"}},
		{Media: "application/vnd.ms-excel", Label: "Excel workbook", Family: "spreadsheet", Signature: ole, Extensions: []string{".xls"}},
		{Media: "application/vnd.ms-powerpoint", Label: "PowerPoint presentation", Family: "presentation", Signature: ole, Extensions: []string{".ppt"}},
		{Media: "application/vnd.ms-outlook", Label: "Outlook message", Family: "email", Signature: ole, Extensions: []string{".msg"}},
		{Media: "application/x-7z-compressed", Label: "7-Zip archive", Family: "archive", Signature: prefix([]byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c})},
		{Media: "application/gzip", Label: "Gzip archive", Family: "archive", Signature: prefix([]byte{0x1f, 0x8b})},
		{Media: "text/csv", Label: "CSV file", Family: "spreadsheet", Extensions: []string{".csv"}, Text: true},
		{Media: "application/json", Label: "JSON file", Family: "code", Extensions: []string{".json"}, Text: true},
		{Media: "application/xml", Label: "XML file", Family: "code", Extensions: []string{".xml"}, Text: true},
		{Media: "text/html", Label: "HTML page", Family: "code", Extensions: []string{".html", ".htm"}, Text: true},
		{Media: "image/svg+xml", Label: "SVG image", Family: "image", Extensions: []string{".svg"}, Text: true},
		{Media: "application/rtf", Label: "Rich text document", Family: "document", Extensions: []string{".rtf"}, Text: true},
		{Media: "message/rfc822", Label: "Email message", Family: "email", Extensions: []string{".eml"}, Text: true},
		{Media: "text/calendar", Label: "Calendar file", Family: "text", Extensions: []string{".ics"}, Text: true},
		{Media: "audio/mpeg", Label: "MP3 audio", Family: "audio", Extensions: []string{".mp3"}},
		{Media: "audio/wav", Label: "WAV audio", Family: "audio", Extensions: []string{".wav"}},
		{Media: "audio/mp4", Label: "M4A audio", Family: "audio", Extensions: []string{".m4a"}},
		{Media: "video/mp4", Label: "MP4 video", Family: "video", Extensions: []string{".mp4"}},
		{Media: "video/quicktime", Label: "QuickTime video", Family: "video", Extensions: []string{".mov"}},
		{Media: "video/webm", Label: "WebM video", Family: "video", Extensions: []string{".webm"}},
	} {
		RegisterFileType(t)
	}
}
