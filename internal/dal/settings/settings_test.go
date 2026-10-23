package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/settings"
)

func runSettingSuite(t *testing.T, newStore func(testing.TB) *dal.Store) {
	t.Run("LevelsVersionsAndPurge", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		f := daltest.NewFixture(t, s)
		repo := s.Settings()
		if _, found, err := repo.Find(ctx, settings.KeyComments, ""); err != nil || found {
			t.Fatalf("unset organization: %v %v", found, err)
		}
		org, err := repo.Put(ctx, settings.KeyComments, "", json.RawMessage(`{"enabled":true}`), 0, "admin")
		if err != nil || org.Version != 1 {
			t.Fatalf("organization: %+v %v", org, err)
		}
		if _, err = repo.Put(ctx, settings.KeyComments, "", json.RawMessage(`{}`), 0, "admin"); !errors.Is(err, dberr.ErrConcurrentUpdate) {
			t.Fatalf("a second first write: %v", err)
		}
		if _, err = repo.Put(ctx, settings.KeyComments, f.Source, json.RawMessage(`{"mode":"readers"}`), 0, "alice"); err != nil {
			t.Fatal(err)
		}
		ws, err := repo.Put(ctx, settings.KeyComments, f.Workspace, json.RawMessage(`{"mode":"off"}`), 0, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if ws, err = repo.Put(ctx, settings.KeyComments, f.Workspace, json.RawMessage(`{"mode":"collaborators"}`), 1, "alice"); err != nil || ws.Version != 2 {
			t.Fatalf("update: %+v %v", ws, err)
		}
		chain, err := repo.Chain(ctx, settings.KeyComments, []string{f.Source, f.Workspace, f.Destination})
		var enabled struct{ Enabled bool }
		if err != nil || len(chain) != 3 || json.Unmarshal(chain[""].Value, &enabled) != nil || !enabled.Enabled || chain[f.Destination].ID != "" {
			t.Fatalf("chain: %+v %v", chain, err)
		}
		var mode struct{ Mode string }
		if err = json.Unmarshal(chain[f.Workspace].Value, &mode); err != nil || mode.Mode != "collaborators" {
			t.Fatalf("workspace value: %s %v", chain[f.Workspace].Value, err)
		}
		if err = s.Resources().Purge(ctx, f.Source); err != nil {
			t.Fatal(err)
		}
		if _, found, err := repo.Find(ctx, settings.KeyComments, f.Source); err != nil || found {
			t.Fatalf("a purged collection kept its setting: %v %v", found, err)
		}
	})
}

func TestSettings(t *testing.T) { daltest.Engines(t, runSettingSuite) }
