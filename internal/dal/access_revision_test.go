package dal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// accessWrite matches a driver statement that writes a table permission
// decisions read: grants, directory users, memberships, roles and
// assignments, the permission scope of resources, and resource rows removed
// from under the scopes that name them.
var accessWrite = regexp.MustCompile(`models\.(Grants|IdentityUsers|IdentityMemberships|IdentityAssignments|IdentityRoles)\.(Insert|Update|Delete)\(` +
	`|models\.Resources\.Delete\(` +
	`|(INSERT INTO|UPDATE|DELETE FROM) (grants|identity_users|identity_memberships|identity_assignments|identity_roles)\b` +
	`|DELETE FROM resources\b|SET scope_id\b`)

// revisionChange matches a call that replaces an access revision.
var revisionChange = regexp.MustCompile(`q\.(accessChanged|accessChangedIf|workspaceAccessChanged)\(ctx\b`)

// readNothing lists the driver functions that write those tables without
// changing anything a decision reads, so they leave the revisions alone.
var readNothing = map[string]string{
	"UpdateDirectoryUser":  "a user's name, email and sync hash",
	"SetDirectoryPassword": "a user's password",
	"InsertDirectoryRole":  "a role nobody holds yet",
}

// Scope decisions are kept between queries under the access revisions, so a
// write the revisions do not follow would leave readers with permissions
// they no longer have. Every driver function with such a write replaces a
// revision, unless it is listed in readNothing.
func TestDriverAccessWritesReplaceTheRevision(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		files, err := filepath.Glob(filepath.Join("driver", engine, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatal(engine, files, err)
		}
		found, exempt := 0, map[string]bool{}
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
				changes := revisionChange.MatchString(body)
				if _, ok := readNothing[fn.Name.Name]; ok {
					exempt[fn.Name.Name] = true
					if changes {
						t.Errorf("%s: %s is listed as changing nothing decisions read, yet replaces a revision", path, fn.Name.Name)
					}
					continue
				}
				if !changes {
					t.Errorf("%s: %s writes what permission decisions read without replacing an access revision", path, fn.Name.Name)
				}
			}
		}
		for name := range readNothing {
			if !exempt[name] {
				t.Errorf("%s: %s is listed in readNothing but writes none of the tables decisions read", engine, name)
			}
		}
		if found < 15 {
			t.Errorf("%s: found only %d access writes; the pattern no longer matches the driver", engine, found)
		}
	}
}
