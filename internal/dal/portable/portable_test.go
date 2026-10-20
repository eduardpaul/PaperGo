package portable_test

import (
	"context"
	"papergo/internal/dal/daltest"
	"slices"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/portable"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
)

func runPortableSuite(t *testing.T, newStore func(testing.TB) *dal.Store) {
	t.Run("PortableColumnsAndValues", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		kinds := map[string]portable.Kind{}
		cols, err := s.Portable().Columns(ctx, "resources")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			kinds[c.Name] = c.Kind
		}
		want := map[string]portable.Kind{"id": portable.Text, "values": portable.JSON, "inherit_permissions": portable.Boolean, "created_at": portable.Time, "next_revision_number": portable.Integer, "deleted_at": portable.Time}
		for name, kind := range want {
			if kinds[name] != kind {
				t.Fatalf("resources.%s kind = %d, want %d", name, kinds[name], kind)
			}
		}
		users, err := s.Portable().Columns(ctx, "identity_users")
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(users, func(c portable.Column) bool { return c.Name == "password_hash" }) {
			t.Fatal("password hashes travel in packages")
		}
		if _, err = s.Portable().Columns(ctx, "operations"); err == nil {
			t.Fatal("a table outside packages has package columns")
		}

		user, err := s.Directory().CreateUser(ctx, "portable-alice", "Alice", "alice@example.com", true, false)
		if err != nil {
			t.Fatal(err)
		}
		ws, err := s.Resources().Create(ctx, resources.New{ID: "portable-ws", WorkspaceID: "portable-ws", Kind: refs.KindWorkspace, Name: "Portable", DefaultCurrency: "EUR", CreatedBy: user.ID})
		if err != nil {
			t.Fatal(err)
		}
		cols, rows, err := s.Portable().Export(ctx, "resources", portable.Scope{WorkspaceID: ws.ID}, 0)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		row := portable.Row{}
		for i, c := range cols {
			row[c.Name] = rows[0][i]
		}
		if row["inherit_permissions"] != false || row["values"] != "{}" || row["next_revision_number"] != int64(1) || row["deleted_at"] != nil {
			t.Fatalf("exported values are not package values: %#v", row)
		}
		created, ok := row["created_at"].(string)
		if _, err := time.Parse(time.RFC3339Nano, created); !ok || err != nil {
			t.Fatalf("created_at = %#v", row["created_at"])
		}
		found, err := s.Portable().Find(ctx, "resources", portable.Row{"id": ws.ID, "parent_id": nil})
		if err != nil || found["name"] != "Portable" || found["created_at"] != created {
			t.Fatal(found, err)
		}
		if _, err = s.Portable().Find(ctx, "resources", portable.Row{"id": "missing"}); !dberr.IsNotFound(err) {
			t.Fatal("missing row found", err)
		}
		if _, rows, err = s.Portable().Export(ctx, "item_revisions", portable.Scope{WorkspaceID: ws.ID}, 0); err != nil || len(rows) != 0 {
			t.Fatal(rows, err)
		}
	})

	t.Run("PortableImportDefersReferences", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		tx, err := s.SQLDB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		q := s.WithTx(tx).Portable()
		if err = q.DeferReferences(ctx); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
		set := portable.Row{"id": "set-1", "created_at": now, "updated_at": now, "group_id": "group-1", "key": "colors", "name": "Colors", "description": "", "is_open": int64(1), "is_keywords": false, "version": int64(1)}
		if err = q.Insert(ctx, "term_sets", set); err != nil {
			t.Fatal("insert before its group", err)
		}
		if unresolved, err := q.Unresolved(ctx); err != nil || !unresolved {
			t.Fatal("missing group not reported", unresolved, err)
		}
		group := portable.Row{"id": "group-1", "created_at": now, "updated_at": now, "name": "Palette", "description": "", "is_system": false, "version": int64(1)}
		if err = q.Insert(ctx, "term_groups", group); err != nil {
			t.Fatal(err)
		}
		if unresolved, err := q.Unresolved(ctx); err != nil || unresolved {
			t.Fatal("resolved references reported", unresolved, err)
		}
		if n, err := q.Count(ctx, "term_sets", portable.Row{"group_id": "group-1", "is_open": true}); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		if err = q.Update(ctx, "term_sets", portable.Row{"id": "set-1"}, portable.Row{"description": "Shades"}); err != nil {
			t.Fatal(err)
		}
		found, err := q.Find(ctx, "term_sets", portable.Row{"id": "set-1"})
		if err != nil || found["description"] != "Shades" || found["is_open"] != true || found["created_at"] != now {
			t.Fatal(found, err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPortable(t *testing.T) { daltest.Engines(t, runPortableSuite) }
