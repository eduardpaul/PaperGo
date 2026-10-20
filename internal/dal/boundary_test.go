package dal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Bob query builders belong to DAL. Applications may consume DAL records and
// filters, but importing Bob itself would let storage queries escape again.
func TestBobQueryBuildersStayInDAL(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, tree := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path == filepath.Join(root, "internal", "dal") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				name, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if name == "github.com/stephenafamo/bob" || strings.HasPrefix(name, "github.com/stephenafamo/bob/") {
					t.Errorf("%s imports %s: move database queries into internal/dal", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
