package database_test

import (
	"bytes"
	"database/sql"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/workflows"
)

func TestStepResultRowidUsesLessSpace(t *testing.T) {
	const without = `CREATE TABLE runner_step_results (
 run_id TEXT NOT NULL, step_id INTEGER NOT NULL, step TEXT NOT NULL, output BLOB NOT NULL,
 PRIMARY KEY (run_id, step_id)
) WITHOUT ROWID`
	const rowid = `CREATE TABLE runner_step_results (
 run_id TEXT NOT NULL, step_id INTEGER NOT NULL, step TEXT NOT NULL, output BLOB NOT NULL,
 PRIMARY KEY (run_id, step_id)
)`
	oldLayout := measureStepLayout(t, without)
	newLayout := measureStepLayout(t, rowid)
	t.Logf("WITHOUT ROWID allocated %d bytes; rowid table plus primary-key index allocated %d bytes; payload %d bytes over %d rows", oldLayout.allocated, newLayout.allocated, newLayout.payload, newLayout.rows)
	if oldLayout.rows != newLayout.rows || oldLayout.payload != newLayout.payload {
		t.Fatalf("layouts did not retain the same records: %+v %+v", oldLayout, newLayout)
	}
	if newLayout.allocated >= oldLayout.allocated {
		t.Fatalf("rowid allocation %d is not below WITHOUT ROWID allocation %d", newLayout.allocated, oldLayout.allocated)
	}
}

type stepLayout struct {
	allocated int
	payload   int
	rows      int
}

func measureStepLayout(t *testing.T, ddl string) stepLayout {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "steps.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`PRAGMA page_size=4096`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	sizes := []struct{ size, count int }{
		{64, 300},  // below the overflow threshold
		{900, 24},  // near the threshold where WITHOUT ROWID starts overflowing
		{1200, 95}, // the sample's large load outputs
	}
	payload := 0
	rows := 0
	for _, group := range sizes {
		size, count := group.size, group.count
		body := bytes.Repeat([]byte("a"), size)
		for i := range count {
			if _, err = db.Exec(`INSERT INTO runner_step_results (run_id, step_id, step, output) VALUES (?,?,?,?)`, "11111111-1111-1111-1111-111111111111", rows+i, "load", body); err != nil {
				t.Fatal(err)
			}
		}
		payload += size * count
		rows += count
	}
	var storedRows, storedPayload int
	if err = db.QueryRow(`SELECT count(*), coalesce(sum(length(output)),0) FROM runner_step_results`).Scan(&storedRows, &storedPayload); err != nil {
		t.Fatal(err)
	}
	if storedRows != rows || storedPayload != payload {
		t.Fatalf("stored %d rows and %d payload bytes, want %d and %d", storedRows, storedPayload, rows, payload)
	}
	var pages, pageSize int
	if err = db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if pageSize != 4096 {
		t.Fatalf("page size %d", pageSize)
	}
	return stepLayout{allocated: pages * pageSize, payload: storedPayload, rows: storedRows}
}

func TestStepResultKeyRejectsConcurrentDuplicates(t *testing.T) {
	db := daltest.DatabaseAt(t, filepath.Join(t.TempDir(), "papergo.db"))
	const runID = "run-step"
	ctx := t.Context()
	ws, err := db.DAL.Resources().Create(ctx, resources.New{Kind: refs.KindWorkspace, Name: "Steps"})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := db.DAL.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: ws.ID, Key: "steps", Name: "Steps", CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if err := daltest.WorkflowRun(ctx, db.DAL, runID, wf.ID, ws.ID, "alice", 1); err != nil {
		t.Fatal(err)
	}
	output := bytes.Repeat([]byte("x"), 1500)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := db.SQL.Exec(`INSERT INTO runner_step_results (run_id, step_id, step, output) VALUES (?,?,?,?)`, runID, 7, "stamp", output)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		if err == nil {
			ok++
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") && !strings.Contains(strings.ToLower(err.Error()), "constraint") {
			t.Fatal(err)
		}
	}
	if ok != 1 {
		t.Fatalf("one insert must win, got %d", ok)
	}
	var got []byte
	if err := db.SQL.QueryRow(`SELECT output FROM runner_step_results WHERE run_id=? AND step_id=?`, runID, 7).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, output) {
		t.Fatalf("lookup returned %d bytes", len(got))
	}
	res, err := db.SQL.Exec(`DELETE FROM runner_step_results WHERE run_id=?`, runID)
	if err != nil {
		t.Fatal(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		t.Fatal("retention delete", n, err)
	}
}
