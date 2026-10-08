// Package transfer keeps content transfers alive under the server's read and
// write timeouts. Transfers can take far longer than those allow, so they bound
// stalls instead: each chunk moved extends the connection deadlines.
package transfer

import (
	"io"
	"net/http"
	"time"
)

const Stall = 60 * time.Second

type reader struct {
	io.Reader
	rc *http.ResponseController
}

// Reader extends both deadlines whenever the request body is read, since the
// response is written only after an upload completes.
func Reader(body io.Reader, w http.ResponseWriter) io.Reader {
	return reader{body, http.NewResponseController(w)}
}
func (p reader) Read(b []byte) (int, error) {
	deadline := time.Now().Add(Stall)
	// Unsupported writers (e.g. test recorders) simply keep the server defaults.
	_ = p.rc.SetReadDeadline(deadline)
	_ = p.rc.SetWriteDeadline(deadline)
	return p.Reader.Read(b)
}

type writer struct {
	http.ResponseWriter
	rc *http.ResponseController
}

// Writer extends the write deadline whenever response bytes are written.
func Writer(w http.ResponseWriter) http.ResponseWriter {
	return writer{w, http.NewResponseController(w)}
}
func (p writer) Write(b []byte) (int, error) {
	_ = p.rc.SetWriteDeadline(time.Now().Add(Stall))
	return p.ResponseWriter.Write(b)
}
func (p writer) Unwrap() http.ResponseWriter { return p.ResponseWriter }
