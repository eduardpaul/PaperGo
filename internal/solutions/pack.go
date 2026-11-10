package solutions

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

// packedAt is the time of every file in a packed solution, so that the same
// solution always packs to the same bytes.
var packedAt = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// FileName is the name a solution packs under: <key>-<version>.zip.
func (s Solution) FileName() string { return s.Manifest.Key + "-" + s.Manifest.Version + ".zip" }

// Pack writes a checked solution as a ZIP: the manifest, and each workflow's
// and app's settings with the source they name. Other files of the folder
// are left out. The same solution packs to the same bytes.
func Pack(s Solution) ([]byte, error) {
	files, err := s.files()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, name := range names {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: packedAt})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// files are the files of a solution: the manifest, and each workflow's
// and app's settings with the source they name.
func (s Solution) files() (map[string][]byte, error) {
	files := map[string][]byte{}
	manifest, err := indented(s.Manifest)
	if err != nil {
		return nil, err
	}
	files[ManifestFile] = manifest
	for _, w := range s.Workflows {
		settings, err := indented(w)
		if err != nil {
			return nil, err
		}
		files[w.File] = settings
		files[path.Join(path.Dir(w.File), w.Script)] = []byte(w.Source)
	}
	for _, a := range s.Apps {
		settings, err := indented(a)
		if err != nil {
			return nil, err
		}
		files[a.File] = settings
		files[path.Join(path.Dir(a.File), a.SourceFile)] = []byte(a.Source)
	}
	return files, nil
}

// Write writes a solution's files into a folder, replacing those it has
// and leaving its other files. It returns how many it wrote.
func Write(s Solution, folder string) (int, error) {
	files, err := s.files()
	if err != nil {
		return 0, err
	}
	for name, content := range files {
		target := filepath.Join(folder, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return 0, err
		}
	}
	return len(files), nil
}

func indented(v any) ([]byte, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
