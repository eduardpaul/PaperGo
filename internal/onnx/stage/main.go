// Command stage writes the ONNX Runtime bundle the Windows executable
// embeds: each file zstd-compressed, and a manifest of their names, sizes
// and SHA-256 sums, in load order. scripts/native.py runs it.
//
//	go run ./internal/onnx/stage DIRECTORY FILE...
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

type file struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: stage DIRECTORY FILE...")
		os.Exit(2)
	}
	if err := stage(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "stage:", err)
		os.Exit(1)
	}
}

func stage(directory string, paths []string) error {
	if err := os.RemoveAll(directory); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return err
	}
	defer encoder.Close()
	manifest := make([]file, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		name := filepath.Base(path)
		manifest = append(manifest, file{Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
		if err = os.WriteFile(filepath.Join(directory, name+".zst"), encoder.EncodeAll(data, nil), 0o644); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "manifest.json"), append(encoded, '\n'), 0o644)
}
