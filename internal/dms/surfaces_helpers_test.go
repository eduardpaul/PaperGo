package dms

import (
	"testing"

	auditstore "papergo/internal/dal/audit"
	eventstore "papergo/internal/dal/events"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
)

func countSurfaces(s *Service, f surfaces.Filter) (int, error) {
	f.Fields = []string{}
	rows, err := s.DAL.Surfaces().Surfaces(testContext, f)
	return len(rows), err
}

func countFieldValues(s *Service, f surfaces.ValueFilter) (int, error) {
	rows, err := s.DAL.Surfaces().FieldValues(testContext, f)
	return len(rows), err
}

func countResources(s *Service, f resources.Filter) (int, error) {
	return s.DAL.Resources().Count(testContext, f)
}

// storedCounts counts the records a failed write must leave unchanged.
func storedCounts(t *testing.T, s *Service) map[string]int {
	t.Helper()
	out := map[string]int{}
	var err error
	if out["field_values"], err = countFieldValues(s, surfaces.ValueFilter{}); err != nil {
		t.Fatal(err)
	}
	if out["item_surfaces"], err = countSurfaces(s, surfaces.Filter{}); err != nil {
		t.Fatal(err)
	}
	if out["relationships"], err = s.DAL.Relationships().Count(testContext, relationships.Filter{}); err != nil {
		t.Fatal(err)
	}
	audit, err := s.DAL.Audit().Count(testContext, auditstore.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.DAL.Events().Count(testContext, eventstore.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	revs, err := s.DAL.Revisions().Revisions(testContext, revisions.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	out["audit_events"], out["domain_events"], out["item_revisions"] = int(audit), int(events), len(revs)
	return out
}
