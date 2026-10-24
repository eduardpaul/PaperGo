package dms

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"papergo/internal/dal/directory"
	"papergo/internal/hosts"
	"papergo/internal/identity"
)

func TestTrustedHostsStartOpenAndChangeWithTheirVersion(t *testing.T) {
	s, _, _ := fixture(t)
	grantOrganization(t, s)
	ctx := t.Context()
	got, err := s.TrustedHosts(ctx)
	if err != nil || !slices.Equal(got.Hosts, []string{"*"}) || got.Version != 0 {
		t.Fatal(got, err)
	}
	if _, err = s.SetTrustedHosts(ctx, "alice", 0, []string{"10.0.0.1"}); err == nil {
		t.Fatal("an address was trusted")
	}
	got, err = s.SetTrustedHosts(ctx, "alice", 0, []string{"Calendar.Example.com", "*.example.org", "calendar.example.com"})
	if err != nil || !slices.Equal(got.Hosts, []string{"calendar.example.com", "*.example.org"}) || got.Version != 1 {
		t.Fatal(got, err)
	}
	if _, err = s.SetTrustedHosts(ctx, "alice", 0, []string{"*"}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("a stale version replaced the list", err)
	}
	if _, err = s.SetTrustedHosts(ctx, "bob", 1, []string{"*"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("a person without organization.manage changed the list", err)
	}
}

func TestIntegrationsFollowTrustedHosts(t *testing.T) {
	s, _, list, body := integrationFixture(t)
	grantOrganization(t, s)
	ctx := t.Context()
	*body = sourceFeed(sourceEvent)
	source := addIntegration(t, s, list.ID, "https://calendar.example.com/feed")
	if err := s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	items := len(integrationItems(t, s, source.ID))
	if items != 1 {
		t.Fatal("the feed was not applied", items)
	}
	use, err := s.TrustedHostUsage(ctx, "alice")
	if err != nil || len(use) != 1 || use[0] != (HostUse{Host: "calendar.example.com", Integrations: 1}) {
		t.Fatal(use, err)
	}

	if _, err = s.SetTrustedHosts(ctx, "alice", 0, []string{"*.example.org"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateIntegration(ctx, "alice", list.ID, IntegrationInput{Kind: IntegrationKindICalendar, Name: "Other", Configuration: mustIntegrationConfig("https://elsewhere.example.net/feed")}); err == nil || !strings.Contains(err.Error(), "elsewhere.example.net isn't a trusted host") {
		t.Fatal("an integration on an untrusted host was added", err)
	}
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); !errors.Is(err, hosts.ErrNotTrusted) {
		t.Fatal("a refresh reached an untrusted host", err)
	}
	row, err := s.DAL.Integrations().Find(ctx, source.ID)
	if err != nil || row.Error != HostNotTrusted || len(integrationItems(t, s, source.ID)) != items {
		t.Fatal("the refusal was not recorded, or items changed", row.Error, err)
	}

	if _, err = s.SetTrustedHosts(ctx, "alice", 1, []string{"*.example.org", "calendar.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if row, err = s.DAL.Integrations().Find(ctx, source.ID); err != nil || row.Error != "" {
		t.Fatal("the integration did not resume", row.Error, err)
	}
}

// grantOrganization lets alice manage the organization.
func grantOrganization(t *testing.T, s *Service) {
	t.Helper()
	role, err := s.Directory().SaveRole(t.Context(), "organization-admin", "", 0, identity.RoleInput{Name: "Organization administrator", Scopes: []string{"organization.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().SetAssignment(t.Context(), "organization-admin", role.ID, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
}
