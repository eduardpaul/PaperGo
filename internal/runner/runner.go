// Package runner embeds DBOS Transact as PaperGo's durable execution engine
// (docs/plans/runner.md). It is the only package that imports DBOS. Writes
// only log their domain events; the runner's dispatcher, on whichever node
// claims them, matches logged events to workflows and enqueues the runs,
// which any node's workers execute with exactly-once steps. Schedules drive
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

	"papergo/internal/activitystore"
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
	// serialQueue runs coalesce workflows: one run per workflow and item at
	// a time, partitioned by RunInput.Partition.
	serialQueue = "workflows-serial"
	// systemQueue runs system built-ins, so product processes never delay
	// people's workflows; partitioned like serialQueue for coalesce.
	systemQueue     = "workflows-system"
	runName         = "papergo.run"
	tickName        = "papergo.tick"
	purgeName       = "papergo.retention"
	systemPurgeName = "papergo.system-retention"
	searchEmbedName = "papergo.search_embed"
	// retentionSchedule is hourly at :45. systemRetentionSchedule is hourly
	// at :15, so the two passes do not start together. A finished record can
	// wait one hour for the next pass.
	retentionSchedule       = "0 45 * * * *"
	systemRetentionSchedule = "0 15 * * * *"
)

// Config configures the runner.
type Config struct {
	// DatabasePath is PaperGo's SQLite file; DBOS keeps its tables there.
	DatabasePath string
	// NodeID identifies this process; a restarted node recovers its runs.
	NodeID string
	// Retention is how long finished runs are kept.
	Retention time.Duration
	// SystemRetention is how long successful automatic system runs are kept.
	// Zero uses the package default. Retention is a ceiling.
	SystemRetention time.Duration
	Logger          *slog.Logger
	// TickSchedule is the 6-field cron (with seconds) of the schedule tick;
	// default every minute.
	TickSchedule string
	// PollInterval is how often the run queue is polled; default 1s.
	PollInterval time.Duration
	// Concurrency bounds heavy activity work per node; default 4. Durable
	// orchestration waits do not occupy a work slot.
	Concurrency int
	// DispatchInterval is how often the dispatcher looks for events that no
	// commit on this node announced (other nodes, restarts); default 1s.
	DispatchInterval time.Duration
}

// Runner executes workflow runs durably.
type Runner struct {
	workSlots    chan struct{}
	previewSlots chan struct{}
	ctx          dbos.Context
	db           *sql.DB
	system       *sql.DB
	dms          *dms.Service
	workflows    *workflow.Service
	log          *slog.Logger
	cfg          Config
	wake         chan struct{}
	stop         context.CancelFunc
	stopped      chan struct{}
}

// New registers PaperGo's runner workflows with DBOS on its own handle to
// the PaperGo file, and makes service wake the dispatcher after commits that
// logged events. db is PaperGo's pool, used for step transactions.
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
	if cfg.DispatchInterval <= 0 {
		cfg.DispatchInterval = time.Second
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
	if workflows.Tools.Records == nil {
		workflows.Tools.Records = activitystore.New(db)
	}
	r := &Runner{workSlots: make(chan struct{}, cfg.Concurrency), previewSlots: make(chan struct{}, 1), ctx: ctx, db: db, system: system, dms: service, workflows: workflows, log: cfg.Logger, cfg: cfg, wake: make(chan struct{}, 1)}
	if _, err = dbos.RegisterQueue(ctx, serialQueue, dbos.WithPartitionConcurrency(1), dbos.WithQueueBasePollingInterval(cfg.PollInterval)); err != nil {
		system.Close()
		return nil, err
	}
	if _, err = dbos.RegisterQueue(ctx, systemQueue, dbos.WithPartitionConcurrency(1), dbos.WithQueueBasePollingInterval(cfg.PollInterval)); err != nil {
		system.Close()
		return nil, err
	}
	if _, err = dbos.RegisterQueue(ctx, runQueue, dbos.WithQueueBasePollingInterval(cfg.PollInterval)); err != nil {
		system.Close()
		return nil, err
	}
	dbos.RegisterWorkflow(ctx, r.run, dbos.WithWorkflowName(runName))
	dbos.RegisterWorkflow(ctx, r.tick, dbos.WithWorkflowName(tickName))
	dbos.RegisterWorkflow(ctx, r.purge, dbos.WithWorkflowName(purgeName))
	dbos.RegisterWorkflow(ctx, r.purgeSystem, dbos.WithWorkflowName(systemPurgeName))
	dbos.RegisterWorkflow(ctx, r.sweepEmbeddings, dbos.WithWorkflowName(searchEmbedName))
	service.OnCommit(r.Wake)
	return r, nil
}

// Launch migrates the DBOS tables, recovers this node's pending runs, brings
// built-in workflows to this release, and starts the schedules and the
// dispatcher.
func (r *Runner) Launch(ctx context.Context) error {
	if err := dbos.Launch(r.ctx); err != nil {
		return fmt.Errorf("launch the runner: %w", err)
	}
	if err := r.protectRunPayloads(ctx); err != nil {
		return err
	}
	r.dms.RegisterRunPurger(r.purgeHomeRunPayloads)
	if err := dbos.ApplySchedules(r.ctx, []dbos.ScheduleSpec{
		{ScheduleName: tickName, Schedule: r.cfg.TickSchedule, Workflow: r.tick},
		{ScheduleName: purgeName, Schedule: retentionSchedule, Workflow: r.purge},
		{ScheduleName: systemPurgeName, Schedule: systemRetentionSchedule, Workflow: r.purgeSystem},
		{ScheduleName: searchEmbedName, Schedule: "0 */15 * * * *", Workflow: r.sweepEmbeddings},
	}); err != nil {
		return fmt.Errorf("apply runner schedules: %w", err)
	}
	if err := r.workflows.SyncBuiltIns(ctx); err != nil {
		return err
	}
	loop, stop := context.WithCancel(context.Background())
	r.stop, r.stopped = stop, make(chan struct{})
	go r.dispatchLoop(loop)
	return nil
}

// Shutdown stops the dispatcher and the runner; pending runs and
// undispatched events continue when it launches again.
func (r *Runner) Shutdown(timeout time.Duration) error {
	if r.stop == nil {
		return nil
	}
	stop := r.stop
	r.stop = nil
	stop()
	<-r.stopped
	return dbos.Shutdown(r.ctx, timeout)
}

// Ready reports whether the runner can take work.
func (r *Runner) Ready(ctx context.Context) error {
	if r.ctx.Err() != nil {
		return errors.New("the runner has stopped")
	}
	return r.system.PingContext(ctx)
}

// run is the DBOS workflow of every PaperGo workflow run.
func (r *Runner) run(ctx dbos.Context, in workflow.RunInput) (workflow.Result, error) {
	res, err := r.workflows.Execute(ctx, &durable{r: r, ctx: ctx}, in)
	if ctx.Err() == nil {
		if cleanup := r.dms.Write(context.Background(), func(t *dms.Service) error {
			if err := workflow.CloseRunWaits(context.Background(), t, in.RunID); err != nil {
				return err
			}
			return nil
		}); cleanup != nil {
			r.log.Error("release activity resources", "error", cleanup)
		}
	}
	if err != nil && ctx.Err() == nil {
		r.log.Warn("workflow run failed", "run_id", in.RunID, "workflow_id", in.WorkflowID, "error", err)
	}
	return res, err
}

// sweepEmbeddings embeds file content that search.embed deferred or that
// has no vector of the current model; a provider failure waits for the next
// sweep.
func (r *Runner) sweepEmbeddings(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (any, error) {
	n, err := r.workflows.SweepEmbeddings(ctx)
	if err != nil {
		r.log.Warn("embedding sweep stopped", "embedded", n, "error", err)
	}
	return n, nil
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

// Work runs fn as a DBOS step without a PaperGo transaction; DBOS records
// its result, so a replay returns it without running fn again.
func (x *durable) Work(name, activity string, fn func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	return dbos.RunAsStep(x.ctx, func(ctx context.Context) (json.RawMessage, error) {
		slots := x.r.workSlots
		if activity == "file.preview" {
			slots = x.r.previewSlots
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-slots }()
		out, err := fn(ctx)
		if err == nil && len(out) == 0 {
			out = json.RawMessage("null")
		}
		return out, err
	}, dbos.WithStepName(name), dbos.WithStepMaxRetries(0))
}

func (x *durable) Sleep(d time.Duration) error {
	_, err := dbos.Sleep(x.ctx, d)
	return err
}
