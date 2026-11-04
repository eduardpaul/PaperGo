package tasks

import (
	"strings"
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/notifications"
	"papergo/internal/dms"
	"papergo/internal/notify"
)

// A cancelled task says who cancelled it and why, and takes its assignment
// notices with it: what they asked for no longer waits for anyone.
func TestCancelledTasksSayWhyAndLeaveNoNotices(t *testing.T) {
	ctx := t.Context()
	s := dms.NewService(daltest.Database(t).SQL)
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{DMS: s}
	inbox := func() []notify.View {
		t.Helper()
		page, err := (&notify.Service{DMS: s}).Inbox(ctx, "bob", notify.Filter{Type: notifications.TypeTaskAssigned})
		if err != nil {
			t.Fatal(err)
		}
		return page.Data
	}
	create := func() View {
		t.Helper()
		v, err := svc.Create(ctx, "alice", "", Input{WorkspaceID: ws.ID, Key: "diesel_measure", Title: "Measure the diesel barrel", Assignees: []string{"bob"}})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	task := create()
	if got := inbox(); len(got) != 1 {
		t.Fatalf("bob is told of the task: %+v", got)
	}
	if _, err = svc.Cancel(ctx, "alice", task.ID, strings.Repeat("x", MaxReason+1)); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("a reason too long: %v", err)
	}
	cancelled, err := svc.Cancel(ctx, "alice", task.ID, "  Measured in the list by Ana  ")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" || cancelled.CompletedBy == nil || *cancelled.CompletedBy != "alice" || cancelled.Comment != "Measured in the list by Ana" {
		t.Fatalf("the cancellation says who and why: %+v", cancelled)
	}
	if got := inbox(); len(got) != 0 {
		t.Fatalf("the notice goes with the task: %+v", got)
	}
	if _, err = svc.Cancel(ctx, "alice", task.ID, ""); err == nil {
		t.Fatal("a cancelled task is cancelled again")
	}

	// Without a reason, the task still takes its notice.
	task = create()
	if cancelled, err = svc.Cancel(ctx, "alice", task.ID, ""); err != nil || cancelled.Comment != "" {
		t.Fatalf("cancel without a reason: %+v %v", cancelled, err)
	}
	if got := inbox(); len(got) != 0 {
		t.Fatalf("no notice left: %+v", got)
	}
}
