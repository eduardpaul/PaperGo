package notifications_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/live"
	"papergo/internal/dal/notifications"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
)

func runNotificationSuite(t *testing.T, newStore func(testing.TB) *dal.Store) {
	t.Run("InboxDedupReadAndDelete", func(t *testing.T) {
		ctx := context.Background()
		s, in := daltest.TaskFixture(t, newStore)
		repo := s.Notifications()
		message := notifications.Notification{Recipient: "bob", WorkspaceID: in.WorkspaceID, Type: notifications.TypeMessage, Title: "Hello", Source: notifications.SourceUser, Sender: "alice", DedupKey: "hello"}
		first, created, err := repo.Create(ctx, message)
		if err != nil || !created {
			t.Fatalf("first send: %v %v", created, err)
		}
		if _, created, err = repo.Create(ctx, message); err != nil || created {
			t.Fatalf("a repeated dedup key created another notification: %v %v", created, err)
		}
		message.Recipient = "carol"
		if _, created, err = repo.Create(ctx, message); err != nil || !created {
			t.Fatalf("the dedup key holds per recipient: %v %v", created, err)
		}
		message.Recipient, message.DedupKey, message.Title = "bob", "", "Second"
		second, _, err := repo.Create(ctx, message)
		if err != nil {
			t.Fatal(err)
		}
		task, err := s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = repo.Create(ctx, notifications.Notification{Recipient: "bob", Type: notifications.TypeTaskAssigned, Title: "Review", Source: notifications.SourceSystem, TaskID: task.ID, ItemID: "x"}); !errors.Is(err, dberr.ErrCheckViolation) {
			t.Fatalf("two links accepted: %v", err)
		}
		if _, _, err = repo.Create(ctx, notifications.Notification{Recipient: "bob", Type: "other", Title: "x", Source: notifications.SourceSystem}); !errors.Is(err, dberr.ErrCheckViolation) {
			t.Fatalf("unknown type accepted: %v", err)
		}
		linked, _, err := repo.Create(ctx, notifications.Notification{Recipient: "bob", WorkspaceID: in.WorkspaceID, Type: notifications.TypeTaskAssigned, Title: "Review", Source: notifications.SourceSystem, TaskID: task.ID})
		if err != nil {
			t.Fatal(err)
		}

		inbox, err := repo.List(ctx, notifications.Filter{Recipient: "bob"})
		if err != nil {
			t.Fatal(err)
		}
		if len(inbox) != 3 || inbox[0].ID != linked.ID || inbox[2].ID != first.ID || inbox[0].TaskID != task.ID {
			t.Fatalf("inbox is not newest first: %+v", inbox)
		}
		if n, err := repo.Unread(ctx, "bob"); err != nil || n != 3 {
			t.Fatalf("unread = %d, %v", n, err)
		}
		marked, err := repo.MarkRead(ctx, "bob", []string{first.ID, "missing"}, time.Time{})
		if err != nil || len(marked) != 1 || marked[0] != first.ID {
			t.Fatalf("marked %v, %v", marked, err)
		}
		if again, err := repo.MarkRead(ctx, "carol", []string{second.ID}, time.Time{}); err != nil || len(again) != 0 {
			t.Fatalf("someone else's notification was marked: %v %v", again, err)
		}
		unread, err := repo.List(ctx, notifications.Filter{Recipient: "bob", Unread: true})
		if err != nil || len(unread) != 2 {
			t.Fatalf("unread list: %d %v", len(unread), err)
		}
		if marked, err = repo.MarkRead(ctx, "bob", nil, second.CreatedAt); err != nil || len(marked) != 1 || marked[0] != second.ID {
			t.Fatalf("marked before: %v %v", marked, err)
		}
		if n, _ := repo.Unread(ctx, "bob"); n != 1 {
			t.Fatalf("unread after marking = %d", n)
		}
		page, err := repo.List(ctx, notifications.Filter{Recipient: "bob", Type: notifications.TypeMessage, Limit: 1})
		if err != nil || len(page) != 1 || page[0].ID != second.ID {
			t.Fatalf("first page by type: %+v %v", page, err)
		}
		page, err = repo.List(ctx, notifications.Filter{Recipient: "bob", Type: notifications.TypeMessage, Limit: 1, After: &notifications.Key{CreatedAt: page[0].CreatedAt, ID: page[0].ID}})
		if err != nil || len(page) != 1 || page[0].ID != first.ID || page[0].ReadAt == nil {
			t.Fatalf("next page by type: %+v %v", page, err)
		}

		if err = repo.Delete(ctx, "carol", second.ID); !errors.Is(err, dberr.ErrNotFound) {
			t.Fatalf("someone else deleted a notification: %v", err)
		}
		if err = repo.Delete(ctx, "bob", second.ID); err != nil {
			t.Fatal(err)
		}
		changes, err := s.LiveChanges().After(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		var kinds []live.Action
		for _, c := range changes {
			if c.Kind == live.KindNotification && c.EntityID == notifications.LiveID("bob", second.ID) {
				kinds = append(kinds, c.Action)
			}
		}
		if len(kinds) != 3 || kinds[0] != live.ActionCreated || kinds[1] != live.ActionUpdated || kinds[2] != live.ActionDeleted {
			t.Fatalf("live changes of a notification: %v", kinds)
		}
	})

	t.Run("PurgeTakesLinkedNotifications", func(t *testing.T) {
		ctx := context.Background()
		s, in := daltest.TaskFixture(t, newStore)
		list, err := s.Resources().Create(ctx, resources.New{ID: "list", Kind: refs.KindList, Name: "List", WorkspaceID: in.WorkspaceID, ParentID: in.WorkspaceID})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.Notifications().Create(ctx, notifications.Notification{Recipient: "bob", WorkspaceID: in.WorkspaceID, Type: notifications.TypeMessage, Title: "On a list", Source: notifications.SourceUser, ItemID: list.ID}); err != nil {
			t.Fatal(err)
		}
		if err = s.Resources().Purge(ctx, list.ID); err != nil {
			t.Fatal(err)
		}
		if inbox, err := s.Notifications().List(ctx, notifications.Filter{Recipient: "bob"}); err != nil || len(inbox) != 0 {
			t.Fatalf("a purged resource left its notifications: %+v %v", inbox, err)
		}
	})
}

func TestNotifications(t *testing.T) { daltest.Engines(t, runNotificationSuite) }
