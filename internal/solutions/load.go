package solutions

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// Limits of a solution's files.
const (
	MaxManifestBytes = 1 << 20
	MaxSourceBytes   = 1 << 20
	MaxComponents    = 200
)

// Problem is one thing wrong with a solution, in the file and at the place
// it was found.
type Problem struct {
	File    string `json:"file"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (p Problem) String() string {
	if p.Path == "" {
		return p.File + ": " + p.Message
	}
	return p.File + ": " + p.Path + ": " + p.Message
}

// Problems is everything wrong with a solution. A solution with problems
// is not packed, planned or applied.
type Problems []Problem

func (p Problems) Error() string {
	lines := make([]string, len(p))
	for i, problem := range p {
		lines[i] = problem.String()
	}
	return strings.Join(lines, "\n")
}

func (p *Problems) add(file, at, format string, args ...any) {
	*p = append(*p, Problem{File: file, Path: at, Message: fmt.Sprintf(format, args...)})
}

// Open returns the files of a solution at name: a folder, or a ZIP.
func Open(name string) (fs.FS, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return os.DirFS(name), nil
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return OpenZIP(raw)
}

// OpenZIP returns the files of a packed solution.
func OpenZIP(raw []byte) (fs.FS, error) {
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("the solution is neither a folder nor a ZIP: %w", err)
	}
	return z, nil
}

// Load reads a solution's files. It returns what could be read with the
// problems of what could not: unknown properties, files that are missing,
// too large or outside the folder. Check finds everything else.
func Load(files fs.FS) (Solution, Problems) {
	var s Solution
	var problems Problems
	raw, err := readFile(files, ManifestFile, MaxManifestBytes)
	if err != nil {
		problems.add(ManifestFile, "", "%s", readProblem(err))
		return s, problems
	}
	if err := decodeStrict(raw, &s.Manifest); err != nil {
		problems.add(ManifestFile, "", "%s", err)
	}
	for _, file := range settingsFiles(files, WorkflowsDir, &problems) {
		w := Workflow{File: file}
		if !loadSettings(files, file, &w, &problems) {
			continue
		}
		w.Source = loadSource(files, file, "script", w.Script, &problems)
		s.Workflows = append(s.Workflows, w)
	}
	for _, file := range settingsFiles(files, AppsDir, &problems) {
		a := App{File: file}
		if !loadSettings(files, file, &a, &problems) {
			continue
		}
		a.Source = loadSource(files, file, "source", a.SourceFile, &problems)
		s.Apps = append(s.Apps, a)
	}
	return s, problems
}

// settingsFiles lists the settings files of a components folder, sorted.
func settingsFiles(files fs.FS, dir string, problems *Problems) []string {
	entries, err := fs.ReadDir(files, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		problems.add(dir, "", "%s", readProblem(err))
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, path.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	if len(out) > MaxComponents {
		problems.add(dir, "", "a solution holds at most %d of these, not %d", MaxComponents, len(out))
		return nil
	}
	return out
}

func loadSettings(files fs.FS, file string, into any, problems *Problems) bool {
	raw, err := readFile(files, file, MaxManifestBytes)
	if err != nil {
		problems.add(file, "", "%s", readProblem(err))
		return false
	}
	if err := decodeStrict(raw, into); err != nil {
		problems.add(file, "", "%s", err)
		return false
	}
	return true
}

// loadSource reads the file a settings file names, which lies beside it.
func loadSource(files fs.FS, settings, property, name string, problems *Problems) string {
	if name == "" {
		problems.add(settings, property, "names no file")
		return ""
	}
	if name != path.Base(name) || name == "." || name == ".." {
		problems.add(settings, property, "%q must be a file beside this one, without a folder", name)
		return ""
	}
	raw, err := readFile(files, path.Join(path.Dir(settings), name), MaxSourceBytes)
	if err != nil {
		problems.add(settings, property, "%s", readProblem(err))
		return ""
	}
	return string(raw)
}

type tooLarge struct {
	name  string
	limit int
}

func (e tooLarge) Error() string {
	return fmt.Sprintf("%s is larger than %d bytes", e.name, e.limit)
}

func readFile(files fs.FS, name string, limit int) ([]byte, error) {
	f, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, tooLarge{name, limit}
	}
	return raw, nil
}

func readProblem(err error) string {
	var missing *fs.PathError
	if errors.As(err, &missing) && errors.Is(err, fs.ErrNotExist) {
		return missing.Path + " is missing"
	}
	return err.Error()
}

// decodeStrict reads one JSON object, refusing properties the solution
// format does not have and naming them.
func decodeStrict(raw []byte, into any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			line, column := position(raw, syntax.Offset)
			return fmt.Errorf("line %d, column %d: %s", line, column, syntax.Error())
		}
		var wrong *json.UnmarshalTypeError
		if errors.As(err, &wrong) {
			line, column := position(raw, wrong.Offset)
			return fmt.Errorf("line %d, column %d: %s must be a %s, not a %s", line, column, wrong.Field, wrong.Type, wrong.Value)
		}
		return errors.New(strings.TrimPrefix(err.Error(), "json: "))
	}
	if d.More() {
		return errors.New("the file holds more than one JSON value")
	}
	return nil
}

func position(raw []byte, offset int64) (line, column int) {
	line, column = 1, 1
	for i := 0; i < len(raw) && int64(i) < offset; i++ {
		if raw[i] == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	return line, column
}
