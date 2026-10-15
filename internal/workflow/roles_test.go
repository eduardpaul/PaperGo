package workflow

import (
	"context"
	"errors"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/testutil"
)

func TestEnsureDefaultsRetriesAfterRollback(t *testing.T) {
	db := testutil.Database(t)
	s := dms.NewService(db.SQL)
	ctx := context.Background()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("dispatch failed")
	err = s.Write(ctx, func(tx *dms.Service) error {
		if err := EnsureDefaults(ctx, tx, ws.ID); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if count, err := s.Client.Workflow.Query().Count(ctx); err != nil || count != 0 {
		t.Fatalf("rollback: %d %v", count, err)
	}
	for range 2 {
		if err = s.Write(ctx, func(tx *dms.Service) error { return EnsureDefaults(ctx, tx, ws.ID) }); err != nil {
			t.Fatal(err)
		}
	}
	want := 0
	for _, b := range BuiltIns() {
		if b.Default {
			want++
		}
	}
	if count, err := s.Client.Workflow.Query().Count(ctx); err != nil || count != want {
		t.Fatalf("retry and repeat: %d, want %d: %v", count, want, err)
	}
}
