package dms

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"papergo/internal/dal/resources"
	"strings"
	"testing"
	"time"
)

func TestDeltaDurableEditsDeletionAndAccess(t *testing.T) {
	s, w, list := fixture(t)
	a := create(t, s, list.ID, "item", "A", nil)
	b := create(t, s, list.ID, "item", "B", nil)
	first, err := s.Delta(testContext, "alice", list.ID, "", "", 1)
	if err != nil || len(first.Data) != 1 || first.NextToken == "" {
		t.Fatal(first, err)
	}
	// Continuations and anchors survive replacement of the service instance.
	s = NewService(s.db)
	last, err := s.Delta(testContext, "alice", list.ID, "", first.NextToken, 1)
	if err != nil || last.DeltaToken == "" {
		t.Fatal(last, err)
	}
	name := "Edited"
	if _, err = s.Update(testContext, "alice", a.ID, a.Version, UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(testContext, "alice", b.ID, b.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Delta(testContext, "alice", list.ID, last.DeltaToken, "", 10)
	if err != nil || len(changed.Data) != 2 {
		t.Fatal(changed, err)
	}
	for _, entry := range changed.Data {
		if entry.ID == a.ID && entry.Name != name {
			t.Fatal(entry)
		}
		if entry.ID == b.ID && (entry.Removed == nil || entry.Removed.Reason != "deleted") {
			t.Fatal(entry)
		}
	}
	if _, err = s.Delta(testContext, "bob", list.ID, last.DeltaToken, "", 10); !errors.Is(err, ErrDeltaExpired) {
		t.Fatal("foreign anchor", err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(testContext, "alice", a.ID, latest(t, s, a.ID).Version); err != nil {
		t.Fatal(err)
	}
	visible, err := s.Delta(testContext, "bob", list.ID, "", "", 10)
	if err != nil || len(visible.Data) != 1 {
		t.Fatal(visible, err)
	}
	a = latest(t, s, a.ID)
	if _, err = s.SetPermissions(testContext, "alice", a.ID, a.Version, Permissions{Inherit: false, Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	lost, err := s.Delta(testContext, "bob", list.ID, visible.DeltaToken, "", 10)
	if err != nil || len(lost.Data) != 1 || lost.Data[0].Removed == nil || lost.Data[0].Removed.Reason != "changed" {
		t.Fatal(lost, err)
	}
	// Account/collection access loss only returns IDs the caller already knew.
	if err = s.RemoveWorkspaceMember(testContext, "alice", w.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	empty, err := s.Delta(testContext, "bob", list.ID, lost.DeltaToken, "", 10)
	if err != nil || len(empty.Data) != 0 {
		t.Fatal(empty, err)
	}
	if err = s.CollectExpiredDeltas(testContext, time.Now().Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	firstPage, err := base64.RawURLEncoding.DecodeString(first.NextToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{strings.Split(string(firstPage), ".")[0], last.DeltaToken} {
		row, err := s.DAL.Delta().Token(testContext, id)
		if err != nil || string(row.Changes) != "[]" {
			t.Fatal("retained page metadata", row, err)
		}
	}
	if _, err = s.Delta(testContext, "alice", list.ID, "", first.NextToken, 10); !errors.Is(err, ErrDeltaExpired) {
		t.Fatal("expired page", err)
	}
	if err = s.DAL.Delta().Renew(testContext, last.DeltaToken, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Delta(testContext, "alice", list.ID, last.DeltaToken, "", 10); !errors.Is(err, ErrDeltaExpired) {
		t.Fatal(err)
	}
	if err = s.CollectExpiredDeltas(testContext, time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if count, err := s.DAL.Delta().CountStates(testContext); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestDeltaCachedPagesRecheckAccess(t *testing.T) {
	s, w, list := fixture(t)
	a := create(t, s, list.ID, "item", "A", nil)
	b := create(t, s, list.ID, "item", "B", nil)
	for _, item := range []*resources.Resource{a, b} {
		if _, err := s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	page, err := s.Delta(testContext, "bob", list.ID, "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page.Data)
	var entries []DeltaEntry
	if err = json.Unmarshal(raw, &entries); err != nil || entries[0].ID == "" || entries[0].Name == "" {
		t.Fatal(entries, err)
	}
	for _, item := range []*resources.Resource{a, b} {
		if _, err = s.SetPermissions(testContext, "alice", item.ID, latest(t, s, item.ID).Version, Permissions{Inherit: false, Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
			t.Fatal(err)
		}
	}
	remaining, err := s.Delta(testContext, "bob", list.ID, "", page.NextToken, 10)
	if err != nil || len(remaining.Data) != 1 || remaining.Data[0].Removed == nil || remaining.Data[0].Resource != nil {
		t.Fatal(remaining, err)
	}
	if err = s.CollectExpiredDeltas(testContext, time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Delta(testContext, "bob", list.ID, "", page.NextToken, 10); !errors.Is(err, ErrDeltaExpired) {
		t.Fatal(err)
	}
}
