package database_test

import (
	"context"
	"encoding/json"
	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/testutil"
	"strings"
	"testing"
	"time"
)

func legacy(t *testing.T) *database.Database {
	t.Helper()
	db := testutil.DatabaseThrough(t, "20261007000300")
	now := time.Now().UTC()
	insert := func(id, kind, name, parent, container string, tags, values string, version int) {
		t.Helper()
		var p, c any
		if parent != "" {
			p = parent
		}
		if container != "" {
			c = container
		}
		_, err := db.SQL.Exec(`INSERT INTO resources(id,created_at,updated_at,workspace_id,kind,name,tags,"values",inherit_permissions,version,parent_id,container_id) VALUES(?,?,?,'w',?,?,?,?,?,?,?,?)`, id, now, now, kind, name, tags, values, kind != "workspace", version, p, c)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("w", "workspace", "Org", "", "", "[]", "{}", 1)
	insert("l", "library", "Documents", "w", "", "[]", "{}", 1)
	insert("i", "item", "Draftsecret", "l", "l", `["private"]`, `{"amount":9007199254740994}`, 4)
	for _, subject := range []string{"alice", "reader"} {
		action := "read"
		if subject == "alice" {
			action = "manage"
		}
		if _, err := db.SQL.Exec("INSERT INTO grants VALUES(?,?,?,?,'allow','w')", subject, now, subject, action); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SQL.Exec("INSERT INTO field_definitions VALUES('f',?,'amount','Amount','number',0,'[]','l')", now); err != nil {
		t.Fatal(err)
	}
	for n, key := range []string{"old", "draft"} {
		if _, err := db.SQL.Exec("INSERT INTO blobs VALUES(?,?,?,?,?,?,?,?,'i')", key, now, n+2, key, "file.txt", "text/plain", 1, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := `{"id":"i","name":"Released","tags":["public"],"values":{"amount":9007199254740993},"blob_id":"old"}`
	if _, err := db.SQL.Exec("INSERT INTO publications VALUES('p',?,2,'alice',?,'i')", now, snapshot); err != nil {
		t.Fatal(err)
	}
	return db
}

func upgrade(t *testing.T, db *database.Database) error {
	t.Helper()
	files := testutil.MigrationFiles(t)
	tx, err := db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, f := range files {
		if f.Version() <= "20261007000300" {
			continue
		}
		if _, err = tx.Exec(string(f.Bytes())); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func TestPopulatedUpgradePreservesPublishedAndDraftContent(t *testing.T) {
	db := legacy(t)
	if err := upgrade(t, db); err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.Client)
	ctx := context.Background()
	published, err := s.Get(ctx, "reader", "i")
	if err != nil || published.Name != "Released" || published.Values["amount"] != json.Number("9007199254740993") {
		t.Fatalf("publication corrupted: %+v %v", published, err)
	}
	head, err := s.Get(ctx, "alice", "i")
	if err != nil || head.Name != "Draftsecret" || head.Values["amount"] != json.Number("9007199254740994") {
		t.Fatalf("draft corrupted: %+v %v", head, err)
	}
	list, err := s.Get(ctx, "alice", "l")
	if err != nil || !list.PublishingEnabled {
		t.Fatal("legacy manual publication policy lost", err)
	}
	for _, in := range []dms.Browse{{ParentID: "l", Search: "Draftsecret"}, {ParentID: "l", Tag: "private"}} {
		p, err := s.Browse(ctx, "reader", in)
		if err != nil || len(p.Data) != 0 {
			t.Fatal("legacy draft index leaked", err)
		}
	}
	p, err := s.Browse(ctx, "reader", dms.Browse{ParentID: "l", Search: "Released", Tag: "public"})
	if err != nil || len(p.Data) != 1 {
		t.Fatal("legacy published index missing", err)
	}
	b, err := s.GetBlob(ctx, "reader", "i", "")
	if err != nil || b.ID != "old" {
		t.Fatal("legacy published blob changed", err)
	}
	b, err = s.GetBlob(ctx, "alice", "i", "")
	if err != nil || b.ID != "draft" {
		t.Fatal("legacy head blob changed", err)
	}
	// Legacy blob lock versions may exceed the new content counter.
	b, err = s.AttachBlob(ctx, "alice", "i", head.Version, dms.BlobInput{ObjectKey: "new", Filename: "new.txt", ContentType: "text/plain", Size: 1, SHA256: strings.Repeat("a", 64)})
	if err != nil || b.Version != 4 {
		t.Fatal("legacy blob numbering collision", err)
	}
	revs, err := s.Revisions(ctx, "alice", "i", 0, 100)
	if err != nil || len(revs) != 3 || revs[0].Name != "Released" {
		t.Fatal("legacy revisions missing", err)
	}
	rows, err := db.SQL.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("upgrade left foreign key violation")
	}
}

func TestUpgradeRejectsLegacyDeniesAtomically(t *testing.T) {
	db := legacy(t)
	if _, err := db.SQL.Exec("UPDATE grants SET effect='deny' WHERE id='reader'"); err != nil {
		t.Fatal(err)
	}
	if err := upgrade(t, db); err == nil {
		t.Fatal("deny silently removed")
	}
	var effect string
	if err := db.SQL.QueryRow("SELECT effect FROM grants WHERE id='reader'").Scan(&effect); err != nil || effect != "deny" {
		t.Fatal("failed upgrade changed access", err)
	}
	var tables int
	if err := db.SQL.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='item_revisions'").Scan(&tables); err != nil || tables != 0 {
		t.Fatal("partial migration persisted", err)
	}
}
