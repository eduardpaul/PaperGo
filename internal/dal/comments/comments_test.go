package comments_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/comments"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/live"
)

func runCommentSuite(t *testing.T, newStore func(testing.TB) *dal.Store) {
	t.Run("ThreadsRepliesMentionsAndCounts", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		for _, id := range []string{"alice", "bob", "carol"} {
			if _, err := s.Directory().CreateUser(ctx, id, id, "", true, false); err != nil {
				t.Fatal(err)
			}
		}
		f := daltest.NewFixture(t, s)
		item, library := f.Item, f.Source
		repo := s.Comments()
		root, err := repo.Create(ctx, comments.Comment{ItemID: item, Author: "alice", Source: comments.SourceUser, Body: "Is this signed? <@user:bob>", Mentions: []string{"bob", "bob"}})
		if err != nil || !root.Root() || len(root.Mentions) != 1 {
			t.Fatalf("root: %+v %v", root, err)
		}
		reply, err := repo.Create(ctx, comments.Comment{ItemID: item, ThreadID: root.ID, Author: "bob", Source: comments.SourceUser, Body: "Yes"})
		if err != nil || reply.Root() {
			t.Fatalf("reply: %+v %v", reply, err)
		}
		if _, err = repo.Create(ctx, comments.Comment{ItemID: item, ThreadID: reply.ID, Author: "bob", Source: comments.SourceUser, Body: "Nested"}); !errors.Is(err, dberr.ErrInvalidContainment) {
			t.Fatalf("a reply to a reply: %v", err)
		}
		if _, err = repo.Create(ctx, comments.Comment{ItemID: library, Author: "bob", Source: comments.SourceUser, Body: "On a list"}); !errors.Is(err, dberr.ErrInvalidContainment) {
			t.Fatalf("a comment on a list: %v", err)
		}
		if _, err = repo.Create(ctx, comments.Comment{ItemID: item, Author: "bob", Source: comments.SourceUser, Body: ""}); !errors.Is(err, dberr.ErrCheckViolation) {
			t.Fatalf("an empty comment: %v", err)
		}
		second, err := repo.Create(ctx, comments.Comment{ItemID: item, Author: "carol", Source: comments.SourceUser, Body: "Second thread"})
		if err != nil {
			t.Fatal(err)
		}
		edited, added, err := repo.Edit(ctx, root.ID, root.Version, "Signed? <@user:carol>", []string{"carol"})
		if err != nil || len(added) != 1 || added[0] != "carol" || edited.EditedAt == nil || len(edited.Mentions) != 1 || edited.Mentions[0] != "carol" {
			t.Fatalf("edit: %+v %v %v", edited, added, err)
		}
		if _, _, err = repo.Edit(ctx, root.ID, root.Version, "Stale", nil); !errors.Is(err, dberr.ErrConcurrentUpdate) {
			t.Fatalf("stale edit: %v", err)
		}
		resolved, err := repo.Resolve(ctx, root.ID, edited.Version, true, "bob")
		if err != nil || resolved.ResolvedAt == nil || resolved.ResolvedBy != "bob" {
			t.Fatalf("resolve: %+v %v", resolved, err)
		}
		if _, err = repo.Resolve(ctx, reply.ID, reply.Version, true, "bob"); !errors.Is(err, dberr.ErrInvalidStateTransition) {
			t.Fatalf("resolving a reply: %v", err)
		}
		open := false
		roots, err := repo.Roots(ctx, comments.Threads{ItemID: item, Resolved: &open})
		if err != nil || len(roots) != 1 || roots[0].ID != second.ID {
			t.Fatalf("open threads: %+v %v", roots, err)
		}
		all, err := repo.Roots(ctx, comments.Threads{ItemID: item, Limit: 1})
		if err != nil || len(all) != 1 || all[0].ID != root.ID || all[0].Mentions[0] != "carol" {
			t.Fatalf("first thread: %+v %v", all, err)
		}
		next, err := repo.Roots(ctx, comments.Threads{ItemID: item, After: &comments.Key{CreatedAt: all[0].CreatedAt, ID: all[0].ID}})
		if err != nil || len(next) != 1 || next[0].ID != second.ID {
			t.Fatalf("next thread: %+v %v", next, err)
		}
		counts, err := repo.Counts(ctx, []string{item, library})
		if err != nil || counts[item] != (comments.Count{Open: 1, Resolved: 1}) || counts[library] != (comments.Count{}) {
			t.Fatalf("counts: %+v %v", counts, err)
		}
		// A deleted root keeps its thread while replies remain; the second
		// thread, without replies, disappears.
		deleted, err := repo.Delete(ctx, root.ID, resolved.Version, "alice")
		if err != nil || deleted.Body != "" || deleted.DeletedAt == nil || deleted.ResolvedAt != nil || len(deleted.Mentions) != 0 {
			t.Fatalf("delete: %+v %v", deleted, err)
		}
		if _, err = repo.Delete(ctx, second.ID, second.Version, "carol"); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.Create(ctx, comments.Comment{ItemID: item, ThreadID: root.ID, Author: "bob", Source: comments.SourceUser, Body: "Late"}); !errors.Is(err, dberr.ErrInvalidContainment) {
			t.Fatalf("a reply to a deleted thread: %v", err)
		}
		roots, err = repo.Roots(ctx, comments.Threads{ItemID: item})
		if err != nil || len(roots) != 1 || roots[0].ID != root.ID {
			t.Fatalf("threads after deletion: %+v %v", roots, err)
		}
		replies, err := repo.Replies(ctx, []string{root.ID})
		if err != nil || len(replies) != 1 || replies[0].ID != reply.ID {
			t.Fatalf("replies: %+v %v", replies, err)
		}
		changes, err := s.LiveChanges().After(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, c := range changes {
			if c.Kind == live.KindComment {
				seen++
			}
		}
		if seen != 7 {
			t.Fatalf("comment live changes: %d", seen)
		}
		// Purging the item takes its discussion.
		if err = s.Resources().Purge(ctx, item); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.Find(ctx, reply.ID); !dberr.IsNotFound(err) {
			t.Fatalf("a purged item's comment remains: %v", err)
		}
	})

	t.Run("MentionsOfManyReplies", func(t *testing.T) {
		ctx := context.Background()
		s := newStore(t)
		if _, err := s.Directory().CreateUser(ctx, "bob", "bob", "", true, false); err != nil {
			t.Fatal(err)
		}
		f := daltest.NewFixture(t, s)
		repo := s.Comments()
		// More replies than one statement names: each keeps its mentions.
		var threads []string
		for range 3 {
			root, err := repo.Create(ctx, comments.Comment{ItemID: f.Item, Author: "bob", Source: comments.SourceUser, Body: "Thread"})
			if err != nil {
				t.Fatal(err)
			}
			threads = append(threads, root.ID)
			for i := range 340 {
				if _, err = repo.Create(ctx, comments.Comment{ItemID: f.Item, ThreadID: root.ID, Author: "bob", Source: comments.SourceUser, Body: fmt.Sprint(i), Mentions: []string{"bob"}}); err != nil {
					t.Fatal(err)
				}
			}
		}
		replies, err := repo.Replies(ctx, threads)
		if err != nil || len(replies) != 1020 {
			t.Fatalf("replies: %d %v", len(replies), err)
		}
		for _, r := range replies {
			if len(r.Mentions) != 1 {
				t.Fatalf("a reply lost its mention: %+v", r)
			}
		}
	})
}

func TestComments(t *testing.T) { daltest.Engines(t, runCommentSuite) }
