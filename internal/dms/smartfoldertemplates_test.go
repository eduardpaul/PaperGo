package dms

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSmartFolderPackagesResolveTermsAndReapplyIdempotently(t *testing.T) {
	s, w, l := fixture(t)
	makeTerms := func() (TermView, TermView) {
		set, e := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "projects", Name: "Projects"})
		if e != nil {
			t.Fatal(e)
		}
		root, e := s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Apollo"})
		if e != nil {
			t.Fatal(e)
		}
		child, e := s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Lander", ParentID: &root.ID})
		if e != nil {
			t.Fatal(e)
		}
		return root, child
	}
	_, child := makeTerms()
	original := smartOK(t, s, SmartFolderInput{Name: "Project files", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Collections: []string{l.Name}, Terms: []string{child.ID}, GroupBy: []SmartFolderGroupBy{{Field: "$modified_at", By: "year"}}}})
	smartOK(t, s, SmartFolderInput{Name: "Private", WorkspaceID: &w.ID, Personal: true})
	pkg, sourceVersion, err := s.ExportSmartFolders(testContext, "alice", w.ID)
	if err != nil || len(pkg.Folders) != 1 || pkg.Folders[0].Name != original.Name || len(pkg.Folders[0].Definition.Terms[0].Path) != 2 {
		t.Fatal(pkg, err)
	}
	raw, _ := json.Marshal(pkg)
	if strings.Contains(string(raw), child.ID) || strings.Contains(string(raw), w.ID) {
		t.Fatal("export contains source identifiers", string(raw))
	}
	current, _ := s.Client.Resource.Get(testContext, w.ID)
	if current.Version != sourceVersion {
		t.Fatal(current.Version, sourceVersion)
	}
	target := create(t, s, "", "workspace", "Target", nil)
	targetChild := child
	result, err := s.ImportSmartFolders(testContext, "alice", target.ID, target.Version, pkg)
	if err != nil || result.Created != 1 || result.Updated != 0 || result.WorkspaceVersion != target.Version+1 {
		t.Fatal(result, err)
	}
	def, err := smartDefinition(result.Data[0])
	if err != nil || len(def.Terms) != 1 || def.Terms[0] != targetChild.ID || def.Collections[0] != l.Name {
		t.Fatal(def, err)
	}
	first := result.Data[0]
	audits, _ := s.Client.AuditEvent.Query().Count(testContext)
	again, err := s.ImportSmartFolders(testContext, "alice", target.ID, result.WorkspaceVersion, pkg)
	auditAfter, _ := s.Client.AuditEvent.Query().Count(testContext)
	if err != nil || again.Created != 0 || again.Updated != 0 || again.Data[0].ID != first.ID || again.Data[0].Version != first.Version || again.WorkspaceVersion != result.WorkspaceVersion || audits != auditAfter {
		t.Fatal("non-idempotent import", again, err)
	}
	pkg.Folders[0].Description = "Updated"
	replaced, err := s.ImportSmartFolders(testContext, "alice", target.ID, again.WorkspaceVersion, pkg)
	if err != nil || replaced.Created != 0 || replaced.Updated != 1 || replaced.Data[0].Version != first.Version+1 {
		t.Fatal(replaced, err)
	}
	_, err = s.ImportSmartFolders(testContext, "alice", target.ID, again.WorkspaceVersion, pkg)
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", target.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ExportSmartFolders(testContext, "reader", target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	targetCurrent, _ := s.Client.Resource.Get(testContext, target.ID)
	if _, err = s.ImportSmartFolders(testContext, "reader", target.ID, targetCurrent.Version, pkg); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSmartFolderPackageAtomicValidation(t *testing.T) {
	s, w, _ := fixture(t)
	pkg := SmartFolderPackage{Folders: []PortableSmartFolder{{Name: "Valid", Definition: PortableSmartFolderDefinition{}}, {Name: "Bad term", Definition: PortableSmartFolderDefinition{Terms: []SmartFolderTermRef{{TermSetKey: "missing", Path: []string{"Unknown"}}}}}}}
	before, _ := s.Client.AuditEvent.Query().Count(testContext)
	_, err := s.ImportSmartFolders(testContext, "alice", w.ID, w.Version, pkg)
	if !isValidation(err) {
		t.Fatal(err)
	}
	rows, err := s.SmartFolders(testContext, "alice", w.ID, "", 100)
	if err != nil || len(rows.Data) != 0 {
		t.Fatal("partial import", rows, err)
	}
	current, _ := s.Client.Resource.Get(testContext, w.ID)
	after, _ := s.Client.AuditEvent.Query().Count(testContext)
	if current.Version != w.Version || before != after {
		t.Fatal("failed import advanced workspace or audit")
	}
	pkg.Folders = []PortableSmartFolder{{Name: "Same"}, {Name: "Same"}}
	_, err = s.ImportSmartFolders(testContext, "alice", w.ID, w.Version, pkg)
	if !isValidation(err) {
		t.Fatal(err)
	}
	pkg.Folders = make([]PortableSmartFolder, 101)
	_, err = s.ImportSmartFolders(testContext, "alice", w.ID, w.Version, pkg)
	if !isValidation(err) {
		t.Fatal(err)
	}
}
