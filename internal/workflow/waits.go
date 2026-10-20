package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/dberr"
	waitstore "papergo/internal/dal/waits"
	"papergo/internal/dms"
)

// Completion separates execution failure and routing from an activity's data.
type Completion struct {
	Output map[string]any `json:"output,omitempty"`
	Port   string         `json:"port,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// WaitResolver adapts external business state to the common completion contract.
type WaitResolver func(context.Context, *dms.Service, Wait) (Completion, bool, error)

var waitResolvers = map[string]WaitResolver{}

func RegisterWaitResolver(kind string, resolve WaitResolver) {
	if kind == "" || resolve == nil || waitResolvers[kind] != nil {
		panic("invalid or duplicate wait resolver: " + kind)
	}
	waitResolvers[kind] = resolve
}

// ResolveWaits completes waits whose registered external source is ready.
func ResolveWaits(ctx context.Context, t *dms.Service) error {
	for kind, resolve := range waitResolvers {
		after := ""
		for {
			page, err := ListOpenWaits(ctx, t, WaitQuery{Kind: kind, After: after, Limit: maxWaitList})
			if err != nil {
				return err
			}
			for _, wait := range page.Data {
				completion, ready, err := resolve(ctx, t, wait)
				if err != nil {
					return err
				}
				if ready {
					if err = completeWait(ctx, t, wait.Kind, wait.Key, completion); err != nil && !errors.Is(err, dms.ErrConflict) {
						return err
					}
				}
			}
			if page.NextCursor == "" {
				break
			}
			after = page.NextCursor
		}
	}
	return nil
}

const (
	maxWaitList       = 100
	maxWaitData       = 16384
	maxWaitCompletion = 65536
)

// Wait is a durable completion rendezvous. Data is small; PayloadID refers
// to an activity record retained until consumption or cancellation. BlobIDs
// acquire explicit file leases in the same transaction as wait creation.
type Wait struct {
	Queue       string           `json:"queue,omitempty"`
	PayloadID   string           `json:"payload_id,omitempty"`
	BlobIDs     []string         `json:"-"`
	ID          string           `json:"id"`
	Kind        string           `json:"kind"`
	Key         string           `json:"key"`
	WorkspaceID string           `json:"workspace_id"`
	RunID       string           `json:"run_id"`
	Node        string           `json:"node"`
	ExecutionID string           `json:"execution_id"`
	Data        json.RawMessage  `json:"data"`
	Completion  json.RawMessage  `json:"completion,omitempty"`
	Deadline    *time.Time       `json:"deadline,omitempty"`
	ClaimRunID  *string          `json:"claim_run_id,omitempty"`
	ClaimToken  *string          `json:"claim_token,omitempty"`
	ClaimUntil  *time.Time       `json:"claim_until,omitempty"`
	NotifiedAt  *time.Time       `json:"notified_at,omitempty"`
	Status      waitstore.Status `json:"status"`
	Parking     bool             `json:"parking"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// WaitQuery lists open waits. An empty WorkspaceID is installation-wide and
// requires organization.manage. Limit is at most 100.
type WaitQuery struct {
	Queue       string
	Kind        string
	WorkspaceID string
	After       string
	Limit       int
}

// WaitClaim claims open waits for holder, which is a run id or another
// stable caller identity. A later update or completion must present the
// token this claim returns.
type WaitClaim struct {
	Kind        string
	Key         string
	WorkspaceID string
	Holder      string
	Limit       int
}

// WaitUpdate replaces the opaque data of an open wait. The caller is the
// run that owns it, or the holder of its claim token.
type WaitUpdate struct {
	Kind       string
	Key        string
	Data       json.RawMessage
	Holder     string
	ClaimToken string
}

func validateWaitIdentity(kind, key string) error {
	if kind == "" || len(kind) > 64 || key == "" || len(key) > 128 {
		return dms.Invalid("a wait needs a kind and a key within their length limits")
	}
	return nil
}

func objectJSON(raw json.RawMessage, limit int, name string) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if !json.Valid(raw) || raw[0] != '{' || len(raw) > limit {
		return nil, dms.Invalid(name + " must be a JSON object within its size limit")
	}
	return raw, nil
}

func toWait(row waitstore.Wait) Wait {
	w := Wait{
		Queue: row.Queue, PayloadID: row.PayloadID, ID: row.ID, Kind: row.Kind, Key: row.Key, WorkspaceID: row.WorkspaceID, RunID: row.RunID,
		Node: row.Node, ExecutionID: row.ExecutionID, Data: row.Data,
		Completion: row.Completion, Deadline: row.Deadline, ClaimRunID: optional(row.ClaimRunID),
		ClaimToken: optional(row.ClaimToken), ClaimUntil: row.ClaimUntil, NotifiedAt: row.NotifiedAt, Status: row.Status, Parking: row.Parking,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if len(w.Data) == 0 {
		w.Data = json.RawMessage(`{}`)
	}
	return w
}

// optional returns nil for an empty string.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// PutWait inserts a wait. A replay of the same run and execution leaves the
// stored data in place. The same kind and key from another execution conflicts.
func PutWait(ctx context.Context, t *dms.Service, in Wait) error {
	if err := validateWaitIdentity(in.Kind, in.Key); err != nil {
		return err
	}
	if in.WorkspaceID == "" || in.RunID == "" || in.Node == "" || in.ExecutionID == "" || len(in.ExecutionID) > 128 {
		return dms.Invalid("a wait needs its workspace, run, node and execution")
	}
	data, err := objectJSON(in.Data, maxWaitData, "wait data")
	if err != nil {
		return err
	}
	row, err := t.DAL.Waits().Put(ctx, waitstore.PutInput{
		Kind: in.Kind, Key: in.Key, WorkspaceID: in.WorkspaceID, RunID: in.RunID, Node: in.Node, ExecutionID: in.ExecutionID,
		Queue: in.Queue, PayloadID: in.PayloadID, Parking: in.Parking, Deadline: in.Deadline, Data: data,
	})
	if err != nil {
		return waitError(err)
	}
	// Closed waits have already released all retention and must not reacquire it.
	if row.Status == waitstore.StatusClosed {
		return nil
	}
	return retainWait(ctx, t, in, row.ID)
}

func waitError(err error) error {
	if dberr.IsNotFound(err) {
		return dms.ErrNotFound
	}
	if errors.Is(err, dberr.ErrConcurrentUpdate) {
		return dms.ErrConflict
	}
	return err
}

func loadWait(ctx context.Context, t *dms.Service, kind, key string) (waitstore.Wait, error) {
	row, err := t.DAL.Waits().Find(ctx, kind, key)
	return row, waitError(err)
}

// GetWait reads one wait by kind and key.
func GetWait(ctx context.Context, t *dms.Service, kind, key string) (Wait, error) {
	if err := validateWaitIdentity(kind, key); err != nil {
		return Wait{}, err
	}
	row, err := loadWait(ctx, t, kind, key)
	if err != nil {
		return Wait{}, err
	}
	return toWait(row), nil
}

// ConsumeWait releases payload references and byte leases after recording
// the completion, including a nonparking child of a joined activity.
func ConsumeWait(ctx context.Context, t *dms.Service, kind, key, runID string) error {
	row, err := loadWait(ctx, t, kind, key)
	if err != nil {
		return err
	}
	if row.RunID != runID {
		return dms.ErrForbidden
	}
	if row.Status == waitstore.StatusOpen {
		return dms.ErrConflict
	}

	err = t.DAL.Waits().Consume(ctx, kind, key, runID)
	if err != nil {
		return err
	}
	return releaseWait(ctx, t, kind, key)
}

// CompleteWait records a completion on an open wait and releases its claim.
// Completing an already completed wait with the same payload is a no-op.
func CompleteWait(ctx context.Context, t *dms.Service, kind, key string, output json.RawMessage) error {
	var values map[string]any
	dec := json.NewDecoder(strings.NewReader(string(output)))
	dec.UseNumber()
	if err := dec.Decode(&values); err != nil {
		return err
	}
	return completeWait(ctx, t, kind, key, Completion{Output: values})
}
func FailWait(ctx context.Context, t *dms.Service, kind, key, message string) error {
	return completeWait(ctx, t, kind, key, Completion{Error: message})
}
func completeWait(ctx context.Context, t *dms.Service, kind, key string, value Completion) error {
	completion, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := validateWaitIdentity(kind, key); err != nil {
		return err
	}
	payload, err := objectJSON(completion, maxWaitCompletion, "completion")
	if err != nil {
		return err
	}
	return waitError(t.DAL.Waits().Complete(ctx, kind, key, payload))
}

// CloseWait closes one open wait of a run, such as a deadline that passed
// before the run parked.
func CloseWait(ctx context.Context, t *dms.Service, kind, key, runID string) error {
	if err := t.DAL.Waits().Close(ctx, kind, key, runID); err != nil {
		return waitError(err)
	}
	return releaseWait(ctx, t, kind, key)
}

// CloseRunWaits closes the open waits a cancelled or deleted run owns,
// releases a park that completion had not recorded yet, and releases claims
// that run holds on other waits.
func CloseRunWaits(ctx context.Context, t *dms.Service, runID string) error {
	store := activitystore.NewTx(t.SQLTx())
	if err := store.ReleaseLeases(ctx, runID); err != nil {
		return err
	}
	if err := store.Release(ctx, runID); err != nil {
		return err
	}
	rows, err := t.DAL.Waits().List(ctx, waitstore.Filter{RunID: runID})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = releaseWait(ctx, t, row.Kind, row.Key); err != nil {
			return err
		}
	}
	if err = t.ReleaseRunBlobLeases(ctx, runID); err != nil {
		return err
	}
	return waitError(t.DAL.Waits().CloseRun(ctx, runID))
}

// DeleteRunWaits removes the waits of finished runs.
func DeleteRunWaits(ctx context.Context, t *dms.Service, runIDs []string) error {
	if len(runIDs) == 0 {
		return nil
	}
	return t.DAL.Waits().DeleteRuns(ctx, runIDs)
}

// ParkingRuns reports which runs an open parking wait is holding.
func ParkingRuns(ctx context.Context, t *dms.Service, runIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := t.DAL.Waits().List(ctx, waitstore.Filter{RunIDs: runIDs, Parking: true, Statuses: []waitstore.Status{waitstore.StatusOpen, waitstore.StatusCompleted}})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.RunID] = true
	}
	return out, nil
}

// ExpireWaits completes open waits whose deadline has passed. The completion
// follows the activity's normal resume and error handling path.
func ExpireWaits(ctx context.Context, t *dms.Service, now time.Time) error {
	rows, err := t.DAL.Waits().List(ctx, waitstore.Filter{Statuses: []waitstore.Status{waitstore.StatusOpen}, DeadlineBefore: &now, Limit: maxWaitList})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = FailWait(ctx, t, row.Kind, row.Key, "the wait reached its deadline"); err != nil && !errors.Is(err, dms.ErrConflict) {
			return err
		}
	}
	return nil
}

// CompletedParking returns generic waits whose completion is ready and whose
// run is still parked.
func CompletedParking(ctx context.Context, t *dms.Service) ([]Wait, error) {
	rows, err := t.DAL.Waits().List(ctx, waitstore.Filter{Statuses: []waitstore.Status{waitstore.StatusCompleted}, Parking: true, Unnotified: true, Limit: maxWaitList})
	if err != nil {
		return nil, err
	}
	out := make([]Wait, len(rows))
	for i, row := range rows {
		out[i] = toWait(row)
	}
	return out, nil
}

func bound(limit int) int {
	if limit <= 0 || limit > maxWaitList {
		return maxWaitList
	}
	return limit
}

// ListOpenWaits returns a page of open waits. The caller has already checked
// that an empty workspace is allowed.
func ListOpenWaits(ctx context.Context, t *dms.Service, q WaitQuery) (dms.Page[Wait], error) {
	if err := validateWaitIdentity(q.Kind, "list"); err != nil && q.Kind != "" {
		return dms.Page[Wait]{}, err
	}
	if q.Kind == "" {
		return dms.Page[Wait]{}, dms.Invalid("kind is required")
	}
	rows, err := t.DAL.Waits().List(ctx, waitstore.Filter{Kind: q.Kind, Queue: q.Queue, WorkspaceID: q.WorkspaceID, After: q.After, Statuses: []waitstore.Status{waitstore.StatusOpen}, Limit: bound(q.Limit) + 1})
	if err != nil {
		return dms.Page[Wait]{}, err
	}
	page := dms.Page[Wait]{Data: []Wait{}}
	if len(rows) > bound(q.Limit) {
		rows = rows[:bound(q.Limit)]
		page.NextCursor = rows[len(rows)-1].ID
	}
	for _, row := range rows {
		page.Data = append(page.Data, toWait(row))
	}
	return page, nil
}

// ClaimWaits claims up to Limit open waits for holder and returns them with
// the claim token. A key claims that wait only.
func ClaimWaits(ctx context.Context, t *dms.Service, in WaitClaim) ([]Wait, string, error) {
	if err := validateWaitIdentity(in.Kind, "claim"); err != nil && in.Key == "" {
		return nil, "", err
	}
	if in.Kind == "" {
		return nil, "", dms.Invalid("kind is required")
	}
	if in.Key != "" {
		if err := validateWaitIdentity(in.Kind, in.Key); err != nil {
			return nil, "", err
		}
	}
	if in.Holder == "" || len(in.Holder) > 80 {
		return nil, "", dms.Invalid("a claim needs a holder")
	}
	rows, token, err := t.DAL.Waits().Claim(ctx, waitstore.ClaimInput{Kind: in.Kind, Key: in.Key, WorkspaceID: in.WorkspaceID, Holder: in.Holder, Limit: bound(in.Limit)})
	if err != nil {
		return nil, "", waitError(err)
	}
	out := make([]Wait, len(rows))
	for i, row := range rows {
		out[i] = toWait(row)
	}
	return out, token, nil
}

// UpdateWaitData replaces the data of an open wait when holder owns the run
// or the claim.
func UpdateWaitData(ctx context.Context, t *dms.Service, in WaitUpdate) error {
	if err := validateWaitIdentity(in.Kind, in.Key); err != nil {
		return err
	}
	data, err := objectJSON(in.Data, maxWaitData, "wait data")
	if err != nil {
		return err
	}
	if in.Holder == "" {
		return dms.Invalid("an update needs its holder")
	}
	return waitError(t.DAL.Waits().UpdateData(ctx, in.Kind, in.Key, in.Holder, in.ClaimToken, data))
}

// ReleaseClaim drops a claim so another holder can take the wait.
func ReleaseClaim(ctx context.Context, t *dms.Service, kind, key, holder, token string) error {
	if err := validateWaitIdentity(kind, key); err != nil {
		return err
	}
	if holder == "" || token == "" {
		return dms.Invalid("releasing a claim needs its holder and token")
	}
	return waitError(t.DAL.Waits().ReleaseClaim(ctx, kind, key, holder, token))
}

func (s *Service) authorizeWaits(ctx context.Context, t *dms.Service, subject, workspaceID string) error {
	if workspaceID == "" {
		return t.RequireScope(ctx, subject, "organization.manage")
	}
	_, err := t.Authorize(ctx, subject, workspaceID, "manage")
	return err
}

// ListWaits lists open waits. An empty workspace is installation-wide.
func (s *Service) ListWaits(ctx context.Context, subject string, q WaitQuery) (dms.Page[Wait], error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (dms.Page[Wait], error) {
		if err := s.authorizeWaits(ctx, t, subject, q.WorkspaceID); err != nil {
			return dms.Page[Wait]{}, err
		}
		return ListOpenWaits(ctx, t, q)
	})
}

// ClaimWaitsFor claims waits after the same scope check as ListWaits.
func (s *Service) ClaimWaitsFor(ctx context.Context, subject string, in WaitClaim) (waits []Wait, token string, err error) {
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		if err := s.authorizeWaits(ctx, t, subject, in.WorkspaceID); err != nil {
			return err
		}
		waits, token, err = ClaimWaits(ctx, t, in)
		return err
	})
	return
}

// UpdateWait replaces wait data after the scope check. The wait's workspace
// is the scope; an installation-wide update requires organization.manage
// and an empty workspace on the request only when the caller passes it.
func (s *Service) UpdateWait(ctx context.Context, subject, workspaceID string, in WaitUpdate) error {
	return s.DMS.Write(ctx, func(t *dms.Service) error {
		if err := s.authorizeWaits(ctx, t, subject, workspaceID); err != nil {
			return err
		}
		if workspaceID != "" {
			row, err := loadWait(ctx, t, in.Kind, in.Key)
			if err != nil {
				return err
			}
			if row.WorkspaceID != workspaceID {
				return dms.ErrNotFound
			}
		}
		return UpdateWaitData(ctx, t, in)
	})
}

// FinishWait completes an open wait after the scope check.
func (s *Service) FinishWait(ctx context.Context, subject, workspaceID, kind, key string, completion Completion) error {
	return s.DMS.Write(ctx, func(t *dms.Service) error {
		if err := s.authorizeWaits(ctx, t, subject, workspaceID); err != nil {
			return err
		}
		if workspaceID != "" {
			row, err := loadWait(ctx, t, kind, key)
			if err != nil {
				return err
			}
			if row.WorkspaceID != workspaceID {
				return dms.ErrNotFound
			}
		}
		return completeWait(ctx, t, kind, key, completion)
	})
}

func retainWait(ctx context.Context, t *dms.Service, w Wait, id string) error {
	if err := t.LeaseBlobs(ctx, id, w.RunID, w.BlobIDs); err != nil {
		return err
	}
	if w.PayloadID != "" {
		return activitystore.NewTx(t.SQLTx()).Reference(ctx, id, w.PayloadID)
	}
	return nil
}
func releaseWait(ctx context.Context, t *dms.Service, kind, key string) error {
	row, err := loadWait(ctx, t, kind, key)
	if err != nil {
		return err
	}
	if err = t.ReleaseBlobLeases(ctx, row.ID); err != nil {
		return err
	}
	return activitystore.NewTx(t.SQLTx()).Release(ctx, row.ID)
}
