package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
	"papergo/internal/model"
	"time"
)

// FilteredBulkUpdate uses head values to select items. The runner freezes the
// matching IDs on its first step; edits cannot change membership mid-operation.
type FilteredBulkUpdate struct {
	ContentTypeID *string        `json:"content_type_id,omitempty"`
	Query         QuerySpec      `json:"query"`
	Name          *string        `json:"name,omitempty"`
	Keywords      *[]string      `json:"keywords,omitempty"`
	Values        map[string]any `json:"values,omitempty"`
}

func (s *Service) StartBulkUpdate(ctx context.Context, subject, collectionID string, in FilteredBulkUpdate) (out *OperationView, err error) {
	if in.Name == nil && in.Keywords == nil && len(in.Values) == 0 && in.ContentTypeID == nil {
		return nil, invalid("a content type, name, keywords or non-empty values patch is required")
	}
	if len(in.Query.Sort) != 0 || in.Query.GroupBy != "" {
		return nil, invalid("bulk selection does not accept sort or group_by")
	}
	err = s.write(ctx, func(t *Service) error {
		c, e := t.authorize(ctx, subject, collectionID, "write")
		if e != nil {
			return e
		}
		if _, e = t.compileQuery(ctx, subject, c.ID, QueryRequest{Query: in.Query, Surface: "head"}, false); e != nil {
			return e
		}
		payload, e := json.Marshal(in)
		if e != nil {
			return e
		}
		op, e := t.enqueue(ctx, operations.Operation{ContainerID: c.ID, Kind: OperationKindBulkUpdate, CreatedBy: subject, Payload: payload})
		if e != nil {
			return e
		}
		out = viewOperation(op)
		return t.audit(ctx, subject, "bulk_update.start", c, map[string]any{"operation_id": out.ID})
	})
	if err == nil {
		select {
		case s.operations <- struct{}{}:
		default:
		}
	}
	return
}

// bulkUpdateStep holds the writer for a batch of items. A savepoint per item
// discards a failed edit's projections, claims, audit and events while
// committing its failure and progress. Successful edits and completion markers
// commit together, so cancellation/restart cannot apply the same patch twice.
func (s *Service) bulkUpdateStep(ctx context.Context, op *operations.Operation) error {
	if _, e := s.authorize(ctx, op.CreatedBy, op.ContainerID, "write"); e != nil {
		return e
	}
	var in FilteredBulkUpdate
	// UseNumber preserves exact integer/decimal literals across durable storage.
	decoder := json.NewDecoder(bytes.NewReader(op.Payload))
	decoder.UseNumber()
	if e := decoder.Decode(&in); e != nil {
		return e
	}
	if !op.Prepared {
		q, e := s.compileQuery(ctx, op.CreatedBy, op.ContainerID, QueryRequest{Query: in.Query, Surface: "head"}, false)
		if e != nil {
			return e
		}
		if e = s.DAL.Content().QueueOperation(ctx, q.q, op.ID, time.Now().UTC()); e != nil {
			return e
		}
		matched, e := s.DAL.Operations().CountItems(ctx, op.ID)
		if e != nil {
			return e
		}
		op.Prepared, op.Matched = true, int(matched)
		if matched == 0 {
			op.Status, op.Progress = operations.StatusSucceeded, 100
		}
		return s.DAL.Operations().Save(ctx, op)
	}
	rows, e := s.DAL.Operations().Items(ctx, op.ID, bulkStepItems)
	if e != nil {
		return e
	}
	if len(rows) == 0 {
		op.Status, op.Progress = operations.StatusSucceeded, 100
		return s.DAL.Operations().Save(ctx, op)
	}
	// One transaction edits a batch of items, each under its own savepoint;
	// the time budget keeps other writers from waiting long behind it.
	s.batch = newBatchCache(op.CreatedBy)
	defer func() { s.batch = nil }()
	deadline := time.Now().Add(bulkStepBudget)
	processed, updated, failed := op.Processed, op.Updated, op.Failed
	failures := op.Failures
	for _, row := range rows {
		itemErr, err := s.bulkUpdateItem(ctx, op, in, row.ItemID)
		if err != nil {
			return err
		}
		if itemErr == nil {
			updated++
		} else {
			failed++
			if len(failures) < 50 {
				failures = append(failures, model.OperationFailure{ItemID: row.ItemID, Reason: bulkFailureReason(itemErr)})
			}
		}
		if e = s.DAL.Operations().RemoveItem(ctx, row.ID); e != nil {
			return e
		}
		processed++
		if time.Now().After(deadline) {
			break
		}
	}
	op.Processed, op.Updated, op.Failed, op.Failures, op.Progress = processed, updated, failed, failures, processed*100/op.Matched
	if processed == op.Matched {
		op.Status = operations.StatusSucceeded
	}
	return s.DAL.Operations().Save(ctx, op)
}

// Each bulk update step edits at most bulkStepItems items, and stops early
// once bulkStepBudget is spent.
var (
	bulkStepItems  = 200
	bulkStepBudget = 250 * time.Millisecond
)

// bulkUpdateItem edits one item under a savepoint. An item that cannot be
// edited (gone, forbidden, changed, invalid) is rolled back and reported as
// itemErr; infrastructure failures stop the operation.
func (s *Service) bulkUpdateItem(ctx context.Context, op *operations.Operation, in FilteredBulkUpdate, itemID string) (itemErr, err error) {
	err = s.DAL.Savepoint(ctx, "bulk_item", func() (bool, error) {
		queued := len(*s.pending)
		r, e := s.authorize(ctx, op.CreatedBy, itemID, "write")
		if e == nil {
			if r.ContainerID == nil || *r.ContainerID != op.ContainerID || !contentKind(r.Kind) {
				e = invalid("bulk target is outside the collection")
			} else {
				patch := UpdateResource{Name: in.Name, Keywords: in.Keywords, ContentTypeID: in.ContentTypeID}
				if len(in.Values) > 0 {
					values := maps.Clone(in.Values)
					patch.Values = &values
				}
				_, e = s.update(ctx, op.CreatedBy, itemID, r.Version, patch)
			}
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if e == nil {
			return false, nil
		}
		var validation *ValidationError
		if !errors.Is(e, ErrForbidden) && !errors.Is(e, ErrNotFound) && !errors.Is(e, ErrConflict) && !errors.Is(e, ErrPreconditionFailed) && !dberr.IsConstraintError(e) && !errors.As(e, &validation) {
			// Infrastructure failures stop the operation and roll back the step.
			return false, e
		}
		itemErr = e
		*s.pending = (*s.pending)[:queued]
		return true, nil
	})
	return itemErr, err
}

func bulkFailureReason(e error) string {
	switch {
	case errors.Is(e, ErrForbidden):
		return "Access denied."
	case errors.Is(e, ErrNotFound):
		return "Item no longer exists."
	case errors.Is(e, ErrPreconditionFailed):
		return "Item changed since it was selected."
	case errors.Is(e, ErrConflict), dberr.IsConstraintError(e):
		return "Item conflicts with a collection constraint."
	default:
		var validation *ValidationError
		if errors.As(e, &validation) {
			return e.Error()
		}
		return "Item update failed."
	}
}
