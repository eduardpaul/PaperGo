package integrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/integrations"
)

func TestIntegrations_SQLite(t *testing.T)   { testIntegrations(t, daltest.SQLiteStore(t)) }
func TestIntegrations_Postgres(t *testing.T) { testIntegrations(t, daltest.PostgresStore(t)) }
func testIntegrations(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	repo := s.Integrations()
	if _, err := repo.Create(ctx, integrations.Integration{ContainerID: f.Source, Kind: "feed", Name: "Feed", Generation: "g", ConfigHash: "h", ConfiguredBy: "alice", ConfigCiphertext: []byte("secret")}); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("library integration accepted: %v", err)
	}
	list, err := s.Resources().Create(ctx, resources.New{ID: "list", WorkspaceID: f.Workspace, ParentID: f.Workspace, Kind: refs.KindList, Name: "List"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.Create(ctx, integrations.Integration{ContainerID: list.ID, Kind: "feed", Name: "Feed", Generation: "g", ConfigHash: "h", ConfiguredBy: "alice", ConfigCiphertext: []byte("secret"), CredentialID: "token", CredentialScopes: []string{"item.write"}})
	if err != nil {
		t.Fatal(err)
	}
	listType, _, err := s.Catalog().Attach(ctx, list.ID, "type", true)
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Resources().Create(ctx, resources.New{ID: "entry", WorkspaceID: f.Workspace, ParentID: list.ID, ContainerID: list.ID, ContentTypeID: listType.ID, Kind: refs.KindItem, Name: "Entry"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddItem(ctx, integrations.Item{ItemID: f.Item, IntegrationID: source.ID, ExternalID: "x", ProviderKind: "feed"}); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("item of another collection bound: %v", err)
	}
	if _, err := repo.AddItem(ctx, integrations.Item{ItemID: item.ID, IntegrationID: source.ID, ExternalID: "x", ProviderKind: "feed", ManagedFields: []string{"$name"}}); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Minute)
	source.LeaseToken, source.LeaseUntil, source.State, source.Paused = "lease", &until, json.RawMessage(`{"etag":"e"}`), true
	if err := repo.Save(ctx, &source); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Find(ctx, source.ID)
	if err != nil || !stored.Leased(time.Now()) || string(stored.ConfigCiphertext) != "secret" || stored.CredentialScopes[0] != "item.write" || !stored.Paused {
		t.Fatalf("saved integration: %+v %v", stored, err)
	}
	if active, err := repo.List(ctx, integrations.Filter{Unpaused: true}); err != nil || len(active) != 0 {
		t.Fatalf("paused integration listed as active: %+v %v", active, err)
	}
	if err := repo.ReleaseLease(ctx, source.ID, "other"); err != nil {
		t.Fatal(err)
	}
	if stored, _ = repo.Find(ctx, source.ID); stored.LeaseToken != "lease" {
		t.Fatal("another worker released the lease")
	}
	if err := repo.ReleaseLease(ctx, source.ID, "lease"); err != nil {
		t.Fatal(err)
	}
	if stored, _ = repo.Find(ctx, source.ID); stored.LeaseToken != "" || stored.LeaseUntil != nil {
		t.Fatalf("lease retained: %+v", stored)
	}
	if err := repo.Remove(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	bound, err := repo.Item(ctx, item.ID)
	if err != nil || bound.IntegrationID != "" || bound.ManagedFields[0] != "$name" {
		t.Fatalf("removed source kept its items: %+v %v", bound, err)
	}
	if detached, err := repo.Items(ctx, ""); err != nil || len(detached) != 1 {
		t.Fatalf("detached items: %+v %v", detached, err)
	}
	if n, err := repo.Count(ctx, integrations.Filter{ContainerID: list.ID}); err != nil || n != 0 {
		t.Fatalf("removed integration counted: %d %v", n, err)
	}
	if data, err := repo.RevisionData(ctx, "missing"); err != nil || data != nil {
		t.Fatalf("missing revision data: %+v %v", data, err)
	}
}
