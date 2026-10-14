package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"papergo/ent"
	"papergo/ent/approvalrequest"
	"papergo/internal/dms"
	"papergo/internal/forms"
)

// An approval node asks people to approve or reject, optionally with a form
// and a review of a staged file. The run waits without holding a worker:
// it parks, and the decision resumes it (see Exec.Await).

// Review types a reviewer's screen renders.
const (
	// ReviewCompare shows the item's current file beside a staged image.
	ReviewCompare = "file.compare"
	// ReviewPreview shows a staged document, such as a composed PDF.
	ReviewPreview = "file.preview"
)

// Approval decisions, the approval node's ports.
const (
	Approved = "approved"
	Rejected = "rejected"
)

func init() {
	register(&Activity{Key: "approval", Kind: "flow", Ports: []string{Approved, Rejected}, Description: "Asks the assignees to approve or reject, and waits for the first decision. The run holds no worker while it waits. An optional form collects input with the decision, and review shows a staged file of this run.",
		InputSchema: inputs(`"title": {"type": "string", "title": "Title"},
			"description": {"type": "string", "title": "Description"},
			"assignees": {"type": "array", "title": "Assignees", "description": "Who may decide; none means the workspace's owners.", "items": {"type": "string"}, "maxItems": 50, "x-papergo": {"kind": "people", "access": "read_draft"}},
			"input_schema": {"type": "object", "title": "Decision form", "description": "A form whose values come with the decision, as {step:node.input...}."},
			"review": {"type": "object", "title": "Review", "description": "A staged file to review: type file.compare (beside the current file) or file.preview, and staged_file_id.", "properties": {"type": {"type": "string", "enum": ["file.compare", "file.preview"]}, "staged_file_id": {"type": "string"}}}`, "title"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"request_id": {"type": "string"}, "decision": {"type": "string", "enum": ["approved", "rejected"]}, "decided_by": {"type": "string"}, "comment": {"type": "string"}, "input": {"type": "object"}}}`),
		run:          runApproval})
}

// validateApproval checks an approval node's literal inputs at save time.
func validateApproval(in map[string]any) error {
	if s, ok := in["input_schema"].(map[string]any); ok {
		if err := forms.Validate(s, "input_schema"); err != nil {
			return err
		}
	}
	if r, ok := in["review"].(map[string]any); ok {
		typ, _ := r["type"].(string)
		if typ != ReviewCompare && typ != ReviewPreview {
			return errors.New("review type is file.compare or file.preview")
		}
		if r["staged_file_id"] == nil {
			return errors.New("review needs staged_file_id")
		}
		for k := range r {
			if k != "type" && k != "staged_file_id" {
				return errors.New("review has no field " + k)
			}
		}
	}
	return nil
}

func runApproval(e *env, in map[string]any) (outcome, error) {
	title, err := e.scope.text(in["title"])
	if err != nil {
		return outcome{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 500 {
		return outcome{}, errors.New("the title must have 1 to 500 characters")
	}
	description := ""
	if v, ok := in["description"]; ok {
		if description, err = e.scope.text(v); err != nil {
			return outcome{}, err
		}
	}
	var assignees []string
	if v, ok := in["assignees"]; ok {
		if assignees, err = e.strings(v); err != nil {
			return outcome{}, err
		}
	}
	assignees = slices.DeleteFunc(assignees, func(s string) bool { return strings.TrimSpace(s) == "" })
	// No assignees, or a token or parameter that gives none, means the owners.
	if len(assignees) == 0 {
		if assignees, err = e.t.WorkspaceManagers(e.ctx, e.workspaceID); err != nil {
			return outcome{}, err
		}
	}
	slices.Sort(assignees)
	assignees = slices.Compact(assignees)
	if len(assignees) == 0 {
		return outcome{}, errors.New("no one can decide: give assignees, or add owners to the workspace")
	}
	if len(assignees) > 50 {
		return outcome{}, errors.New("an approval has at most 50 assignees")
	}
	for _, a := range assignees {
		if err = canReview(e.ctx, e.t, a, e.workspaceID, e.itemID); err != nil {
			return outcome{}, err
		}
	}
	c := e.t.Client.ApprovalRequest.Create().SetWorkspaceID(e.workspaceID).SetWorkflowID(e.workflowID).SetRunID(e.runID).SetNode(e.node).
		SetTitle(title).SetDescription(description).SetAssignees(assignees).SetCreatedBy(e.author)
	if e.itemID != "" {
		c.SetItemID(e.itemID)
	}
	if s, ok := in["input_schema"].(map[string]any); ok {
		c.SetInputSchema(s)
	}
	if r, ok := in["review"].(map[string]any); ok {
		id, err := e.scope.text(r["staged_file_id"])
		if err != nil {
			return outcome{}, err
		}
		f, err := e.t.StagedFile(e.ctx, e.author, id)
		if err != nil {
			return outcome{}, fmt.Errorf("review: %w", err)
		}
		if f.RunID != e.runID {
			return outcome{}, errors.New("review: the staged file is not this run's")
		}
		c.SetReview(map[string]any{"type": r["type"], "staged_file_id": id})
	}
	req, err := c.Save(e.ctx)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Wait: req.ID, Output: map[string]any{"request_id": req.ID}}, nil
}

// canReview checks that subject may decide: read the workspace and, when
// the run has an item, its current draft.
func canReview(ctx context.Context, t *dms.Service, subject, workspaceID, itemID string) error {
	if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
		return fmt.Errorf("%s cannot read the workspace", subject)
	}
	if itemID != "" {
		if _, err := t.Authorize(ctx, subject, itemID, "read_draft"); err != nil {
			return fmt.Errorf("%s cannot read the item's draft", subject)
		}
	}
	return nil
}

// Approval is an approval request as the API shows it.
type Approval struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	WorkflowID  string          `json:"workflow_id"`
	RunID       string          `json:"run_id"`
	Node        string          `json:"node"`
	ItemID      *string         `json:"item_id,omitempty"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Assignees   []string        `json:"assignees"`
	InputSchema map[string]any  `json:"input_schema,omitempty"`
	Review      map[string]any  `json:"review,omitempty"`
	Status      string          `json:"status"`
	DecidedBy   *string         `json:"decided_by,omitempty"`
	DecidedAt   *time.Time      `json:"decided_at,omitempty"`
	Comment     string          `json:"comment,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	CreatedBy   string          `json:"created_by"`
}

func toApproval(r *ent.ApprovalRequest) Approval {
	return Approval{ID: r.ID, WorkspaceID: r.WorkspaceID, WorkflowID: r.WorkflowID, RunID: r.RunID, Node: r.Node, ItemID: r.ItemID, Title: r.Title,
		Description: r.Description, Assignees: r.Assignees, InputSchema: r.InputSchema, Review: r.Review, Status: string(r.Status),
		DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt, Comment: r.Comment, Input: r.Input, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy}
}

// ApprovalFilter selects approval requests.
type ApprovalFilter struct {
	// Status is open, approved, rejected or cancelled; empty is all.
	Status string
	Limit  int
}

func (f ApprovalFilter) query(q *ent.ApprovalRequestQuery) (*ent.ApprovalRequestQuery, error) {
	if f.Status != "" {
		if err := approvalrequest.StatusValidator(approvalrequest.Status(f.Status)); err != nil {
			return nil, dms.Invalid("status is open, approved, rejected or cancelled")
		}
		q.Where(approvalrequest.StatusEQ(approvalrequest.Status(f.Status)))
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return q.Order(ent.Desc(approvalrequest.FieldCreatedAt), ent.Desc(approvalrequest.FieldID)).Limit(limit), nil
}

// MyApprovals lists the requests subject is an assignee of, newest first.
func (s *Service) MyApprovals(ctx context.Context, subject string, f ApprovalFilter) ([]Approval, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]Approval, error) {
		q, err := f.query(t.Client.ApprovalRequest.Query().Where(assignedTo(subject)))
		if err != nil {
			return nil, err
		}
		rows, err := q.All(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]Approval, 0, len(rows))
		for _, r := range rows {
			// Lost access hides a request without changing it.
			if canReview(ctx, t, subject, r.WorkspaceID, deref(r.ItemID)) == nil {
				out = append(out, toApproval(r))
			}
		}
		return out, nil
	})
}

// WorkspaceApprovals lists a workspace's requests to its managers.
func (s *Service) WorkspaceApprovals(ctx context.Context, subject, workspaceID string, f ApprovalFilter) ([]Approval, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]Approval, error) {
		if err := workspace(ctx, t, subject, workspaceID, "manage"); err != nil {
			return nil, err
		}
		q, err := f.query(t.Client.ApprovalRequest.Query().Where(approvalrequest.WorkspaceIDEQ(workspaceID)))
		if err != nil {
			return nil, err
		}
		rows, err := q.All(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]Approval, len(rows))
		for i, r := range rows {
			out[i] = toApproval(r)
		}
		return out, nil
	})
}

func assignedTo(subject string) func(*entsql.Selector) {
	return func(s *entsql.Selector) {
		s.Where(entsql.ExprP("EXISTS (SELECT 1 FROM json_each("+s.C(approvalrequest.FieldAssignees)+") WHERE value = ?)", subject))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// visible returns a request to an assignee who may still review it, or to
// a manager of its workspace.
func visible(ctx context.Context, t *dms.Service, subject, id string) (*ent.ApprovalRequest, error) {
	r, err := t.Client.ApprovalRequest.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, dms.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if slices.Contains(r.Assignees, subject) && canReview(ctx, t, subject, r.WorkspaceID, deref(r.ItemID)) == nil {
		return r, nil
	}
	if workspace(ctx, t, subject, r.WorkspaceID, "manage") == nil {
		return r, nil
	}
	return nil, dms.ErrNotFound
}

// Approval returns a request to its assignees and the workspace's managers.
func (s *Service) Approval(ctx context.Context, subject, id string) (Approval, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Approval, error) {
		r, err := visible(ctx, t, subject, id)
		if err != nil {
			return Approval{}, err
		}
		return toApproval(r), nil
	})
}

// Decision decides an approval request.
type Decision struct {
	// Decision is approve or reject.
	Decision string         `json:"decision"`
	Comment  string         `json:"comment,omitempty"`
	Input    map[string]any `json:"input,omitempty"`
}

// ErrStaleReview refuses to approve a review whose staged file was made
// from a file that is no longer current.
var ErrStaleReview = fmt.Errorf("%w: a file changed after this review was requested; reject it, or wait for a new review", dms.ErrConflict)

// Decide records an assignee's decision. The first decision wins; the run
// continues with it, resumed by the runner. Approving a review checks that
// its staged file is still pending and was made from the current files.
func (s *Service) Decide(ctx context.Context, subject, id string, d Decision) (Approval, error) {
	var out Approval
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		r, err := t.Client.ApprovalRequest.Get(ctx, id)
		if ent.IsNotFound(err) {
			return dms.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !slices.Contains(r.Assignees, subject) || canReview(ctx, t, subject, r.WorkspaceID, deref(r.ItemID)) != nil {
			if workspace(ctx, t, subject, r.WorkspaceID, "manage") == nil {
				return dms.ErrForbidden
			}
			return dms.ErrNotFound
		}
		if r.Status != approvalrequest.StatusOpen {
			return fmt.Errorf("%w: the request is %s", dms.ErrConflict, r.Status)
		}
		var status approvalrequest.Status
		switch d.Decision {
		case "approve":
			status = approvalrequest.StatusApproved
		case "reject":
			status = approvalrequest.StatusRejected
		default:
			return dms.Invalid("decision is approve or reject")
		}
		if len([]rune(d.Comment)) > 4000 {
			return dms.Invalid("the comment is limited to 4000 characters")
		}
		var input json.RawMessage
		if r.InputSchema != nil {
			values, err := forms.Values(r.InputSchema, d.Input, "input")
			if err != nil {
				return err
			}
			if err = forms.CheckValues(ctx, t, subject, r.WorkspaceID, r.InputSchema, values, "input"); err != nil {
				return err
			}
			if input, err = json.Marshal(values); err != nil {
				return err
			}
		} else if len(d.Input) > 0 {
			return dms.Invalid("this request has no form")
		}
		if status == approvalrequest.StatusApproved {
			if id, _ := r.Review["staged_file_id"].(string); id != "" {
				f, err := t.Client.StagedFile.Get(ctx, id)
				if err != nil {
					return err
				}
				fresh, err := t.StagedFresh(ctx, f)
				if err != nil {
					return err
				}
				if !fresh {
					return ErrStaleReview
				}
			}
		}
		u := t.Client.ApprovalRequest.UpdateOne(r).SetStatus(status).SetDecidedBy(subject).SetDecidedAt(time.Now().UTC()).SetComment(d.Comment)
		if input != nil {
			u.SetInput(input)
		}
		if r, err = u.Save(ctx); err != nil {
			return err
		}
		ws, err := t.Client.Resource.Get(ctx, r.WorkspaceID)
		if err != nil {
			return err
		}
		if err = t.Record(ctx, subject, "approval.decide", ws, map[string]any{"approval_id": r.ID, "run_id": r.RunID, "decision": string(status)}); err != nil {
			return err
		}
		out = toApproval(r)
		return nil
	})
	return out, err
}

// decisionOutput is the approval node's output once decided.
func decisionOutput(r *ent.ApprovalRequest) map[string]any {
	out := map[string]any{"request_id": r.ID, "decision": string(r.Status), "comment": r.Comment}
	if r.DecidedBy != nil {
		out["decided_by"] = *r.DecidedBy
	}
	if len(r.Input) > 0 {
		var input any
		if json.Unmarshal(r.Input, &input) == nil {
			out["input"] = input
		}
	}
	return out
}

// Decided reads an approval request: its node output when it was decided,
// or nil while it is open. A cancelled request is an error.
func Decided(ctx context.Context, t *dms.Service, id string) (map[string]any, error) {
	r, err := t.Client.ApprovalRequest.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch r.Status {
	case approvalrequest.StatusOpen:
		return nil, nil
	case approvalrequest.StatusCancelled:
		return nil, errors.New("the approval request was cancelled")
	}
	return decisionOutput(r), nil
}
