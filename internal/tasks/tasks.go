// Package tasks is PaperGo's action-request building block. A task asks
// its assignees to answer with one of its outcomes, optionally filling in a
// form (the same forms as workflow launch forms) and looking at a review of a
// staged file. Workflows, people and PaperGo itself create tasks; an approval
// is a task of kind approval. The package works inside the caller's DMS
// transaction, so a task, its events and its audit records commit together.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	grantstore "papergo/internal/dal/grants"
	taskstore "papergo/internal/dal/tasks"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/model"
)

// Events tasks raise. Workflows can start on them.
const (
	EventCreated   = "task.created"
	EventCompleted = "task.completed"
	EventCancelled = "task.cancelled"
)

// Events lists the task events workflows can be triggered by.
var Events = []string{EventCreated, EventCompleted, EventCancelled}

// Outcome settings.
const (
	// Approved and Rejected are an approval's outcomes.
	Approved = "approved"
	Rejected = "rejected"

	// ReviewCompare shows the item's current file beside a staged image;
	// ReviewPreview shows a staged document, such as a composed PDF.
	ReviewCompare = "file.compare"
	ReviewPreview = "file.preview"
)

// Limits keep tasks bounded.
const (
	MaxAssignees = 50
	MaxOutcomes  = 10
)

// Reserved outcome keys: the ports every task node also has.
var reservedOutcomes = map[string]bool{"error": true, "expired": true}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// ValidKey reports whether key is a valid task key, such as legal_review.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// DefaultOutcomes is the one answer of a task that gives none: done.
func DefaultOutcomes() []model.TaskOutcome {
	return []model.TaskOutcome{{Key: "done", Label: "Done", Style: "primary"}}
}

// ApprovalOutcomes are an approval's answers. A rejection needs a reason,
// skips no form, and may reject a review whose files changed.
func ApprovalOutcomes() []model.TaskOutcome {
	return []model.TaskOutcome{
		{Key: Approved, Label: "Approve", Style: "primary"},
		{Key: Rejected, Label: "Reject", Style: "danger", Comment: "required", AllowStale: true, Decisive: true},
	}
}

// Input describes a new task. Assignees are recipient entries (see
// Resolve); none means the workspace's owners.
type Input struct {
	WorkspaceID string
	ItemID      string
	Kind        taskstore.Kind
	Key         string
	Title       string
	Description string
	Assignees   []string
	Completion  taskstore.Completion
	Outcomes    []model.TaskOutcome
	// InputSchema is the form answers fill in; Values prefill it.
	InputSchema map[string]any
	Values      map[string]any
	Review      map[string]any
	Priority    taskstore.Priority
	DueAt       *time.Time
	// Workflow is set for a workflow's task; Wait when its run waits.
	Workflow *Source
	Wait     bool
	// System marks a task PaperGo itself creates.
	System bool
	// Actor is who caused the run, for the actor entry.
	Actor string
}

// Source is the workflow node that created a task.
type Source struct {
	WorkflowID string
	RunID      string
	Node       string
}

// Create creates a task as subject inside t's write transaction, after
// checking its settings, its creator's access and every assignee's.
func Create(ctx context.Context, t *dms.Service, subject string, in Input) (taskstore.Task, error) {
	if err := in.normalize(); err != nil {
		return taskstore.Task{}, err
	}
	ws, err := t.Authorize(ctx, subject, in.WorkspaceID, "read")
	if err != nil {
		return taskstore.Task{}, err
	}
	if ws.Kind != "workspace" {
		return taskstore.Task{}, dms.Invalid("workspace_id is not a workspace")
	}
	var collection *string
	if in.ItemID != "" {
		action := "read_draft"
		if in.Workflow == nil && !in.System {
			action = "write"
		}
		item, err := t.Authorize(ctx, subject, in.ItemID, action)
		if err != nil {
			return taskstore.Task{}, err
		}
		if item.Kind != "item" || item.WorkspaceID != in.WorkspaceID {
			return taskstore.Task{}, dms.Invalid("item_id must be an item of the task's workspace")
		}
		collection = item.ContainerID
	}
	assignees, err := Resolve(ctx, t, subject, in.Assignees, in.WorkspaceID, in.ItemID, in.Actor)
	if err != nil {
		return taskstore.Task{}, err
	}
	for _, a := range assignees {
		if strings.HasPrefix(a, "group:") {
			// A group's members are checked when they answer.
			continue
		}
		if err = CanReview(ctx, t, a, in.WorkspaceID, in.ItemID); err != nil {
			return taskstore.Task{}, err
		}
	}
	if in.InputSchema != nil {
		// Pickers point at this workspace; prefilled picks are valid answers.
		if err = forms.CheckConfig(ctx, t, subject, in.WorkspaceID, in.InputSchema, "input_schema"); err != nil {
			return taskstore.Task{}, err
		}
		if err = forms.CheckValues(ctx, t, subject, in.WorkspaceID, in.InputSchema, in.Values, "values"); err != nil {
			return taskstore.Task{}, err
		}
	}
	if in.Review != nil {
		if err = checkReview(ctx, t, subject, in); err != nil {
			return taskstore.Task{}, err
		}
	}
	input := taskstore.CreateInput{
		WorkspaceID: in.WorkspaceID, ItemID: in.ItemID, Kind: in.Kind, Key: in.Key, Title: in.Title, Description: in.Description,
		Assignees: assignees, Completion: in.Completion, Outcomes: in.Outcomes, InputSchema: in.InputSchema, Values: in.Values, Review: in.Review,
		Priority: in.Priority, DueAt: in.DueAt, Wait: in.Wait, CreatedBy: subject, Source: taskstore.SourceUser,
	}
	if in.Workflow != nil {
		input.Source = taskstore.SourceWorkflow
		input.WorkflowID = in.Workflow.WorkflowID
		input.RunID = in.Workflow.RunID
		input.Node = in.Workflow.Node
	} else if in.System {
		input.Source = taskstore.SourceSystem
	}
	tk, err := t.DAL.Tasks().Create(ctx, input)
	if err != nil {
		return taskstore.Task{}, taskError(err)
	}

	t.Raise(ctx, EventCreated, subject, in.WorkspaceID, collection, in.ItemID, eventData(tk))
	if err = t.Record(ctx, subject, "task.create", ws, map[string]any{"task_id": tk.ID, "kind": string(in.Kind), "key": in.Key, "assignees": assignees}); err != nil {
		return taskstore.Task{}, err
	}
	return tk, nil
}

func (in *Input) normalize() error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 500 {
		return dms.Invalid("the title must have 1 to 500 characters")
	}
	if utf8.RuneCountInString(in.Description) > 4000 {
		return dms.Invalid("the description is limited to 4000 characters")
	}
	if in.WorkspaceID == "" {
		return dms.Invalid("workspace_id is required")
	}
	if in.Kind == "" {
		in.Kind = taskstore.KindTask
	}
	if !in.Kind.Valid() {
		return dms.Invalid("kind is task or approval")
	}
	if in.Key != "" && !keyPattern.MatchString(in.Key) {
		return dms.Invalid("key is a lowercase name such as legal_review")
	}
	if in.Completion == "" {
		in.Completion = taskstore.CompletionAny
	}
	if !in.Completion.Valid() {
		return dms.Invalid("completion is any or all")
	}
	if in.Priority == "" {
		in.Priority = taskstore.PriorityNormal
	}
	if !in.Priority.Valid() {
		return dms.Invalid("priority is low, normal or high")
	}
	if len(in.Outcomes) == 0 {
		in.Outcomes = DefaultOutcomes()
		if in.Kind == taskstore.KindApproval {
			in.Outcomes = ApprovalOutcomes()
		}
	}
	if err := CheckOutcomes(in.Outcomes); err != nil {
		return err
	}
	if in.InputSchema != nil {
		if err := forms.Validate(in.InputSchema, "input_schema"); err != nil {
			return dms.Invalid(err.Error())
		}
		if in.Values != nil {
			if err := checkPrefill(in.InputSchema, in.Values); err != nil {
				return err
			}
			in.Values = forms.Plain(in.Values).(map[string]any)
		}
	} else if len(in.Values) > 0 {
		return dms.Invalid("values prefill a form; give input_schema")
	}
	if in.Review != nil {
		if err := CheckReviewShape(in.Review); err != nil {
			return err
		}
		if in.Workflow == nil {
			return dms.Invalid("only a workflow's task can review a staged file of its run")
		}
	}
	if in.DueAt != nil {
		due := in.DueAt.UTC()
		in.DueAt = &due
	}
	return nil
}

// CheckOutcomes checks a task's answers. Decisive matters only when every
// assignee answers (completion all).
func CheckOutcomes(outcomes []model.TaskOutcome) error {
	if len(outcomes) == 0 || len(outcomes) > MaxOutcomes {
		return dms.Invalid(fmt.Sprintf("a task has 1 to %d outcomes", MaxOutcomes))
	}
	seen := map[string]bool{}
	for _, o := range outcomes {
		if !forms.NamePattern.MatchString(o.Key) || reservedOutcomes[o.Key] {
			return dms.Invalid("outcome keys are lowercase names other than error and expired, such as approved")
		}
		if seen[o.Key] {
			return dms.Invalid("outcome " + o.Key + " is listed twice")
		}
		seen[o.Key] = true
		if l := utf8.RuneCountInString(strings.TrimSpace(o.Label)); l == 0 || l > 100 {
			return dms.Invalid("outcome " + o.Key + " needs a label of 1 to 100 characters")
		}
		switch o.Style {
		case "", "primary", "danger", "default":
		default:
			return dms.Invalid("outcome style is primary, danger or default")
		}
		switch o.Comment {
		case "", "none", "optional", "required":
		default:
			return dms.Invalid("outcome comment is none, optional or required")
		}
	}
	return nil
}

// checkPrefill checks that values are valid answers to the form, whose
// required fields people still fill in.
func checkPrefill(schema, values map[string]any) error {
	relaxed := map[string]any{}
	for k, v := range schema {
		if k != "required" {
			relaxed[k] = v
		}
	}
	if _, err := forms.Values(relaxed, values, "values"); err != nil {
		return dms.Invalid(err.Error())
	}
	return nil
}

// CheckReviewShape checks a review's settings.
func CheckReviewShape(r map[string]any) error {
	typ, _ := r["type"].(string)
	if typ != ReviewCompare && typ != ReviewPreview {
		return dms.Invalid("review type is file.compare or file.preview")
	}
	if r["staged_file_id"] == nil {
		return dms.Invalid("review needs staged_file_id")
	}
	for k := range r {
		if k != "type" && k != "staged_file_id" {
			return dms.Invalid("review has no field " + k)
		}
	}
	return nil
}

func checkReview(ctx context.Context, t *dms.Service, subject string, in Input) error {
	id, _ := in.Review["staged_file_id"].(string)
	f, err := t.StagedFile(ctx, subject, id)
	if err != nil {
		return fmt.Errorf("review: %w", err)
	}
	if f.RunID != in.Workflow.RunID {
		return dms.Invalid("review: the staged file is not this run's")
	}
	return nil
}

// Resolve turns recipient entries into assignees: subjects and group:<id>
// entries, sorted and without duplicates.
//
//	<subject>                  a user
//	group:<id>                 a group, nested groups included
//	role:owners, role:members  the workspace's owners or members
//	creator                    the item's creator
//	actor                      who caused the run
//	field:<key>                the people in a field of the item
//
// No entries means the workspace's owners. Unknown users and groups fail
// with their names; inactive users are left out. subject reads the item.
func Resolve(ctx context.Context, t *dms.Service, subject string, entries []string, workspaceID, itemID, actor string) ([]string, error) {
	var out []string
	var unknown []string
	var itemLoaded bool
	var creator string
	var itemValues map[string]any
	loadItem := func(entry string) error {
		if itemID == "" {
			return dms.Invalid(entry + " needs a task about an item")
		}
		if !itemLoaded {
			r, err := t.GetSurface(ctx, subject, itemID, "auto")
			if err != nil {
				return err
			}
			creator, itemValues, itemLoaded = r.CreatedBy, r.Values, true
		}
		return nil
	}
	if len(entries) == 0 {
		entries = []string{"role:owners"}
	}
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		switch {
		case entry == "":
		case entry == "role:owners":
			owners, err := t.WorkspaceManagers(ctx, workspaceID)
			if err != nil {
				return nil, err
			}
			out = append(out, owners...)
		case entry == "role:members":
			members, err := t.DAL.Grants().List(ctx, grantstore.Filter{ResourceID: workspaceID, PrincipalType: grantstore.PrincipalUser})
			if err != nil {
				return nil, err
			}
			for _, g := range members {
				out = append(out, g.PrincipalID)
			}
		case entry == "creator":
			if err := loadItem(entry); err != nil {
				return nil, err
			}
			out = append(out, creator)
		case entry == "actor":
			if actor == "" {
				return nil, dms.Invalid("actor needs a run caused by a person")
			}
			out = append(out, actor)
		case strings.HasPrefix(entry, "field:"):
			if err := loadItem(entry); err != nil {
				return nil, err
			}
			switch v := itemValues[strings.TrimPrefix(entry, "field:")].(type) {
			case string:
				out = append(out, v)
			case []any:
				for _, x := range v {
					if s, ok := x.(string); ok {
						out = append(out, s)
					}
				}
			}
		case strings.HasPrefix(entry, "group:"):
			ok, err := t.DAL.Directory().GroupExists(ctx, strings.TrimPrefix(entry, "group:"))
			if err != nil {
				return nil, err
			}
			if !ok {
				unknown = append(unknown, entry)
				continue
			}
			out = append(out, entry)
		case strings.HasPrefix(entry, "role:"):
			return nil, dms.Invalid("role is owners or members, as in role:owners")
		default:
			out = append(out, entry)
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	users := make([]string, 0, len(out))
	for _, a := range out {
		if !strings.HasPrefix(a, "group:") {
			users = append(users, a)
		}
	}
	if len(users) > 0 {
		rows, err := t.DAL.Directory().Users(ctx, directory.UserFilter{IDs: users})
		if err != nil {
			return nil, err
		}
		known := map[string]bool{}
		inactive := map[string]bool{}
		for _, u := range rows {
			known[u.ID] = true
			inactive[u.ID] = !u.Active
		}
		out = slices.DeleteFunc(out, func(a string) bool {
			if strings.HasPrefix(a, "group:") {
				return false
			}
			if !known[a] {
				unknown = append(unknown, a)
				return true
			}
			return inactive[a]
		})
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, dms.Invalid("unknown recipients: " + strings.Join(slices.Compact(unknown), ", "))
	}
	if len(out) == 0 {
		return nil, dms.Invalid("no one can be assigned: the recipients give no active user or group, or the workspace has no owners")
	}
	if len(out) > MaxAssignees {
		return nil, dms.Invalid(fmt.Sprintf("a task has at most %d assignees", MaxAssignees))
	}
	return out, nil
}

// CanReview checks that subject may see and answer a task: read the
// workspace and, for a task about an item, the item's draft.
func CanReview(ctx context.Context, t *dms.Service, subject, workspaceID, itemID string) error {
	if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
		return dms.Invalid(subject + " cannot read the workspace")
	}
	if itemID != "" {
		if _, err := t.Authorize(ctx, subject, itemID, "read_draft"); err != nil {
			return dms.Invalid(subject + " cannot read the item's draft")
		}
	}
	return nil
}

// entriesOf returns the assignee entries subject answers a task for, in the
// task's order: their own and their groups'. groups are subject's groups.
// A person holding several entries answers once for each.
func entriesOf(tk taskstore.Task, subject string, groups []string) []string {
	var out []string
	for _, a := range tk.Assignees {
		if g, ok := strings.CutPrefix(a, "group:"); a == subject || ok && slices.Contains(groups, g) {
			out = append(out, a)
		}
	}
	return out
}

// unanswered returns the first of entries that has not answered the task,
// and whether one of them has.
func unanswered(ctx context.Context, t *dms.Service, taskID string, entries []string) (next string, answered bool, err error) {
	if len(entries) == 0 {
		return "", false, nil
	}
	rows, err := t.DAL.Tasks().Responses(ctx, []string{taskID}, entries)
	if err != nil {
		return "", false, err
	}
	done := make([]string, len(rows))
	for i, row := range rows {
		done[i] = row.Assignee
	}

	for _, e := range entries {
		if !slices.Contains(done, e) {
			return e, len(done) > 0, nil
		}
	}
	return "", len(done) > 0, nil
}

// Response is an assignee's answer.
type Response struct {
	Outcome string         `json:"outcome"`
	Comment string         `json:"comment,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
}

// ErrStaleReview refuses an answer that acts on a review whose staged file
// was made from files that are no longer current.
var ErrStaleReview = fmt.Errorf("%w: a file changed after this review was requested; reject it, or wait for a new review", dms.ErrConflict)

// Respond records subject's answer inside t's write transaction and
// completes the task when its completion mode says so.
func Respond(ctx context.Context, t *dms.Service, subject, id string, r Response) (taskstore.Task, error) {
	tk, err := t.DAL.Tasks().Find(ctx, id)
	if dberr.IsNotFound(err) {
		return taskstore.Task{}, dms.ErrNotFound
	}
	if err != nil {
		return taskstore.Task{}, err
	}
	groups, err := t.GroupIDs(ctx, subject)
	if err != nil {
		return taskstore.Task{}, err
	}
	entries := entriesOf(tk, subject, groups)
	if len(entries) == 0 || CanReview(ctx, t, subject, tk.WorkspaceID, tk.ItemID) != nil {
		if visibleTo(ctx, t, tk, subject, groups) {
			return taskstore.Task{}, dms.ErrForbidden
		}
		return taskstore.Task{}, dms.ErrNotFound
	}
	if tk.Status != taskstore.StatusOpen {
		return taskstore.Task{}, fmt.Errorf("%w: the task is %s", dms.ErrConflict, tk.Status)
	}
	entry, _, err := unanswered(ctx, t, id, entries)
	if err != nil {
		return taskstore.Task{}, err
	}
	if entry == "" {
		return taskstore.Task{}, fmt.Errorf("%w: you already answered this task", dms.ErrConflict)
	}
	i := slices.IndexFunc(tk.Outcomes, func(o model.TaskOutcome) bool { return o.Key == r.Outcome })
	if i < 0 {
		keys := make([]string, len(tk.Outcomes))
		for j, o := range tk.Outcomes {
			keys[j] = o.Key
		}
		return taskstore.Task{}, dms.Invalid("outcome is one of " + strings.Join(keys, ", "))
	}
	o := tk.Outcomes[i]
	r.Comment = strings.TrimSpace(r.Comment)
	switch {
	case utf8.RuneCountInString(r.Comment) > 4000:
		return taskstore.Task{}, dms.Invalid("the comment is limited to 4000 characters")
	case o.Comment == "required" && r.Comment == "":
		return taskstore.Task{}, dms.Invalid(o.Label + " needs a comment")
	case o.Comment == "none" && r.Comment != "":
		return taskstore.Task{}, dms.Invalid(o.Label + " takes no comment")
	}
	var input json.RawMessage
	switch {
	case tk.InputSchema != nil && !o.SkipForm:
		values, err := forms.Values(tk.InputSchema, r.Input, "input")
		if err != nil {
			return taskstore.Task{}, dms.Invalid(err.Error())
		}
		if err = forms.CheckValues(ctx, t, subject, tk.WorkspaceID, tk.InputSchema, values, "input"); err != nil {
			return taskstore.Task{}, dms.Invalid(err.Error())
		}
		if input, err = json.Marshal(values); err != nil {
			return taskstore.Task{}, err
		}
	case len(r.Input) > 0 && tk.InputSchema == nil:
		return taskstore.Task{}, dms.Invalid("this task has no form")
	case len(r.Input) > 0:
		return taskstore.Task{}, dms.Invalid(o.Label + " takes no form values")
	}
	if !o.AllowStale {
		if sid, _ := tk.Review["staged_file_id"].(string); sid != "" {
			fresh, err := t.StagedFresh(ctx, sid)
			if err != nil {
				return taskstore.Task{}, err
			}
			if !fresh {
				return taskstore.Task{}, ErrStaleReview
			}
		}
	}
	tk, err = t.DAL.Tasks().Respond(ctx, id, tk.Version, taskstore.ResponseInput{Subject: subject, Assignee: entry, Outcome: o.Key, Comment: r.Comment, Input: input})
	if err != nil {
		return taskstore.Task{}, taskError(err)
	}

	ws, err := t.Authorize(ctx, subject, tk.WorkspaceID, "read")
	if err != nil {
		return taskstore.Task{}, err
	}
	if err = t.Record(ctx, subject, "task.respond", ws, map[string]any{"task_id": id, "outcome": o.Key, "assignee": entry}); err != nil {
		return taskstore.Task{}, err
	}
	if tk.Status == taskstore.StatusCompleted {
		if err = raise(ctx, t, tk, EventCompleted, subject); err != nil {
			return taskstore.Task{}, err
		}
	}
	return tk, nil
}

// Reassign replaces an open task's assignees, as an owner of its workspace
// or the creator of a person's task does when someone can no longer answer.
// Answers of entries that stay assigned count; a task with completion all
// completes when every entry left has answered.
func Reassign(ctx context.Context, t *dms.Service, subject, id string, entries []string) (taskstore.Task, error) {
	tk, err := t.DAL.Tasks().Find(ctx, id)
	if dberr.IsNotFound(err) {
		return taskstore.Task{}, dms.ErrNotFound
	}
	if err != nil {
		return taskstore.Task{}, err
	}
	groups, err := t.GroupIDs(ctx, subject)
	if err != nil {
		return taskstore.Task{}, err
	}
	if !visibleTo(ctx, t, tk, subject, groups) {
		return taskstore.Task{}, dms.ErrNotFound
	}
	if !manages(ctx, t, subject, tk.WorkspaceID) && (tk.CreatedBy != subject || tk.Source == taskstore.SourceWorkflow) {
		return taskstore.Task{}, dms.ErrForbidden
	}
	if tk.Status != taskstore.StatusOpen {
		return taskstore.Task{}, fmt.Errorf("%w: the task is %s", dms.ErrConflict, tk.Status)
	}
	assignees, err := Resolve(ctx, t, subject, entries, tk.WorkspaceID, tk.ItemID, "")
	if err != nil {
		return taskstore.Task{}, err
	}
	for _, a := range assignees {
		if !strings.HasPrefix(a, "group:") {
			if err = CanReview(ctx, t, a, tk.WorkspaceID, tk.ItemID); err != nil {
				return taskstore.Task{}, err
			}
		}
	}
	tk, err = t.DAL.Tasks().Reassign(ctx, id, tk.Version, assignees)
	if err != nil {
		return taskstore.Task{}, taskError(err)
	}

	ws, err := t.Authorize(ctx, subject, tk.WorkspaceID, "read")
	if err != nil {
		return taskstore.Task{}, err
	}
	if err = t.Record(ctx, subject, "task.reassign", ws, map[string]any{"task_id": id, "assignees": assignees}); err != nil {
		return taskstore.Task{}, err
	}
	if tk.Status == taskstore.StatusCompleted {
		if err = raise(ctx, t, tk, EventCompleted, subject); err != nil {
			return taskstore.Task{}, err
		}
	}
	return tk, nil
}

func eventData(tk taskstore.Task) map[string]any {
	data := map[string]any{"task_id": tk.ID, "kind": string(tk.Kind), "key": tk.Key, "assignees": tk.Assignees, "source": string(tk.Source)}
	if tk.ItemID != "" {
		data["item_id"] = tk.ItemID
	}
	if tk.Outcome != "" {
		data["outcome"] = tk.Outcome
	}
	if tk.CompletedBy != "" {
		data["completed_by"] = tk.CompletedBy
	}
	if tk.RunID != "" {
		data["run_id"] = tk.RunID
	}
	return data
}

func raise(ctx context.Context, t *dms.Service, tk taskstore.Task, typ, actor string) error {
	var collection *string
	if tk.ItemID != "" {
		item, err := t.DAL.Resources().Resource(ctx, tk.ItemID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if item != nil {
			collection = item.ContainerID
		}
	}
	t.Raise(ctx, typ, actor, tk.WorkspaceID, collection, tk.ItemID, eventData(tk))
	return nil
}

// ErrCancelled is the error a run waiting for a cancelled task gets.
var ErrCancelled = errors.New("the task was cancelled")

// Cancel cancels an open task inside t's write transaction. Its creator and
// the workspace's owners may cancel a person's or the system's task; a
// workflow's task belongs to its run, so cancel the run.
func Cancel(ctx context.Context, t *dms.Service, subject, id string) (taskstore.Task, error) {
	tk, err := t.DAL.Tasks().Find(ctx, id)
	if dberr.IsNotFound(err) {
		return taskstore.Task{}, dms.ErrNotFound
	}
	if err != nil {
		return taskstore.Task{}, err
	}
	groups, err := t.GroupIDs(ctx, subject)
	if err != nil {
		return taskstore.Task{}, err
	}
	if !visibleTo(ctx, t, tk, subject, groups) {
		return taskstore.Task{}, dms.ErrNotFound
	}
	if tk.CreatedBy != subject && !manages(ctx, t, subject, tk.WorkspaceID) {
		return taskstore.Task{}, dms.ErrForbidden
	}
	if tk.Source == taskstore.SourceWorkflow {
		return taskstore.Task{}, dms.Invalid("a workflow's task ends with its run: cancel the run")
	}
	if tk.Status != taskstore.StatusOpen {
		return taskstore.Task{}, fmt.Errorf("%w: the task is %s", dms.ErrConflict, tk.Status)
	}
	return cancel(ctx, t, tk, subject)
}

// CancelByWorkflow cancels a task as a workflow's task.cancel node does: an
// open task of the workflow's workspace that no run waits for. A task a run
// waits for ends with its run.
func CancelByWorkflow(ctx context.Context, t *dms.Service, author, workspaceID, id string) (taskstore.Task, error) {
	tk, err := t.DAL.Tasks().Find(ctx, id)
	if dberr.IsNotFound(err) {
		return taskstore.Task{}, dms.ErrNotFound
	}
	if err != nil {
		return taskstore.Task{}, err
	}
	if tk.WorkspaceID != workspaceID {
		return taskstore.Task{}, dms.ErrNotFound
	}
	if tk.Status != taskstore.StatusOpen {
		return tk, nil
	}
	if tk.Wait {
		return taskstore.Task{}, dms.Invalid("a run waits for this task: cancel the run instead")
	}
	return cancel(ctx, t, tk, author)
}

func cancel(ctx context.Context, t *dms.Service, tk taskstore.Task, actor string) (taskstore.Task, error) {
	tk, err := t.DAL.Tasks().Cancel(ctx, tk.ID, tk.Version)
	if err != nil {
		return taskstore.Task{}, taskError(err)
	}
	if err = raise(ctx, t, tk, EventCancelled, actor); err != nil {
		return taskstore.Task{}, err
	}
	return tk, nil
}

// CancelRun cancels open tasks whose owning run was cancelled.
func CancelRun(ctx context.Context, t *dms.Service, runID string) (bool, error) {
	rows, err := t.DAL.Tasks().List(ctx, taskstore.Filter{RunID: runID, Status: taskstore.StatusOpen, WaitOnly: true})
	if err != nil {
		return false, err
	}
	for _, tk := range rows {
		if _, err = cancel(ctx, t, tk, tk.CreatedBy); err != nil {
			return false, err
		}
	}
	return len(rows) > 0, nil
}

func manages(ctx context.Context, t *dms.Service, subject, workspaceID string) bool {
	_, err := t.Authorize(ctx, subject, workspaceID, "manage")
	return err == nil
}

// visibleTo reports whether subject may see a task: an assignee who may
// review it, its creator, or an owner of its workspace.
func visibleTo(ctx context.Context, t *dms.Service, tk taskstore.Task, subject string, groups []string) bool {
	if len(entriesOf(tk, subject, groups)) > 0 && CanReview(ctx, t, subject, tk.WorkspaceID, tk.ItemID) == nil {
		return true
	}
	if tk.CreatedBy == subject {
		if _, err := t.Authorize(ctx, subject, tk.WorkspaceID, "read"); err == nil {
			return true
		}
	}
	return manages(ctx, t, subject, tk.WorkspaceID)
}

func taskError(err error) error {
	if dberr.IsNotFound(err) {
		return dms.ErrNotFound
	}
	if errors.Is(err, dberr.ErrConcurrentUpdate) || errors.Is(err, dberr.ErrInvalidStateTransition) || errors.Is(err, dberr.ErrUniqueViolation) {
		return fmt.Errorf("%w: %v", dms.ErrConflict, err)
	}
	if errors.Is(err, dberr.ErrInvalidReference) || errors.Is(err, dberr.ErrInvalidContainment) {
		return dms.Invalid(err.Error())
	}
	return err
}
