package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
)

func TestOperations(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		t.Run("Records", func(t *testing.T) { testRecords(t, newStore(t)) })
		t.Run("Idempotency", func(t *testing.T) { testIdempotency(t, newStore(t)) })
		t.Run("ClaimsAndFencing", func(t *testing.T) { testClaims(t, newStore(t)) })
		t.Run("CompetingClaimers", func(t *testing.T) { testCompetingClaimers(t, newStore(t)) })
		t.Run("Membership", func(t *testing.T) { testMembership(t, newStore(t)) })
		t.Run("GroupsAndWorkspaces", func(t *testing.T) { testGroups(t, newStore(t)) })
		t.Run("Termination", func(t *testing.T) { testTermination(t, newStore(t)) })
	})
}

var start = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func accept(t *testing.T, ops *operations.Repo, in operations.Operation, at time.Time) operations.Operation {
	t.Helper()
	if in.Kind == "" {
		in.Kind = "test"
	}
	if in.CreatedBy == "" {
		in.CreatedBy = "alice"
	}
	op, replayed, err := ops.Accept(context.Background(), in, at)
	if err != nil || replayed {
		t.Fatalf("accept: %v %v", replayed, err)
	}
	return op
}

func claim(t *testing.T, ops *operations.Repo, holder string, limit int, now time.Time) []operations.Operation {
	t.Helper()
	claimed, err := ops.Claim(context.Background(), operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{"test"}, Now: now, Limit: limit}, Holder: holder, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return claimed
}

func testRecords(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	ops := s.Operations()
	refresh := accept(t, ops, operations.Operation{ContainerID: f.Source, WorkspaceID: f.Workspace, Kind: "refresh", Payload: json.RawMessage(`{"integration_id":"one"}`)}, start)
	index := accept(t, ops, operations.Operation{ContainerID: f.Source, Kind: "field_index", FieldID: "field", CreatedBy: "bob", CredentialID: "token", CredentialScopes: []string{"list.read"}}, start)
	stored, err := ops.Find(ctx, index.ID)
	if err != nil || stored.CredentialID != "token" || len(stored.CredentialScopes) != 1 || stored.FieldID != "field" || !stored.AttemptAt.Equal(start) || string(stored.Result) != "{}" {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	if found, err := ops.Find(ctx, refresh.ID); err != nil || found.WorkspaceID != f.Workspace {
		t.Fatalf("workspace: %+v %v", found, err)
	}
	// Owners see their operations and everyone sees shared kinds.
	visible, err := ops.List(ctx, operations.Filter{ContainerID: f.Source, OwnedBy: "carol", SharedKinds: []string{"field_index"}})
	if err != nil || len(visible) != 1 || visible[0].ID != index.ID {
		t.Fatalf("visible operations: %+v %v", visible, err)
	}
	if ok, err := ops.Exists(ctx, operations.Filter{PayloadField: "integration_id", PayloadValue: "one", Statuses: []operations.Status{operations.StatusPending}}); err != nil || !ok {
		t.Fatalf("payload match: %v %v", ok, err)
	}
	totals, err := ops.PendingTotals(ctx, "alice", f.Workspace, []string{"field_index"})
	// Exempt kinds, such as index maintenance, do not count.
	if err != nil || totals.Creator != 1 || totals.Workspace != 1 || totals.Installation != 1 || totals.WorkspacePayloadBytes < int64(len(`{"integration_id":"one"}`)) {
		t.Fatalf("totals: %+v %v", totals, err)
	}
	ended, err := ops.Supersede(ctx, operations.Filter{Kinds: []string{"refresh"}, PayloadField: "integration_id", PayloadValue: "one"}, start)
	if err != nil || len(ended) != 1 || ended[0] != refresh.ID {
		t.Fatalf("supersede: %v %v", ended, err)
	}
	if superseded, err := ops.Find(ctx, refresh.ID); err != nil || superseded.Status != operations.StatusSuperseded || superseded.CompletedAt == nil {
		t.Fatalf("superseded: %+v %v", superseded, err)
	}
	if n, err := ops.Count(ctx, operations.Filter{Finished: true}); err != nil || n != 1 {
		t.Fatalf("finished count: %d %v", n, err)
	}
	if finished, err := ops.Finished(ctx, []string{refresh.ID, index.ID}); err != nil || len(finished) != 1 || finished[0].ID != refresh.ID {
		t.Fatalf("finished: %+v %v", finished, err)
	}
	if n, err := ops.CollectExpired(ctx, time.Now().Add(time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("expired operations: %d %v", n, err)
	}
	if _, err := ops.Find(ctx, refresh.ID); !dberr.IsNotFound(err) {
		t.Fatalf("expired operation retained: %v", err)
	}
}

func testIdempotency(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	ops := s.Operations()
	in := operations.Operation{ContainerID: f.Source, Kind: "test", CreatedBy: "alice", IdempotencyKey: "key", PayloadDigest: "one"}
	first := accept(t, ops, in, start)
	again, replayed, err := ops.Accept(ctx, in, start.Add(time.Minute))
	if err != nil || !replayed || again.ID != first.ID {
		t.Fatalf("replay: %+v %v %v", again, replayed, err)
	}
	changed := in
	changed.PayloadDigest = "two"
	if _, _, err := ops.Accept(ctx, changed, start); !errors.Is(err, operations.ErrIdempotencyConflict) {
		t.Fatalf("changed payload: %v", err)
	}
	// Keys are scoped to the creator, kind and container; organization work
	// has no container and its keys still match.
	for _, other := range []operations.Operation{
		{ContainerID: f.Source, Kind: "test", CreatedBy: "bob", IdempotencyKey: "key", PayloadDigest: "one"},
		{ContainerID: f.Destination, Kind: "test", CreatedBy: "alice", IdempotencyKey: "key", PayloadDigest: "one"},
		{ContainerID: f.Source, Kind: "other", CreatedBy: "alice", IdempotencyKey: "key", PayloadDigest: "one"},
	} {
		if op := accept(t, ops, other, start); op.ID == first.ID {
			t.Fatalf("key crossed its scope: %+v", other)
		}
	}
	org := operations.Operation{Kind: "test", CreatedBy: "alice", IdempotencyKey: "org", PayloadDigest: "one"}
	firstOrg := accept(t, ops, org, start)
	if again, replayed, err := ops.Accept(ctx, org, start); err != nil || !replayed || again.ID != firstOrg.ID {
		t.Fatalf("organization replay: %+v %v %v", again, replayed, err)
	}
}

func testClaims(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	ops := s.Operations()
	first := accept(t, ops, operations.Operation{}, start)
	second := accept(t, ops, operations.Operation{}, start.Add(time.Second))
	got := claim(t, ops, "worker-a", 1, start.Add(2*time.Second))
	if len(got) != 1 || got[0].ID != first.ID || got[0].Attempts != 1 || got[0].Claim.Holder != "worker-a" {
		t.Fatalf("oldest claimed first: %+v", got)
	}
	a := got[0]
	// A claimed operation is not ready for anyone else.
	if got := claim(t, ops, "worker-b", 5, start.Add(3*time.Second)); len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("second claimer: %+v", got)
	}
	a.Processed, a.Progress = 3, 50
	if err := ops.Checkpoint(ctx, &a, start.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := ops.Renew(ctx, a.ID, a.Claim, start.Add(time.Hour), start.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	// After its claim expires, another worker takes the operation over and
	// the first execution is fenced out of every write.
	expired := start.Add(2 * time.Hour)
	taken := claim(t, ops, "worker-c", 5, expired)
	if len(taken) != 2 || taken[0].ID != first.ID || taken[0].Attempts != 2 || taken[0].Processed != 3 {
		t.Fatalf("expired claims: %+v", taken)
	}
	if err := ops.Renew(ctx, a.ID, a.Claim, expired.Add(time.Hour), expired); !errors.Is(err, operations.ErrClaimLost) {
		t.Fatalf("stale renew: %v", err)
	}
	if err := ops.Checkpoint(ctx, &a, expired); !errors.Is(err, operations.ErrClaimLost) {
		t.Fatalf("stale checkpoint: %v", err)
	}
	a.Status = operations.StatusSucceeded
	if err := ops.Finish(ctx, &a, expired); !errors.Is(err, operations.ErrClaimLost) {
		t.Fatalf("stale finish: %v", err)
	}
	// Deferral moves the next attempt; release gives the attempt back.
	c := taken[0]
	at := expired.Add(10 * time.Minute)
	if err := ops.Defer(ctx, c.ID, c.Claim, at, "provider busy", expired); err != nil {
		t.Fatal(err)
	}
	if err := ops.Release(ctx, taken[1].ID, taken[1].Claim, expired); err != nil {
		t.Fatal(err)
	}
	if next, err := ops.NextAttempt(ctx, []string{"test"}, expired); err != nil || next == nil || !next.Equal(second.AttemptAt) {
		t.Fatalf("next attempt: %v %v", next, err)
	}
	released, err := ops.Find(ctx, second.ID)
	if err != nil || released.Attempts != 1 || released.Claim.Token != "" {
		t.Fatalf("released: %+v %v", released, err)
	}
	d := claim(t, ops, "worker-d", 5, expired)
	if len(d) != 1 || d[0].ID != second.ID {
		t.Fatalf("deferred operation claimed early: %+v", d)
	}
	d[0].Status, d[0].Error = operations.StatusFailed, "broken"
	if err := ops.Finish(ctx, &d[0], expired); err != nil {
		t.Fatal(err)
	}
	deferred, err := ops.Find(ctx, first.ID)
	if err != nil || !deferred.AttemptAt.Equal(at) || deferred.Error != "provider busy" || deferred.Claim.Token != "" {
		t.Fatalf("deferred: %+v %v", deferred, err)
	}
	got = claim(t, ops, "worker-e", 5, at)
	if len(got) != 1 || got[0].ID != first.ID || got[0].Attempts != 3 {
		t.Fatalf("deferred claim: %+v", got)
	}
	done := got[0]
	done.Status, done.Result = operations.StatusSucceeded, json.RawMessage(`{"ok":true}`)
	if err := ops.Finish(ctx, &done, at); err != nil {
		t.Fatal(err)
	}
	finished, err := ops.Find(ctx, first.ID)
	if err != nil || finished.Status != operations.StatusSucceeded || finished.Progress != 100 || finished.CompletedAt == nil || finished.Claim.Token != "" {
		t.Fatalf("finished: %+v %v", finished, err)
	}
}

// testCompetingClaimers claims from concurrent transactions: no operation
// is claimed twice.
func testCompetingClaimers(t *testing.T, s *dal.Store) {
	ops := s.Operations()
	for range 20 {
		accept(t, ops, operations.Operation{}, start)
	}
	var mu sync.Mutex
	seen := map[string]string{}
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			holder := string(rune('a' + w))
			for {
				got, err := ops.Claim(context.Background(), operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{"test"}, Now: start, Limit: 2}, Holder: holder, TTL: time.Minute})
				if err != nil {
					t.Error(err)
					return
				}
				if len(got) == 0 {
					return
				}
				mu.Lock()
				for _, op := range got {
					if previous, taken := seen[op.ID]; taken {
						t.Errorf("%s claimed by %s and %s", op.ID, previous, holder)
					}
					seen[op.ID] = holder
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("claimed %d of 20", len(seen))
	}
}

func testMembership(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	ops := s.Operations()
	one := accept(t, ops, operations.Operation{}, start)
	two := accept(t, ops, operations.Operation{}, start)
	got := claim(t, ops, "batch", 2, start)
	if len(got) != 2 {
		t.Fatalf("claimed: %+v", got)
	}
	if err := ops.Park(ctx, []string{one.ID, two.ID}, got[0].Claim, "flight", start); err != nil {
		t.Fatal(err)
	}
	// Parked members wait for their execution, not for a claim.
	if got := claim(t, ops, "other", 5, start.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("parked member claimed: %+v", got)
	}
	if next, err := ops.NextAttempt(ctx, []string{"test"}, start); err != nil || next != nil {
		t.Fatalf("parked next attempt: %v %v", next, err)
	}
	// One caller cancels; the execution finishes the other and skips it.
	if ok, err := ops.Cancel(ctx, two.ID, start); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	member, _ := ops.Find(ctx, one.ID)
	member.Status = operations.StatusSucceeded
	if err := ops.FinishMember(ctx, &member, "flight", start); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := ops.Find(ctx, two.ID)
	cancelled.Status = operations.StatusSucceeded
	if err := ops.FinishMember(ctx, &cancelled, "flight", start); !errors.Is(err, operations.ErrClaimLost) {
		t.Fatalf("cancelled member finished: %v", err)
	}
	if stored, _ := ops.Find(ctx, two.ID); stored.Status != operations.StatusCancelled || stored.ExecutionID != "" {
		t.Fatalf("cancelled member: %+v", stored)
	}
	// Unparked members are ready again with their original creation time.
	three := accept(t, ops, operations.Operation{}, start)
	got = claim(t, ops, "batch", 1, start)
	if err := ops.Park(ctx, []string{three.ID}, got[0].Claim, "retry", start); err != nil {
		t.Fatal(err)
	}
	if err := ops.Unpark(ctx, "retry", []string{three.ID}, start.Add(time.Minute), start); err != nil {
		t.Fatal(err)
	}
	if got := claim(t, ops, "batch", 1, start.Add(time.Minute)); len(got) != 1 || got[0].ID != three.ID || !got[0].CreatedAt.Equal(start) {
		t.Fatalf("unparked: %+v", got)
	}
}

func testGroups(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	ops := s.Operations()
	accept(t, ops, operations.Operation{WorkspaceID: f.Workspace, GroupKey: "g", InputKey: "x"}, start.Add(time.Minute))
	accept(t, ops, operations.Operation{CreatedBy: "bob", GroupKey: "g", InputKey: "x"}, start.Add(3*time.Minute))
	accept(t, ops, operations.Operation{GroupKey: "g", InputKey: "y"}, start.Add(2*time.Minute))
	accept(t, ops, operations.Operation{GroupKey: "g"}, start.Add(4*time.Minute))
	accept(t, ops, operations.Operation{GroupKey: "h", InputKey: "z"}, start)
	now := start.Add(time.Hour)
	groups, err := ops.ReadyGroups(ctx, []string{"test"}, now)
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups: %+v %v", groups, err)
	}
	byKey := map[string]operations.Group{}
	for _, g := range groups {
		byKey[g.Key] = g
	}
	// A duplicate input counts once and never moves the oldest time.
	if g := byKey["g"]; g.Inputs != 3 || !g.Oldest.Equal(start.Add(time.Minute)) {
		t.Fatalf("group g: %+v", g)
	}
	if g := byKey["h"]; g.Inputs != 1 || !g.Oldest.Equal(start) {
		t.Fatalf("group h: %+v", g)
	}
	workspaces, err := ops.ReadyWorkspaces(ctx, []string{"test"}, now, 10)
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("workspaces: %v %v", workspaces, err)
	}
	// Two distinct inputs claim both operations of input x and the one of y,
	// oldest first; a drain cutoff leaves later arrivals.
	key, through := "g", start.Add(3*time.Minute)
	got, err := ops.Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{"test"}, GroupKey: &key, Through: &through, Now: now, Limit: 2}, Holder: "batch", TTL: time.Minute})
	if err != nil || len(got) != 3 {
		t.Fatalf("group claim: %+v %v", got, err)
	}
	for _, op := range got {
		if op.InputKey == "" {
			t.Fatalf("claimed past the cutoff: %+v", op)
		}
	}
	workspace := f.Workspace
	if got, err := ops.Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{"test"}, WorkspaceID: &workspace, Now: now, Limit: 5}, Holder: "w", TTL: time.Minute}); err != nil || len(got) != 0 {
		t.Fatalf("workspace claim after group claim: %+v %v", got, err)
	}
}

func claimOf(t *testing.T, ops *operations.Repo, id string) operations.Claim {
	t.Helper()
	op, err := ops.Find(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return op.Claim
}

func testTermination(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	ops := s.Operations()
	deadline := start.Add(time.Hour)
	late := accept(t, ops, operations.Operation{Deadline: &deadline}, start)
	running := accept(t, ops, operations.Operation{}, start)
	// The running operation retains a source blob and a payload.
	blob, err := s.Blobs().AddBlob(ctx, blobs.Blob{ItemID: f.Item, ObjectKey: "source", Filename: "a.pdf", ContentType: blobs.MediaPDF, Sha256: strings.Repeat("a", 64), Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Blobs().LeaseOperation(ctx, running.ID, []string{blob.ID}); err != nil {
		t.Fatal(err)
	}
	records := activitystore.New(s.SQLDB())
	past := time.Now().Add(-time.Hour)
	if err := records.Put(ctx, activitystore.Record{Namespace: "test", Scope: "s", Key: "payload", Payload: json.RawMessage(`{}`), ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if err := records.Reference(ctx, activitystore.OperationOwner(running.ID), activitystore.ID("test", "s", "payload")); err != nil {
		t.Fatal(err)
	}
	if leased, err := s.Blobs().Leased(ctx, blob.ID); err != nil || !leased {
		t.Fatalf("operation lease: %v %v", leased, err)
	}
	// Past its deadline an operation is neither claimed nor can succeed.
	if got := claim(t, ops, "late", 5, deadline); len(got) != 1 || got[0].ID != running.ID {
		t.Fatalf("claimed past a deadline: %+v", got)
	}
	early := accept(t, ops, operations.Operation{Deadline: &deadline}, start)
	got := claim(t, ops, "early", 1, start)
	if len(got) != 1 || got[0].Deadline == nil {
		t.Fatalf("claim before the deadline: %+v", got)
	}
	got[0].Status = operations.StatusSucceeded
	if err := ops.Finish(ctx, &got[0], deadline.Add(time.Second)); !errors.Is(err, operations.ErrClaimLost) {
		t.Fatalf("succeeded past its deadline: %v", err)
	}
	if err := ops.Release(ctx, got[0].ID, got[0].Claim, start); err != nil {
		t.Fatal(err)
	}
	if ok, err := ops.Cancel(ctx, early.ID, start); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := ops.Release(ctx, running.ID, claimOf(t, ops, running.ID), start); err != nil {
		t.Fatal(err)
	}
	if ended, err := ops.Expire(ctx, []string{"test"}, deadline, 10); err != nil || len(ended) != 0 {
		t.Fatalf("expired at its deadline: %v %v", ended, err)
	}
	ended, err := ops.Expire(ctx, []string{"test"}, deadline.Add(time.Second), 10)
	if err != nil || len(ended) != 1 || ended[0] != late.ID {
		t.Fatalf("expire: %v %v", ended, err)
	}
	if expired, _ := ops.Find(ctx, late.ID); expired.Status != operations.StatusFailed || expired.ErrorCode != operations.ErrorDeadlineExceeded {
		t.Fatalf("expired: %+v", expired)
	}
	got = claim(t, ops, "worker", 1, start)
	if len(got) != 1 || got[0].ID != running.ID {
		t.Fatalf("claim: %+v", got)
	}
	// Cancelling fences out the execution and releases retention.
	if ok, err := ops.Cancel(ctx, running.ID, start); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	if ok, err := ops.Cancel(ctx, running.ID, start); err != nil || ok {
		t.Fatalf("cancel twice: %v %v", ok, err)
	}
	op := got[0]
	op.Status = operations.StatusSucceeded
	if err := ops.Finish(ctx, &op, start); !errors.Is(err, operations.ErrClaimLost) {
		t.Fatalf("cancelled execution finished: %v", err)
	}
	if leased, err := s.Blobs().Leased(ctx, blob.ID); err != nil || leased {
		t.Fatalf("lease outlived its operation: %v %v", leased, err)
	}
	if err := records.Purge(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r, err := records.Get(ctx, "test", "s", "payload"); err != nil || r != nil {
		t.Fatalf("payload outlived its operation: %+v %v", r, err)
	}
}
