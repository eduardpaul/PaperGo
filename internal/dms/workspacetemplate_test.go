package dms

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"papergo/ent/workflow"
	"papergo/ent/workflowversion"
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
	before, _ := s.Client.IdentityUser.Get(t.Context(), "organization-admin")
	resources, _ := s.Client.Resource.Query().Count(t.Context())
	users, _ := s.Client.IdentityUser.Query().Count(t.Context())
	result, err := importWorkspaceForTest(t, s, store, obj, true, "")
	if err != nil || !result.Valid {
		t.Fatal(result, err)
	}
	if count, _ := s.Client.Resource.Query().Count(t.Context()); count != resources {
		t.Fatal("dry run wrote resources")
	}
	if count, _ := s.Client.IdentityUser.Query().Count(t.Context()); count != users {
		t.Fatal("dry run wrote users")
	}
	result, err = importWorkspaceForTest(t, s, store, obj, false, "")
	if err != nil || !result.Valid {
		t.Fatal(result, err)
	}
	if count, _ := s.Client.IdentityUser.Query().Count(t.Context()); count != users {
		t.Fatal("template created users")
	}
	after, _ := s.Client.IdentityUser.Get(t.Context(), "organization-admin")
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if !bytes.Equal(a, b) {
		t.Fatal("import changed account", string(a), string(b))
	}
	ws, err := s.Get(t.Context(), "organization-admin", result.WorkspaceID)
	if err != nil || ws.CreatedBy != "organization-admin" || ws.Description != source {
		t.Fatal(ws, err)
	}
	f, err := s.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(result.WorkspaceID), workflow.NameEQ("Read receipts")).Only(t.Context())
	if err != nil || f.CreatedBy != "organization-admin" || f.UpdatedBy != "organization-admin" {
		t.Fatal(f, err)
	}
	version, err := s.Client.WorkflowVersion.Query().Where(workflowversion.WorkflowIDEQ(f.ID)).Only(t.Context())
	if err != nil || version.CreatedBy != "organization-admin" {
		t.Fatal(version, err)
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
			if n, _ := s.Client.Resource.Query().Count(t.Context()); n != 0 {
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
