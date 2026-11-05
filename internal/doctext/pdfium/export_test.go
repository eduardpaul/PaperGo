package pdfium

import (
	"testing"
	"time"

	sdk "github.com/klippa-app/go-pdfium"
)

// MaxOpen is the bound on PDFs open at once.
var MaxOpen = maxOpen

// Starts counts how often a reader started PDFium.
func Starts(r *Reader) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

// Users counts the opens and warm-ups under way and the open PDFs of a
// reader's current start.
func Users(r *Reader) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.users
}

// Loaded reports whether a reader has PDFium loaded.
func Loaded(r *Reader) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run == nil {
		return false
	}
	select {
	case <-r.run.done:
		return r.run.err == nil
	default:
		return false
	}
}

// UnloadAfter makes readers made from now on unload PDFium after d idle, until
// the test ends.
func UnloadAfter(t testing.TB, d time.Duration) {
	before := idleUnload
	idleUnload = d
	t.Cleanup(func() { idleUnload = before })
}

// Held counts a reader's open PDFs, and Waiting the opens waiting for one
// to close.
func Held(r *Reader) int    { return len(r.slots) }
func Waiting(r *Reader) int { return int(r.waiting.Load()) }

// AgeStart makes a reader's last start look as old as retryAfter.
func AgeStart(r *Reader) {
	r.mu.Lock()
	defer r.mu.Unlock()
	<-r.run.done
	r.run.ended = r.run.ended.Add(-retryAfter)
}

// ReplaceStarts makes starts call start, which gets the real start, and
// retries failed starts after retry, until the test ends.
func ReplaceStarts(t testing.TB, retry time.Duration, start func(dir string, real func(string) (sdk.Pool, error)) (sdk.Pool, error)) {
	pool, after := newPool, retryAfter
	newPool = func(dir string) (sdk.Pool, error) { return start(dir, pool) }
	retryAfter = retry
	t.Cleanup(func() { newPool, retryAfter = pool, after })
}
