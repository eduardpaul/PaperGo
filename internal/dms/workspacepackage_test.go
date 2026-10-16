package dms

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"papergo/ent/workflowtrigger"
	"strings"
	"testing"
	"time"

	"papergo/ent"
	"papergo/ent/identityuser"
	"papergo/ent/resource"
	"papergo/ent/workflow"
	"papergo/ent/workflowversion"
	"papergo/internal/identity"
	"papergo/internal/model"
	"papergo/internal/storage"
)

func exportWorkspaceForTest(t *testing.T, s *Service, store storage.Store, options ExportOptions) (storage.Object, corePackage) {
	t.Helper()
	var object storage.Object
	if err := s.Write(t.Context(), func(tx *Service) error {
		var err error
		object, err = tx.exportCorePackage(t.Context(), store, options)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f, err := store.Open(t.Context(), object.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	var p corePackage
	decoder := json.NewDecoder(manifest)
	decoder.UseNumber()
	if err = decoder.Decode(&p); err != nil {
		t.Fatal(err)
	}
	return object, p
}

func importWorkspaceForTest(t *testing.T, s *Service, store storage.Store, object storage.Object, dry bool, name string) (PackageResult, error) {
	t.Helper()
	var result PackageResult
	err := s.Write(t.Context(), func(tx *Service) error {
		var err error
		result, err = tx.importCorePackage(t.Context(), store, "organization-admin", packagePayload{ObjectKey: object.Key, DryRun: dry, WorkspaceName: name})
		return err
	})
	return result, err
}

func TestWorkspacePackageRoundTripIntoPopulatedInstance(t *testing.T) {
	s, ws, invoices := fixture(t)
	ctx := t.Context()
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vendors := create(t, s, ws.ID, "list", "Vendors", nil)
	vendor := create(t, s, vendors.ID, "item", "Supplier", nil)
	fieldOK(t, s, invoices.ID, CreateField{Key: "vendor", Label: "Vendor", Type: "lookup", Indexed: true, Options: model.FieldOptions{LookupContainerID: vendors.ID}})
	fieldOK(t, s, invoices.ID, CreateField{Key: "memo", Label: "Memo", Type: "text", Indexed: true})
	fieldOK(t, s, invoices.ID, CreateField{Key: "amount", Label: "Amount", Type: "integer", Indexed: true})
	group, err := s.CreateTermGroup(ctx, "alice", TermGroupInput{Name: "Filing"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "alice", TermSetInput{GroupID: group.ID, Key: "regions", Name: "Regions"})
	if err != nil {
		t.Fatal(err)
	}
	term, err := s.CreateTerm(ctx, "alice", set.ID, TermInput{Name: "Europe"})
	if err != nil {
		t.Fatal(err)
	}
	fieldOK(t, s, invoices.ID, CreateField{Key: "region", Label: "Region", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}})
	folder := create(t, s, invoices.ID, "folder", "2026", nil)
	item := create(t, s, folder.ID, "item", "Invoice", map[string]any{"vendor": vendor.ID, "memo": vendor.ID, "region": term.ID, "amount": json.Number("9007199254740993")})
	if _, err = s.Publish(ctx, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	library := create(t, s, ws.ID, "library", "Documents", nil)
	object, err := store.Put(ctx, strings.NewReader("%PDF-1.7\nReceipt\n%%EOF"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreateDocument(ctx, "alice", library.ID, DocumentMetadata{Name: "Receipt"}, BlobInput{ObjectKey: object.Key, Filename: "receipt.pdf", ContentType: "application/pdf", Size: object.Size, SHA256: object.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(ctx, strings.NewReader("%PDF-1.7\nReceipt version 2\n%%EOF"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachBlob(ctx, "alice", doc.Item.ID, latest(t, s, doc.Item.ID).Version, BlobInput{ObjectKey: second.Key, Filename: "receipt-v2.pdf", ContentType: "application/pdf", Size: second.Size, SHA256: second.SHA256}); err != nil {
		t.Fatal(err)
	}
	textObject, err := store.Put(ctx, strings.NewReader(`{"pages":[{"page":1,"text":"Receipt version 2","ocr":true}]}`), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveBlobText(ctx, BlobTextInput{SHA256: second.SHA256, Settings: "ocr", ObjectKey: textObject.Key, ObjectSize: textObject.Size, ObjectSHA256: textObject.SHA256, Pages: 1, Chars: 17, OCRPages: 1, Language: "english", Extractor: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Link(ctx, "alice", item.ID, CreateRelationship{TargetID: doc.Item.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTemplate(ctx, "alice", ws.ID, TemplateInput{Key: "amounts", Name: "Amounts", Fields: []CreateField{{Key: "total", Label: "Total", Type: "integer"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSmartFolder(ctx, "alice", SmartFolderInput{Name: "Europe", WorkspaceID: &ws.ID, Definition: SmartFolderDefinition{Terms: []string{term.ID}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSmartFolder(ctx, "alice", SmartFolderInput{Name: "Personal excluded", WorkspaceID: &ws.ID, Personal: true}); err != nil {
		t.Fatal(err)
	}
	staff, err := s.Directory().SaveGroup(ctx, "organization-admin", "", 0, identity.GroupInput{Name: "Staff"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().SetMember(ctx, "organization-admin", staff.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(ctx, "alice", invoices.ID, latest(t, s, invoices.ID).Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}, {"group", staff.ID, "write", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	// A workflow references a collection, a term, and a group inside JSON.
	var flow *ent.Workflow
	if err = s.Write(ctx, func(tx *Service) error {
		var err error
		flow, err = tx.Client.Workflow.Create().SetWorkspaceID(ws.ID).SetKey("copy-test").SetName("Copy test").SetCurrentVersion(1).SetCreatedBy("alice").SetUpdatedBy("alice").SetParameters(map[string]any{"list": invoices.ID}).Save(ctx)
		if err != nil {
			return err
		}
		definition, _ := json.Marshal(map[string]any{"triggers": []any{map[string]any{"type": "item.created", "collection_id": invoices.ID, "terms": []string{term.ID}}, map[string]any{"type": "schedule", "collection_id": invoices.ID, "cron": "0 12 * * *", "time_zone": "Europe/Madrid"}}, "flow": map[string]any{"start": "done", "nodes": map[string]any{"done": map[string]any{"activity": "end"}}}, "variables": map[string]any{"assignee": "group:" + staff.ID}})
		if err = tx.Client.WorkflowVersion.Create().SetWorkflowID(flow.ID).SetNumber(1).SetDefinition(definition).SetCreatedBy("alice").Exec(ctx); err != nil {
			return err
		}
		if err = tx.Client.WorkflowTrigger.Create().SetWorkflowID(flow.ID).SetWorkspaceID(ws.ID).SetCollectionID(invoices.ID).SetType("item.created").SetPosition(0).Exec(ctx); err != nil {
			return err
		}
		return tx.Client.WorkflowTrigger.Create().SetWorkflowID(flow.ID).SetWorkspaceID(ws.ID).SetCollectionID(invoices.ID).SetType("schedule").SetPosition(1).SetNextAt(time.Now().UTC().Add(-24 * time.Hour)).Exec(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	other := create(t, s, "", "workspace", "Unrelated secret", nil)
	create(t, s, other.ID, "list", "Unrelated records", nil)
	if _, err = s.CreateCatalogField(ctx, "alice", CreateField{Key: "unused", Label: "Unrelated secret field", Type: "text"}); err != nil {
		t.Fatal(err)
	}
	for _, include := range []bool{false, true} {
		t.Run(fmt.Sprint("data=", include), func(t *testing.T) {
			if include {
				value, _ := json.Marshal(vendor.ID)
				for _, field := range []string{"memo", "vendor"} {
					if _, err := s.CreateView(ctx, "alice", invoices.ID, ViewInput{Name: "Filter " + field, Columns: []string{"$name", field}, Query: QuerySpec{Filter: &FilterExpr{Field: field, Op: "eq", Value: value}}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			object, manifest := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: ws.ID, IncludeData: &include})
			raw, _ := json.Marshal(manifest)
			for _, excluded := range []string{"Unrelated secret", "Personal excluded", "identity_rest_tokens"} {
				if bytes.Contains(raw, []byte(excluded)) {
					t.Fatal("unrelated state in workspace package", excluded)
				}
			}
			if manifest.Scope != "workspace" || manifest.IncludeData != include {
				t.Fatal(manifest.Scope, manifest.IncludeData)
			}
			if !include && (len(manifest.Objects) != 0 || len(manifest.Tables["item_revisions"].Rows) != 0) {
				t.Fatal("configuration export included data")
			}
			target, existing, _ := fixture(t)
			original, err := target.Get(ctx, "alice", existing.ID)
			if err != nil {
				t.Fatal(err)
			}
			var org string
			if err = target.db.QueryRowContext(ctx, "SELECT display_name FROM identity_organizations").Scan(&org); err != nil {
				t.Fatal(err)
			}
			before, _ := target.Client.Resource.Query().Count(ctx)
			plan, err := importWorkspaceForTest(t, target, store, object, true, "Copied Finance")
			if err != nil || !plan.Valid || !plan.DryRun {
				t.Fatal("dry run", plan, err)
			}
			after, _ := target.Client.Resource.Query().Count(ctx)
			if after != before {
				t.Fatal("dry run wrote resources")
			}
			result, err := importWorkspaceForTest(t, target, store, object, false, "Copied Finance")
			if err != nil || !result.Valid || result.WorkspaceID == ws.ID {
				t.Fatal("import", result, err)
			}
			current, err := target.Get(ctx, "alice", existing.ID)
			if err != nil || current.Version != original.Version || current.Name != original.Name {
				t.Fatal("changed target workspace", current, err)
			}
			var targetOrg string
			if err = target.db.QueryRowContext(ctx, "SELECT display_name FROM identity_organizations").Scan(&targetOrg); err != nil || org != targetOrg {
				t.Fatal("changed target organization", err)
			}
			collections, err := target.Client.Resource.Query().Where(resource.WorkspaceIDEQ(result.WorkspaceID), resource.KindIn(resource.KindList, resource.KindLibrary)).All(ctx)
			if err != nil || len(collections) != 3 {
				t.Fatal(collections, err)
			}
			var copiedInvoices, copiedVendors string
			for _, c := range collections {
				if c.Name == "Invoices" {
					copiedInvoices = c.ID
				}
				if c.Name == "Vendors" {
					copiedVendors = c.ID
				}
			}
			defs, err := target.Fields(ctx, "alice", copiedInvoices)
			if err != nil {
				t.Fatal(err)
			}
			for _, def := range defs {
				if def.Key == "vendor" && def.Options.LookupContainerID != copiedVendors {
					t.Fatal("lookup container not remapped", def)
				}
			}
			copiedFlow, err := target.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(result.WorkspaceID), workflow.KeyEQ("copy-test")).Only(ctx)
			if err != nil {
				t.Fatal(err)
			}
			version, err := target.Client.WorkflowVersion.Query().Where(workflowversion.WorkflowIDEQ(copiedFlow.ID)).Only(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(version.Definition, []byte(invoices.ID)) || bytes.Contains(version.Definition, []byte(term.ID)) || bytes.Contains(version.Definition, []byte(staff.ID)) || !bytes.Contains(version.Definition, []byte(copiedInvoices)) {
				t.Fatal("workflow references not remapped", string(version.Definition))
			}
			scheduled, err := target.Client.WorkflowTrigger.Query().Where(workflowtrigger.WorkflowIDEQ(copiedFlow.ID), workflowtrigger.TypeEQ("schedule")).Only(ctx)
			if err != nil || scheduled.NextAt == nil || !scheduled.NextAt.After(time.Now().UTC()) {
				t.Fatal("schedule cursor not rebuilt", scheduled, err)
			}
			count, err := target.Client.Resource.Query().Where(resource.WorkspaceIDEQ(result.WorkspaceID), resource.KindEQ(resource.KindItem)).Count(ctx)
			if err != nil || (include && count != 3) || (!include && count != 0) {
				t.Fatal("content selection", count, err)
			}
			if include {
				invoice, err := target.Client.Resource.Query().Where(resource.WorkspaceIDEQ(result.WorkspaceID), resource.NameEQ("Invoice")).Only(ctx)
				if err != nil {
					t.Fatal(err)
				}
				invoice, err = target.Get(ctx, "alice", invoice.ID)
				if err != nil {
					t.Fatal(err)
				}
				if invoice.Values["memo"] != vendor.ID || invoice.Values["vendor"] == vendor.ID || fmt.Sprint(invoice.Values["amount"]) != "9007199254740993" {
					t.Fatal("values not preserved/remapped", invoice.Values)
				}
				views, err := target.Views(ctx, "alice", copiedInvoices, "", 50)
				if err != nil {
					t.Fatal(err)
				}
				matchedViews := 0
				for _, view := range views.Data {
					if strings.HasPrefix(view.Name, "Filter ") {
						matchedViews++
						matches, err := target.QueryView(ctx, "alice", view.ID, ViewQueryRequest{})
						if err != nil || len(matches.Data) != 1 || matches.Data[0].Name != "Invoice" {
							t.Fatal("typed saved-view filter changed", view.Name, matches, err)
						}
					}
				}
				if matchedViews != 2 {
					t.Fatal("saved views not copied", matchedViews)
				}
				vendorID := fmt.Sprint(invoice.Values["vendor"])
				if _, err = target.Get(ctx, "alice", vendorID); err != nil {
					t.Fatal("lookup does not resolve", err)
				}
				receipt, err := target.Client.Resource.Query().Where(resource.WorkspaceIDEQ(result.WorkspaceID), resource.NameEQ("Receipt")).Only(ctx)
				if err != nil {
					t.Fatal(err)
				}
				blobs, err := target.FileVersions(ctx, "alice", receipt.ID, 0, 10)
				if err != nil || len(blobs.Data) != 2 {
					t.Fatal(blobs, err)
				}
				blob, err := target.GetBlob(ctx, "alice", receipt.ID, "")
				if err != nil || blob.ObjectKey == second.Key {
					t.Fatal("file object key not localized", blob, err)
				}
				content, err := store.Open(ctx, blob.ObjectKey)
				if err != nil {
					t.Fatal(err)
				}
				bytes, err := io.ReadAll(content)
				content.Close()
				if err != nil || !strings.Contains(string(bytes), "Receipt version 2") {
					t.Fatal("file bytes not restored", err)
				}
			}
			// Repeating without a new name cannot duplicate a workspace.
			before, _ = target.Client.Resource.Query().Count(ctx)
			if _, err = importWorkspaceForTest(t, target, store, object, false, "Copied Finance"); err == nil {
				t.Fatal("duplicate workspace accepted")
			}
			after, _ = target.Client.Resource.Query().Count(ctx)
			if before != after {
				t.Fatal("failed import changed resources")
			}
			// A second independent copy reuses the taxonomy and matching built-ins.
			again, err := importWorkspaceForTest(t, target, store, object, false, "Another copy")
			if err != nil || !again.Valid {
				t.Fatal("second copy", again, err)
			}
		})
	}
}

func TestWorkspacePackageRejectsOutsideDependenciesAndPersonalHome(t *testing.T) {
	s, ws, list := fixture(t)
	ctx := t.Context()
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remote := create(t, s, "", "workspace", "Remote", nil)
	external := create(t, s, remote.ID, "list", "External catalog", nil)
	fieldOK(t, s, list.ID, CreateField{Key: "outside", Label: "Outside", Type: "lookup", Options: model.FieldOptions{LookupContainerID: external.ID}})
	for _, include := range []bool{false, true} {
		err = s.Write(ctx, func(tx *Service) error {
			_, err := tx.exportCorePackage(ctx, store, ExportOptions{WorkspaceID: ws.ID, IncludeData: &include})
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "excluded resource") {
			t.Fatal("outside dependency accepted", err)
		}
	}
	if err = s.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartExport(ctx, "bob", ExportOptions{WorkspaceID: ws.ID}); err == nil {
		t.Fatal("non-administrator exported workspace")
	}
	home, err := s.Home(ctx, "organization-admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartExport(ctx, "organization-admin", ExportOptions{WorkspaceID: home.WorkspaceID}); err == nil {
		t.Fatal("personal Home exported as shared workspace")
	}
}

func TestWorkspacePackageAccountsConflictsAndUntrustedManifest(t *testing.T) {
	s, ws, list := fixture(t)
	ctx := t.Context()
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().EnsureUser(ctx, "source-admin"); err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: "user", ID: "source-admin"}, false); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, WorkspaceMember{Subject: "source-admin", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateTermGroup(ctx, "alice", TermGroupInput{Name: "Shared"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "alice", TermSetInput{GroupID: group.ID, Key: "shared_terms", Name: "Shared terms", Description: "Source definition"})
	if err != nil {
		t.Fatal(err)
	}
	fieldOK(t, s, list.ID, CreateField{Key: "region", Label: "Region", Type: "term", Options: model.FieldOptions{TermSetID: set.ID}})
	noData := false
	object, manifest := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: ws.ID, IncludeData: &noData})
	t.Run("existing accounts and source admin", func(t *testing.T) {
		target, _, _ := fixture(t)
		if _, err = target.Directory().SetUserActive(ctx, "organization-admin", "bob", false); err != nil {
			t.Fatal(err)
		}
		result, err := importWorkspaceForTest(t, target, store, object, false, "Copied")
		if err != nil || !result.Valid {
			t.Fatal(result, err)
		}
		if err = target.RequireScope(ctx, "source-admin", "organization.manage"); err == nil {
			t.Fatal("source administrator role transferred")
		}
		status, err := target.Directory().UserStatus(ctx, "organization-admin", "bob")
		if err != nil || status.Active {
			t.Fatal("reactivated target user", status, err)
		}
		members, err := target.WorkspaceMembers(ctx, "organization-admin", result.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, member := range members {
			if member.Subject == "source-admin" && member.Role == "owner" {
				found = true
			}
		}
		if !found {
			t.Fatal("workspace ownership not copied", members)
		}
	})
	t.Run("conflicting taxonomy leaves no changes", func(t *testing.T) {
		target, _, _ := fixture(t)
		g, err := target.CreateTermGroup(ctx, "alice", TermGroupInput{Name: "Shared"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = target.CreateTermSet(ctx, "alice", TermSetInput{GroupID: g.ID, Key: "shared_terms", Name: "Shared terms", Description: "Target definition"}); err != nil {
			t.Fatal(err)
		}
		for _, dry := range []bool{true, false} {
			before, _ := target.Client.Resource.Query().Count(ctx)
			if _, err = importWorkspaceForTest(t, target, store, object, dry, "Copied"); err == nil || !strings.Contains(err.Error(), "conflicts") {
				t.Fatal("conflict accepted", err)
			}
			after, _ := target.Client.Resource.Query().Count(ctx)
			if after != before {
				t.Fatal("conflict changed resources")
			}
			if exists, _ := target.Client.IdentityUser.Query().Where(identityuser.IDEQ("source-admin")).Exist(ctx); exists {
				t.Fatal("failed import created a user")
			}
		}
	})
	for _, test := range []struct {
		name   string
		mutate func(*corePackage)
	}{
		{"unrelated organization roles", func(p *corePackage) { p.Tables["identity_roles"] = manifestTableForTest(t, s, "identity_roles") }},
		{"organization profile", func(p *corePackage) { p.Organization = map[string]any{"display_name": "Injected"} }},
		{"missing lookup target", func(p *corePackage) {
			table := p.Tables["field_definitions"]
			for _, row := range table.Rows {
				for i, col := range table.Columns {
					if col == "options" {
						row[i] = `{"lookup_container_id":"ffffffff-ffff-4fff-8fff-ffffffffffff"}`
					}
				}
			}
			p.Tables["field_definitions"] = table
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(manifest)
			var mutated corePackage
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&mutated); err != nil {
				t.Fatal(err)
			}
			test.mutate(&mutated)
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			entry, _ := writer.Create("manifest.json")
			if err := json.NewEncoder(entry).Encode(mutated); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			bad, err := store.Put(ctx, &archive, PackageLimit)
			if err != nil {
				t.Fatal(err)
			}
			target := emptyPackageTarget(t)
			if _, err = importWorkspaceForTest(t, target, store, bad, false, ""); err == nil {
				t.Fatal("untrusted graph accepted")
			}
			if count, _ := target.Client.Resource.Query().Count(ctx); count != 0 {
				t.Fatal("invalid graph wrote resources")
			}
		})
	}
}

func manifestTableForTest(t *testing.T, s *Service, table string) packageTable {
	t.Helper()
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, manifest := exportWorkspaceForTest(t, s, store, ExportOptions{})
	return manifest.Tables[table]
}

func TestWorkspacePackageTemplateTypesAndLiteralDefaults(t *testing.T) {
	s, ws, _ := fixture(t)
	ctx := t.Context()
	contacts, err := s.Create(ctx, "alice", ws.ID, CreateResource{Name: "Contacts", TemplateKey: "contacts"})
	if err != nil {
		t.Fatal(err)
	}
	types, err := s.CollectionContentTypes(ctx, "alice", contacts.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceType := types[0].ContentType
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	noData := false
	object, _ := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: ws.ID, IncludeData: &noData})
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint("conflict=", conflict), func(t *testing.T) {
			target, existing, _ := fixture(t)
			collection, err := target.Create(ctx, "alice", existing.ID, CreateResource{Name: "Existing contacts", TemplateKey: "contacts"})
			if err != nil {
				t.Fatal(err)
			}
			targetTypes, err := target.CollectionContentTypes(ctx, "alice", collection.ID)
			if err != nil {
				t.Fatal(err)
			}
			typ := targetTypes[0].ContentType
			if typ.ID == sourceType.ID {
				t.Fatal("fixture must use independent catalog identities")
			}
			if conflict {
				if _, err = target.UpdateContentType(ctx, "alice", typ.ID, typ.Version, ContentTypeInput{Name: "Customized contact", FieldIds: typ.FieldIds}); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := target.Client.Resource.Query().Count(ctx)
			result, err := importWorkspaceForTest(t, target, store, object, false, "Copy")
			if conflict {
				if err == nil || !strings.Contains(err.Error(), "conflicts") {
					t.Fatal("customized target overwritten", err)
				}
				after, _ := target.Client.Resource.Query().Count(ctx)
				if before != after {
					t.Fatal("conflict partially imported")
				}
				return
			}
			if err != nil || !result.Valid {
				t.Fatal(result, err)
			}
			copied, err := target.Client.Resource.Query().Where(resource.WorkspaceIDEQ(result.WorkspaceID), resource.NameEQ("Contacts")).Only(ctx)
			if err != nil {
				t.Fatal(err)
			}
			copiedTypes, err := target.CollectionContentTypes(ctx, "alice", copied.ID)
			if err != nil || copiedTypes[0].ID != typ.ID {
				t.Fatal("template type not reused", copiedTypes, err)
			}
			views, err := target.Views(ctx, "alice", copied.ID, "", 50)
			if err != nil || len(views.Data) != 1 || !views.Data[0].IsDefault {
				t.Fatal("template view not copied", views, err)
			}
		})
	}
	// A plain text default equal to an omitted item's UUID is data, not a lookup.
	item := create(t, s, contacts.ID, "item", "Literal ID", nil)
	defaultValue, _ := json.Marshal(item.ID)
	fieldOK(t, s, contacts.ID, CreateField{Key: "literal", Label: "Literal", Type: "text", Options: model.FieldOptions{DefaultValue: defaultValue}})
	if err := s.Directory().EnsureUser(ctx, "default-person-only"); err != nil {
		t.Fatal(err)
	}
	fieldOK(t, s, contacts.ID, CreateField{Key: "default_person", Label: "Default person", Type: "person", Options: model.FieldOptions{DefaultValue: json.RawMessage(`"default-person-only"`)}})
	configuration, _ := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: ws.ID, IncludeData: &noData})
	target := emptyPackageTarget(t)
	result, err := importWorkspaceForTest(t, target, store, configuration, false, "")
	if err != nil || !result.Valid {
		t.Fatal(result, err)
	}
	copied, err := target.Client.Resource.Query().Where(resource.WorkspaceIDEQ(result.WorkspaceID), resource.NameEQ("Contacts")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := target.Fields(ctx, "organization-admin", copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range defs {
		if field.Key == "literal" {
			found = true
			if string(field.Options.DefaultValue) != string(defaultValue) {
				t.Fatal("plain text default was remapped", string(field.Options.DefaultValue))
			}
		}
	}
	if !found {
		t.Fatal("literal field missing")
	}
	if _, err := target.Client.IdentityUser.Get(ctx, "default-person-only"); err != nil {
		t.Fatal("person default dependency was not copied", err)
	}
}

func TestConfigurationPackageScopeAndContentConsistency(t *testing.T) {
	s, ws, list := fixture(t)
	ctx := t.Context()
	item := create(t, s, list.ID, "item", "Excluded", nil)
	noData := false
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	object, p := exportWorkspaceForTest(t, s, store, ExportOptions{IncludeData: &noData})
	if p.Scope != "organization" || len(p.Tables["resources"].Rows) != 2 || len(p.Tables["item_revisions"].Rows) != 0 {
		t.Fatal("organization configuration export has data")
	}
	target := emptyPackageTarget(t)
	result, err := importWorkspaceForTest(t, target, store, object, false, "")
	if err != nil || !result.Valid {
		t.Fatal(result, err)
	}
	if count, _ := target.Client.Resource.Query().Count(ctx); count != 2 {
		t.Fatal("configuration not imported", count)
	}
	defaultValue, _ := json.Marshal(item.ID)
	fieldOK(t, s, list.ID, CreateField{Key: "default_reference", Label: "Default reference", Type: "lookup", Options: model.FieldOptions{LookupContainerID: list.ID, DefaultValue: defaultValue}})
	for _, options := range []ExportOptions{{IncludeData: &noData}, {WorkspaceID: ws.ID, IncludeData: &noData}} {
		err := s.Write(ctx, func(tx *Service) error { _, err := tx.exportCorePackage(ctx, store, options); return err })
		if err == nil {
			t.Fatal("configuration exported an omitted item reference")
		}
	}
}

func TestWorkspacePackageCollectedPublicationRevision(t *testing.T) {
	for _, unpublish := range []bool{false, true} {
		t.Run(fmt.Sprintf("unpublish=%v", unpublish), func(t *testing.T) {
			s, ws, vendors := fixture(t)
			ctx := t.Context()
			store, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			file, err := store.Put(ctx, strings.NewReader("%PDF-1.7\nPublished\n%%EOF"), 1000)
			if err != nil {
				t.Fatal(err)
			}
			vendor := create(t, s, vendors.ID, "item", "Supplier", nil)
			library, err := s.Create(ctx, "alice", ws.ID, CreateResource{Kind: "library", Name: "Documents", Versioning: ptr("off"), PublishingEnabled: true})
			if err != nil {
				t.Fatal(err)
			}
			fieldOK(t, s, library.ID, CreateField{Key: "vendor", Label: "Vendor", Type: "lookup", Options: model.FieldOptions{LookupContainerID: vendors.ID}})
			fieldOK(t, s, library.ID, CreateField{Key: "memo", Label: "Memo", Type: "text"})
			document, err := s.CreateDocument(ctx, "alice", library.ID, DocumentMetadata{Name: "Published", Values: map[string]any{"vendor": vendor.ID, "memo": vendor.ID}}, BlobInput{ObjectKey: file.Key, Filename: "published.pdf", ContentType: "application/pdf", Size: file.Size, SHA256: file.SHA256})
			if err != nil {
				t.Fatal(err)
			}
			item := document.Item
			published, err := s.Publish(ctx, "alice", item.ID, item.Version)
			if err != nil {
				t.Fatal(err)
			}
			item, err = s.Update(ctx, "alice", item.ID, latest(t, s, item.ID).Version, UpdateResource{Name: ptr("Current"), Values: ptr(map[string]any{"vendor": nil, "memo": nil})})
			if err != nil {
				t.Fatal(err)
			}
			if unpublish {
				if _, err = s.Unpublish(ctx, "alice", item.ID, item.Version); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Update(ctx, "alice", library.ID, latest(t, s, library.ID).Version, UpdateResource{PublishingEnabled: ptr(false)}); err != nil {
				t.Fatal(err)
			}
			if err = s.Client.DomainEvent.Update().SetDispatchedAt(time.Now().UTC()).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if err = s.CollectRevisions(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Client.ItemRevision.Get(ctx, *published.RevisionID); !ent.IsNotFound(err) {
				t.Fatal("published revision was not collected", err)
			}
			object, manifest := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: ws.ID})
			found := false
			for _, row := range packageMaps(manifest.Tables["publications"]) {
				if row["id"] == published.ID {
					found = true
					if row["revision_id"] != nil || row["snapshot"] != string(published.Snapshot) {
						t.Fatal("export did not detach the collected revision while preserving the snapshot", row)
					}
				}
			}
			if !found {
				t.Fatal("publication missing from package")
			}
			source, err := s.Client.Publication.Get(ctx, published.ID)
			if err != nil || source.RevisionID == nil || *source.RevisionID != *published.RevisionID || !bytes.Equal(source.Snapshot, published.Snapshot) {
				t.Fatal("export changed the source publication", source, err)
			}
			target, _, _ := fixture(t)
			for _, dry := range []bool{true, false} {
				result, err := importWorkspaceForTest(t, target, store, object, dry, "Copy")
				if err != nil || !result.Valid {
					t.Fatal("import", result, err)
				}
			}
			publications, err := target.Client.Publication.Query().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found = false
			for _, event := range publications {
				var snapshot map[string]any
				if err = json.Unmarshal(event.Snapshot, &snapshot); err != nil {
					t.Fatal(err)
				}
				if snapshot["name"] != "Published" {
					continue
				}
				found = true
				values := snapshot["values"].(map[string]any)
				copiedVendor, e := target.Client.Resource.Get(ctx, fmt.Sprint(values["vendor"]))
				if e != nil || copiedVendor.Name != vendor.Name || copiedVendor.ID == vendor.ID || values["memo"] != vendor.ID || event.RevisionID != nil {
					t.Fatal("snapshot references or literal values were not preserved", event, snapshot, copiedVendor, e)
				}
			}
			if !found {
				t.Fatal("imported publication snapshot missing")
			}
			// Import validation still rejects broken live revision references.
			for i, column := range manifest.Tables["resources"].Columns {
				if column == "head_revision_id" {
					for _, row := range manifest.Tables["resources"].Rows {
						if row[i] != nil {
							row[i] = "missing-revision"
						}
					}
				}
			}
			if err = validateWorkspaceReferences(manifest); err == nil || !strings.Contains(err.Error(), "resources.head_revision_id") {
				t.Fatal("unresolved live revision accepted", err)
			}
		})
	}
}
