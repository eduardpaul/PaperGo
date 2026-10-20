package waits_test

import (
	"context"
	"errors"
	"fmt"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"sync"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/waits"
	"papergo/internal/dal/workflows"
)

func waitFixture(t *testing.T, newStore func(testing.TB) *dal.Store) (*dal.Store, waits.PutInput) {
	t.Helper()
	ctx := context.Background()
	s, task := daltest.TaskFixture(t, newStore)
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{ID: "workflow", WorkspaceID: task.WorkspaceID, Key: "review", Name: "Review", CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Runs().Create(ctx, runstore.CreateInput{ID: "run", WorkflowID: wf.ID, WorkspaceID: task.WorkspaceID, ItemID: "", EventID: "event", EventType: "manual", Actor: "alice", WorkflowVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	return s, waits.PutInput{Kind: "review", Key: "one", WorkspaceID: task.WorkspaceID, RunID: run.ID, Node: "review", ExecutionID: "execution", Queue: "reviews", Data: []byte(`{"step":1}`)}
}

func runDALWaitSuite(t *testing.T, newStore func(testing.TB) *dal.Store) {
	t.Run("WaitReplayAndTransactionOwnership", func(t *testing.T) {
		ctx := context.Background()
		s, in := waitFixture(t, newStore)
		first, err := s.Waits().Put(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := s.SQLDB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		bound := s.WithTx(tx)
		in.Data, in.Parking = []byte(`{"step":2}`), true
		replay, err := bound.Waits().Put(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if replay.ID != first.ID || string(replay.Data) != string(first.Data) || !replay.Parking {
			t.Fatalf("replay mutated identity/data: %+v", replay)
		}
		in.ExecutionID = "other"
		if _, err = bound.Waits().Put(ctx, in); !errors.Is(err, dberr.ErrConcurrentUpdate) {
			t.Fatalf("different execution accepted: %v", err)
		}
		// ON CONFLICT must leave PostgreSQL transactions usable, even when the
		// application decides not to accept the replay.
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_waits`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("transaction unusable: %d %v", count, err)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		restored, err := s.Waits().Find(ctx, in.Kind, in.Key)
		if err != nil || restored.Parking {
			t.Fatalf("external rollback lost: %+v %v", restored, err)
		}
		in.ExecutionID = "execution"
		daltest.Update(t, s, "workflow_waits", daltest.Row{"status": string(waits.StatusClosed), "parking": false}, daltest.Row{"id": first.ID})
		closed, err := s.Waits().Put(ctx, in)
		if err != nil || closed.Parking || closed.Status != waits.StatusClosed {
			t.Fatalf("closed wait reopened: %+v %v", closed, err)
		}
	})
	t.Run("WaitWorkspaceContainment", func(t *testing.T) {
		ctx := context.Background()
		s, in := waitFixture(t, newStore)
		other, err := s.Resources().Create(ctx, resources.New{Kind: refs.KindWorkspace, Name: "Other"})
		if err != nil {
			t.Fatal(err)
		}
		in.WorkspaceID = other.ID
		if _, err := s.Waits().Put(ctx, in); !errors.Is(err, dberr.ErrInvalidReference) {
			t.Fatalf("cross-workspace wait accepted: %v", err)
		}
		if count := daltest.Count(t, s, "workflow_waits", nil); count != 0 {
			t.Fatalf("invalid wait persisted: %d", count)
		}
	})
	t.Run("WaitCompletionReplayAndPrecision", func(t *testing.T) {
		ctx := context.Background()
		s, in := waitFixture(t, newStore)
		if _, err := s.Waits().Put(ctx, in); err != nil {
			t.Fatal(err)
		}
		claims, _, err := s.Waits().Claim(ctx, waits.ClaimInput{Kind: in.Kind, Key: in.Key, Holder: "worker"})
		if err != nil || len(claims) != 1 {
			t.Fatalf("claim: %v %v", claims, err)
		}
		payload := []byte(`{"output":{"z":1,"id":9007199254740993,"a":[null,true]}}`)
		if err := s.Waits().Complete(ctx, in.Kind, in.Key, payload); err != nil {
			t.Fatal(err)
		}
		if err := s.Waits().Complete(ctx, in.Kind, in.Key, []byte(`{ "output": {"a":[null,true], "id":9007199254740993.0, "z":1e0}}`)); err != nil {
			t.Fatalf("equivalent JSON replay rejected: %v", err)
		}
		if err := s.Waits().Complete(ctx, in.Kind, in.Key, []byte(`{"output":{"z":1,"id":9007199254740992,"a":[null,true]}}`)); !errors.Is(err, dberr.ErrConcurrentUpdate) {
			t.Fatalf("numeric precision lost: %v", err)
		}
		row, err := s.Waits().Find(ctx, in.Kind, in.Key)
		if err != nil || row.Status != waits.StatusCompleted || row.ClaimRunID != "" || row.ClaimToken != "" || row.ClaimUntil != nil {
			t.Fatalf("completion retained claim: %+v %v", row, err)
		}
	})
	t.Run("ConcurrentWaitClaimsAndExpiry", func(t *testing.T) {
		ctx := context.Background()
		s, in := waitFixture(t, newStore)
		const size = 12
		for i := 0; i < size; i++ {
			in.Key = fmt.Sprint(i)
			if _, err := s.Waits().Put(ctx, in); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		results := make(chan []waits.Wait, 3)
		failures := make(chan error, 3)
		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				rows, _, err := s.Waits().Claim(ctx, waits.ClaimInput{Kind: in.Kind, Holder: fmt.Sprint("worker", i), Limit: 4})
				results <- rows
				failures <- err
			}(i)
		}
		close(start)
		wg.Wait()
		close(results)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		ids := map[string]bool{}
		var claimed waits.Wait
		for rows := range results {
			for _, row := range rows {
				if ids[row.ID] {
					t.Fatalf("wait claimed twice: %s", row.ID)
				}
				ids[row.ID] = true
				claimed = row
			}
		}
		if len(ids) != size {
			t.Fatalf("claimed %d of %d waits", len(ids), size)
		}
		if rows, _, err := s.Waits().Claim(ctx, waits.ClaimInput{Kind: in.Kind, Holder: "stranger"}); err != nil || len(rows) != 0 {
			t.Fatalf("live claims stolen: %v %v", rows, err)
		}
		// An owner refresh returns a new fencing token and the stored timestamp.
		refreshed, token, err := s.Waits().Claim(ctx, waits.ClaimInput{Kind: in.Kind, Key: claimed.Key, Holder: claimed.ClaimRunID})
		if err != nil || len(refreshed) != 1 || token == claimed.ClaimToken || refreshed[0].ClaimToken != token || refreshed[0].UpdatedAt.Before(claimed.UpdatedAt) {
			t.Fatalf("claim refresh: %v %s %v", refreshed, token, err)
		}
		daltest.Update(t, s, "workflow_waits", daltest.Row{"claim_until": time.Now().UTC().Add(-time.Hour)}, daltest.Row{"id": refreshed[0].ID})
		recovered, next, err := s.Waits().Claim(ctx, waits.ClaimInput{Kind: in.Kind, Key: claimed.Key, Holder: "stranger"})
		if err != nil || len(recovered) != 1 || next == token || recovered[0].ClaimRunID != "stranger" {
			t.Fatalf("expired claim not recovered: %v %v", recovered, err)
		}
	})
}

func TestWaits(t *testing.T) { daltest.Engines(t, runDALWaitSuite) }
