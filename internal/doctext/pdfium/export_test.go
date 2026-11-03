package pdfium

// MaxOpen is the bound on PDFs open at once.
var MaxOpen = maxOpen

// Starts counts how often a reader started PDFium.
func Starts(r *Reader) int { _, _ = r.start(); return r.starts }
