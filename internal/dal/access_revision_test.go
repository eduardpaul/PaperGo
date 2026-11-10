package dal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// accessWrite matches a driver statement that writes what permission
// decisions read: grants, directory users, memberships, roles and
// assignments, the permission scope of resources, and resource rows removed
// from under the scopes that name them.
var accessWrite = regexp.MustCompile(`models\.(Grants|IdentityUsers|IdentityMemberships|IdentityAssignments|IdentityRoles)\.(Insert|Update|Delete)\(` +
	`|models\.Resources\.Delete\(` +
	`|(INSERT INTO|UPDATE|DELETE FROM) (grants|identity_users|identity_memberships|identity_assignments|identity_roles)\b` +
	`|DELETE FROM resources\b|SET scope_id\b`)

// Scope decisions are kept between queries under the access revision, so a
// write the revision does not follow would leave readers with permissions
// they no longer have. Every driver function with such a write replaces the
// revision.
func TestDriverAccessWritesReplaceTheRevision(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		files, err := filepath.Glob(filepath.Join("driver", engine, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatal(engine, files, err)
		}
		found := 0
		for _, path := range files {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, source, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				body := string(source[set.Position(fn.Body.Pos()).Offset:set.Position(fn.Body.End()).Offset])
				if !accessWrite.MatchString(body) {
					continue
				}
				found++
				if !strings.Contains(body, "q.accessChanged(ctx)") {
					t.Errorf("%s: %s writes what permission decisions read without replacing the access revision", path, fn.Name.Name)
				}
			}
		}
		if found < 15 {
			t.Errorf("%s: found only %d access writes; the pattern no longer matches the driver", engine, found)
		}
	}
}
