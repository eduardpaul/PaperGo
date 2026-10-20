package workflow

import (
	"context"
	"errors"
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
)

func TestEnsureDefaultsRetriesAfterRollback(t *testing.T) {
	db := daltest.Database(t)
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
	if rows, err := s.DAL.Workflows().List(ctx, workflows.Filter{}); err != nil || len(rows) != 0 {
		t.Fatalf("rollback: %d %v", len(rows), err)
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
	if rows, err := s.DAL.Workflows().List(ctx, workflows.Filter{}); err != nil || len(rows) != want {
		t.Fatalf("retry and repeat: %d, want %d: %v", len(rows), want, err)
	}
}
