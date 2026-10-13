package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"papergo/ent"
	"papergo/ent/operation"
	"papergo/ent/operationitem"
	"papergo/internal/model"
	"time"
)

// FilteredBulkUpdate uses head values to select items. The runner freezes the
// matching IDs on its first step; edits cannot change membership mid-operation.
type FilteredBulkUpdate struct {
	ContentTypeID *string        `json:"content_type_id,omitempty"`
	Query         QuerySpec      `json:"query"`
	Name          *string        `json:"name,omitempty"`
	Tags          *[]string      `json:"tags,omitempty"`
	Values        map[string]any `json:"values,omitempty"`
}

func (s *Service) StartBulkUpdate(ctx context.Context, subject, collectionID string, in FilteredBulkUpdate) (out *ent.Operation, err error) {
	if in.Name == nil && in.Tags == nil && len(in.Values) == 0 && in.ContentTypeID == nil {
		return nil, invalid("a content type, name, tags or non-empty values patch is required")
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
		out, e = operationCredentials(ctx, t.Client.Operation.Create().SetContainerID(c.ID).SetKind(OperationKindBulkUpdate).SetCreatedBy(subject).SetPayload(payload).SetTraceID(t.traceID)).Save(ctx)
		if e != nil {
			return e
		}
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
func (s *Service) bulkUpdateStep(ctx context.Context, op *ent.Operation) error {
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
		args := append([]any{op.ID, time.Now().UTC(), op.ID}, q.args...)
		if _, e = s.Client.ExecContext(ctx, `INSERT INTO operation_items(id,created_at,operation_id,item_id) SELECT ?||':'||id,?,?,id FROM (`+q.sql+`)`, args...); e != nil {
			return e
		}
		matched, e := s.Client.OperationItem.Query().Where(operationitem.OperationIDEQ(op.ID)).Count(ctx)
		if e != nil {
			return e
		}
		b := s.Client.Operation.UpdateOne(op).SetPrepared(true).SetMatched(matched)
		if matched == 0 {
			b.SetStatus(operation.StatusSucceeded).SetProgress(100)
		}
		return b.Exec(ctx)
	}
	rows, e := s.Client.OperationItem.Query().Where(operationitem.OperationIDEQ(op.ID)).Order(ent.Asc(operationitem.FieldItemID)).Limit(bulkStepItems).All(ctx)
	if e != nil {
		return e
	}
	if len(rows) == 0 {
		return s.Client.Operation.UpdateOne(op).SetStatus(operation.StatusSucceeded).SetProgress(100).Exec(ctx)
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
		if e = s.Client.OperationItem.DeleteOne(row).Exec(ctx); e != nil {
			return e
		}
		processed++
		if time.Now().After(deadline) {
			break
		}
	}
	b := s.Client.Operation.UpdateOne(op).SetProcessed(processed).SetUpdated(updated).SetFailed(failed).SetFailures(failures).SetProgress(processed * 100 / op.Matched)
	if processed == op.Matched {
		b.SetStatus(operation.StatusSucceeded)
	}
	return b.Exec(ctx)
}

// Each bulk update step edits at most bulkStepItems items, and stops early
// once the budget is spent.
var bulkStepItems = 200

const bulkStepBudget = 250 * time.Millisecond

// bulkUpdateItem edits one item under a savepoint. An item that cannot be
// edited (gone, forbidden, changed, invalid) is rolled back and reported as
// itemErr; infrastructure failures stop the operation.
func (s *Service) bulkUpdateItem(ctx context.Context, op *ent.Operation, in FilteredBulkUpdate, itemID string) (itemErr, err error) {
	if _, err = s.tx.ExecContext(ctx, "SAVEPOINT bulk_item"); err != nil {
		return nil, err
	}
	queued := len(*s.pending)
	r, e := s.authorize(ctx, op.CreatedBy, itemID, "write")
	if e == nil {
		if r.ContainerID == nil || *r.ContainerID != op.ContainerID || !contentKind(r.Kind) {
			e = invalid("bulk target is outside the collection")
		} else {
			patch := UpdateResource{Name: in.Name, Tags: in.Tags, ContentTypeID: in.ContentTypeID}
			if len(in.Values) > 0 {
				values := maps.Clone(in.Values)
				patch.Values = &values
			}
			_, e = s.update(ctx, op.CreatedBy, itemID, r.Version, patch)
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if e != nil {
		var validation *ValidationError
		if !errors.Is(e, ErrForbidden) && !errors.Is(e, ErrNotFound) && !errors.Is(e, ErrConflict) && !errors.Is(e, ErrPreconditionFailed) && !ent.IsConstraintError(e) && !errors.As(e, &validation) {
			// Infrastructure failures stop the operation and roll back the step.
			return nil, e
		}
		if _, rollback := s.tx.ExecContext(ctx, "ROLLBACK TO bulk_item"); rollback != nil {
			return nil, errors.Join(e, rollback)
		}
		*s.pending = (*s.pending)[:queued]
	}
	if _, err = s.tx.ExecContext(ctx, "RELEASE bulk_item"); err != nil {
		return nil, err
	}
	return e, nil
}

func bulkFailureReason(e error) string {
	switch {
	case errors.Is(e, ErrForbidden):
		return "Access denied."
	case errors.Is(e, ErrNotFound):
		return "Item no longer exists."
	case errors.Is(e, ErrPreconditionFailed):
		return "Item changed since it was selected."
	case errors.Is(e, ErrConflict), ent.IsConstraintError(e):
		return "Item conflicts with a collection constraint."
	default:
		var validation *ValidationError
		if errors.As(e, &validation) {
			return e.Error()
		}
		return "Item update failed."
	}
}
