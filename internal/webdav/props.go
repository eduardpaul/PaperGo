package webdav

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"papergo/internal/dms"
	"strconv"
	"strings"
	"time"
)

// Properties are the live DAV properties derived from library entries. Dead
// properties are not stored: PROPPATCH acknowledges them, because Windows sets
// Win32 timestamps after every upload and reports failures to the user.

const maxXMLBody = 64 * 1024

var liveProps = []string{"resourcetype", "displayname", "getcontentlength", "getcontenttype", "getetag", "getlastmodified", "creationdate", "supportedlock", "lockdiscovery"}

const supportedLock = `<D:supportedlock><D:lockentry><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockentry>` +
	`<D:lockentry><D:lockscope><D:shared/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockentry></D:supportedlock>`

type element struct{ XMLName xml.Name }
type propList struct {
	Names []element `xml:",any"`
}
type propfindBody struct {
	XMLName  xml.Name  `xml:"DAV: propfind"`
	AllProp  *struct{} `xml:"DAV: allprop"`
	PropName *struct{} `xml:"DAV: propname"`
	Prop     *propList `xml:"DAV: prop"`
}

// propQuery is a parsed PROPFIND: all properties, only their names, or a selection.
type propQuery struct {
	names bool
	props []xml.Name
}

func readBody(r *request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxXMLBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxXMLBody {
		return nil, badRequest("request body is too large")
	}
	return body, nil
}
func readPropfind(r *request) (propQuery, error) {
	body, err := readBody(r)
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return propQuery{}, err
	}
	var in propfindBody
	if err = xml.Unmarshal(body, &in); err != nil {
		return propQuery{}, badRequest("invalid propfind")
	}
	switch {
	case in.PropName != nil:
		return propQuery{names: true}, nil
	case in.Prop != nil && in.AllProp == nil:
		q := propQuery{props: []xml.Name{}}
		for _, e := range in.Prop.Names {
			q.props = append(q.props, e.XMLName)
		}
		return q, nil
	}
	return propQuery{}, nil
}

func (h *Handler) propfind(w http.ResponseWriter, r *request) error {
	// Unbounded listings would walk whole libraries (RFC 4918 §9.1).
	depth := r.Header.Get("Depth")
	if depth != "0" && depth != "1" {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, err := io.WriteString(w, xml.Header+`<D:error xmlns:D="DAV:"><D:propfind-finite-depth/></D:error>`)
		return err
	}
	q, err := readPropfind(r)
	if err != nil {
		return err
	}
	var f *dms.File
	var entries []*dms.File
	if depth == "0" {
		f, err = h.DMS.LibraryFile(r.Context(), r.subject, r.library, r.path)
	} else {
		f, entries, err = h.DMS.LibraryFolder(r.Context(), r.subject, r.library, r.path)
	}
	if err != nil {
		return err
	}
	locks := h.locks.snapshot()
	b := multistatus(w)
	h.writeResponse(b, r.library, r.path, f, q, locks)
	for _, e := range entries {
		h.writeResponse(b, r.library, append(r.path[:len(r.path):len(r.path)], e.Name), e, q, locks)
	}
	b.WriteString(`</D:multistatus>`)
	return b.Flush()
}

// propfindRoot describes the mount root as an empty collection.
func (h *Handler) propfindRoot(w http.ResponseWriter, r *request) {
	q, err := readPropfind(r)
	if err != nil {
		h.fail(w, r.Request, err)
		return
	}
	if q.props == nil && !q.names {
		q.props = []xml.Name{{Space: "DAV:", Local: "resourcetype"}, {Space: "DAV:", Local: "displayname"}}
	}
	b := multistatus(w)
	found, missing := "", []xml.Name{}
	for _, name := range q.props {
		switch {
		case name.Space == "DAV:" && name.Local == "resourcetype":
			found += `<D:resourcetype><D:collection/></D:resourcetype>`
		case name.Space == "DAV:" && name.Local == "displayname":
			found += `<D:displayname>webdav</D:displayname>`
		default:
			missing = append(missing, name)
		}
	}
	if q.names {
		found = `<D:resourcetype/><D:displayname/>`
	}
	b.WriteString(`<D:response><D:href>` + Prefix + `</D:href>`)
	writePropstats(b, found, missing)
	b.WriteString(`</D:response></D:multistatus>`)
	_ = b.Flush()
}

func multistatus(w http.ResponseWriter) *bufio.Writer {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	b := bufio.NewWriterSize(w, 32*1024)
	b.WriteString(xml.Header + `<D:multistatus xmlns:D="DAV:">`)
	return b
}
func (h *Handler) writeResponse(b *bufio.Writer, library string, path []string, f *dms.File, q propQuery, locks []lock) {
	b.WriteString(`<D:response><D:href>` + escape(href(library, path, f.Collection())) + `</D:href>`)
	names := q.props
	if names == nil {
		names = make([]xml.Name, 0, len(liveProps))
		for _, local := range liveProps {
			names = append(names, xml.Name{Space: "DAV:", Local: local})
		}
	}
	var found strings.Builder
	missing := []xml.Name{}
	for _, name := range names {
		value, ok := liveProp(name, library, path, f, locks)
		switch {
		case !ok:
			missing = append(missing, name)
		case q.names:
			found.WriteString(`<D:` + name.Local + `/>`)
		default:
			found.WriteString(value)
		}
	}
	// allprop and propname describe only the properties the entry has.
	if q.props == nil {
		missing = nil
	}
	writePropstats(b, found.String(), missing)
	b.WriteString(`</D:response>`)
}
func writePropstats(b *bufio.Writer, found string, missing []xml.Name) {
	if found != "" {
		b.WriteString(`<D:propstat><D:prop>` + found + `</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>`)
	}
	if len(missing) > 0 {
		b.WriteString(`<D:propstat><D:prop>`)
		for _, name := range missing {
			b.WriteString(emptyElement(name))
		}
		b.WriteString(`</D:prop><D:status>HTTP/1.1 404 Not Found</D:status></D:propstat>`)
	}
}

// liveProp renders one property of f, reporting whether f has it.
func liveProp(name xml.Name, library string, path []string, f *dms.File, locks []lock) (string, bool) {
	if name.Space != "DAV:" {
		return "", false
	}
	text := func(v string) string { return `<D:` + name.Local + `>` + escape(v) + `</D:` + name.Local + `>` }
	switch name.Local {
	case "resourcetype":
		if f.Collection() {
			return `<D:resourcetype><D:collection/></D:resourcetype>`, true
		}
		return `<D:resourcetype/>`, true
	case "displayname":
		return text(f.Name), true
	case "getcontentlength":
		return text(strconv.FormatInt(f.Size(), 10)), !f.Collection()
	case "getcontenttype":
		return text(f.ContentType()), !f.Collection()
	case "getetag":
		return text(f.ETag()), true
	case "getlastmodified":
		return text(f.UpdatedAt.UTC().Format(http.TimeFormat)), true
	case "creationdate":
		return text(f.CreatedAt.UTC().Format(time.RFC3339)), true
	case "supportedlock":
		return supportedLock, true
	case "lockdiscovery":
		key := lockKey(library, path)
		var b strings.Builder
		b.WriteString(`<D:lockdiscovery>`)
		for _, l := range locks {
			if l.covers(key) {
				b.WriteString(activeLock(l))
			}
		}
		b.WriteString(`</D:lockdiscovery>`)
		return b.String(), true
	}
	return "", false
}

type propertyUpdate struct {
	XMLName xml.Name `xml:"DAV: propertyupdate"`
	Set     []struct {
		Prop propList `xml:"DAV: prop"`
	} `xml:"DAV: set"`
	Remove []struct {
		Prop propList `xml:"DAV: prop"`
	} `xml:"DAV: remove"`
}

// proppatch rejects changes to live DAV properties atomically and otherwise
// acknowledges dead properties without storing them.
func (h *Handler) proppatch(w http.ResponseWriter, r *request) error {
	if err := h.confirm(r, r.path, false); err != nil {
		return err
	}
	existing, err := h.DMS.WriteTarget(r.Context(), r.subject, r.library, r.path, dms.FileConditions{})
	if err != nil {
		return err
	}
	if existing == nil {
		return dms.ErrNotFound
	}
	body, err := readBody(r)
	if err != nil {
		return err
	}
	var in propertyUpdate
	if err = xml.Unmarshal(body, &in); err != nil {
		return badRequest("invalid propertyupdate")
	}
	var names []xml.Name
	for _, set := range in.Set {
		for _, e := range set.Prop.Names {
			names = append(names, e.XMLName)
		}
	}
	for _, remove := range in.Remove {
		for _, e := range remove.Prop.Names {
			names = append(names, e.XMLName)
		}
	}
	protected := false
	for _, name := range names {
		protected = protected || name.Space == "DAV:"
	}
	b := multistatus(w)
	b.WriteString(`<D:response><D:href>` + escape(href(r.library, r.path, existing.Collection())) + `</D:href>`)
	for _, name := range names {
		status := "200 OK"
		switch {
		case name.Space == "DAV:":
			status = "403 Forbidden"
		case protected:
			status = "424 Failed Dependency"
		}
		b.WriteString(`<D:propstat><D:prop>` + emptyElement(name) + `</D:prop><D:status>HTTP/1.1 ` + status + `</D:status></D:propstat>`)
	}
	b.WriteString(`</D:response></D:multistatus>`)
	return b.Flush()
}

// emptyElement renders a property name in its own namespace.
func emptyElement(name xml.Name) string {
	switch name.Space {
	case "DAV:":
		return `<D:` + name.Local + `/>`
	case "":
		return `<` + name.Local + ` xmlns=""/>`
	}
	return `<P:` + name.Local + ` xmlns:P="` + escape(name.Space) + `"/>`
}
func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
