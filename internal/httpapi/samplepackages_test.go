package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"papergo/ent/resource"
	"papergo/ent/termset"
	"papergo/internal/auth"
	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/model"
	"papergo/internal/storage"
	"papergo/internal/testutil"
	"papergo/internal/workflow"
	"papergo/internal/workflowai"
)

var updateSamplePackages = flag.Bool("update-sample-packages", false, "rebuild the checked-in workspace sample ZIPs from their definitions")

const sampleSubject = "sample-author"

func sampleOperations(t *testing.T, s *dms.Service, store storage.Store) {
	t.Helper()
	if err := s.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.RunOperations(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	t.Cleanup(func() { cancel(); <-done })
}

func awaitSampleOperation(t *testing.T, s *dms.Service, subject, id string) dms.PackageResult {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		op, err := s.Operation(t.Context(), subject, id)
		if err != nil {
			t.Fatal(err)
		}
		if op.Status == "succeeded" {
			var result dms.PackageResult
			if err := json.Unmarshal(op.Result, &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
		if op.Status != "pending" {
			t.Fatalf("sample package: %s: %s", op.Status, op.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("sample operation timed out")
	return dms.PackageResult{}
}

// Exercises exactly the upload used by the browser's Export and import page.
func importSampleHTTP(t *testing.T, handler http.Handler, s *dms.Service, subject, path, name string, dry bool) dms.PackageResult {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	url := "/v1/portability/imports?dry_run=false"
	if dry {
		url = "/v1/portability/imports?dry_run=true"
	}
	if name != "" {
		url += "&workspace_name=" + name
	}
	rec := request(handler, "POST", url, string(raw), testToken, "", "application/zip")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import sample: %d %s", rec.Code, rec.Body.String())
	}
	var op struct {
		ID string `json:"id"`
	}
	decodeJSON(t, rec, &op)
	result := awaitSampleOperation(t, s, subject, op.ID)
	if !result.Valid || result.DryRun != dry {
		t.Fatalf("sample import: %+v", result)
	}
	return result
}

func sampleWorkflowSource(t *testing.T, file string, bindings ...string) workflow.Save {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../samples", file))
	if err != nil {
		t.Fatal(err)
	}
	var save workflow.Save
	if err := json.Unmarshal([]byte(strings.NewReplacer(bindings...).Replace(string(raw))), &save); err != nil {
		t.Fatal(err)
	}
	return save
}

func sampleWorkflow(t *testing.T, s *workflow.Service, workspace, file string, bindings ...string) workflow.Workflow {
	t.Helper()
	save := sampleWorkflowSource(t, file, bindings...)
	w, err := s.Create(t.Context(), "organization-admin", workspace, save)
	if err != nil {
		t.Fatal(file, err)
	}
	return w
}

func checkImportedSampleSource(t *testing.T, s *dms.Service, w workflow.Workflow) {
	t.Helper()
	collections, err := s.Client.Resource.Query().Where(resource.WorkspaceIDEQ(w.WorkspaceID), resource.KindIn(resource.KindLibrary, resource.KindList)).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, collection := range collections {
		ids[collection.Name] = collection.ID
	}
	var source workflow.Save
	switch w.Name {
	case "Read receipts":
		types, err := s.ContentTypes(t.Context(), "organization-admin", ids["Receipt lines"])
		if err != nil || len(types) != 1 {
			t.Fatal(types, err)
		}
		source = sampleWorkflowSource(t, "receipts-package/workflow.json", "$LIBRARY", ids["Receipts"], "$LINES", ids["Receipt lines"], "$LINE_TYPE", types[0].ID)
		for _, name := range []string{"Receipts", "Receipt lines"} {
			views, err := s.Views(t.Context(), "organization-admin", ids[name], "", 100)
			if err != nil {
				t.Fatal(err)
			}
			defaults := 0
			for _, view := range views.Data {
				if view.IsDefault {
					defaults++
				}
			}
			if defaults != 1 {
				t.Fatal("missing default sample view", name, views)
			}
		}
	case "Index receipts":
		source = sampleWorkflowSource(t, "workflows/receipts.json", "$LIBRARY", ids["Receipt summaries"])
	case "Index contracts":
		source = sampleWorkflowSource(t, "workflows/contracts.json", "$LIBRARY", ids["Contracts"])
	case "Taxonomy review":
		set, err := s.Client.TermSet.Query().Where(termset.KeyEQ("sample_document_topics")).Only(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		source = sampleWorkflowSource(t, "workflows/taxonomy-review.json", "$LIBRARY", ids["Documents to classify"], "$TERM_SET", set.ID, "$FIELD", "topics")
	case "Decision routing":
		source = sampleWorkflowSource(t, "workflows/decision-routing.json")
	case "AI batch coordinator":
		if w.BuiltInKey == nil || *w.BuiltInKey != "ai.batch" || !w.Enabled || w.Parameters["queue"] != "shared" || w.Parameters["policy"] != "both" {
			t.Fatal("sample lost its enabled batch coordinator", w)
		}
		return
	default:
		t.Fatal("unexpected sample workflow", w.Name)
	}
	expected, err := workflow.ParseDefinition(source.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustJSON(t, expected), mustJSON(t, w.Definition)) || source.Description != w.Description {
		t.Fatal("sample ZIP differs from its workflow source; rebuild the sample packages", w.Name)
	}
	if source.CollectionID != "" && (w.CollectionID == nil || *w.CollectionID != source.CollectionID || w.Role == nil || *w.Role != source.Role) {
		t.Fatal("sample lost its library search role", w.Name)
	}
}

// The sources remain ordinary action definitions. Only maintainers rebuilding
// artifacts use this helper; installing either sample needs just the ZIP.
func buildSamplePackage(t *testing.T, receipt bool, path string) {
	t.Helper()
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, migration := range testutil.MigrationFiles(t) {
		if _, err := db.SQL.ExecContext(t.Context(), string(migration.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	s := dms.NewService(db.SQL)
	if err := s.Directory().Initialize(t.Context(), "organization-admin", ""); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sampleOperations(t, s, store)
	wf := &workflow.Service{DMS: s}
	create := func(parent string, in dms.CreateResource) string {
		r, err := s.Create(t.Context(), "organization-admin", parent, in)
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	var workspace string
	if receipt {
		var spec struct {
			Workspace dms.CreateResource `json:"workspace"`
			Fields    []dms.CreateField  `json:"fields"`
			Types     []struct {
				Key, Name string
				Fields    []string
			} `json:"content_types"`
			Collections []struct {
				Name, Kind string
				WebDAV     bool   `json:"webdav_enabled"`
				Type       string `json:"content_type"`
				Views      []struct {
					dms.ViewInput
					Default bool `json:"is_default"`
				} `json:"views"`
			} `json:"collections"`
			Relationship dms.RelationshipTypeInput `json:"relationship"`
		}
		raw, err := os.ReadFile("../../samples/receipts-package/resources.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		fields, types, collections := map[string]string{}, map[string]string{}, map[string]string{}
		for _, in := range spec.Fields {
			f, err := s.CreateCatalogField(t.Context(), "organization-admin", in)
			if err != nil {
				t.Fatal(err)
			}
			fields[in.Key] = f.ID
		}
		for _, in := range spec.Types {
			ids := []string{}
			for _, key := range in.Fields {
				ids = append(ids, fields[key])
			}
			ct, err := s.CreateContentType(t.Context(), "organization-admin", dms.ContentTypeInput{Key: in.Key, Name: in.Name, FieldIds: ids})
			if err != nil {
				t.Fatal(err)
			}
			types[in.Key] = ct.ID
		}
		if _, err := s.CreateRelationshipType(t.Context(), "organization-admin", spec.Relationship); err != nil {
			t.Fatal(err)
		}
		spec.Workspace.Kind = "workspace"
		workspace = create("", spec.Workspace)
		for _, in := range spec.Collections {
			id := create(workspace, dms.CreateResource{Kind: in.Kind, Name: in.Name, ContentTypeIDs: []string{types[in.Type]}, WebDAVEnabled: in.WebDAV})
			collections[in.Name] = id
			for _, view := range in.Views {
				input := view.ViewInput
				input.IsDefault = view.Default
				raw, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				raw = []byte(strings.NewReplacer("$receipt_line", types["receipt_line"], "$receipt", types["receipt"]).Replace(string(raw)))
				if err := json.Unmarshal(raw, &input); err != nil {
					t.Fatal(err)
				}
				if _, err := s.CreateView(t.Context(), "organization-admin", id, input); err != nil {
					t.Fatal(err)
				}
			}
		}
		sampleWorkflow(t, wf, workspace, "receipts-package/workflow.json", "$LIBRARY", collections["Receipts"], "$LINES", collections["Receipt lines"], "$LINE_TYPE", types["receipt_line"])
		if err := workflowai.EnsureCoordinator(t.Context(), wf, "organization-admin", workspace); err != nil {
			t.Fatal(err)
		}
	} else {
		workspace = create("", dms.CreateResource{Kind: "workspace", Name: "Workflow samples", Description: "Receipt summaries, contract indexing, local routing and taxonomy review."})
		receipts := create(workspace, dms.CreateResource{Kind: "library", Name: "Receipt summaries", WebDAVEnabled: true})
		contracts := create(workspace, dms.CreateResource{Kind: "library", Name: "Contracts", WebDAVEnabled: true})
		documents := create(workspace, dms.CreateResource{Kind: "library", Name: "Documents to classify", WebDAVEnabled: true})
		group, err := s.CreateTermGroup(t.Context(), "organization-admin", dms.TermGroupInput{Name: "Sample document topics"})
		if err != nil {
			t.Fatal(err)
		}
		set, err := s.CreateTermSet(t.Context(), "organization-admin", dms.TermSetInput{GroupID: group.ID, Key: "sample_document_topics", Name: "Document topics"})
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range []dms.TermInput{{Name: "Invoices", Description: "Invoices, bills, amounts due and payment requests"}, {Name: "Contracts", Description: "Agreements, obligations and contractual clauses"}, {Name: "Correspondence", Description: "Letters, messages and general correspondence"}} {
			if _, err := s.CreateTerm(t.Context(), "organization-admin", set.ID, in); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.CreateField(t.Context(), "organization-admin", documents, dms.CreateField{Key: "topics", Label: "Topics", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID, Multiple: true}}); err != nil {
			t.Fatal(err)
		}
		sampleWorkflow(t, wf, workspace, "workflows/receipts.json", "$LIBRARY", receipts)
		sampleWorkflow(t, wf, workspace, "workflows/contracts.json", "$LIBRARY", contracts)
		sampleWorkflow(t, wf, workspace, "workflows/taxonomy-review.json", "$LIBRARY", documents, "$TERM_SET", set.ID, "$FIELD", "topics")
		sampleWorkflow(t, wf, workspace, "workflows/decision-routing.json")
	}
	include := false
	op, err := s.StartExport(t.Context(), "organization-admin", dms.ExportOptions{WorkspaceID: workspace, IncludeData: &include})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitSampleOperation(t, s, "organization-admin", op.ID)
	f, err := store.Open(t.Context(), result.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	dec := json.NewDecoder(reader)
	dec.UseNumber()
	err = dec.Decode(&manifest)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	manifest["template_subject"] = sampleSubject
	// Use a marker account in the artifact, never a real deployment identity.
	tables := manifest["tables"].(map[string]any)
	for name, value := range tables {
		table := value.(map[string]any)
		cols := table["columns"].([]any)
		for _, value := range table["rows"].([]any) {
			row := value.([]any)
			for i, col := range cols {
				if (col == "created_by" || col == "updated_by" || col == "modified_by" || col == "owner_id" || col == "principal_id" || (name == "identity_users" && col == "id")) && row[i] == "organization-admin" {
					row[i] = sampleSubject
				}
			}
		}
	}
	raw, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	z := zip.NewWriter(&output)
	entry, err := z.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, output.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSampleWorkspacePackages(t *testing.T) {
	for _, sample := range []struct {
		Path, Name string
		Receipt    bool
		Workflows  int
	}{{"receipts-package/receipts.zip", "Receipts desk", true, 2}, {"workflows/workflows.zip", "Workflow samples", false, 4}} {
		t.Run(sample.Name, func(t *testing.T) {
			path := filepath.Join("../../samples", sample.Path)
			if *updateSamplePackages {
				buildSamplePackage(t, sample.Receipt, path)
			}
			s := dms.NewService(testutil.Database(t).SQL)
			store, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			sampleOperations(t, s, store)
			existing, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Unrelated workspace"})
			if err != nil {
				t.Fatal(err)
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler := (&API{DMS: s, Storage: store, Auth: auth.Development{Token: testToken, Subject: "organization-admin"}, Logger: logger}).Handler()
			before, _ := s.Client.Resource.Query().Count(t.Context())
			importSampleHTTP(t, handler, s, "organization-admin", path, "", true)
			after, _ := s.Client.Resource.Query().Count(t.Context())
			if after != before {
				t.Fatal("dry run wrote resources")
			}
			for _, name := range []string{"", "Another-copy"} {
				result := importSampleHTTP(t, handler, s, "organization-admin", path, name, false)
				if name == "" && result.WorkspaceName != sample.Name {
					t.Fatal(result)
				}
				workflows, err := (&workflow.Service{DMS: s}).List(t.Context(), "organization-admin", result.WorkspaceID)
				if err != nil || len(workflows) != sample.Workflows {
					t.Fatal(workflows, err)
				}
				for _, w := range workflows {
					checkImportedSampleSource(t, s, w)
					if w.CreatedBy != "organization-admin" {
						t.Fatal("template retained source author", w)
					}
					var got workflow.Workflow
					if w.BuiltInKey != nil {
						got, err = (&workflow.Service{DMS: s}).UpdateBuiltIn(t.Context(), "organization-admin", w.WorkspaceID, *w.BuiltInKey, workflow.SetBuiltIn{Enabled: true, Version: w.Version, Parameters: w.Parameters})
					} else {
						got, err = (&workflow.Service{DMS: s}).Update(t.Context(), "organization-admin", w.ID, w.Version, workflow.Save{Name: w.Name, Description: w.Description, Definition: mustJSON(t, w.Definition)})
					}
					if err != nil || got.ID != w.ID {
						t.Fatal("imported workflow cannot be saved", w.Name, err)
					}
					raw := string(mustJSON(t, w.Definition))
					for _, placeholder := range []string{"$LIBRARY", "$LINES", "$LINE_TYPE", "$TERM_SET", "$FIELD", sampleSubject} {
						if strings.Contains(raw, placeholder) {
							t.Fatal("unbound sample", placeholder)
						}
					}
				}
				if count, err := s.Client.Resource.Query().Where(resource.WorkspaceIDEQ(result.WorkspaceID), resource.KindIn(resource.KindItem, resource.KindFolder)).Count(t.Context()); err != nil || count != 0 {
					t.Fatal("sample included content", count, err)
				}
			}
			if _, err := s.Get(t.Context(), "alice", existing.ID); err != nil {
				t.Fatal("import changed other workspace", err)
			}
			if present, err := s.Client.IdentityUser.Get(t.Context(), sampleSubject); err == nil {
				t.Fatal("template created dummy account", present)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
