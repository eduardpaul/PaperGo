package dms

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/resources"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"papergo/internal/calendar"
	"papergo/internal/dal/integrations"
	"papergo/internal/dal/operations"
	"papergo/internal/identity"
	"papergo/internal/integration"
	"papergo/internal/storage"
)

func integrationFixture(t *testing.T) (*Service, *resources.Resource, *resources.Resource, *string) {
	t.Helper()
	s, w, list := fixture(t)
	secrets, err := integration.NewSecrets(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureIntegrations(secrets)
	s.integrations.ical.validateURL = func(_ context.Context, raw string) (string, error) {
		u, err := calendar.ParseURL(raw)
		if err != nil {
			return "", err
		}
		return u.String(), nil
	}
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"
	s.integrations.ical.download = func(_ context.Context, _ string, _, _ string) (calendar.Download, error) {
		return calendar.Download{Body: []byte(body), ETag: "version"}, nil
	}
	return s, w, list, &body
}
func sourceFeed(events string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\n" + events + "END:VCALENDAR\n"
}

const sourceEvent = "BEGIN:VEVENT\nUID:a\nSUMMARY:Meeting\nDTSTART;TZID=Europe/Madrid:20261019T090000\nDTEND;TZID=Europe/Madrid:20261019T100000\nRRULE:FREQ=WEEKLY;COUNT=3\nEND:VEVENT\n"

func addIntegration(t *testing.T, s *Service, list string, url string) *IntegrationView {
	t.Helper()
	row, err := s.CreateIntegration(t.Context(), "alice", list, IntegrationInput{Kind: IntegrationKindICalendar, Name: "Work", Configuration: mustIntegrationConfig(url)})
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func integrationItems(t *testing.T, s *Service, id string) []integrations.Item {
	t.Helper()
	rows, err := s.DAL.Integrations().Items(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestIntegrationCreateWithListScopedManagerToken(t *testing.T) {
	s, w, list, body := integrationFixture(t)
	ctx := t.Context()
	*body = sourceFeed(sourceEvent)
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPermissions(ctx, "alice", list.ID, list.Version, Permissions{Inherit: false, Grants: []Permission{{PrincipalType: "user", PrincipalID: "bob", Action: "manage", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	token, err := s.Directory().CreateAccessToken(ctx, "bob", identity.AccessTokenInput{Name: "Feed", Scopes: []string{"list.read", "list.write"}})
	if err != nil {
		t.Fatal(err)
	}
	limited := identity.WithAccessToken(ctx, identity.AccessTokenIdentity{Subject: "bob", ID: token.Token.ID, Scopes: token.Token.Scopes})
	source, err := s.CreateIntegration(limited, "bob", list.ID, IntegrationInput{Kind: IntegrationKindICalendar, Name: "Feed", Configuration: mustIntegrationConfig("https://example.com/feed")})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if len(integrationItems(t, s, source.ID)) != 1 || storedIntegration(t, s, source.ID).CredentialID != token.Token.ID {
		t.Fatal("list-scoped integration did not retain its credential or import events")
	}
	// Selecting catalog types directly still requires the catalog read scope.
	if _, err = s.AttachContentType(limited, "bob", list.ID, list.Version, AttachContentTypeInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("catalog attachment bypassed its scope requirement", err)
	}
}

func TestIntegrationRevalidatesCredentialAfterDownload(t *testing.T) {
	for _, mode := range []string{"revoked", "expired", "revoked_not_modified"} {
		t.Run(mode, func(t *testing.T) {
			s, _, list, body := integrationFixture(t)
			ctx := t.Context()
			*body = sourceFeed(sourceEvent)
			token, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Feed", Scopes: []string{"list.read", "list.write"}})
			if err != nil {
				t.Fatal(err)
			}
			limited := identity.WithAccessToken(ctx, identity.AccessTokenIdentity{Subject: "alice", ID: token.Token.ID, Scopes: token.Token.Scopes})
			source, err := s.CreateIntegration(limited, "alice", list.ID, IntegrationInput{Kind: IntegrationKindICalendar, Name: "Feed", Configuration: mustIntegrationConfig("https://example.com/feed")})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.runOperations(ctx); err != nil {
				t.Fatal(err)
			}
			itemID := integrationItems(t, s, source.ID)[0].ItemID
			before, err := s.Get(ctx, "alice", itemID)
			if err != nil {
				t.Fatal(err)
			}
			stored := storedIntegration(t, s, source.ID)
			op, err := s.RefreshIntegration(limited, "alice", list.ID, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			s.integrations.ical.download = func(_ context.Context, _, _, _ string) (calendar.Download, error) {
				if mode == "expired" {
					_, err = daltest.TryUpdate(s.DAL, "access_tokens", daltest.Row{"expires_at": time.Now().UTC().Add(-time.Second)}, daltest.Row{"id": token.Token.ID})
				} else {
					err = s.Directory().RevokeAccessToken(ctx, "alice", token.Token.ID)
				}
				return calendar.Download{Body: []byte(sourceFeed(strings.ReplaceAll(sourceEvent, "Meeting", "Changed"))), ETag: "new-version", NotModified: mode == "revoked_not_modified"}, err
			}
			if worked, err := s.operationStep(ctx); !worked || !errors.Is(err, ErrForbidden) {
				t.Fatal(worked, err)
			}
			op, err = s.Operation(ctx, "alice", op.ID)
			if err != nil || op.Status != operations.StatusFailed {
				t.Fatal(op, err)
			}
			after, err := s.Get(ctx, "alice", itemID)
			if err != nil || after.Version != before.Version || *after.HeadRevisionID != *before.HeadRevisionID {
				t.Fatal("invalid credential changed existing events", after, err)
			}
			current, err := s.DAL.Integrations().Find(ctx, source.ID)
			if err != nil || !current.LastSuccessAt.Equal(*stored.LastSuccessAt) || !bytes.Equal(current.State, stored.State) || current.LeaseUntil != nil {
				t.Fatal("invalid credential changed refresh state or retained its lease", current, err)
			}
		})
	}
}

func TestIntegrationRefreshIdentityOwnershipAndReconciliation(t *testing.T) {
	s, _, list, body := integrationFixture(t)
	ctx := t.Context()
	*body = sourceFeed(sourceEvent)
	source := addIntegration(t, s, list.ID, "https://example.com/secret")
	if err := s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	rows := integrationItems(t, s, source.ID)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	itemID := rows[0].ItemID
	item, err := s.Get(ctx, "alice", itemID)
	if err != nil || item.Name != "Meeting" {
		t.Fatal(item, err)
	}
	if _, err = s.Update(ctx, "alice", itemID, item.Version, UpdateResource{Name: ptr("Changed")}); err == nil {
		t.Fatal("source title changed locally")
	}
	changes := map[string]any{"event_location": "Changed"}
	if _, err = s.Update(ctx, "alice", itemID, item.Version, UpdateResource{Values: &changes}); err == nil {
		t.Fatal("source field changed locally")
	}
	keywords := []string{"local"}
	_, err = s.Update(ctx, "alice", itemID, item.Version, UpdateResource{Keywords: &keywords})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RefreshIntegration(ctx, "bob", list.ID, source.ID); err == nil {
		t.Fatal("unreadable integration refreshed")
	}
	second := addIntegration(t, s, list.ID, "https://example.com/other")
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	secondRows := integrationItems(t, s, second.ID)
	if len(secondRows) != 1 || secondRows[0].ItemID == itemID {
		t.Fatal("subscription identities collided")
	}
	if _, err = s.CreateIntegration(ctx, "alice", list.ID, IntegrationInput{Kind: IntegrationKindICalendar, Name: "Duplicate", Configuration: mustIntegrationConfig("https://example.com/secret")}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate accepted", err)
	}
	// Invalid snapshot is a failed operation and keeps every existing item.
	*body = "broken"
	op, err := s.RefreshIntegration(ctx, "alice", list.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err == nil {
		t.Fatal("bad feed succeeded")
	}
	failed, err := s.Operation(ctx, "alice", op.ID)
	if err != nil || failed.Status != operations.StatusFailed {
		t.Fatal(failed, err)
	}
	item, err = s.Get(ctx, "alice", itemID)
	if err != nil || len(item.Keywords) != 1 {
		t.Fatal(item, err)
	}
	// Empty is a valid complete snapshot: only this subscription is recycled.
	*body = sourceFeed("")
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	deleted, _ := s.DAL.Resources().Resource(ctx, itemID)
	untouched, _ := s.DAL.Resources().Resource(ctx, secondRows[0].ItemID)
	if deleted.DeletedAt == nil || untouched.DeletedAt != nil {
		t.Fatal("incorrect removal scope")
	}
	*body = sourceFeed(sourceEvent)
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	item, err = s.Get(ctx, "alice", itemID)
	if err != nil || len(item.Keywords) != 1 {
		t.Fatal("identity or local metadata lost", item, err)
	}
	// Paused still owns fields; removal detaches existing content.
	source, err = s.UpdateIntegration(ctx, "alice", list.ID, source.ID, source.Version, UpdateIntegration{Paused: ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err == nil {
		t.Fatal("paused source refreshed")
	}
	if err = s.RemoveIntegration(ctx, "alice", list.ID, source.ID, source.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(ctx, "alice", itemID, item.Version, UpdateResource{Name: ptr("Local")}); err != nil {
		t.Fatal(err)
	}
}
func TestIntegrationSecretsGenericStorageResetAndPublishedRecurrence(t *testing.T) {
	s, w, list, body := integrationFixture(t)
	ctx := t.Context()
	*body = sourceFeed(sourceEvent)
	source := addIntegration(t, s, list.ID, "https://example.com/super-secret-token")
	if err := s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	row := integrationItems(t, s, source.ID)[0]
	item, _ := s.Get(ctx, "alice", row.ItemID)
	// Provider payload lives in a generic revision store, never core surfaces.
	data, err := s.revisionIntegrationData(ctx, *item.HeadRevisionID)
	if err != nil || data == nil || data.ProviderKind != IntegrationKindICalendar {
		t.Fatal(data, err)
	}
	sources, err := s.ListIntegrations(ctx, "alice", list.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sources)
	if strings.Contains(string(raw), "super-secret") || strings.Contains(string(raw), "ciphertext") || strings.Contains(string(raw), "etag") {
		t.Fatal("secret leaked", string(raw))
	}
	stored := daltest.Values(t, s.DAL, "list_integrations", "config_ciphertext", daltest.Row{"id": source.ID})
	if len(stored) != 1 || strings.Contains(fmt.Sprint(stored[0]), "super-secret") {
		t.Fatal("plaintext configuration")
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	before, err := s.CalendarOccurrences(ctx, "bob", list.ID, from, to, "published")
	if err != nil || len(before) != 2 {
		t.Fatal(before, err)
	}
	*body = sourceFeed(strings.ReplaceAll(sourceEvent, "COUNT=3", "COUNT=1"))
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.CalendarOccurrences(ctx, "bob", list.ID, from, to, "published")
	if err != nil || len(after) != 2 {
		t.Fatal("draft recurrence leaked", after, err)
	}
	draft, err := s.CalendarOccurrences(ctx, "alice", list.ID, from, to, "head")
	if err != nil || len(draft) != 1 {
		t.Fatal(draft, err)
	}
	// Same-feed URL rotation retains IDs. A different feed detaches and starts new IDs.
	source, err = s.UpdateIntegration(ctx, "alice", list.ID, source.ID, source.Version, UpdateIntegration{Configuration: ptr(mustIntegrationConfig("https://example.com/rotated"))})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if integrationItems(t, s, source.ID)[0].ItemID != item.ID {
		t.Fatal("rotation changed identity")
	}
	source, err = s.UpdateIntegration(ctx, "alice", list.ID, source.ID, source.Version, UpdateIntegration{Configuration: ptr(mustIntegrationConfig("https://example.com/new-feed")), Reset: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if integrationItems(t, s, source.ID)[0].ItemID == item.ID {
		t.Fatal("reset reused old identity")
	}
	old, _ := s.Get(ctx, "alice", item.ID)
	if _, err = s.Update(ctx, "alice", item.ID, old.Version, UpdateResource{Name: ptr("Kept locally")}); err != nil {
		t.Fatal(err)
	}
}
func TestIntegrationDownloadDoesNotHoldWriterAndLeaseBlocksConfiguration(t *testing.T) {
	s, _, list, body := integrationFixture(t)
	*body = sourceFeed(sourceEvent)
	source := addIntegration(t, s, list.ID, "https://example.com/feed")
	started := make(chan struct{})
	finish := make(chan struct{})
	original := s.integrations.ical.download
	s.integrations.ical.download = func(ctx context.Context, u, a, b string) (calendar.Download, error) {
		close(started)
		<-finish
		return original(ctx, u, a, b)
	}
	done := make(chan error, 1)
	go func() { _, err := s.operationStep(t.Context()); done <- err }()
	<-started
	if _, err := s.UpdateIntegration(t.Context(), "alice", list.ID, source.ID, source.Version, UpdateIntegration{Paused: ptr(true)}); !errors.Is(err, ErrIntegrationBusy) {
		close(finish)
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), "alice", list.ID, CreateResource{Kind: "item", Name: "Independent local item"}); err != nil {
		close(finish)
		t.Fatal(err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationSnapshotRollsBackAllWritesOnApplyFailure(t *testing.T) {
	s, _, list, body := integrationFixture(t)
	ctx := t.Context()
	b := strings.ReplaceAll(sourceEvent, "UID:a", "UID:b")
	*body = sourceFeed(sourceEvent + b)
	source := addIntegration(t, s, list.ID, "https://example.com/feed")
	if err := s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	rows := integrationItems(t, s, source.ID)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	// The first item would update, then a later item's invalid title fails.
	// No earlier write or absent-event removal may survive the failed snapshot.
	bad := strings.ReplaceAll(b, "Meeting", strings.Repeat("x", 5000))
	*body = sourceFeed(strings.ReplaceAll(sourceEvent, "Meeting", "Changed first") + bad)
	if _, err := s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.runOperations(ctx); err == nil {
		t.Fatal("invalid title accepted")
	}
	for _, row := range rows {
		item, err := s.Get(ctx, "alice", row.ItemID)
		if err != nil || item.Name != "Meeting" {
			t.Fatal("partial snapshot committed", item, err)
		}
	}
}
func TestGenericIntegrationRevisionDataAndFieldOwnership(t *testing.T) {
	s, _, list, _ := integrationFixture(t)
	ctx := t.Context()
	// The generic stores accept another provider's payload and ownership keys
	// without adding any calendar fields or columns to the ordinary item model.
	source, err := s.DAL.Integrations().Create(ctx, integrations.Integration{ContainerID: list.ID, Kind: "example.connector", Name: "Example", Generation: uuid.NewString(), ConfiguredBy: "alice", ConfigCiphertext: []byte("opaque-encrypted-configuration"), ConfigHash: "example-configuration"})
	if err != nil {
		t.Fatal(err)
	}
	var item *resources.Resource
	err = s.Write(ctx, func(t *Service) error {
		if err := t.SetIntegrationData("example.connector", json.RawMessage(`{"external_version":"v1","arbitrary":{"flags":[1,2]}}`)); err != nil {
			return err
		}
		var err error
		item, err = t.Create(ctx, "alice", list.ID, CreateResource{Kind: "item", Name: "External record"})
		if err != nil {
			return err
		}
		_, err = t.DAL.Integrations().AddItem(ctx, integrations.Item{ItemID: item.ID, IntegrationID: source.ID, ProviderKind: "example.connector", ExternalID: "external-1", ManagedFields: []string{"$name"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Local title")}); err == nil {
		t.Fatal("generic ownership was ignored")
	}
	keywords := []string{"local"}
	item, err = s.Update(ctx, "alice", item.ID, item.Version, UpdateResource{Keywords: &keywords})
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.revisionIntegrationData(ctx, *item.HeadRevisionID)
	if err != nil || data == nil || data.ProviderKind != "example.connector" || !strings.Contains(string(data.Data), "arbitrary") {
		t.Fatal(data, err)
	}
}

func TestIntegrationPortablePackageRetainsGenericDataAndOmitsSecrets(t *testing.T) {
	s, _, list, body := integrationFixture(t)
	ctx := t.Context()
	*body = sourceFeed(sourceEvent)
	source := addIntegration(t, s, list.ID, "https://example.com/private-export-secret")
	if err := s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	itemID := integrationItems(t, s, source.ID)[0].ItemID
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var object storage.Object
	if err = s.Write(ctx, func(t *Service) error {
		var e error
		object, e = t.exportCorePackage(ctx, store, ExportOptions{})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	file, err := store.Open(ctx, object.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
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
	manifestData, err := io.ReadAll(manifest)
	manifest.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifestData), "private-export-secret") || strings.Contains(string(manifestData), "config_ciphertext") {
		t.Fatal("portable secret leaked")
	}
	target := emptyPackageTarget(t)
	var result PackageResult
	if err = target.Write(ctx, func(t *Service) error {
		var e error
		result, e = t.importCorePackage(ctx, store, "organization-admin", packagePayload{ObjectKey: object.Key})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if !result.Valid || len(result.Warnings) != 1 {
		t.Fatal(result)
	}
	count, err := target.DAL.Integrations().Count(ctx, integrations.Filter{})
	if err != nil || count != 0 {
		t.Fatal("configuration imported", count, err)
	}
	row, err := target.DAL.Integrations().Item(ctx, itemID)
	if err != nil || row.IntegrationID != "" {
		t.Fatal("item ownership was not detached", row, err)
	}
	item, err := target.Get(ctx, "alice", itemID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := target.revisionIntegrationData(ctx, *item.HeadRevisionID)
	if err != nil || data == nil || data.ProviderKind != IntegrationKindICalendar {
		t.Fatal(data, err)
	}
	workspaceObject, _ := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: list.WorkspaceID})
	populated, _, _ := fixture(t)
	workspaceResult, err := importWorkspaceForTest(t, populated, store, workspaceObject, false, "Imported integration")
	if err != nil || !workspaceResult.Valid || len(workspaceResult.Warnings) == 0 {
		t.Fatal(workspaceResult, err)
	}
	detached, err := populated.DAL.Integrations().Items(ctx, "")
	if err != nil || len(detached) != 1 || detached[0].ExternalID == "" || detached[0].ItemID == itemID {
		t.Fatal("workspace integration data not localized", detached, err)
	}
	if n, err := populated.DAL.Integrations().Count(ctx, integrations.Filter{}); err != nil || n != 0 {
		t.Fatal("workspace integration connection was imported", n, err)
	}

}

// storedIntegration reads an integration with its private settings.
func storedIntegration(t *testing.T, s *Service, id string) integrations.Integration {
	t.Helper()
	row, err := s.DAL.Integrations().Find(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}
