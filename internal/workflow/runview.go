package workflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
)

// Run is a workflow run as the API shows it: a run operation with its
// provenance.
type Run struct {
	ID              string  `json:"id"`
	WorkflowID      string  `json:"workflow_id"`
	WorkflowVersion int     `json:"workflow_version"`
	WorkspaceID     string  `json:"workspace_id"`
	ItemID          *string `json:"item_id,omitempty"`
	// Items is a selection run's ordered membership; ItemID is its primary.
	Items     []string `json:"items,omitempty"`
	EventID   string   `json:"event_id"`
	EventType string   `json:"event_type"`
	Depth     int      `json:"depth"`
	Actor     string   `json:"actor"`
	RetryOf   *string  `json:"retry_of,omitempty"`
	// ParentRunID is the run that called this one.
	ParentRunID *string `json:"parent_run_id,omitempty"`
	// Generation counts the times the run continued as new; State is what
	// its last generation carried into the current one.
	Generation   int                  `json:"generation,omitempty"`
	State        any                  `json:"state,omitempty"`
	Status       runstore.Status      `json:"status"`
	Error        string               `json:"error,omitempty"`
	ErrorCode    operations.ErrorCode `json:"error_code,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    *time.Time           `json:"updated_at,omitempty"`
	StartedAt    *time.Time           `json:"started_at,omitempty"`
	CompletedAt  *time.Time           `json:"completed_at,omitempty"`
	WaitingUntil *time.Time           `json:"waiting_until,omitempty"`
	// Inputs are the launch form's values of a manual run, or a call's
	// input, defaults applied.
	Inputs map[string]any  `json:"inputs,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Steps  []Step          `json:"steps,omitempty"`
	Logs   []runstore.Log  `json:"logs,omitempty"`
}

// Step is one recorded step of a run's script.
type Step struct {
	Seq         int        `json:"seq"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Input       any        `json:"input,omitempty"`
	Output      any        `json:"output,omitempty"`
	Size        int        `json:"size,omitempty"`
	Operations  []string   `json:"operations,omitempty"`
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

func toRun(row runstore.Run) Run {
	out := Run{ID: row.ID, WorkflowID: row.WorkflowID, WorkflowVersion: row.WorkflowVersion, WorkspaceID: row.WorkspaceID, ItemID: optional(row.ItemID), EventID: row.EventID, EventType: row.EventType,
		Depth: row.Depth, Actor: row.Actor, RetryOf: optional(row.RetryOf), Generation: row.Generation, Status: row.Status, Error: row.Error, ErrorCode: row.ErrorCode, CreatedAt: row.CreatedAt,
		StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, WaitingUntil: row.WaitingUntil}
	if !row.UpdatedAt.IsZero() {
		at := row.UpdatedAt
		out.UpdatedAt = &at
	}
	return out
}

func manage(ctx context.Context, t *dms.Service, subject, workspaceID string) error {
	_, err := t.Authorize(ctx, subject, workspaceID, "manage")
	return err
}

// Runs lists workspace runs to managers, or item-scoped summaries to draft
// readers.
func (s *Service) Runs(ctx context.Context, subject, workspaceID string, f RunFilter) (RunPage, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (RunPage, error) {
		restricted := false
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
		if f.Origin != "" && f.Origin != TriggerManual && f.Origin != TriggerSchedule && f.Origin != TestStart && f.Origin != TriggerCall && f.Origin != "automatic" {
			return RunPage{}, dms.Invalid("origin must be manual, schedule, test, call or automatic")
		}
		if f.Status != "" {
			if !runstore.Status(f.Status).Valid() {
				return RunPage{}, dms.Invalid("invalid run status")
			}
			filter.Statuses = []runstore.Status{runstore.Status(f.Status)}
		}
		if f.WorkflowID == "" && f.ItemID == "" && !f.IncludeSystem {
			filter.SystemKeys, filter.HideSystem = SystemBuiltIns(), true
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
		if !restricted {
			return out, members(ctx, t, out.Data)
		}
		for i := range out.Data {
			// Summaries expose no execution payloads or identities of other members.
			out.Data[i].Error = ""
			if primary := out.Data[i].ItemID; primary != nil && *primary != f.ItemID {
				out.Data[i].ItemID = nil
			}
		}
		return out, nil
	})
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

// managedRun returns a run its workspace's manager may see.
func managedRun(ctx context.Context, t *dms.Service, subject, id string) (runstore.Run, error) {
	row, err := t.DAL.Runs().Find(ctx, id)
	if dberr.IsNotFound(err) {
		return runstore.Run{}, dms.ErrNotFound
	}
	if err != nil {
		return runstore.Run{}, err
	}
	return row, manage(ctx, t, subject, row.WorkspaceID)
}

// pending returns a manual run whose event is logged but not dispatched
// yet: the ID that StartRuns returned exists before the dispatcher records
// it.
func pending(ctx context.Context, t *dms.Service, subject, id string) (Run, error) {
	rest, ok := strings.CutPrefix(id, "wf:")
	workflowID, eventID, ok2 := strings.Cut(rest, ":")
	if !ok || !ok2 {
		return Run{}, dms.ErrNotFound
	}
	e, err := t.DAL.Events().Find(ctx, eventID)
	if dberr.IsNotFound(err) || err == nil && (e.Data["workflow_id"] != workflowID || e.DispatchedAt != nil) {
		return Run{}, dms.ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	if _, err = viewer(ctx, t, subject, e.WorkspaceID, e.Type, e.Actor); err != nil {
		return Run{}, err
	}
	run := Run{ID: id, WorkflowID: workflowID, WorkspaceID: e.WorkspaceID, EventID: e.ID, EventType: e.Type, Depth: e.Depth, Actor: e.Actor, Status: runstore.StatusQueued, CreatedAt: e.CreatedAt}
	if e.ResourceID != "" {
		run.ItemID = &e.ResourceID
	}
	return run, nil
}

// viewer reports whether subject sees a run of workspaceID started by
// event type by actor as a workspace manager, with its steps, logs and
// state, or else as the person who started it by hand: its status, inputs,
// result and error. Steps, logs and state hold what the workflow's author
// could read, so only managers see them.
func viewer(ctx context.Context, t *dms.Service, subject, workspaceID, eventType, actor string) (managed bool, err error) {
	err = manage(ctx, t, subject, workspaceID)
	if err == nil || !errors.Is(err, dms.ErrForbidden) && !errors.Is(err, dms.ErrNotFound) {
		return err == nil, err
	}
	if eventType != TriggerManual || actor != subject {
		return false, err
	}
	return false, workspace(ctx, t, subject, workspaceID, "read")
}

// Run returns one run with its inputs, result, steps and logs to workspace
// managers, and the runs people started by hand, without steps, logs and
// state, to them.
func (s *Service) Run(ctx context.Context, subject, id string) (Run, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Run, error) {
		row, err := t.DAL.Runs().Find(ctx, id)
		if dberr.IsNotFound(err) {
			return pending(ctx, t, subject, id)
		}
		if err != nil {
			return Run{}, err
		}
		managed, err := viewer(ctx, t, subject, row.WorkspaceID, row.EventType, row.Actor)
		if err != nil {
			return Run{}, err
		}
		runs := []Run{toRun(row)}
		if err = members(ctx, t, runs); err != nil {
			return Run{}, err
		}
		run := runs[0]
		run.Result, run.Logs, run.State = row.Output, row.Logs, decode(row.State)
		op, err := t.DAL.Operations().Find(ctx, id)
		if err != nil {
			return Run{}, err
		}
		var payload RunPayload
		if err = json.Unmarshal(op.Payload, &payload); err != nil {
			return Run{}, err
		}
		run.Inputs = payload.Input
		if payload.Trigger != nil && payload.Trigger.ParentRunID != "" {
			run.ParentRunID = &payload.Trigger.ParentRunID
		}
		if !managed {
			run.Logs, run.State = nil, nil
			return run, nil
		}
		steps, err := t.DAL.Runs().Steps(ctx, id)
		if err != nil {
			return Run{}, err
		}
		run.Steps = make([]Step, len(steps))
		for i, step := range steps {
			started := step.StartedAt
			run.Steps[i] = Step{Seq: step.Seq, Kind: string(step.Kind), Name: step.Name, Status: string(step.Status), Input: decode(step.Input), Output: stepOutput(step), Size: step.OutputSize,
				Operations: step.Operations, Error: step.Error, StartedAt: &started, CompletedAt: step.CompletedAt}
		}
		return run, nil
	})
}

// RunUpdate is a run's status as live updates show it, without its
// payloads, to workspace managers; it is ErrNotFound for anything else.
func (s *Service) RunUpdate(ctx context.Context, subject, id string) (Run, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Run, error) {
		row, err := managedRun(ctx, t, subject, id)
		if err != nil {
			return Run{}, err
		}
		return toRun(row), nil
	})
}

// decode shows recorded JSON as JSON.
func decode(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if d.Decode(&out) != nil {
		return string(raw)
	}
	return out
}

// stepOutput shows a request's recorded response with its JSON body as
// JSON, and other steps' outputs as recorded.
func stepOutput(step runstore.Step) any {
	out := decode(step.Output)
	if step.Kind != runstore.StepRequest {
		return out
	}
	response, ok := out.(map[string]any)
	if !ok {
		return out
	}
	if body, ok := response["body"].(string); ok && body != "" {
		if parsed := decode(json.RawMessage(body)); parsed != body {
			response["body"] = parsed
		}
	}
	return response
}

// CancelRun cancels a run that has not finished, with what its requests
// started, its open tasks and its staged files.
func (s *Service) CancelRun(ctx context.Context, subject, id string) (Run, error) {
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		if _, err := managedRun(ctx, t, subject, id); err != nil {
			return err
		}
		return t.CancelOperations(ctx, []string{id})
	})
	if err != nil {
		return Run{}, err
	}
	return s.Run(ctx, subject, id)
}

// RetryRun runs a failed run again as a new run of the same version, input
// and parameters. The steps its script recorded replay, except the last
// request, wait or sleep and what followed it, which run again: what the
// run wrote before is not written twice. Runs it called that failed are
// retried too, and its replayed calls return their retries in their place;
// those that succeeded replay what they returned.
func (s *Service) RetryRun(ctx context.Context, subject, id string) (Run, error) {
	var retryID string
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		var err error
		retryID, err = retry(ctx, t, subject, id, "")
		return err
	})
	if err != nil {
		return Run{}, err
	}
	return s.Run(ctx, subject, retryID)
}

// retry starts failed run id again and returns the new run's ID. A called
// run's retry belongs to parent, the retry of its caller.
func retry(ctx context.Context, t *dms.Service, subject, id, parent string) (string, error) {
	retryID := "retry:" + uuid.NewString()
	row, err := managedRun(ctx, t, subject, id)
	if err != nil {
		return "", err
	}
	if row.Status != runstore.StatusFailed {
		return "", dms.Invalid("only failed runs can be retried")
	}
	op, err := t.DAL.Operations().Find(ctx, id)
	if err != nil {
		return "", err
	}
	var payload RunPayload
	if err = json.Unmarshal(op.Payload, &payload); err != nil {
		return "", err
	}
	payload.StartError = ""
	if parent != "" && payload.Trigger != nil {
		called := *payload.Trigger
		called.ParentRunID = parent
		payload.Trigger = &called
		ctx = dms.WithParentOperation(ctx, parent)
	}
	run := runstore.CreateInput{ID: retryID, WorkflowID: row.WorkflowID, WorkflowVersion: row.WorkflowVersion, WorkspaceID: row.WorkspaceID, EventID: retryID,
		EventType: row.EventType, Depth: row.Depth, Actor: subject, RetryOf: row.ID, ItemID: row.ItemID}
	if err = newRun(ctx, t, op.CreatedBy, run, payload, false); err != nil {
		return "", err
	}
	steps, err := t.DAL.Runs().Steps(ctx, id)
	if err != nil {
		return "", err
	}
	last := 0
	for _, step := range steps {
		if step.Kind != runstore.StepValue {
			last = step.Seq
		}
	}
	// The runs that replayed calls started and that failed run again. A wait
	// for one of them runs again too, with what follows it, since it now
	// waits for the retry.
	calls := map[int][]string{}
	failed := map[string]bool{}
	for _, step := range steps {
		if step.Seq >= last || step.Kind != runstore.StepRequest || !strings.HasPrefix(step.Name, "POST ") || !strings.HasSuffix(step.Name, "/workflow-calls") {
			continue
		}
		ids := calledRuns(step.Output)
		calls[step.Seq] = ids
		for _, called := range ids {
			r, err := t.DAL.Runs().Find(ctx, called)
			if err != nil && !dberr.IsNotFound(err) {
				return "", err
			}
			if err == nil && r.Status == runstore.StatusFailed {
				failed[called] = true
			}
		}
	}
	for _, step := range steps {
		if step.Seq >= last {
			break
		}
		if step.Kind == runstore.StepWait && waitsFor(step, failed) {
			last = step.Seq
			break
		}
	}
	if err = t.DAL.Runs().ForkSteps(ctx, retryID, id, last); err != nil {
		return "", err
	}
	for seq, ids := range calls {
		if seq >= last {
			continue
		}
		replaced := false
		for i, called := range ids {
			if failed[called] {
				if ids[i], err = retry(ctx, t, subject, called, retryID); err != nil {
					return "", err
				}
				replaced = true
			}
		}
		if !replaced {
			continue
		}
		for _, step := range steps {
			if step.Seq == seq {
				output, err := withCalledRuns(step.Output, ids)
				if err != nil {
					return "", err
				}
				if err = t.DAL.Runs().ReplaceOutput(ctx, retryID, seq, output); err != nil {
					return "", err
				}
			}
		}
	}
	return retryID, nil
}

// recorded is a request step's output, as the script engine records it.
type recorded struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Base64  bool              `json:"base64"`
}

// calledRuns returns the runs a recorded call request started.
func calledRuns(output json.RawMessage) []string {
	var response recorded
	var started struct {
		RunIDs []string `json:"run_ids"`
	}
	if json.Unmarshal(output, &response) != nil || response.Status != 202 || json.Unmarshal([]byte(response.Body), &started) != nil {
		return nil
	}
	return started.RunIDs
}

// withCalledRuns returns a recorded call request's output with ids as the
// runs it started.
func withCalledRuns(output json.RawMessage, ids []string) (json.RawMessage, error) {
	var response recorded
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string][]string{"run_ids": ids})
	if err != nil {
		return nil, err
	}
	response.Body = string(body)
	return json.Marshal(response)
}

// waitsFor reports whether wait step waits for one of ids.
func waitsFor(step runstore.Step, ids map[string]bool) bool {
	var input struct {
		Operations []string `json:"operations"`
	}
	if json.Unmarshal(step.Input, &input) != nil {
		return false
	}
	for _, id := range input.Operations {
		if ids[id] {
			return true
		}
	}
	return false
}
