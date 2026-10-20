package dal_test

import (
	"context"
	_ "modernc.org/sqlite"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"testing"
)

func TestExternalTransactionRollback(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)
		tx, err := s.SQLDB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		bound := s.WithTx(tx)
		if _, err := bound.Directory().CreateUser(ctx, "rolled-back", "Alice", "", true, false); err != nil {
			t.Fatal(err)
		}
		if user, err := bound.Directory().User(ctx, "rolled-back"); err != nil || user.ID != "rolled-back" {
			t.Fatalf("transaction read: %+v, %v", user, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if n := daltest.Count(t, s, "identity_users", daltest.Row{"id": "rolled-back"}); n != 0 {
			t.Fatalf("transaction escaped: %d rows", n)
		}
	})
}
