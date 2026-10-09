package runner

// Phase 0 spike (docs/plans/runner.md): DBOS keeps its system tables in the
// PaperGo SQLite file, so work can start atomically with domain writes.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite"

	"papergo/ent"
	"papergo/ent/auditevent"
	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/testutil"
)

const (
	spikeApp     = "papergo"
	spikeVersion = "spike"
	spikeQueue   = "spike"
)

// pool describes one database/sql handle on the shared SQLite file.
type pool struct {
	database.Options
	maxOpen int
}

// defaultPool has the settings the plan adopts for every handle.
var defaultPool = pool{database.Options{TxLock: "immediate", BusyTimeout: 30 * time.Second}, 8}

func openPool(t testing.TB, path string, p pool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", database.DSN(path, p.Options))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(p.maxOpen)
	db.SetMaxIdleConns(p.maxOpen)
	t.Cleanup(func() { db.Close() })
	return db
}

// openPaperGo opens a PaperGo database the way database.Open does, with the
// given transaction locking, and applies the schema when the file is new.
func openPaperGo(t testing.TB, path string, p pool) *sql.DB {
	t.Helper()
	fresh := true
	if _, err := os.Stat(path); err == nil {
		fresh = false
	}
	db := openPool(t, path, p)
	if fresh {
		applySchema(t, db)
	}
	return db
}

// stepResultsTable is the runner table phase 1 adds to the schema file.
const stepResultsTable = `CREATE TABLE runner_step_results (
	workflow_id TEXT NOT NULL,
	step_id INTEGER NOT NULL,
	step TEXT NOT NULL,
	output BLOB NOT NULL,
	PRIMARY KEY (workflow_id, step_id)
)`

func applySchema(t testing.TB, db *sql.DB) {
	t.Helper()
	for _, f := range testutil.MigrationFiles(t) {
		if _, err := db.Exec(string(f.Bytes())); err != nil {
			t.Fatalf("apply %s: %v", f.Name(), err)
		}
	}
	if _, err := db.Exec(stepResultsTable); err != nil {
		t.Fatal(err)
	}
}

// entOver runs Ent on a transaction the caller owns, so Ent writes and DBOS
// enqueues or sends share one commit.
func entOver(tx *sql.Tx) *ent.Client {
	return ent.NewClient(ent.Driver(entsql.NewDriver(dialect.SQLite, entsql.Conn{ExecQuerier: tx})))
}

// launch starts DBOS on its own handle to the PaperGo file: DBOS Shutdown
// closes the handle it was given, so it must never be PaperGo's pool.
func launch(t testing.TB, path string, executor string, register func(dbos.Context)) dbos.Context {
	t.Helper()
	return launchOn(t, openPool(t, path, defaultPool), executor, register)
}

func launchOn(t testing.TB, system *sql.DB, executor string, register func(dbos.Context)) dbos.Context {
	t.Helper()
	ctx, err := dbos.NewContext(context.Background(), dbos.Config{
		AppName:            spikeApp,
		SQLiteSystemDB:     system,
		ExecutorID:         executor,
		ApplicationVersion: spikeVersion,
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbos.RegisterQueue(ctx, spikeQueue, dbos.WithQueueBasePollingInterval(20*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if register != nil {
		register(ctx)
	}
	if err = dbos.Launch(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dbos.Shutdown(ctx, 10*time.Second); err != nil {
			t.Errorf("shutdown %s: %v", executor, err)
		}
	})
	return ctx
}

func audit(ctx context.Context, c *ent.Client, action, resource string) error {
	_, err := c.AuditEvent.Create().SetWorkspaceID("spike").SetResourceID(resource).SetSubject("spike").SetAction(action).Save(ctx)
	return err
}

func audits(t testing.TB, db *sql.DB, action string) int {
	t.Helper()
	n, err := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db))).AuditEvent.Query().Where(auditevent.ActionEQ(action)).Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// steps holds what the runner.Tx prototype needs: PaperGo's pool, and for
// tests a hook that fails a step after its transaction committed, which is
// the window a crash before the DBOS checkpoint would hit.
type steps struct {
	db          *sql.DB
	afterCommit func(step string) error
}

// stepTx is the prototype of runner.Tx. DBOS RunAsTransaction hands its
// callback a DBOS Tx without the underlying *sql.Tx, so Ent cannot run on it.
// Instead, the step's effects and a completion marker commit together; a
// retried or recovered step finds the marker and returns the stored output.
// The marker is keyed by the DBOS step ID, which replay assigns again in the
// same order, so a step name can repeat within a workflow. The output always
// comes back decoded from the marker, so a first run and a replay see the
// same value.
func stepTx[R any](ctx dbos.Context, s steps, step string, fn func(context.Context, *ent.Client) (R, error), opts ...dbos.StepOption) (R, error) {
	opts = append(opts, dbos.WithStepName(step))
	return dbos.RunAsStep(ctx, func(stepCtx context.Context) (out R, err error) {
		dctx, ok := stepCtx.(dbos.Context)
		if !ok {
			return out, errors.New("step context is not a DBOS context")
		}
		workflowID, err := dbos.GetWorkflowID(dctx)
		if err != nil {
			return out, err
		}
		stepID, err := dbos.GetStepID(dctx)
		if err != nil {
			return out, err
		}
		tx, err := s.db.BeginTx(stepCtx, nil)
		if err != nil {
			return out, err
		}
		defer tx.Rollback()
		var (
			stored     []byte
			storedStep string
		)
		switch err = tx.QueryRowContext(stepCtx, `SELECT step, output FROM runner_step_results WHERE workflow_id=? AND step_id=?`, workflowID, stepID).Scan(&storedStep, &stored); {
		case err == nil:
			if storedStep != step {
				return out, fmt.Errorf("step %d of %s replayed as %q, recorded as %q", stepID, workflowID, step, storedStep)
			}
			return out, json.Unmarshal(stored, &out)
		case !errors.Is(err, sql.ErrNoRows):
			return out, err
		}
		result, err := fn(stepCtx, entOver(tx))
		if err != nil {
			return out, err
		}
		if stored, err = json.Marshal(result); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(stepCtx, `INSERT INTO runner_step_results (workflow_id, step_id, step, output) VALUES (?,?,?,?)`, workflowID, stepID, step, stored); err != nil {
			return out, err
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		if err = json.Unmarshal(stored, &out); err != nil {
			return out, err
		}
		if s.afterCommit != nil {
			return out, s.afterCommit(step)
		}
		return out, nil
	}, opts...)
}

func tables(t testing.TB, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	return out
}

func TestSpikeSchemaCoexistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	db := openPaperGo(t, path, defaultPool)
	before := tables(t, db)
	launch(t, path, "node-a", nil)
	after := tables(t, db)
	var added []string
	for name := range after {
		if !before[name] {
			added = append(added, name)
		}
	}
	for name := range before {
		if !after[name] {
			t.Fatalf("DBOS migration removed PaperGo table %s", name)
		}
	}
	sort.Strings(added)
	if len(added) == 0 {
		t.Fatal("DBOS created no system tables")
	}
	t.Logf("DBOS tables added to the PaperGo file: %s", strings.Join(added, ", "))
	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity check: %q %v", check, err)
	}
	var fk int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&fk); err != nil || fk != 0 {
		t.Fatalf("foreign key violations: %d %v", fk, err)
	}

	// The other order: a database migrated by DBOS first still accepts the
	// PaperGo schema, so no table name collides.
	reverse := filepath.Join(t.TempDir(), "reverse.db")
	launch(t, reverse, "node-r", nil)
	applySchema(t, openPool(t, reverse, defaultPool))
}

type echoInput struct {
	Resource string `json:"resource"`
}

func TestSpikeTransactionalEnqueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	db := openPaperGo(t, path, defaultPool)
	var runs atomic.Int32
	ctx := launch(t, path, "node-a", func(ctx dbos.Context) {
		dbos.RegisterWorkflow(ctx, func(ctx dbos.Context, in echoInput) (string, error) {
			return dbos.RunAsStep(ctx, func(context.Context) (string, error) {
				runs.Add(1)
				return "handled " + in.Resource, nil
			})
		}, dbos.WithWorkflowName("spike.echo"))
	})
	start := func(resource string, commit, existing bool) (dbos.WorkflowHandle[string], error) {
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err = audit(ctx, entOver(tx), "spike.enqueue", resource); err != nil {
			return nil, err
		}
		h, err := dbos.Enqueue[string](ctx, spikeQueue, "spike.echo", echoInput{Resource: resource},
			dbos.WithEnqueueWorkflowID("evt:"+resource), dbos.WithEnqueueTransaction(tx))
		if err != nil {
			return nil, err
		}
		var visible int
		if err = db.QueryRow(`SELECT count(*) FROM workflow_status WHERE workflow_uuid=?`, h.GetWorkflowID()).Scan(&visible); err != nil {
			return nil, err
		}
		if visible != 0 && !existing {
			return nil, errors.New("workflow visible before commit")
		}
		if commit {
			return h, tx.Commit()
		}
		return h, tx.Rollback()
	}

	if _, err := start("rolled-back", false, false); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM workflow_status WHERE workflow_uuid='evt:rolled-back'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rolled back enqueue left %d workflows: %v", left, err)
	}
	if n := audits(t, db, "spike.enqueue"); n != 0 {
		t.Fatalf("rolled back domain write kept %d rows", n)
	}

	h, err := start("committed", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := h.GetResult(dbos.WithHandleTimeout(10 * time.Second)); err != nil || got != "handled committed" {
		t.Fatalf("result %q, %v", got, err)
	}
	if n := audits(t, db, "spike.enqueue"); n != 1 {
		t.Fatalf("committed domain write: %d rows", n)
	}

	// The event ID is the idempotency key: enqueuing it again in another
	// committed transaction keeps that transaction's domain write and returns
	// the finished run without executing it again.
	again, err := start("committed", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.GetWorkflowID() != "evt:committed" {
		t.Fatal("unexpected workflow id")
	}
	if got, err := again.GetResult(dbos.WithHandleTimeout(10 * time.Second)); err != nil || got != "handled committed" {
		t.Fatalf("repeated key result %q, %v", got, err)
	}
	if n := runs.Load(); n != 1 {
		t.Fatalf("repeated key executed the workflow %d times", n)
	}
	if n := audits(t, db, "spike.enqueue"); n != 2 {
		t.Fatalf("second committed domain write: %d rows", n)
	}
}

func TestSpikeTransactionalSignal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	db := openPaperGo(t, path, defaultPool)
	// Each waiter blocks in a step until the test opens its gate, so the
	// test's signals are committed before the workflow reaches Recv.
	var (
		gatesMu sync.Mutex
		gates   = map[string]chan struct{}{}
		opened  = map[string]bool{}
	)
	gate := func(id string) chan struct{} {
		gatesMu.Lock()
		defer gatesMu.Unlock()
		if gates[id] == nil {
			gates[id] = make(chan struct{})
		}
		return gates[id]
	}
	open := func(id string) {
		g := gate(id)
		gatesMu.Lock()
		defer gatesMu.Unlock()
		if !opened[id] {
			opened[id] = true
			close(g)
		}
	}
	var waiter func(dbos.Context, time.Duration) (string, error)
	ctx := launch(t, path, "node-a", func(ctx dbos.Context) {
		waiter = func(ctx dbos.Context, timeout time.Duration) (string, error) {
			id, err := dbos.GetWorkflowID(ctx)
			if err != nil {
				return "", err
			}
			if _, err = dbos.RunAsStep(ctx, func(context.Context) (string, error) {
				<-gate(id)
				return "", nil
			}, dbos.WithStepName("gate")); err != nil {
				return "", err
			}
			return dbos.Recv[string](ctx, "task.completed", timeout)
		}
		dbos.RegisterWorkflow(ctx, waiter, dbos.WithWorkflowName("spike.wait"))
	})
	// Registered after launch, so it runs before Shutdown if the test fails.
	t.Cleanup(func() {
		open("wait-rollback")
		open("wait-commit")
	})
	send := func(id, outcome string, commit bool) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = audit(ctx, entOver(tx), "spike.complete", id); err != nil {
			t.Fatal(err)
		}
		if err = dbos.Send(ctx, id, outcome, "task.completed", dbos.WithIdempotencyKey("task:"+id), dbos.WithSendTransaction(tx)); err != nil {
			t.Fatal(err)
		}
		if commit {
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
	}

	rolledBack, err := dbos.RunWorkflow(ctx, waiter, 2*time.Second, dbos.WithWorkflowID("wait-rollback"))
	if err != nil {
		t.Fatal(err)
	}
	send("wait-rollback", "approved", false)
	open("wait-rollback")
	if got, err := rolledBack.GetResult(dbos.WithHandleTimeout(10 * time.Second)); !errors.Is(err, dbos.ErrTimeout) || got != "" {
		t.Fatalf("rolled back signal was delivered: %q %v", got, err)
	}

	// A signal committed before the workflow reaches Recv is kept and delivered.
	committed, err := dbos.RunWorkflow(ctx, waiter, 10*time.Second, dbos.WithWorkflowID("wait-commit"))
	if err != nil {
		t.Fatal(err)
	}
	send("wait-commit", "approved", true)
	send("wait-commit", "approved", true) // same idempotency key: stored once
	var messages int
	if err = db.QueryRow(`SELECT count(*) FROM notifications WHERE destination_uuid='wait-commit'`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 1 {
		t.Fatalf("idempotent send stored %d messages before Recv", messages)
	}
	open("wait-commit")
	if got, err := committed.GetResult(dbos.WithHandleTimeout(15 * time.Second)); err != nil || got != "approved" {
		t.Fatalf("committed signal: %q %v", got, err)
	}
}

func TestSpikeExactlyOnceStep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	db := openPaperGo(t, path, defaultPool)
	var failures atomic.Int32
	s := steps{db: db, afterCommit: func(step string) error {
		if step == "publish" && failures.Add(1) == 1 {
			return errors.New("lost checkpoint")
		}
		return nil
	}}
	var wf func(dbos.Context, string) (string, error)
	ctx := launch(t, path, "node-a", func(ctx dbos.Context) {
		wf = func(ctx dbos.Context, resource string) (string, error) {
			out, err := stepTx(ctx, s, "publish", func(ctx context.Context, c *ent.Client) (string, error) {
				return "published " + resource, audit(ctx, c, "spike.publish", resource)
			}, dbos.WithStepMaxRetries(3), dbos.WithStepBaseInterval(10*time.Millisecond))
			if err != nil {
				return "", err
			}
			// One step name for several writes, as a loop over recipients would.
			for _, to := range []string{"alice", "bob"} {
				if _, err = stepTx(ctx, s, "notify", func(ctx context.Context, c *ent.Client) (string, error) {
					return to, audit(ctx, c, "spike.notify", resource+":"+to)
				}); err != nil {
					return "", err
				}
			}
			return out, nil
		}
		dbos.RegisterWorkflow(ctx, wf, dbos.WithWorkflowName("spike.once"))
	})
	h, err := dbos.RunWorkflow(ctx, wf, "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := h.GetResult(dbos.WithHandleTimeout(10 * time.Second)); err != nil || got != "published item-1" {
		t.Fatalf("result %q %v", got, err)
	}
	// The retry found the marker, so the injected failure ran only once.
	if failures.Load() != 1 {
		t.Fatalf("step committed %d times", failures.Load())
	}
	if n := audits(t, db, "spike.publish"); n != 1 {
		t.Fatalf("retried step wrote %d times", n)
	}
	if n := audits(t, db, "spike.notify"); n != 2 {
		t.Fatalf("repeated step name wrote %d of 2 times", n)
	}
}

const crashEnv = "PAPERGO_SPIKE_CRASH"

// crashWorkflow completes one durable step, then the process dies in the
// second step when crashEnv is set.
func crashWorkflow(db *sql.DB) func(dbos.Context, string) (string, error) {
	return func(ctx dbos.Context, resource string) (string, error) {
		if _, err := stepTx(ctx, steps{db: db}, "first", func(ctx context.Context, c *ent.Client) (string, error) {
			return "", audit(ctx, c, "spike.first", resource)
		}); err != nil {
			return "", err
		}
		return dbos.RunAsStep(ctx, func(context.Context) (string, error) {
			if os.Getenv(crashEnv) != "" {
				os.Exit(3)
			}
			return "recovered " + resource, nil
		}, dbos.WithStepName("second"))
	}
}

// TestSpikeCrashChild is the process that crashes; the parent runs it.
func TestSpikeCrashChild(t *testing.T) {
	spec := os.Getenv(crashEnv)
	if spec == "" {
		t.Skip("run by TestSpikeCrashRecovery")
	}
	parts := strings.Split(spec, "|")
	path, executor, id := parts[0], parts[1], parts[2]
	db := openPaperGo(t, path, defaultPool)
	wf := crashWorkflow(db)
	ctx := launch(t, path, executor, func(ctx dbos.Context) { dbos.RegisterWorkflow(ctx, wf, dbos.WithWorkflowName("spike.crash")) })
	h, err := dbos.RunWorkflow(ctx, wf, id, dbos.WithWorkflowID(id))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.GetResult(dbos.WithHandleTimeout(30 * time.Second))
	t.Fatal("process should have exited in the second step")
}

func crash(t *testing.T, path, executor, id string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSpikeCrashChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), crashEnv+"="+path+"|"+executor+"|"+id)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("child did not crash in the second step: %v\n%s", err, out)
	}
}

func TestSpikeCrashRecovery(t *testing.T) {
	if os.Getenv(crashEnv) != "" {
		t.Skip("child process")
	}
	path := filepath.Join(t.TempDir(), "papergo.db")
	openPaperGo(t, path, defaultPool).Close()
	crash(t, path, "node-a", "crash-same-node")
	crash(t, path, "node-dead", "crash-dead-node")

	db := openPaperGo(t, path, defaultPool)
	if n := audits(t, db, "spike.first"); n != 2 {
		t.Fatalf("first steps before recovery: %d", n)
	}
	wf := crashWorkflow(db)
	// Relaunching with the crashed node's executor ID recovers its own work.
	ctx := launch(t, path, "node-a", func(ctx dbos.Context) { dbos.RegisterWorkflow(ctx, wf, dbos.WithWorkflowName("spike.crash")) })
	for _, id := range []string{"crash-same-node", "crash-dead-node"} {
		if id == "crash-dead-node" {
			// A node that never returns: a live node lists its pending runs
			// and resumes them, which re-enqueues them on the internal queue.
			pending, err := dbos.ListWorkflows(ctx, dbos.WithFilterExecutorIDs("node-dead"), dbos.WithFilterStatus(dbos.WorkflowStatusPending))
			if err != nil || len(pending) != 1 || pending[0].ID != id {
				t.Fatalf("pending runs of the dead node: %+v %v", pending, err)
			}
			if _, err = dbos.ResumeWorkflows[string](ctx, []string{id}); err != nil {
				t.Fatal(err)
			}
		}
		h, err := dbos.RetrieveWorkflow[string](ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := h.GetResult(dbos.WithHandleTimeout(20 * time.Second)); err != nil || got != "recovered "+id {
			t.Fatalf("%s: %q %v", id, got, err)
		}
	}
	if n := audits(t, db, "spike.first"); n != 2 {
		t.Fatalf("recovery repeated a completed step: %d writes", n)
	}
}

// TestSpikeWriteContention runs PaperGo service writes, transactional
// enqueues and DBOS workflows against one file at once.
func TestSpikeWriteContention(t *testing.T) {
	for _, v := range []struct {
		name        string
		paperGo     pool
		mustNotFail bool
	}{
		{"deferred", pool{database.Options{BusyTimeout: 5 * time.Second}, 8}, false},
		{"immediate", pool{database.Options{TxLock: "immediate", BusyTimeout: 5 * time.Second}, 8}, false},
		{"immediate-busy30s", pool{database.Options{TxLock: "immediate", BusyTimeout: 30 * time.Second}, 8}, true},
	} {
		t.Run(v.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "papergo.db")
			db := openPaperGo(t, path, v.paperGo)
			client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
			s := dms.NewService(client)
			// Fixtures exist before DBOS starts writing to the file.
			w, err := s.Create(context.Background(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Load"})
			if err != nil {
				t.Fatal(err)
			}
			list, err := s.Create(context.Background(), "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Items"})
			if err != nil {
				t.Fatal(err)
			}
			system := v.paperGo
			system.TxLock = "immediate"
			var wf func(dbos.Context, string) (string, error)
			ctx := launchOn(t, openPool(t, path, system), "node-a", func(ctx dbos.Context) {
				wf = func(ctx dbos.Context, resource string) (string, error) {
					if _, err := stepTx(ctx, steps{db: db}, "write", func(ctx context.Context, c *ent.Client) (string, error) {
						return "", audit(ctx, c, "spike.load", resource)
					}, dbos.WithStepMaxRetries(5), dbos.WithStepBaseInterval(10*time.Millisecond)); err != nil {
						return "", err
					}
					return dbos.RunAsStep(ctx, func(context.Context) (string, error) { return resource, nil })
				}
				dbos.RegisterWorkflow(ctx, wf, dbos.WithWorkflowName("spike.load"))
			})
			const workers, each = 6, 20
			var (
				mu      sync.Mutex
				errs    = map[string]int{}
				handles []dbos.WorkflowHandle[string]
				wg      sync.WaitGroup
			)
			record := func(kind string, err error) {
				msg := err.Error()
				if len(msg) > 90 {
					msg = msg[:90]
				}
				mu.Lock()
				errs[kind+": "+msg]++
				mu.Unlock()
			}
			began := time.Now()
			for g := range workers {
				wg.Add(2)
				go func() { // PaperGo request writes
					defer wg.Done()
					for i := range each {
						if _, err := s.Create(context.Background(), "alice", list.ID, dms.CreateResource{Kind: "item", Name: fmt.Sprintf("item-%d-%d", g, i)}); err != nil {
							record("service", err)
						}
					}
				}()
				go func() { // domain write + transactional enqueue
					defer wg.Done()
					for i := range each {
						id := fmt.Sprintf("load-%d-%d", g, i)
						err := func() error {
							tx, err := db.Begin()
							if err != nil {
								return err
							}
							defer tx.Rollback()
							if err = audit(ctx, entOver(tx), "spike.enqueue", id); err != nil {
								return err
							}
							h, err := dbos.Enqueue[string](ctx, spikeQueue, "spike.load", id, dbos.WithEnqueueWorkflowID(id), dbos.WithEnqueueTransaction(tx))
							if err != nil {
								return err
							}
							if err = tx.Commit(); err != nil {
								return err
							}
							mu.Lock()
							handles = append(handles, h)
							mu.Unlock()
							return nil
						}()
						if err != nil {
							record("enqueue", err)
						}
					}
				}()
			}
			wg.Wait()
			failedRuns := 0
			for _, h := range handles {
				if _, err := h.GetResult(dbos.WithHandleTimeout(60 * time.Second)); err != nil {
					failedRuns++
					record("workflow", err)
				}
			}
			elapsed := time.Since(began)
			total := 0
			for _, n := range errs {
				total += n
			}
			keys := make([]string, 0, len(errs))
			for k := range errs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				t.Logf("%4d × %s", errs[k], k)
			}
			t.Logf("%s: %d service writes, %d enqueues (%d runs ok), %d errors, %s", v.name, workers*each, workers*each, len(handles)-failedRuns, total, elapsed.Round(time.Millisecond))
			if v.mustNotFail {
				if total != 0 {
					t.Fatalf("%s still failed %d times", v.name, total)
				}
				if n := audits(t, db, "spike.load"); n != len(handles) {
					t.Fatalf("workflow writes %d, runs %d", n, len(handles))
				}
			}
		})
	}
}
