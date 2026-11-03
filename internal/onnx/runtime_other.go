//go:build !windows

package onnx

// EmbeddedLibrary is empty: only the Windows executable embeds ONNX
// Runtime. Linux and macOS packages ship it in lib beside the executable.
func EmbeddedLibrary(string) string { return "" }

func prepareLibrary(path string) (string, error) { return path, nil }
