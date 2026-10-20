package dms

import (
	"errors"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/identity"
	"papergo/internal/requestmeta"
	"slices"
	"testing"
	"time"
)

func TestOrganizationAuditFiltersPropertiesAndPaging(t *testing.T) {
	s, _, list := fixture(t)
	ctx := requestmeta.WithTrace(testContext, "request-trace-123")
	item := create(t, s, list.ID, "item", "A", nil)
	name := "B"
	if _, err := s.Update(ctx, "alice", item.ID, item.Version, UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Directory().SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Group"}); err != nil {
		t.Fatal(err)
	}
	page, err := s.OrganizationAudit(ctx, "alice", AuditQuery{EntityID: item.ID, Limit: 1})
	if err != nil || len(page.Data) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	change := page.Data[0]
	if change.Action != "resource.update" || change.EntityType != "item" || change.TraceID != "request-trace-123" || !slices.Equal(change.Properties, []string{"name"}) {
		t.Fatal(change)
	}
	older, err := s.OrganizationAudit(ctx, "alice", AuditQuery{EntityID: item.ID, After: page.NextCursor, Limit: 1})
	if err != nil || len(older.Data) != 1 || older.Data[0].Action != "resource.create" || older.NextCursor != "" {
		t.Fatal(older, err)
	}
	identityEvents, err := s.OrganizationAudit(ctx, "alice", AuditQuery{Actor: "alice", EntityType: "identity.group", Limit: 10})
	if err != nil || len(identityEvents.Data) != 1 || identityEvents.Data[0].TraceID != "request-trace-123" {
		t.Fatal(identityEvents, err)
	}
	end := change.CreatedAt
	exclusive, err := s.OrganizationAudit(ctx, "alice", AuditQuery{EntityID: item.ID, To: &end, Limit: 10})
	if err != nil || len(exclusive.Data) != 1 {
		t.Fatal(exclusive, err)
	}
	if _, err = s.OrganizationAudit(ctx, "bob", AuditQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("audit scope", err)
	}
	if _, err = s.OrganizationAudit(ctx, "alice", AuditQuery{After: "garbage"}); err == nil {
		t.Fatal("invalid continuation accepted")
	}
	// Equal timestamps use descending IDs as the stable second key.
	tie := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if _, err = s.DAL.Audit().Append(ctx, auditstore.Event{CreatedAt: tie, Subject: "alice", Action: "tie.update", EntityType: "tie", TraceID: "tie"}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	after := ""
	previous := ""
	for {
		page, err = s.OrganizationAudit(ctx, "alice", AuditQuery{EntityType: "tie", After: after, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range page.Data {
			if seen[event.ID] || previous != "" && event.ID >= previous {
				t.Fatal(event)
			}
			seen[event.ID] = true
			previous = event.ID
		}
		after = page.NextCursor
		if after == "" {
			break
		}
	}
	if len(seen) != 2 {
		t.Fatal(seen)
	}
}
