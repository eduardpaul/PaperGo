package dms

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/workflows"
	"papergo/internal/storage"
	"strings"
	"testing"
)

func sampleTemplateForTest(t *testing.T) corePackage {
	t.Helper()
	raw, err := os.ReadFile("../../samples/receipts-package/receipts.zip")
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var p corePackage
	d := json.NewDecoder(f)
	d.UseNumber()
	if err = d.Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func storeTemplateForTest(t *testing.T, store storage.Store, p corePackage) storage.Object {
	t.Helper()
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	f, err := z.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(f).Encode(p); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	obj, err := store.Put(t.Context(), &archive, PackageLimit)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestWorkspaceTemplateBindsOnlyIdentityPositions(t *testing.T) {
	s := emptyPackageTarget(t)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := sampleTemplateForTest(t)
	source := p.TemplateSubject
	// Marker-like ordinary text is data, even when it equals the source subject.
	table := p.Tables["resources"]
	for _, row := range table.Rows {
		for i, col := range table.Columns {
			if col == "description" {
				row[i] = source
			}
		}
	}
	p.Tables["resources"] = table
	obj := storeTemplateForTest(t, store, p)
	before, _ := s.DAL.Directory().User(t.Context(), "organization-admin")
	resourceCount, _ := countResources(s, resources.Filter{})
	users := userCount(t, s)
	result, err := importWorkspaceForTest(t, s, store, obj, true, "")
	if err != nil || !result.Valid {
		t.Fatal(result, err)
	}
	if count, _ := countResources(s, resources.Filter{}); count != resourceCount {
		t.Fatal("dry run wrote resources")
	}
	if count := userCount(t, s); count != users {
		t.Fatal("dry run wrote users")
	}
	result, err = importWorkspaceForTest(t, s, store, obj, false, "")
	if err != nil || !result.Valid {
		t.Fatal(result, err)
	}
	if count := userCount(t, s); count != users {
		t.Fatal("template created users")
	}
	after, _ := s.DAL.Directory().User(t.Context(), "organization-admin")
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if !bytes.Equal(a, b) {
		t.Fatal("import changed account", string(a), string(b))
	}
	ws, err := s.Get(t.Context(), "organization-admin", result.WorkspaceID)
	if err != nil || ws.CreatedBy != "organization-admin" || ws.Description != source {
		t.Fatal(ws, err)
	}
	flows, err := s.DAL.Workflows().List(t.Context(), workflows.Filter{WorkspaceID: result.WorkspaceID, Name: "Read receipts"})
	if err != nil || len(flows) != 1 || flows[0].CreatedBy != "organization-admin" || flows[0].UpdatedBy != "organization-admin" {
		t.Fatal(flows, err)
	}
	versions, err := s.DAL.Workflows().Versions(t.Context(), flows[0].ID)
	if err != nil || len(versions) != 1 || versions[0].CreatedBy != "organization-admin" {
		t.Fatal(versions, err)
	}
	if _, err := s.Authorize(t.Context(), "organization-admin", result.WorkspaceID, "manage"); err != nil {
		t.Fatal(err)
	}
	// An ordinary export describes the installed copy; it is no longer a template.
	include := false
	_, exported := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: result.WorkspaceID, IncludeData: &include})
	if exported.TemplateSubject != "" {
		t.Fatal("export retained template binding")
	}
}

func TestWorkspaceTemplateRejectsUnboundIdentitiesAndInvalidScopes(t *testing.T) {
	for _, test := range []struct {
		Name   string
		Mutate func(*corePackage)
	}{
		{"organization", func(p *corePackage) { p.Scope = "organization"; p.WorkspaceID = "" }},
		{"with content", func(p *corePackage) { p.IncludeData = true }},
		{"missing source", func(p *corePackage) { p.TemplateSubject = "missing" }},
		{"another author", func(p *corePackage) {
			table := p.Tables["workflow_versions"]
			for _, row := range table.Rows {
				for i, col := range table.Columns {
					if col == "created_by" {
						row[i] = "organization-admin"
					}
				}
			}
			p.Tables["workflow_versions"] = table
		}},
		{"additional account", func(p *corePackage) {
			table := p.Tables["identity_users"]
			row := append([]any(nil), table.Rows[0]...)
			for i, col := range table.Columns {
				if col == "id" {
					row[i] = "another-user"
				}
			}
			table.Rows = append(table.Rows, row)
			p.Tables["identity_users"] = table
		}},
	} {
		t.Run(test.Name, func(t *testing.T) {
			s := emptyPackageTarget(t)
			store, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			p := sampleTemplateForTest(t)
			test.Mutate(&p)
			obj := storeTemplateForTest(t, store, p)
			if result, err := importWorkspaceForTest(t, s, store, obj, false, ""); err == nil || result.Valid {
				t.Fatal("accepted invalid template", result, err)
			}
			if n, _ := countResources(s, resources.Filter{}); n != 0 {
				t.Fatal("invalid template left resources", n)
			}
		})
	}
}

// Keep imports above checked when the ZIP is regenerated: the manifest stays a
// single ordinary package entry, with no executable installer or staged files.
func TestSampleTemplateArchiveHasOnlyManifest(t *testing.T) {
	for _, path := range []string{"../../samples/receipts-package/receipts.zip", "../../samples/workflows/workflows.zip"} {
		z, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(z.File) != 1 || z.File[0].Name != "manifest.json" {
			t.Fatal("unexpected sample entries", path)
		}
		f, err := z.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(f)
		f.Close()
		z.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, placeholder := range []string{"$LIBRARY", "$LINES", "$LINE_TYPE", "$TERM_SET", "$FIELD"} {
			if strings.Contains(string(raw), placeholder) {
				t.Fatal("unresolved placeholder", path, placeholder)
			}
		}
	}
}

// userCount counts the enrolled users.
func userCount(t *testing.T, s *Service) int {
	t.Helper()
	users, err := s.DAL.Directory().Users(t.Context(), directory.UserFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return len(users)
}
