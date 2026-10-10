package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/operation"
	"papergo/ent/operationitem"
	"papergo/internal/model"
	"time"
)

// FilteredBulkUpdate uses head values to select items. The runner freezes the
// matching IDs on its first step; edits cannot change membership mid-operation.
type FilteredBulkUpdate struct {
	Query  QuerySpec      `json:"query"`
	Name   *string        `json:"name,omitempty"`
	Tags   *[]string      `json:"tags,omitempty"`
	Values map[string]any `json:"values,omitempty"`
}

func (s *Service) StartBulkUpdate(ctx context.Context, subject, collectionID string, in FilteredBulkUpdate) (out *ent.Operation, err error) {
	if in.Name == nil && in.Tags == nil && len(in.Values) == 0 {
		return nil, invalid("a name, tags or non-empty values patch is required")
	}
	if in.Query.Sort != (SortSpec{}) || in.Query.GroupBy != "" {
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
		out, e = t.Client.Operation.Create().SetContainerID(c.ID).SetKind(operation.KindBulkUpdate).SetCreatedBy(subject).SetPayload(payload).Save(ctx)
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

// bulkUpdateStep holds the writer for a single item. A savepoint discards a
// failed edit's projections, claims, audit and events while committing its
// failure and progress. Successful edits and completion markers commit together,
// so cancellation/restart cannot apply the same patch twice.
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
	row, e := s.Client.OperationItem.Query().Where(operationitem.OperationIDEQ(op.ID)).Order(ent.Asc(operationitem.FieldItemID)).First(ctx)
	if ent.IsNotFound(e) {
		return s.Client.Operation.UpdateOne(op).SetStatus(operation.StatusSucceeded).SetProgress(100).Exec(ctx)
	}
	if e != nil {
		return e
	}
	if _, e = s.tx.ExecContext(ctx, "SAVEPOINT bulk_item"); e != nil {
		return e
	}
	queued := len(*s.pending)
	r, e := s.authorize(ctx, op.CreatedBy, row.ItemID, "write")
	if e == nil {
		if r.ContainerID == nil || *r.ContainerID != op.ContainerID || r.Kind != "item" {
			e = invalid("bulk target is outside the collection")
		} else {
			patch := UpdateResource{Name: in.Name, Tags: in.Tags}
			if len(in.Values) > 0 {
				patch.Values = &in.Values
			}
			_, e = s.update(ctx, op.CreatedBy, row.ItemID, r.Version, patch)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil {
		if _, rollback := s.tx.ExecContext(ctx, "ROLLBACK TO bulk_item"); rollback != nil {
			return errors.Join(e, rollback)
		}
		*s.pending = (*s.pending)[:queued]
	}
	if _, release := s.tx.ExecContext(ctx, "RELEASE bulk_item"); release != nil {
		return release
	}
	updated, failed := op.Updated, op.Failed
	failures := op.Failures
	if e == nil {
		updated++
	} else {
		failed++
		if len(failures) < 50 {
			failures = append(failures, model.OperationFailure{ItemID: row.ItemID, Reason: bulkFailureReason(e)})
		}
	}
	if e = s.Client.OperationItem.DeleteOne(row).Exec(ctx); e != nil {
		return e
	}
	processed := op.Processed + 1
	b := s.Client.Operation.UpdateOne(op).SetProcessed(processed).SetUpdated(updated).SetFailed(failed).SetFailures(failures).SetProgress(processed * 100 / op.Matched)
	if processed == op.Matched {
		b.SetStatus(operation.StatusSucceeded)
	}
	return b.Exec(ctx)
}

func bulkFailureReason(e error) string {
	switch {
	case errors.Is(e, ErrForbidden):
		return "Access denied."
	case errors.Is(e, ErrNotFound):
		return "Item no longer exists."
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
