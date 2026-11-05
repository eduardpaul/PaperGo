package dms

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"papergo/internal/appcode"
	appstore "papergo/internal/dal/apps"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/jsonutil"
	"papergo/internal/storage"
)

// appSourceForTest is an app whose pickers include one named search, a
// name the generic JSON walk passes over, and whose source names a list in
// lowercase and in uppercase.
func appSourceForTest(list, render string) string {
	return strings.NewReplacer("$LIST", list, "$UPPER", strings.ToUpper(list), "$RENDER", render).Replace(`import { app } from "@papergo/app";

export default app({
  parameters: {
    type: "object",
    properties: {
      list: { type: "string", "x-papergo": { kind: "collection" } },
      search: { type: "string", "x-papergo": { kind: "collection" } },
      lists: { type: "array", items: { type: "string" }, "x-papergo": { kind: "collection" } },
      invoice: { type: "string", "x-papergo": { kind: "item", collection_id: "$LIST" } },
      owner: { type: "string", "x-papergo": { kind: "people" } },
      note: { type: "string" },
    },
  },
  render: () => "$RENDER $UPPER",
});`)
}

func appContentForTest(t *testing.T, source string) appstore.Content {
	t.Helper()
	c, err := appcode.Compile(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	return appstore.Content{Source: source, Bundle: c.Code, BundleHash: c.Hash, ParametersSchema: c.ParametersSchema}
}

// createAppForTest creates a published app whose versions have sources,
// oldest first.
func createAppForTest(t *testing.T, s *Service, workspace, key string, parameters map[string]any, sources ...string) appstore.App {
	t.Helper()
	a, err := s.DAL.Apps().Create(t.Context(), appstore.CreateInput{WorkspaceID: workspace, Key: key, Name: key, State: appstore.StatePublished, CreatedBy: "alice", Parameters: parameters, Content: appContentForTest(t, sources[0])})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources[1:] {
		content := appContentForTest(t, source)
		if a, _, err = s.DAL.Apps().Revise(t.Context(), a.ID, a.Version, "alice", &content, appstore.Changes{}); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// putPackageForTest stores p as a package ZIP.
func putPackageForTest(t *testing.T, store storage.Store, p corePackage) storage.Object {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, _ := writer.Create("manifest.json")
	if err := json.NewEncoder(entry).Encode(p); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	object, err := store.Put(t.Context(), &archive, PackageLimit)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

// setPackageColumn sets a column of the package table's rows that match.
func setPackageColumn(p *corePackage, table, column string, match func(row map[string]any) bool, value func(row map[string]any) any) {
	data := p.Tables[table]
	i := slices.Index(data.Columns, column)
	for r, row := range packageMaps(data) {
		if match == nil || match(row) {
			data.Rows[r][i] = value(row)
		}
	}
	p.Tables[table] = data
}

func resourcesFilterForTest(workspace, name string) resources.Filter {
	return resources.Filter{WorkspaceID: workspace, Name: name, Kinds: []refs.ResourceKind{refs.KindList}}
}

func TestWorkspacePackageApps(t *testing.T) {
	s, ws, invoices := fixture(t)
	ctx := t.Context()
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vendors := create(t, s, ws.ID, "list", "Vendors", nil)
	remote := create(t, s, "", "workspace", "Remote", nil)
	external := create(t, s, remote.ID, "list", "External catalog", nil)
	// A text parameter is the app's own value, even when it holds an ID. An
	// earlier version that named another workspace's list is history.
	parameters := map[string]any{"list": invoices.ID, "search": vendors.ID, "lists": []any{vendors.ID, invoices.ID}, "owner": "alice", "note": external.ID}
	first := createAppForTest(t, s, ws.ID, "dashboard", parameters, appSourceForTest(external.ID, "first"), appSourceForTest(invoices.ID, "second"))
	noData := false
	object, manifest := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: ws.ID, IncludeData: &noData})
	if raw, _ := json.Marshal(manifest); bytes.Contains(raw, []byte("External catalog")) {
		t.Fatal("a text parameter or an earlier version brought its list into the package")
	}
	current := func(row map[string]any) bool { return fmt.Sprint(row["number"]) == "2" }
	earlier := func(row map[string]any) bool { return fmt.Sprint(row["number"]) == "1" }

	t.Run("copy", func(t *testing.T) {
		target, _, _ := fixture(t)
		result, err := importWorkspaceForTest(t, target, store, object, false, "Copied Finance")
		if err != nil || !result.Valid {
			t.Fatal(result, err)
		}
		lists := map[string]string{}
		for _, name := range []string{"Invoices", "Vendors"} {
			list, err := target.DAL.Resources().One(ctx, resourcesFilterForTest(result.WorkspaceID, name))
			if err != nil {
				t.Fatal(err)
			}
			lists[name] = list.ID
		}
		apps, err := target.DAL.Apps().List(ctx, appstore.Filter{WorkspaceIDs: []string{result.WorkspaceID}})
		if err != nil || len(apps) != 1 {
			t.Fatal("apps not copied", apps, err)
		}
		copied := apps[0]
		if copied.ID == first.ID || copied.Key != "dashboard" || copied.State != appstore.StatePublished || copied.CurrentVersion != 2 || copied.CreatedBy != "alice" {
			t.Fatal("app not copied", copied)
		}
		want := map[string]any{"list": lists["Invoices"], "search": lists["Vendors"], "lists": []any{lists["Vendors"], lists["Invoices"]}, "owner": "alice", "note": external.ID}
		if fmt.Sprint(copied.Parameters) != fmt.Sprint(want) {
			t.Fatal("app parameters", copied.Parameters, want)
		}
		versions, err := target.DAL.Apps().Versions(ctx, copied.ID)
		if err != nil || len(versions) != 2 {
			t.Fatal(versions, err)
		}
		// The current version names the copy's list, whatever its case, and
		// its code and form are what its source compiles to.
		version := versions[0]
		source := strings.NewReplacer(invoices.ID, lists["Invoices"], strings.ToUpper(invoices.ID), lists["Invoices"]).Replace(appSourceForTest(invoices.ID, "second"))
		c, err := appcode.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		if version.Number != 2 || version.Source != source || version.Bundle != c.Code || version.BundleHash != c.Hash || !jsonutil.Equal(version.ParametersSchema, c.ParametersSchema) {
			t.Fatal("current app version does not name the copy", version.Number, version.Source)
		}
		// The earlier version keeps the list it named, as history.
		if versions[1].Source != appSourceForTest(external.ID, "first") {
			t.Fatal("earlier app version changed", versions[1].Source)
		}
	})

	mutated := func(t *testing.T, mutate func(*corePackage)) storage.Object {
		t.Helper()
		raw, _ := json.Marshal(manifest)
		var p corePackage
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&p); err != nil {
			t.Fatal(err)
		}
		mutate(&p)
		return putPackageForTest(t, store, p)
	}

	t.Run("code comes from the source", func(t *testing.T) {
		bad := mutated(t, func(p *corePackage) {
			setPackageColumn(p, "app_versions", "bundle", nil, func(map[string]any) any { return "fetch('https://example.com')" })
			setPackageColumn(p, "app_versions", "bundle_hash", nil, func(map[string]any) any { return strings.Repeat("0", 64) })
			setPackageColumn(p, "app_versions", "parameters_schema", current, func(map[string]any) any { return `{"type":"object"}` })
		})
		target, _, _ := fixture(t)
		result, err := importWorkspaceForTest(t, target, store, bad, false, "Copied Finance")
		if err != nil {
			t.Fatal(err)
		}
		apps, err := target.DAL.Apps().List(ctx, appstore.Filter{WorkspaceIDs: []string{result.WorkspaceID}})
		if err != nil || len(apps) != 1 {
			t.Fatal(apps, err)
		}
		version, err := target.DAL.Apps().Version(ctx, apps[0].ID, int64(apps[0].CurrentVersion))
		if err != nil {
			t.Fatal(err)
		}
		c, err := appcode.Compile(ctx, version.Source)
		if err != nil || version.Bundle != c.Code || version.BundleHash != c.Hash || !jsonutil.Equal(version.ParametersSchema, c.ParametersSchema) {
			t.Fatal("the current version's code is not its source's", err)
		}
	})

	t.Run("sources that do not compile", func(t *testing.T) {
		target, _, _ := fixture(t)
		// Only the current version runs, so only it must compile.
		old := mutated(t, func(p *corePackage) {
			setPackageColumn(p, "app_versions", "source", earlier, func(map[string]any) any { return "export default 1;" })
		})
		if result, err := importWorkspaceForTest(t, target, store, old, true, "Copied Finance"); err != nil || !result.Valid {
			t.Fatal("an earlier version that no longer compiles refused the import", result, err)
		}
		bad := mutated(t, func(p *corePackage) {
			setPackageColumn(p, "app_versions", "source", current, func(map[string]any) any { return "export default 1;" })
		})
		if _, err := importWorkspaceForTest(t, target, store, bad, true, "Copied Finance"); err == nil || !strings.Contains(err.Error(), "app dashboard does not compile") {
			t.Fatal("uncompilable app accepted", err)
		}
	})

	t.Run("parameters outside the package", func(t *testing.T) {
		target, _, targetList := fixture(t)
		for _, test := range []struct {
			name  string
			value any
		}{
			{"list", targetList.ID},
			{"search", targetList.ID},
			{"owner", "mallory"},
			// Carried, but not what the picker picks.
			{"list", ws.ID},
			{"search", fmt.Sprint(packageMaps(manifest.Tables["app_versions"])[0]["id"])},
			{"lists", []any{"", vendors.ID}},
		} {
			bad := mutated(t, func(p *corePackage) {
				setPackageColumn(p, "apps", "parameters", nil, func(map[string]any) any {
					changed := map[string]any{"list": invoices.ID, "search": vendors.ID, "owner": "alice"}
					changed[test.name] = test.value
					raw, _ := json.Marshal(changed)
					return string(raw)
				})
			})
			if _, err := importWorkspaceForTest(t, target, store, bad, true, "Copied Finance"); err == nil || !strings.Contains(err.Error(), "parameters."+test.name) || !strings.Contains(err.Error(), " selects ") {
				t.Fatal("outside parameter accepted", test.name, test.value, err)
			}
		}
	})

	t.Run("export refuses references outside the workspace", func(t *testing.T) {
		for _, test := range []struct {
			name, source string
			parameters   func(catalog string) map[string]any
		}{
			{"list picker", "", func(catalog string) map[string]any { return map[string]any{"list": catalog} }},
			{"search picker", "", func(catalog string) map[string]any { return map[string]any{"search": catalog} }},
			{"uppercase ID in the source", "upper", func(string) map[string]any { return map[string]any{} }},
		} {
			outside, home, list := fixture(t)
			other := create(t, outside, "", "workspace", "Remote", nil)
			catalog := create(t, outside, other.ID, "list", "External catalog", nil)
			source := appSourceForTest(list.ID, "x")
			if test.source == "upper" {
				source = appSourceForTest(list.ID, strings.ToUpper(catalog.ID))
			}
			createAppForTest(t, outside, home.ID, "outside", test.parameters(catalog.ID), source)
			err := outside.Write(ctx, func(tx *Service) error {
				_, err := tx.exportCorePackage(ctx, store, ExportOptions{WorkspaceID: home.ID, IncludeData: &noData})
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "excluded resource External catalog") {
				t.Fatal("outside reference accepted", test.name, err)
			}
		}
	})
}

// A template binds its source account where an app's people pickers select
// it, and refuses any other subject there.
func TestWorkspaceTemplateBindsAppPeople(t *testing.T) {
	template := samplePackageForTest(t, "../../samples/diesel/diesel.zip")
	source := template.TemplateSubject
	var list string
	for _, row := range packageMaps(template.Tables["resources"]) {
		if row["name"] == "Diesel" {
			list = fmt.Sprint(row["id"])
		}
	}
	for _, owner := range []string{source, "someone-else"} {
		t.Run(owner, func(t *testing.T) {
			p := samplePackageForTest(t, "../../samples/diesel/diesel.zip")
			setPackageColumn(&p, "app_versions", "source", nil, func(map[string]any) any { return appSourceForTest(list, "x") })
			setPackageColumn(&p, "apps", "parameters", nil, func(map[string]any) any {
				raw, _ := json.Marshal(map[string]any{"list": list, "owner": owner})
				return string(raw)
			})
			s := emptyPackageTarget(t)
			store, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			result, err := importWorkspaceForTest(t, s, store, putPackageForTest(t, store, p), false, "")
			if owner != source {
				// The package does not carry the account; were it carried, binding would refuse it.
				if err == nil || !strings.Contains(err.Error(), "someone-else") {
					t.Fatal("another subject accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			apps, err := s.DAL.Apps().List(t.Context(), appstore.Filter{WorkspaceIDs: []string{result.WorkspaceID}})
			if err != nil || len(apps) != 1 || apps[0].Parameters["owner"] != "organization-admin" || apps[0].Parameters["list"] == list {
				t.Fatal("app people not bound", apps, err)
			}
		})
	}
}
