package runner

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"papergo/internal/workflow"
)

// Under write load on SQLite, every connection of DBOS's pool can be waiting
// for the write lock that the dispatch transaction holds. Matching a
// concurrency policy must not need one of them.
func TestDispatchMatchesWithoutFreeDurableConnections(t *testing.T) {
	f := setup(t)
	if f.db.URL != "" {
		t.Skip("PostgreSQL has no single write lock, and DBOS keeps its own pool")
	}
	f.searchOff(t)
	item := f.item(t, "alice", "Manual item", nil)
	w := f.workflow(t, "Manual skip", `{"triggers":[{"type":"manual","collection_id":"$LIST"}],"concurrency":"skip","flow":{"start":"hold","nodes":{"hold":{"activity":"delay","inputs":{"duration":"1h"}}}}}`)
	first, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	held := make([]*sql.Conn, f.r.system.Stats().MaxOpenConnections)
	for i := range held {
		if held[i], err = f.r.system.Conn(ctx); err != nil {
			t.Fatal(err)
		}
	}
	release := func() {
		for _, conn := range held {
			conn.Close()
		}
		held = nil
	}
	defer func() { release() }()
	done := make(chan error, 1)
	go func() {
		_, err := f.r.Dispatch(ctx)
		done <- err
	}()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		release()
		t.Fatal("dispatch waited for a DBOS connection while it held the write lock")
	}
	release()
	if run := f.wait(t, second[0], finished); run.Status != "failed" || !strings.Contains(run.Error, "another run is active") {
		t.Fatal("manual skip lost its rejection", run)
	}
	if _, err = f.r.Cancel(ctx, "alice", first[0]); err != nil {
		t.Fatal(err)
	}
}
