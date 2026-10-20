package dms

import (
	"testing"

	"papergo/internal/dal/revisions"
)

// countRevisions counts the revisions of an item, or of every item.
func countRevisions(s *Service, itemID string) (int, error) {
	rows, err := s.DAL.Revisions().Revisions(testContext, revisions.Filter{ItemID: itemID})
	return len(rows), err
}

// countHistory counts every history entry.
func countHistory(s *Service) (int, error) {
	rows, err := s.DAL.Revisions().Entries(testContext, revisions.HistoryFilter{})
	return len(rows), err
}

// countPublications counts the publication events of an item, or of every item.
func countPublications(s *Service, itemID string) (int, error) {
	rows, err := s.DAL.Revisions().Publications(testContext, revisions.PublicationFilter{ItemID: itemID})
	return len(rows), err
}

// publication returns a publication event.
func findPublication(t *testing.T, s *Service, id string) revisions.Publication {
	t.Helper()
	rows, err := s.DAL.Revisions().Publications(testContext, revisions.PublicationFilter{ID: id})
	if err != nil || len(rows) != 1 {
		t.Fatal("publication", id, rows, err)
	}
	return rows[0]
}
