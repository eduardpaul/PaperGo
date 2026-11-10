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
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"papergo/internal/auth"
	appstore "papergo/internal/dal/apps"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/database"
	"papergo/internal/dms"
	"papergo/internal/model"
	"papergo/internal/storage"
	"papergo/internal/worker"
	"papergo/internal/workflow"
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
	go func() {
		defer close(done)
		_ = worker.Single(s, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx)
	}()
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
	if err := json.Unmarshal(rec.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	result := awaitSampleOperation(t, s, subject, op.ID)
	if !result.Valid || result.DryRun != dry {
		t.Fatalf("sample import: %+v", result)
	}
	return result
}

// readSample reads a sample's settings file into settings, with the
// placeholders of its parameters bound, and returns the source file that
// script names, beside it.
func readSample(t *testing.T, file string, settings any, script func() string, bindings ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../samples", file))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.NewReplacer(bindings...).Replace(string(raw))), settings); err != nil {
		t.Fatal(file, err)
	}
	source, err := os.ReadFile(filepath.Join("../../samples", filepath.Dir(file), script()))
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

// sampleWorkflowSource reads a sample workflow: its settings, with the
// placeholders of its parameters bound, and the script they name.
func sampleWorkflowSource(t *testing.T, file string, bindings ...string) workflow.Save {
	t.Helper()
	var sample struct {
		workflow.Save
		Script string `json:"script"`
	}
	sample.Save.Source = readSample(t, file, &sample, func() string { return sample.Script }, bindings...)
	return sample.Save
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
	collections, err := s.DAL.Resources().Resources(t.Context(), resources.Filter{WorkspaceID: w.WorkspaceID, Kinds: []refs.ResourceKind{refs.KindLibrary, refs.KindList}})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, collection := range collections {
		ids[collection.Name] = collection.ID
	}
	var source workflow.Save
	switch w.Name {
	case "Read receipts", "Sync receipt lines":
		// One library holds the receipts and their lines, receipts by default.
		listTypes, err := s.CollectionContentTypes(t.Context(), "organization-admin", ids["Receipts"])
		if err != nil || len(listTypes) != 2 {
			t.Fatal(listTypes, err)
		}
		types := map[string]*catalog.ContentType{}
		for _, lt := range listTypes {
			types[lt.Key] = lt
		}
		if types["receipts_receipt"] == nil || !types["receipts_receipt"].IsDefault || types["receipts_line"] == nil {
			t.Fatal("receipts library types", listTypes)
		}
		// The workflow names the catalog type; the library accepts it.
		file := map[string]string{"Read receipts": "receipts/workflows/receipts-read.json", "Sync receipt lines": "receipts/workflows/receipts-sync-lines.json"}[w.Name]
		source = sampleWorkflowSource(t, file, receiptsBindings(ids["Receipts"], *types["receipts_receipt"].ParentID, *types["receipts_line"].ParentID)...)
		views, err := s.Views(t.Context(), "organization-admin", ids["Receipts"], "", 100)
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
			t.Fatal("missing default sample view", views)
		}
	case "Diesel levels", "Diesel measuring reminder", "Diesel refill", "Load diesel sample data":
		file := map[string]string{"Diesel levels": "diesel/workflows/diesel-levels.json", "Diesel measuring reminder": "diesel/workflows/diesel-measure-reminder.json", "Diesel refill": "diesel/workflows/diesel-refill.json", "Load diesel sample data": "diesel/workflows/diesel-sample-data.json"}[w.Name]
		source = sampleWorkflowSource(t, file, dieselBindings(ids["Diesel"], dieselTypes(t, s, ids["Diesel"]))...)
	case "Index receipts":
		source = sampleWorkflowSource(t, "workflows/index-receipts.json", "$LIBRARY", ids["Receipt summaries"])
	case "Index contracts":
		source = sampleWorkflowSource(t, "workflows/index-contracts.json", "$LIBRARY", ids["Contracts"])
	case "Taxonomy review":
		set, err := s.DAL.Taxonomy().SetByKey(t.Context(), "sample_document_topics")
		if err != nil {
			t.Fatal(err)
		}
		source = sampleWorkflowSource(t, "workflows/taxonomy-review.json", "$LIBRARY", ids["Documents to classify"], "$TERM_SET", set.ID, "$FIELD", "topics")
	case "Decision routing":
		source = sampleWorkflowSource(t, "workflows/decision-routing.json")
	default:
		t.Fatal("unexpected sample workflow", w.Name)
	}
	if source.Source != w.Source || !bytes.Equal(mustJSON(t, source.Parameters), mustJSON(t, w.Parameters)) || source.Description != w.Description {
		t.Fatal("sample ZIP differs from its workflow source; rebuild the sample packages", w.Name)
	}
	if source.CollectionID != "" && (w.CollectionID == nil || *w.CollectionID != source.CollectionID || w.Role == nil || *w.Role != source.Role) {
		t.Fatal("sample lost its library search role", w.Name)
	}
}

// SampleApp is a sample's app: its settings file, with its source read and
// the placeholders of its parameters bound.
type SampleApp struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Source      string         `json:"source"`
	Parameters  map[string]any `json:"parameters"`
	State       appstore.State `json:"state"`
}

// SaveSampleApp creates a sample's app in a workspace as
// organization-admin, or with an id replaces that app at version, through
// the apps service, which compiles it and checks its parameters. It returns
// the parameters the service keeps, defaults filled in. The apps module
// imports this package, so its test in package httpapi_test sets it.
var SaveSampleApp func(t *testing.T, s *dms.Service, workspace, id string, version int, app SampleApp) map[string]any

// sampleAppSource reads a sample app's settings and the source they name.
func sampleAppSource(t *testing.T, file string, bindings ...string) SampleApp {
	t.Helper()
	var sample SampleApp
	sample.Source = readSample(t, file, &sample, func() string { return sample.Source }, bindings...)
	return sample
}

// dieselTypes returns the catalog types the Diesel list accepts, by key.
func dieselTypes(t *testing.T, s *dms.Service, list string) map[string]string {
	t.Helper()
	listTypes, err := s.CollectionContentTypes(t.Context(), "organization-admin", list)
	if err != nil || len(listTypes) != 4 {
		t.Fatal("the Diesel list's types", listTypes, err)
	}
	types := map[string]string{}
	for _, lt := range listTypes {
		if lt.ParentID == nil {
			t.Fatal("the Diesel list's type has no catalog type", lt.Key)
		}
		types[lt.Key] = *lt.ParentID
	}
	return types
}

// dieselAppBindings binds the diesel app's parameters to an imported copy.
func dieselAppBindings(t *testing.T, s *dms.Service, workspace string) []string {
	t.Helper()
	diesel, err := s.DAL.Resources().One(t.Context(), resources.Filter{WorkspaceID: workspace, Name: "Diesel", Kinds: []refs.ResourceKind{refs.KindList}})
	if err != nil {
		t.Fatal(err)
	}
	return dieselBindings(diesel.ID, dieselTypes(t, s, diesel.ID))
}

// receiptsAppBindings binds the receipts desk's parameters to an imported copy.
func receiptsAppBindings(t *testing.T, s *dms.Service, workspace string) []string {
	t.Helper()
	library, err := s.DAL.Resources().One(t.Context(), resources.Filter{WorkspaceID: workspace, Name: "Receipts", Kinds: []refs.ResourceKind{refs.KindLibrary}})
	if err != nil {
		t.Fatal(err)
	}
	listTypes, err := s.CollectionContentTypes(t.Context(), "organization-admin", library.ID)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for _, lt := range listTypes {
		types[lt.Key] = *lt.ParentID
	}
	return receiptsBindings(library.ID, types["receipts_receipt"], types["receipts_line"])
}

// checkImportedSampleApps checks that an imported sample carries its apps
// as their settings and sources say, with exactly their parameters bound to
// the copy, and code that saving the same source again keeps.
func checkImportedSampleApps(t *testing.T, s *dms.Service, workspace string, files []string, appBindings func(*testing.T, *dms.Service, string) []string) {
	t.Helper()
	apps, err := s.DAL.Apps().List(t.Context(), appstore.Filter{WorkspaceIDs: []string{workspace}})
	if err != nil || len(apps) != len(files) {
		t.Fatal("sample apps", apps, err)
	}
	if len(files) == 0 {
		return
	}
	bindings := appBindings(t, s, workspace)
	byKey := map[string]appstore.App{}
	for _, a := range apps {
		byKey[a.Key] = a
	}
	for _, file := range files {
		want := sampleAppSource(t, file, bindings...)
		a, ok := byKey[want.Key]
		if !ok {
			t.Fatal("sample app missing; rebuild the sample packages", want.Key)
		}
		version, err := s.DAL.Apps().Version(t.Context(), a.ID, int64(a.CurrentVersion))
		if err != nil {
			t.Fatal(err)
		}
		if a.Name != want.Name || a.Description != want.Description || a.State != want.State || version.Source != want.Source {
			t.Fatal("sample ZIP differs from its app source; rebuild the sample packages", a.Key)
		}
		if a.CreatedBy != "organization-admin" || a.UpdatedBy != "organization-admin" || version.CreatedBy != "organization-admin" {
			t.Fatal("template retained the app's source author", a, version.CreatedBy)
		}
		// Saving the sample's own settings as the importer keeps the app as
		// imported: the same parameters, none more, and the same code.
		saved := SaveSampleApp(t, s, workspace, a.ID, a.Version, want)
		if !bytes.Equal(mustJSON(t, saved), mustJSON(t, a.Parameters)) {
			t.Fatal("imported app parameters differ from the sample's", string(mustJSON(t, a.Parameters)), string(mustJSON(t, saved)))
		}
		again, err := s.DAL.Apps().Find(t.Context(), a.ID)
		if err != nil || again.CurrentVersion != a.CurrentVersion {
			t.Fatal("imported app code is not its source's", again.CurrentVersion, err)
		}
	}
}

// receiptsApps are the receipts sample's apps, by their settings files.
var receiptsApps = []string{"receipts/apps/receipts-desk.json"}

// receiptsBindings bind the references of the receipts sample's parameters
// to a library and the catalog types of receipts and their lines.
func receiptsBindings(library, receipt, line string) []string {
	return []string{"$collection:receipts", library, "$type:receipts_receipt", receipt, "$type:receipts_line", line}
}

// dieselApps are the diesel sample's apps, by their settings files.
var dieselApps = []string{"diesel/apps/diesel-dashboard.json"}

// dieselWorkflows are the diesel sample's workflows, by their settings files.
var dieselWorkflows = []string{"diesel/workflows/diesel-levels.json", "diesel/workflows/diesel-measure-reminder.json", "diesel/workflows/diesel-refill.json", "diesel/workflows/diesel-sample-data.json"}

// dieselBindings bind the placeholders of the diesel workflows' parameters
// to its list and catalog types.
func dieselBindings(list string, types map[string]string) []string {
	// A reference stands for an ID; a setting for its default, as JSON.
	return []string{"$collection:tank", list, "$type:diesel_reading", types["diesel_reading"], "$type:diesel_purchase", types["diesel_purchase"], "$type:diesel_heating", types["diesel_heating"], "$type:diesel_temperature", types["diesel_temperature"],
		`"$setting:capacity_l"`, "750", `"$setting:time_zone"`, `"UTC"`}
}

// buildSampleSpec creates a sample's catalog fields and types, workspace,
// collections and views from its resources.json, and returns the workspace
// and the IDs of its collections by name and of its types by key. A view's
// query names a type as $key.
func buildSampleSpec(t *testing.T, s *dms.Service, file string) (string, map[string]string, map[string]string) {
	t.Helper()
	var spec struct {
		Name, Description string
		Workspace         dms.CreateResource `json:"-"`
		Fields            []dms.CreateField  `json:"fields"`
		Types             []struct {
			Key, Name string
			Fields    []string
		} `json:"content_types"`
		Collections []struct {
			Name, Kind string
			WebDAV     bool     `json:"webdav_enabled"`
			Types      []string `json:"content_types"`
			Views      []struct {
				dms.ViewInput
				Default bool `json:"is_default"`
			} `json:"views"`
		} `json:"collections"`
		Relationships []dms.RelationshipTypeInput `json:"relationship_types"`
	}
	raw, err := os.ReadFile(file)
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
			t.Fatal(in.Key, err)
		}
		fields[in.Key] = f.ID
	}
	// Longer keys first, so $receipt_line is not read as $receipt.
	var replace []string
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
	keys := make([]string, 0, len(types))
	for key := range types {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int { return len(b) - len(a) })
	for _, key := range keys {
		replace = append(replace, "$type:"+key, types[key])
	}
	for _, relationship := range spec.Relationships {
		if _, err := s.CreateRelationshipType(t.Context(), "organization-admin", relationship); err != nil {
			t.Fatal(err)
		}
	}
	spec.Workspace = dms.CreateResource{Kind: "workspace", Name: spec.Name, Description: spec.Description}
	ws, err := s.Create(t.Context(), "organization-admin", "", spec.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range spec.Collections {
		accepted := []string{}
		for _, key := range in.Types {
			accepted = append(accepted, types[key])
		}
		c, err := s.Create(t.Context(), "organization-admin", ws.ID, dms.CreateResource{Kind: in.Kind, Name: in.Name, ContentTypeIDs: accepted, WebDAVEnabled: in.WebDAV})
		if err != nil {
			t.Fatal(err)
		}
		collections[in.Name] = c.ID
		for _, view := range in.Views {
			input := view.ViewInput
			input.IsDefault = view.Default
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(strings.NewReplacer(replace...).Replace(string(raw)))
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateView(t.Context(), "organization-admin", c.ID, input); err != nil {
				t.Fatal(view.Name, err)
			}
		}
	}
	return ws.ID, collections, types
}

// The sources remain ordinary action definitions. Only maintainers rebuilding
// artifacts use this helper; installing either sample needs just the ZIP.
func buildSamplePackage(t *testing.T, kind, path string) {
	t.Helper()
	// Sample workflows fill this extension role; generating a package needs no search store.
	workflow.RegisterRole(&workflow.Role{Key: "search.content", Name: "File content in search"})
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.DAL.Migrate(t.Context()); err != nil {
		t.Fatal(err)
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
	switch kind {
	case "receipts":
		var collections map[string]string
		var types map[string]string
		workspace, collections, types = buildSampleSpec(t, s, "../../samples/receipts/solution.json")
		bindings := receiptsBindings(collections["Receipts"], types["receipts_receipt"], types["receipts_line"])
		sampleWorkflow(t, wf, workspace, "receipts/workflows/receipts-read.json", bindings...)
		sampleWorkflow(t, wf, workspace, "receipts/workflows/receipts-sync-lines.json", bindings...)
		for _, file := range receiptsApps {
			SaveSampleApp(t, s, workspace, "", 0, sampleAppSource(t, file, bindings...))
		}
	case "diesel":
		var collections map[string]string
		var types map[string]string
		workspace, collections, types = buildSampleSpec(t, s, "../../samples/diesel/solution.json")
		for _, file := range dieselWorkflows {
			sampleWorkflow(t, wf, workspace, file, dieselBindings(collections["Diesel"], types)...)
		}
		for _, file := range dieselApps {
			SaveSampleApp(t, s, workspace, "", 0, sampleAppSource(t, file, dieselBindings(collections["Diesel"], types)...))
		}
	default:
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
		sampleWorkflow(t, wf, workspace, "workflows/index-receipts.json", "$LIBRARY", receipts)
		sampleWorkflow(t, wf, workspace, "workflows/index-contracts.json", "$LIBRARY", contracts)
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
		Path, Name, Kind string
		Workflows        int
		Apps             []string
		AppBindings      func(*testing.T, *dms.Service, string) []string
	}{{"../dms/testdata/sample-receipts.zip", "Receipts desk", "receipts", 2, receiptsApps, receiptsAppBindings}, {"../dms/testdata/sample-diesel.zip", "Diesel tracker", "diesel", 4, dieselApps, dieselAppBindings}, {"../../samples/workflows/workflows.zip", "Workflow samples", "workflows", 4, nil, nil}} {
		t.Run(sample.Name, func(t *testing.T) {
			path := sample.Path
			if *updateSamplePackages {
				buildSamplePackage(t, sample.Kind, path)
			}
			s := dms.NewService(daltest.Database(t).SQL)
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
			before, _ := s.DAL.Resources().Count(t.Context(), resources.Filter{})
			importSampleHTTP(t, handler, s, "organization-admin", path, "", true)
			after, _ := s.DAL.Resources().Count(t.Context(), resources.Filter{})
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
					got, err := (&workflow.Service{DMS: s}).Update(t.Context(), "organization-admin", w.ID, w.Version, workflow.Save{AutomaticEnabled: new(true), Name: w.Name, Description: w.Description, Source: w.Source})
					if err != nil || got.ID != w.ID {
						t.Fatal("imported workflow cannot be saved", w.Name, err)
					}
					raw := string(mustJSON(t, w.Definition)) + string(mustJSON(t, w.Parameters))
					for _, placeholder := range []string{"$LIBRARY", "$TERM_SET", "$FIELD", "$collection:", "$type:", "$setting:", sampleSubject} {
						if strings.Contains(raw, placeholder) {
							t.Fatal("unbound sample", placeholder)
						}
					}
				}
				checkImportedSampleApps(t, s, result.WorkspaceID, sample.Apps, sample.AppBindings)
				if count, err := s.DAL.Resources().Count(t.Context(), resources.Filter{WorkspaceID: result.WorkspaceID, Kinds: []refs.ResourceKind{refs.KindItem, refs.KindFolder}}); err != nil || count != 0 {
					t.Fatal("sample included content", count, err)
				}
			}
			if _, err := s.Get(t.Context(), "alice", existing.ID); err != nil {
				t.Fatal("import changed other workspace", err)
			}
			if present, err := s.DAL.Directory().User(t.Context(), sampleSubject); err == nil {
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
