// Package runner embeds DBOS Transact as PaperGo's durable execution engine
// (docs/plans/runner.md). It is the only package that imports DBOS: domain
// events of committed writes start workflow runs in the same transaction,
// runs execute workflow.Execute with exactly-once steps, and schedules drive
// timed triggers and retention.
package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite" // DBOS's SQLite system database
	_ "time/tzdata"                                                 // schedule time zones in minimal images

	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

const (
	appName = "papergo"
	// appVersion is fixed so a new binary recovers the pending runs of the
	// previous one. Incompatible changes to Execute's step sequence must be
	// guarded with dbos.Patch.
	appVersion = "papergo-1"
	runQueue   = "workflows"
	runName    = "papergo.run"
	tickName   = "papergo.tick"
	purgeName  = "papergo.retention"
)

// Config configures the runner.
type Config struct {
	// DatabasePath is PaperGo's SQLite file; DBOS keeps its tables there.
	DatabasePath string
	// NodeID identifies this process; a restarted node recovers its runs.
	NodeID string
	// Retention is how long finished runs are kept.
	Retention time.Duration
	Logger    *slog.Logger
	// TickSchedule is the 6-field cron (with seconds) of the schedule tick;
	// default every minute.
	TickSchedule string
	// PollInterval is how often the run queue is polled; default 1s.
	PollInterval time.Duration
	// Concurrency bounds the runs one node executes at once; default 4.
	Concurrency int
}

// Runner executes workflow runs durably.
type Runner struct {
	ctx       dbos.Context
	db        *sql.DB
	system    *sql.DB
	dms       *dms.Service
	workflows *workflow.Service
	log       *slog.Logger
	cfg       Config
}

// New registers PaperGo's runner workflows with DBOS on its own handle to
// the PaperGo file, and makes service deliver its events to the runner.
// db is PaperGo's pool, used for step transactions.
func New(cfg Config, db *sql.DB, service *dms.Service, workflows *workflow.Service) (*Runner, error) {
	if cfg.NodeID == "" {
		cfg.NodeID = "local"
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 30 * 24 * time.Hour
	}
	if cfg.TickSchedule == "" {
		cfg.TickSchedule = "0 * * * * *"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	abs, err := filepath.Abs(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	// DBOS Shutdown closes the handle it is given, so it gets its own.
	system, err := sql.Open("sqlite", database.DSN(abs, database.DefaultOptions))
	if err != nil {
		return nil, err
	}
	system.SetMaxOpenConns(8)
	system.SetMaxIdleConns(8)
	ctx, err := dbos.NewContext(context.Background(), dbos.Config{
		AppName:            appName,
		SQLiteSystemDB:     system,
		ExecutorID:         cfg.NodeID,
		ApplicationVersion: appVersion,
		Logger:             cfg.Logger.With("component", "runner"),
	})
	if err != nil {
		system.Close()
		return nil, fmt.Errorf("start the runner: %w", err)
	}
	r := &Runner{ctx: ctx, db: db, system: system, dms: service, workflows: workflows, log: cfg.Logger, cfg: cfg}
	if _, err = dbos.RegisterQueue(ctx, runQueue, dbos.WithWorkerConcurrency(cfg.Concurrency), dbos.WithQueueBasePollingInterval(cfg.PollInterval)); err != nil {
		system.Close()
		return nil, err
	}
	dbos.RegisterWorkflow(ctx, r.run, dbos.WithWorkflowName(runName))
	dbos.RegisterWorkflow(ctx, r.tick, dbos.WithWorkflowName(tickName))
	dbos.RegisterWorkflow(ctx, r.purge, dbos.WithWorkflowName(purgeName))
	service.SetEvents(r)
	return r, nil
}

// Launch migrates the DBOS tables, recovers this node's pending runs, brings
// built-in workflows to this release and starts the schedules.
func (r *Runner) Launch(ctx context.Context) error {
	if err := dbos.Launch(r.ctx); err != nil {
		return fmt.Errorf("launch the runner: %w", err)
	}
	if err := dbos.ApplySchedules(r.ctx, []dbos.ScheduleSpec{
		{ScheduleName: tickName, Schedule: r.cfg.TickSchedule, Workflow: r.tick},
		{ScheduleName: purgeName, Schedule: "0 30 3 * * *", Workflow: r.purge},
	}); err != nil {
		return fmt.Errorf("apply runner schedules: %w", err)
	}
	return r.workflows.SyncBuiltIns(ctx)
}

// Shutdown stops the runner; pending runs continue when it launches again.
func (r *Runner) Shutdown(timeout time.Duration) error {
	return dbos.Shutdown(r.ctx, timeout)
}

// Ready reports whether the runner can take work.
func (r *Runner) Ready(ctx context.Context) error {
	if r.ctx.Err() != nil {
		return errors.New("the runner has stopped")
	}
	return r.system.PingContext(ctx)
}

// Publish starts the runs that a write's events trigger, in the write's
// transaction: if the write rolls back, no run starts.
func (r *Runner) Publish(ctx context.Context, t *dms.Service, events []dms.Event) error {
	starts, err := workflow.Match(ctx, t, events)
	if err != nil {
		return err
	}
	for _, in := range starts {
		if _, err = dbos.Enqueue[workflow.Result](r.ctx, runQueue, runName, in, dbos.WithEnqueueWorkflowID(in.RunID), dbos.WithEnqueueTransaction(t.SQLTx())); err != nil {
			return fmt.Errorf("start run %s: %w", in.RunID, err)
		}
	}
	return nil
}

// run is the DBOS workflow of every PaperGo workflow run.
func (r *Runner) run(ctx dbos.Context, in workflow.RunInput) (workflow.Result, error) {
	res, err := workflow.Execute(ctx, &durable{r: r, ctx: ctx}, in)
	if err != nil {
		r.log.Warn("workflow run failed", "run_id", in.RunID, "workflow_id", in.WorkflowID, "error", err)
	}
	return res, err
}

// tick raises due schedule triggers; DBOS runs it once per tick across nodes.
func (r *Runner) tick(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (any, error) {
	return r.Tick(ctx)
}

// Tick raises the schedule triggers due now. The schedule calls it every
// minute; tests call it directly.
func (r *Runner) Tick(ctx context.Context) (int, error) {
	var raised int
	var skipped error
	err := r.dms.Write(ctx, func(t *dms.Service) error {
		var err error
		raised, skipped, err = workflow.Tick(ctx, t, time.Now().UTC())
		return err
	})
	if skipped != nil {
		r.log.Warn("workflow schedules could not run", "error", skipped)
	}
	return raised, err
}

type durable struct {
	r   *Runner
	ctx dbos.Context
}

func (x *durable) Step(name string, fn func(context.Context, *dms.Service, func(dms.Event)) (json.RawMessage, error)) (json.RawMessage, error) {
	return stepTx(x.ctx, x.r, name, fn)
}

func (x *durable) Sleep(d time.Duration) error {
	_, err := dbos.Sleep(x.ctx, d)
	return err
}
