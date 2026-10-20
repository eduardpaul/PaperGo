package runner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resultcache"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
)

// Run is a workflow run as the API shows it.
type Run struct {
	Outcome         string  `json:"outcome,omitempty"`
	ID              string  `json:"id"`
	WorkflowID      string  `json:"workflow_id"`
	WorkflowVersion int     `json:"workflow_version"`
	WorkspaceID     string  `json:"workspace_id"`
	ItemID          *string `json:"item_id,omitempty"`
	// Items is a selection run's ordered membership; ItemID is its primary.
	Items     []string        `json:"items,omitempty"`
	EventID   string          `json:"event_id"`
	EventType string          `json:"event_type"`
	Depth     int             `json:"depth"`
	Actor     string          `json:"actor"`
	RetryOf   *string         `json:"retry_of,omitempty"`
	Status    runstore.Status `json:"status"`
	Error     string          `json:"error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
	// Inputs are the launch form's values of a manual run, defaults applied.
	Inputs map[string]any `json:"inputs,omitempty"`
	Result any            `json:"result,omitempty"`
	Steps  []Step         `json:"steps,omitempty"`
}

// Step is one recorded step of a run: the load, each node attempt, and the
// finish.
type Step struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	Output      any        `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// RunFilter selects runs of a workspace. Without a WorkflowID, the runs of
// automatic system runs are left out unless IncludeSystem is true. Manual
// system invocations remain visible.
type RunFilter struct {
	Origin        string
	Status        string
	IncludeSystem bool
	WorkflowID    string
	ItemID        string
	After         string
	Limit         int
}

// RunPage is one page of runs, newest first.
type RunPage struct {
	Data       []Run  `json:"value"`
	NextCursor string `json:"-"`
}

func (p RunPage) APIPage() (map[string]any, string) {
	return map[string]any{"value": p.Data}, p.NextCursor
}

func status(s dbos.WorkflowStatusType) runstore.Status {
	switch s {
	case dbos.WorkflowStatusEnqueued, dbos.WorkflowStatusDelayed:
		return runstore.StatusQueued
	case dbos.WorkflowStatusPending:
		return runstore.StatusRunning
	case dbos.WorkflowStatusSuccess:
		return runstore.StatusCompleted
	case dbos.WorkflowStatusCancelled:
		return runstore.StatusCancelled
	}
	return runstore.StatusFailed
}

func toRun(row runstore.Run) Run {
	return Run{ID: row.ID, WorkflowID: row.WorkflowID, WorkflowVersion: row.WorkflowVersion, WorkspaceID: row.WorkspaceID, ItemID: optional(row.ItemID), EventID: row.EventID, EventType: row.EventType,
		Depth: row.Depth, Actor: row.Actor, RetryOf: optional(row.RetryOf), Status: runstore.StatusQueued, CreatedAt: row.CreatedAt}
}

// optional returns nil for an empty string.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r *Runner) attach(runs []Run, output bool) error {
	if len(runs) == 0 {
		return nil
	}
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.ID
	}
	// Errors are stored with the output, so it is always loaded.
	statuses, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterWorkflowIDs(ids...), dbos.WithFilterLoadInput(output), dbos.WithFilterLoadOutput(true))
	if err != nil {
		return err
	}
	byID := map[string]dbos.WorkflowStatus{}
	for _, s := range statuses {
		byID[s.ID] = s
	}
	parked, err := parkedRuns(context.Background(), r.dms, ids)
	if err != nil {
		return err
	}
	for i := range runs {
		s, ok := byID[runs[i].ID]
		if !ok {
			continue
		}
		runs[i].Status = status(s.Status)
		if parked[runs[i].ID] && (s.Status == dbos.WorkflowStatusPending || s.Status == dbos.WorkflowStatusEnqueued) {
			runs[i].Status = runstore.StatusWaiting
			runs[i].Error = ""
			s.Error = nil
		}
		if !s.UpdatedAt.IsZero() {
			t := s.UpdatedAt.UTC()
			runs[i].UpdatedAt = &t
		}
		if s.Error != nil {
			runs[i].Error = s.Error.Error()
		}
		if result, ok := decodeOutput(s.Output).(map[string]any); ok && result["outcome"] == "unchanged" {
			runs[i].Outcome = "unchanged"
		}
		if output {
			runs[i].Result = decodeOutput(s.Output)
			if in, ok := decodeOutput(s.Input).(map[string]any); ok {
				runs[i].Inputs, _ = in["inputs"].(map[string]any)
			}
		}
	}
	return nil
}

func manage(ctx context.Context, t *dms.Service, subject, workspaceID string) error {
	_, err := t.Authorize(ctx, subject, workspaceID, "manage")
	return err
}

// Runs lists workspace runs to managers, or item-scoped summaries to draft readers.
func (r *Runner) Runs(ctx context.Context, subject, workspaceID string, f RunFilter) (RunPage, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	restricted := false
	page, err := dms.Read(ctx, r.dms, func(t *dms.Service) (RunPage, error) {
		if err := manage(ctx, t, subject, workspaceID); err != nil {
			if f.ItemID == "" {
				return RunPage{}, err
			}
			item, err := t.Authorize(ctx, subject, f.ItemID, "read_draft")
			if err != nil {
				return RunPage{}, err
			}
			if item.WorkspaceID != workspaceID || item.Kind != "item" {
				return RunPage{}, dms.ErrNotFound
			}
			restricted = true
		}
		filter := runstore.Filter{WorkspaceID: workspaceID, WorkflowID: f.WorkflowID, Origin: f.Origin, Limit: f.Limit + 1}
		if f.Origin != "" && f.Origin != "manual" && f.Origin != "schedule" && f.Origin != "automatic" {
			return RunPage{}, dms.Invalid("origin must be manual, schedule or automatic")
		}
		if f.Status != "" {
			if !runstore.Status(f.Status).Valid() {
				return RunPage{}, dms.Invalid("invalid run status")
			}
			filter.Statuses = []runstore.Status{runstore.Status(f.Status)}
		}
		if f.WorkflowID == "" && f.ItemID == "" && !f.IncludeSystem {
			filter.SystemKeys, filter.HideSystem = workflow.SystemBuiltIns(), true
		}
		if f.ItemID != "" {
			filter.ItemIDs = []string{f.ItemID}
		}
		if f.After != "" {
			at, id, err := decodeCursor(f.After)
			if err != nil {
				return RunPage{}, err
			}
			filter.BeforeCreatedAt, filter.BeforeID = &at, id
		}
		rows, err := t.DAL.Runs().List(ctx, filter)
		if err != nil {
			return RunPage{}, err
		}
		out := RunPage{Data: make([]Run, 0, len(rows))}
		if len(rows) > f.Limit {
			last := rows[f.Limit-1]
			out.NextCursor = encodeCursor(last.CreatedAt, last.ID)
			rows = rows[:f.Limit]
		}
		for _, row := range rows {
			out.Data = append(out.Data, toRun(row))
		}
		if restricted {
			return out, nil
		}
		return out, members(ctx, t, out.Data)
	})
	if err != nil {
		return page, err
	}
	if err = r.attach(page.Data, false); err != nil {
		return page, err
	}
	if restricted {
		for i := range page.Data {
			// Summaries expose no execution payloads or identities of other members.
			page.Data[i].Error = ""
			if primary := page.Data[i].ItemID; primary != nil && *primary != f.ItemID {
				page.Data[i].ItemID = nil
			}
		}
	}
	return page, nil
}

// members fills in the membership of selection runs.
func members(ctx context.Context, t *dms.Service, runs []Run) error {
	if len(runs) == 0 {
		return nil
	}
	ids := make([]string, len(runs))
	at := map[string]int{}
	for i, run := range runs {
		ids[i], at[run.ID] = run.ID, i
	}
	rows, err := t.DAL.Runs().Items(ctx, ids, "")
	if err != nil {
		return err
	}
	for _, row := range rows {
		i := at[row.RunID]
		runs[i].Items = append(runs[i].Items, row.ItemID)
	}
	return nil
}

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", dms.Invalid("invalid cursor")
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, "", dms.Invalid("invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", dms.Invalid("invalid cursor")
	}
	return t, id, nil
}

func (r *Runner) row(ctx context.Context, subject, id string) (runstore.Run, error) {
	return dms.Read(ctx, r.dms, func(t *dms.Service) (runstore.Run, error) {
		row, err := t.DAL.Runs().Find(ctx, id)
		if dberr.IsNotFound(err) {
			return runstore.Run{}, dms.ErrNotFound
		}
		if err != nil {
			return runstore.Run{}, err
		}
		return row, manage(ctx, t, subject, row.WorkspaceID)
	})
}

// errDispatched reports that a pending run's event was dispatched meanwhile.
var errDispatched = errors.New("event dispatched")

// pending returns a manual run whose event is logged but not dispatched yet:
// the ID that StartRuns returned exists before the dispatcher records it.
func (r *Runner) pending(ctx context.Context, subject, id string) (Run, error) {
	return dms.Read(ctx, r.dms, func(t *dms.Service) (Run, error) {
		rest, ok := strings.CutPrefix(id, "wf:")
		workflowID, eventID, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 {
			return Run{}, dms.ErrNotFound
		}
		e, err := t.DAL.Events().Find(ctx, eventID)
		if dberr.IsNotFound(err) || err == nil && e.Data["workflow_id"] != workflowID {
			return Run{}, dms.ErrNotFound
		}
		if err == nil && e.DispatchedAt != nil {
			return Run{}, errDispatched
		}
		if err != nil {
			return Run{}, err
		}
		if err = manage(ctx, t, subject, e.WorkspaceID); err != nil {
			return Run{}, err
		}
		run := Run{ID: id, WorkflowID: workflowID, WorkspaceID: e.WorkspaceID, EventID: e.ID, EventType: e.Type, Depth: e.Depth, Actor: e.Actor, Status: runstore.StatusQueued, CreatedAt: e.CreatedAt}
		if e.ResourceID != "" {
			run.ItemID = &e.ResourceID
		}
		return run, nil
	})
}

// Run returns one run with its result and steps to workspace managers.
func (r *Runner) Run(ctx context.Context, subject, id string) (Run, error) {
	row, err := r.row(ctx, subject, id)
	if errors.Is(err, dms.ErrNotFound) {
		run, pendingErr := r.pending(ctx, subject, id)
		if !errors.Is(pendingErr, errDispatched) {
			return run, pendingErr
		}
		// The dispatcher started the run between the two reads.
		row, err = r.row(ctx, subject, id)
	}
	if err != nil {
		return Run{}, err
	}
	runs := []Run{toRun(row)}
	if _, err = dms.Read(ctx, r.dms, func(t *dms.Service) (struct{}, error) { return struct{}{}, members(ctx, t, runs) }); err != nil {
		return Run{}, err
	}
	if err = r.attach(runs, true); err != nil {
		return Run{}, err
	}
	steps, err := dbos.GetWorkflowSteps(r.ctx, id)
	if err != nil {
		return Run{}, err
	}
	bodies, err := r.stepBodies(ctx, id)
	if err != nil {
		return Run{}, err
	}
	runs[0].Steps = make([]Step, 0, len(steps))
	for _, s := range steps {
		step := Step{ID: s.StepID, Name: s.StepName, Output: decodeOutput(s.Output)}
		if body, ok := bodies[s.StepID]; ok {
			step.Output = decodeOutput(body)
		}
		if s.Error != nil {
			step.Error = s.Error.Error()
		}
		if !s.StartedAt.IsZero() {
			t := s.StartedAt.UTC()
			step.StartedAt = &t
		}
		if !s.CompletedAt.IsZero() {
			t := s.CompletedAt.UTC()
			step.CompletedAt = &t
		}
		runs[0].Steps = append(runs[0].Steps, step)
	}
	return runs[0], nil
}

// stepBodies loads transactional outputs in one query. Work steps and DBOS
// waits keep their outputs in DBOS; transactional steps keep only pointers there.
func (r *Runner) stepBodies(ctx context.Context, runID string) (map[int]json.RawMessage, error) {
	return r.dms.DAL.Runs().StepBodies(ctx, runID)
}

// decodeOutput shows a step's recorded JSON as JSON, not as a string.
func decodeOutput(v any) any {
	var raw []byte
	switch t := v.(type) {
	case string:
		raw = []byte(t)
	case []byte:
		raw = t
	case json.RawMessage:
		raw = t
	default:
		return v
	}
	var out any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&out) != nil {
		return v
	}
	return out
}

// Cancel stops a queued or running run.
func (r *Runner) Cancel(ctx context.Context, subject, id string) (Run, error) {
	if _, err := r.row(ctx, subject, id); err != nil {
		return Run{}, err
	}
	if err := r.cancelRun(ctx, id); err != nil {
		return Run{}, err
	}
	return r.Run(ctx, subject, id)
}

// Retry runs a failed run again from the step that failed, as a new run:
// the steps before it keep their recorded results, so nothing they wrote is
// written twice.
func (r *Runner) Retry(ctx context.Context, subject, id string) (Run, error) {
	row, err := r.row(ctx, subject, id)
	if err != nil {
		return Run{}, err
	}
	current := []Run{toRun(row)}
	if err = r.attach(current, false); err != nil {
		return Run{}, err
	}
	if current[0].Status != runstore.StatusFailed {
		return Run{}, dms.Invalid("only failed runs can be retried")
	}
	steps, err := dbos.GetWorkflowSteps(r.ctx, id, dbos.WithStepsLoadOutput(false))
	if err != nil {
		return Run{}, err
	}
	start := -1
	for _, s := range steps {
		if s.Error != nil {
			start = s.StepID
		}
	}
	if start < 0 {
		return Run{}, errors.New("the failed run has no failed step")
	}
	retryID := "retry:" + uuid.NewString()
	// Persist the retry and reused step bodies together before DBOS can run it.
	// Every step body references a real application run in both schema masters.

	err = r.dms.Write(ctx, func(t *dms.Service) error {
		if _, err := t.DAL.Runs().Create(ctx, runstore.CreateInput{ID: retryID, WorkflowID: row.WorkflowID, WorkflowVersion: row.WorkflowVersion, WorkspaceID: row.WorkspaceID, EventID: retryID, EventType: row.EventType, Depth: row.Depth, Actor: subject, RetryOf: row.ID, ItemID: row.ItemID}); err != nil {
			return err
		}
		original, err := t.DAL.Runs().Items(ctx, []string{row.ID}, "")
		if err != nil {
			return err
		}
		for _, member := range original {
			if err := t.DAL.Runs().AddItem(ctx, retryID, member.ItemID, member.Position); err != nil {
				return err
			}
		}

		return t.DAL.Runs().ForkSteps(ctx, retryID, id, int(start))
	})
	if err != nil {
		return Run{}, fmt.Errorf("retry run %s: %w", id, err)
	}
	if _, err = dbos.ForkWorkflow[any](r.ctx, dbos.ForkWorkflowInput{OriginalWorkflowID: id, ForkedWorkflowID: retryID, StartStep: uint(start), QueueName: runQueue}); err != nil {
		cleanupErr := r.dms.Write(context.WithoutCancel(ctx), func(t *dms.Service) error {
			return t.DAL.Runs().Delete(context.WithoutCancel(ctx), []string{retryID})
		})
		return Run{}, fmt.Errorf("retry run %s: %w", id, errors.Join(err, cleanupErr))
	}
	return r.Run(ctx, subject, retryID)
}

// purge deletes runs that finished more than the retention ago, with their
// step markers first: a crash in between leaves only finished runs without
// markers, which never replay, and markers never outlive their run. Events
// dispatched before the retention go too. Reusable results have their own
// expiry and are collected independently of run retention.
func (r *Runner) purge(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (any, error) {
	return r.Purge(ctx)
}

// purgePage is how many finished runs Purge reads at a time.
var purgePage = 200

// Purge applies the retention now.
func (r *Runner) Purge(ctx context.Context) (int, error) {
	if err := r.workflows.Tools.Records.Purge(ctx, time.Now()); err != nil {
		return 0, err
	}
	if _, err := resultcache.New(r.db).PurgeExpired(ctx, time.Now()); err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-r.cfg.Retention)
	if err := r.deleteDispatchedEvents(ctx, cutoff); err != nil {
		return 0, err
	}
	if err := r.dms.Write(ctx, func(t *dms.Service) error {
		return t.CollectRevisions(ctx)
	}); err != nil {
		return 0, err
	}
	// DBOS pending waits are excluded by the terminal status filter.
	deleted := 0
	for {
		done, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterName(runName), dbos.WithFilterStatus(dbos.WorkflowStatusSuccess, dbos.WorkflowStatusError, dbos.WorkflowStatusCancelled, dbos.WorkflowStatusMaxRecoveryAttemptsExceeded),
			dbos.WithFilterCompletedBefore(cutoff), dbos.WithFilterLimit(purgePage), dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
		if err != nil {
			return deleted, err
		}
		if len(done) == 0 {
			break
		}
		ids := make([]string, 0, len(done))
		for _, s := range done {
			ids = append(ids, s.ID)
		}
		if err = r.deleteRuns(ctx, ids); err != nil {
			return deleted, err
		}
		deleted += len(ids)
	}
	if err := r.collectMaintenance(ctx); err != nil {
		return deleted, err
	}
	return deleted, nil
}

// deleteDispatchedEvents removes dispatched events in pages. Each page is its
// own transaction, so a large backlog releases the writer between pages.
func (r *Runner) deleteDispatchedEvents(ctx context.Context, cutoff time.Time) error {
	for {
		var n int64
		err := r.dms.Write(ctx, func(t *dms.Service) error {
			var err error
			n, err = t.DAL.Events().DeleteDispatched(ctx, cutoff, purgePage)
			return err
		})
		if err != nil || n < int64(purgePage) {
			return err
		}
	}
}

func (r *Runner) purgeSystem(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (any, error) {
	n, err := r.PurgeSystemRuns(ctx)
	if err != nil {
		return n, err
	}
	if err = r.collectMaintenance(ctx); err != nil {
		return n, err
	}
	return n, nil
}

// systemRetention is how long successful automatic runs of system built-ins
// are kept when SystemRetention is unset. Tests lower it.
var systemRetention = 24 * time.Hour

// systemRunRetention applies the configured system-run lifetime. Run
// retention is a ceiling because the application retention pass collects
// every finished application run.
func (r *Runner) systemRunRetention() time.Duration {
	kept := systemRetention
	if r.cfg.SystemRetention > 0 {
		kept = r.cfg.SystemRetention
	}
	if r.cfg.Retention > 0 && r.cfg.Retention < kept {
		kept = r.cfg.Retention
	}
	return kept
}

// PurgeSystemRuns deletes the successful automatic runs of system built-ins
// finished before systemRunRetention. Failures and manual runs stay for the
// full run retention.
func (r *Runner) PurgeSystemRuns(ctx context.Context) (int, error) {
	keys := workflow.SystemBuiltIns()
	if len(keys) == 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-r.systemRunRetention())
	deleted, after := 0, ""
	for {
		var ids []string
		err := r.dms.Write(ctx, func(t *dms.Service) error {
			rows, err := t.DAL.Runs().List(ctx, runstore.Filter{AfterID: after, CreatedBefore: &cutoff, SystemKeys: keys, SystemOnly: true, Order: "id", Limit: purgePage})
			ids = make([]string, len(rows))
			for i, row := range rows {
				ids[i] = row.ID
			}
			return err
		})
		if err != nil || len(ids) == 0 {
			return deleted, err
		}
		after = ids[len(ids)-1]
		done, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterWorkflowIDs(ids...), dbos.WithFilterStatus(dbos.WorkflowStatusSuccess), dbos.WithFilterCompletedBefore(cutoff),
			dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
		if err != nil {
			return deleted, err
		}
		finished := make([]string, 0, len(done))
		for _, s := range done {
			finished = append(finished, s.ID)
		}
		if len(finished) == 0 {
			continue
		}
		if err = r.deleteRuns(ctx, finished); err != nil {
			return deleted, err
		}
		deleted += len(finished)
	}
}

// deleteRuns removes finished runs: their step markers, staged files, the
// tasks they wait for, their rows and DBOS's records.
func (r *Runner) deleteRuns(ctx context.Context, ids []string) error {
	if err := r.dms.Write(ctx, func(t *dms.Service) error {
		if err := t.DAL.Runs().DeleteSteps(ctx, ids); err != nil {
			return err
		}

		for _, id := range ids {
			if err := workflow.CloseRunWaits(ctx, t, id); err != nil {
				return err
			}
		}
		if err := workflow.DeleteRunWaits(ctx, t, ids); err != nil {
			return err
		}
		if err := t.DiscardRunStagedFiles(ctx, ids...); err != nil {
			return err
		}
		// Tasks outlive their runs as a record; one still waiting closes.
		for _, id := range ids {
			if _, err := tasks.CancelRun(ctx, t, id); err != nil {
				return err
			}
		}
		return t.DAL.Runs().Delete(ctx, ids)
	}); err != nil {
		return err
	}
	return dbos.DeleteWorkflows(r.ctx, ids)
}
