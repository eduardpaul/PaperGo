package dms

import (
	"errors"
	"testing"
	"time"
)

func TestLivePositionsCommitRollbackAndExpiry(t *testing.T) {
	s, _, list := fixture(t)
	before, err := s.LivePosition(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := create(t, s, list.ID, "item", "A", nil)
	changes, err := s.LiveChanges(testContext, before)
	if err != nil || len(changes) == 0 || changes[0].EntityID != item.ID {
		t.Fatal(changes, err)
	}
	head, err := s.LivePosition(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.LivePosition(testContext, &before); err != nil {
		t.Fatal(err)
	}
	future := head + 1
	if _, err = s.LivePosition(testContext, &future); err == nil {
		t.Fatal("future cursor accepted")
	}
	name := "Failed"
	if _, err = s.Update(testContext, "alice", item.ID, item.Version+1, UpdateResource{Name: &name}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal(err)
	}
	if after, err := s.LivePosition(testContext, nil); err != nil || after != head {
		t.Fatal("failed write emitted a change", after, head, err)
	}
	if err = s.CollectExpiredLiveChanges(testContext, time.Now().Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if changes, err := s.LiveChanges(testContext, 0); err != nil || len(changes) != 0 {
		t.Fatal(changes, err)
	}
	if _, err = s.LivePosition(testContext, &before); !errors.Is(err, ErrLiveExpired) {
		t.Fatal(err)
	}
	// The committed cursor never reuses a position after collecting the whole journal.
	create(t, s, list.ID, "item", "B", nil)
	changes, err = s.LiveChanges(testContext, 0)
	if err != nil || len(changes) == 0 || changes[len(changes)-1].ID <= head {
		t.Fatal(changes, head, err)
	}
}
