package apps_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/apps"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
)

func content(source string) apps.Content {
	return apps.Content{Source: source, Bundle: "bundle of " + source, BundleHash: strings.Repeat("a", 63) + string(source[0]), ParametersSchema: json.RawMessage(`{"type":"object"}`)}
}

func TestApps(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)
		ws, err := s.Resources().Create(ctx, resources.New{ID: "workspace", Kind: refs.KindWorkspace, Name: "Farm"})
		if err != nil {
			t.Fatal(err)
		}
		in := apps.CreateInput{WorkspaceID: ws.ID, Key: "diesel", Name: "Diesel tracker", Parameters: map[string]any{"list": "l1", "capacity": json.Number("200.5")}, Content: content("1"), CreatedBy: "alice"}

		t.Run("CreatesADraftWithItsFirstVersion", func(t *testing.T) {
			a, err := s.Apps().Create(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Apps().Find(ctx, a.ID)
			if err != nil || got.State != apps.StateDraft || got.CurrentVersion != 1 || got.Version != 1 || got.Parameters["list"] != "l1" || got.UpdatedBy != "alice" {
				t.Fatalf("stored app: %+v %v", got, err)
			}
			if n, _ := got.Parameters["capacity"].(json.Number); n.String() != "200.5" {
				t.Fatalf("parameters lost their exact number: %#v", got.Parameters["capacity"])
			}
			v, err := s.Apps().Version(ctx, a.ID, 1)
			if err != nil || v.Source != "1" || v.Bundle != "bundle of 1" || string(v.ParametersSchema) != `{"type":"object"}` && string(v.ParametersSchema) != `{"type": "object"}` {
				t.Fatalf("first version: %+v %v", v, err)
			}
			if schema, err := s.Apps().Schema(ctx, a.ID, 1); err != nil || string(schema) != string(v.ParametersSchema) {
				t.Fatalf("the first version's form alone: %s %v", schema, err)
			}
			if _, err := s.Apps().Create(ctx, in); !errors.Is(err, dberr.ErrUniqueViolation) {
				t.Fatalf("a second app with the key: %v", err)
			}
			other := in
			other.Key = "other"
			if _, err := s.Apps().Create(ctx, other); !errors.Is(err, dberr.ErrUniqueViolation) {
				t.Fatalf("a second app with the name: %v", err)
			}
			other.Name, other.WorkspaceID = "Other", "missing"
			if _, err := s.Apps().Create(ctx, other); !errors.Is(err, dberr.ErrInvalidContainment) {
				t.Fatalf("an app outside a workspace: %v", err)
			}
		})

		t.Run("RevisesWithVersionsAndDeletes", func(t *testing.T) {
			second := in
			second.Key, second.Name = "second", "Second"
			a, err := s.Apps().Create(ctx, second)
			if err != nil {
				t.Fatal(err)
			}
			published, name := apps.StatePublished, "Second app"
			same := content("1")
			a, v, err := s.Apps().Revise(ctx, a.ID, a.Version, "bob", &same, apps.Changes{Name: &name, State: &published})
			if err != nil || v.Number != 1 || a.CurrentVersion != 1 || a.Version != 2 || a.Name != name || a.State != published || a.UpdatedBy != "bob" {
				t.Fatalf("a change without new content: %+v %+v %v", a, v, err)
			}
			changed := content("2")
			a, v, err = s.Apps().Revise(ctx, a.ID, a.Version, "bob", &changed, apps.Changes{})
			if err != nil || v.Number != 2 || v.CreatedBy != "bob" || a.CurrentVersion != 2 {
				t.Fatalf("new content: %+v %+v %v", a, v, err)
			}
			if _, _, err = s.Apps().Revise(ctx, a.ID, a.Version-1, "bob", nil, apps.Changes{Name: &name}); !errors.Is(err, dberr.ErrConcurrentUpdate) {
				t.Fatalf("a stale change: %v", err)
			}
			versions, err := s.Apps().Versions(ctx, a.ID)
			if err != nil || len(versions) != 2 || versions[0].Number != 2 || versions[1].Source != "1" {
				t.Fatalf("versions: %+v %v", versions, err)
			}
			listed, err := s.Apps().List(ctx, apps.Filter{WorkspaceID: ws.ID, State: apps.StatePublished})
			if err != nil || len(listed) != 1 || listed[0].ID != a.ID {
				t.Fatalf("published apps: %+v %v", listed, err)
			}
			if err = s.Apps().Delete(ctx, a.ID, a.Version-1); !errors.Is(err, dberr.ErrConcurrentUpdate) {
				t.Fatalf("a stale delete: %v", err)
			}
			if err = s.Apps().Delete(ctx, a.ID, a.Version); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Apps().Find(ctx, a.ID); !dberr.IsNotFound(err) {
				t.Fatalf("a deleted app: %v", err)
			}
			if versions, err = s.Apps().Versions(ctx, a.ID); err != nil || len(versions) != 0 {
				t.Fatalf("a deleted app's versions: %+v %v", versions, err)
			}
		})
	})
}
