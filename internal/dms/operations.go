package dms

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/surfaces"
	"papergo/internal/model"
	"time"
)

// Collection-wide index maintenance runs as tracked operations. Changing a
// field's indexing commits at once and marks the field building; queries reject
// it until RunOperations has converged its field_values rows, one operationBatch
// per write transaction, and marked it ready. Item writes between batches index
// the field themselves (indexedDefinitions follows the live definition), and a
// batch re-derives rows from the surface it reads, so the result is exact.

// enqueueFieldIndex supersedes the field's earlier operation and queues one
// that converges its index rows. A collection without items needs no rows.
func (s *Service) enqueueFieldIndex(ctx context.Context, subject string, d *catalog.FieldDefinition) error {
	if err := s.DAL.Operations().Supersede(ctx, operations.Filter{FieldID: d.ID}); err != nil {
		return err
	}
	collections, err := s.fieldCollections(ctx, d.ID)
	if err != nil {
		return err
	}
	queued := false
	for _, c := range collections {
		populated, e := s.DAL.Surfaces().Exists(ctx, surfaces.Filter{ContainerID: c.ID})
		if e != nil {
			return e
		}
		if !populated {
			continue
		}
		if _, e = s.DAL.Operations().Enqueue(ctx, operations.Operation{ContainerID: c.ID, FieldID: d.ID, Kind: OperationKindFieldIndex, CreatedBy: subject, TraceID: s.traceID}); e != nil {
			return e
		}
		queued = true
	}
	status := catalog.IndexReady
	if queued {
		status = catalog.IndexBuilding
	}
	if err = s.DAL.Catalog().SetIndexStatus(ctx, []string{d.ID}, status); err != nil {
		return err
	}
	// The runner's batch waits for this transaction's writer slot, so it
	// sees the operation once this commits.
	select {
	case s.operations <- struct{}{}:
	default:
	}
	return nil
}

// RunOperations processes pending operations until ctx ends, resuming any left
// by an earlier process.
func (s *Service) RunOperations(ctx context.Context, log *slog.Logger) {
	nextCleanup := time.Time{}
	for {
		if now := time.Now().UTC(); !now.Before(nextCleanup) {
			if err := s.CollectExpiredOperations(ctx, now); err != nil && ctx.Err() == nil {
				log.Error("operation cleanup failed", "error", err)
			}
			if err := s.CollectExpiredDeltas(ctx, now); err != nil && ctx.Err() == nil {
				log.Error("delta cleanup failed", "error", err)
			}
			if err := s.CollectExpiredLiveChanges(ctx, now); err != nil && ctx.Err() == nil {
				log.Error("live change cleanup failed", "error", err)
			}
			nextCleanup = now.Add(time.Hour)
		}
		worked, err := s.operationStep(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error("background operation failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.operations:
		case <-time.After(time.Minute):
		}
	}
}

// runOperations drains every pending operation.
func (s *Service) runOperations(ctx context.Context) error {
	for {
		worked, err := s.operationStep(ctx)
		if err != nil || !worked {
			return err
		}
	}
}

// operationStep advances the oldest pending operation by one index batch or
// bulk item. An operation error fails it; item errors are bulk results.
func (s *Service) operationStep(ctx context.Context) (bool, error) {
	var op *operations.Operation
	var external func(context.Context, *Service, *operations.Operation) error
	err := s.write(ctx, func(t *Service) error {
		next, e := t.DAL.Operations().Next(ctx)
		if dberr.IsNotFound(e) {
			op = nil
			return nil
		}
		if e != nil {
			return e
		}
		op = &next
		handler, exists := t.operationHandlers[op.Kind]
		if !exists {
			return errUnknownOperation
		}
		ctx, e = t.operationContext(ctx, op)
		if e != nil {
			return e
		}
		t.traceID = op.TraceID
		// Field index builds and PaperGo's own search rebuilds act for no one.
		if op.Kind != OperationKindFieldIndex && (op.Kind != OperationKindSearchReindex || op.CreatedBy != systemSubject) {
			if e = t.operationPermission(ctx, op.CreatedBy, op.ContainerID, handler.Scope); e != nil {
				return e
			}
		}
		if op.StartedAt == nil {
			started := time.Now().UTC()
			op.StartedAt = &started
			if e = t.DAL.Operations().Save(ctx, op); e != nil {
				return e
			}
		}
		if handler.ExternalStep != nil {
			external = handler.ExternalStep
			return nil
		}
		previousState := operationCheckpoint(op)
		if e = executeOperation(ctx, t, handler, op); e != nil {
			return e
		}
		current, e := t.DAL.Operations().Find(ctx, op.ID)
		if e != nil {
			return e
		}
		if current.Status != operations.StatusPending {
			completed := time.Now().UTC()
			current.CompletedAt = &completed
			if current.Status == operations.StatusSucceeded {
				current.Progress = 100
			}
			return t.DAL.Operations().Save(ctx, &current)
		}
		if operationCheckpoint(&current) == previousState {
			return invalid("operation handler did not persist progress")
		}
		return nil
	})
	if err == nil && external != nil {
		err = executeExternalOperation(ctx, s, external, op)
		if errors.Is(err, ErrIntegrationBusy) {
			return false, nil
		}
	}
	if op == nil || err == nil || ctx.Err() != nil {
		return op != nil, err
	}
	failed := s.failOperation(ctx, op, err)
	return true, errors.Join(err, failed)
}

// operationBatch is the surfaces one background batch reindexes while holding
// the writer. Measured with BenchmarkWriteDuringIndexBuild at 2000 items, a
// concurrent edit waits about 50 ms extra behind 500-surface batches, 27 ms
// behind 100 and 20 ms behind 50, where per-commit costs start to dominate.
var operationBatch = 100

// fieldIndexBatch converges the field's rows on the next operationBatch surfaces
// and records the resume point, finishing after the published surfaces.
func (s *Service) fieldIndexBatch(ctx context.Context, op *operations.Operation) error {
	d, err := s.DAL.Catalog().Field(ctx, op.FieldID)
	if dberr.IsNotFound(err) {
		op.Status = operations.StatusSuperseded
		return s.DAL.Operations().Save(ctx, op)
	}
	if err != nil {
		return err
	}
	projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ContainerID: op.ContainerID, Surface: op.Surface, AfterItemID: op.AfterItemID, Limit: operationBatch})
	if err != nil {
		return err
	}
	if len(projections) > 0 {
		if err = s.reindexBatch(ctx, projections, d, map[string][]*catalog.FieldDefinition{}); err != nil {
			return err
		}
	}
	op.Processed += len(projections)
	switch {
	case len(projections) == operationBatch:
		op.AfterItemID = projections[len(projections)-1].ItemID
		return s.DAL.Operations().Save(ctx, op)
	case op.Surface == refs.SurfaceHead:
		op.Surface, op.AfterItemID = refs.SurfacePublished, ""
		return s.DAL.Operations().Save(ctx, op)
	}
	op.Status = operations.StatusSucceeded
	if err = s.DAL.Operations().Save(ctx, op); err != nil {
		return err
	}
	pending, err := s.DAL.Operations().Exists(ctx, operations.Filter{FieldID: d.ID, Statuses: []operations.Status{operations.StatusPending, operations.StatusFailed}})
	if err != nil {
		return err
	}
	if pending {
		return nil
	}
	return s.DAL.Catalog().SetIndexStatus(ctx, []string{d.ID}, catalog.IndexReady)
}

// Operations lists index maintenance and the caller's own bulk operations.
func (s *Service) Operations(ctx context.Context, subject, containerID, after string, limit int) (Page[*OperationView], error) {
	return read(ctx, s, func(t *Service) (Page[*OperationView], error) {
		if _, e := t.authorize(ctx, subject, containerID, "read"); e != nil {
			return Page[*OperationView]{}, e
		}
		rows, e := t.DAL.Operations().List(ctx, operations.Filter{ContainerID: containerID, AfterID: after, OwnedBy: subject, SharedKinds: []string{OperationKindFieldIndex}, Limit: pageSize(limit) + 1})
		views := make([]*OperationView, len(rows))
		for i, row := range rows {
			views[i] = viewOperation(row)
		}
		return entityPage(views, limit, func(v *OperationView) string { return v.ID }), e
	})
}

// Operation requires collection read access; bulk results also require ownership.
func (s *Service) Operation(ctx context.Context, subject, id string) (*OperationView, error) {
	return read(ctx, s, func(t *Service) (*OperationView, error) {
		op, e := t.DAL.Operations().Find(ctx, id)
		if dberr.IsNotFound(e) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if op.Kind != OperationKindFieldIndex && op.CreatedBy != subject {
			return nil, ErrNotFound
		}
		if op.ContainerID == "" {
			if e = t.RequireScope(ctx, subject, "organization.manage"); e != nil {
				return nil, e
			}
			return viewOperation(op), nil
		}
		if _, e = t.authorize(ctx, subject, op.ContainerID, "read"); e != nil {
			return nil, e
		}
		return viewOperation(op), nil
	})
}

// queryable reports whether queries may filter, sort or group by a field.
func queryable(d *catalog.FieldDefinition) bool {
	return d.Indexed && d.IndexStatus == catalog.IndexReady
}

// unqueryable explains why a custom field cannot be queried.
func unqueryable(key string, d *catalog.FieldDefinition) error {
	if d != nil && d.Indexed {
		return invalid("field index is " + string(d.IndexStatus) + ": " + key)
	}
	return invalid("query fields must exist and be indexed: " + key)
}

// OperationView is an operation as the API shows it. Payloads, credential
// constraints and resume points stay private.
type OperationView struct {
	ID          string                   `json:"id"`
	CreatedAt   time.Time                `json:"created_at"`
	UpdatedAt   time.Time                `json:"updated_at"`
	ContainerID string                   `json:"container_id,omitempty"`
	FieldID     string                   `json:"field_id"`
	Kind        string                   `json:"kind"`
	TraceID     string                   `json:"trace_id,omitempty"`
	Status      operations.Status        `json:"status"`
	Result      json.RawMessage          `json:"result,omitempty"`
	StartedAt   *time.Time               `json:"started_at,omitempty"`
	CompletedAt *time.Time               `json:"completed_at,omitempty"`
	Processed   int                      `json:"processed,omitempty"`
	Matched     int                      `json:"matched,omitempty"`
	Updated     int                      `json:"updated,omitempty"`
	Failed      int                      `json:"failed,omitempty"`
	Progress    int                      `json:"progress,omitempty"`
	Failures    []model.OperationFailure `json:"failures,omitempty"`
	Error       string                   `json:"error,omitempty"`
	CreatedBy   string                   `json:"created_by,omitempty"`
}

func viewOperation(op operations.Operation) *OperationView {
	return &OperationView{
		ID: op.ID, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt, ContainerID: op.ContainerID, FieldID: op.FieldID, Kind: op.Kind,
		TraceID: op.TraceID, Status: op.Status, Result: op.Result, StartedAt: op.StartedAt, CompletedAt: op.CompletedAt,
		Processed: op.Processed, Matched: op.Matched, Updated: op.Updated, Failed: op.Failed, Progress: op.Progress,
		Failures: op.Failures, Error: op.Error, CreatedBy: op.CreatedBy,
	}
}
