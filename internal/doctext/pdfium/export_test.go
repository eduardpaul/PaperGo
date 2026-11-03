package pdfium

// MaxOpen is the bound on PDFs open at once.
var MaxOpen = maxOpen

// Starts counts how often a reader started PDFium.
func Starts(r *Reader) int { _, _ = r.start(); return r.starts }

// Held counts a reader's open PDFs, and Waiting the opens waiting for one
// to close.
func Held(r *Reader) int    { return len(r.slots) }
func Waiting(r *Reader) int { return int(r.waiting.Load()) }
