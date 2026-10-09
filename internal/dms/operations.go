package dms

import (
	"context"
	"errors"
	"log/slog"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/itemsurface"
	"papergo/ent/operation"
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
func (s *Service) enqueueFieldIndex(ctx context.Context, subject string, d *ent.FieldDefinition) error {
	if _, err := s.Client.Operation.Update().Where(operation.FieldIDEQ(d.ID), operation.StatusEQ(operation.StatusPending)).SetStatus(operation.StatusSuperseded).Save(ctx); err != nil {
		return err
	}
	populated, err := s.Client.ItemSurface.Query().Where(itemsurface.ContainerIDEQ(d.ContainerID)).Exist(ctx)
	if err != nil {
		return err
	}
	if !populated {
		return s.Client.FieldDefinition.UpdateOneID(d.ID).SetIndexStatus(fielddefinition.IndexStatusReady).Exec(ctx)
	}
	if err = s.Client.FieldDefinition.UpdateOneID(d.ID).SetIndexStatus(fielddefinition.IndexStatusBuilding).Exec(ctx); err != nil {
		return err
	}
	if err = s.Client.Operation.Create().SetContainerID(d.ContainerID).SetFieldID(d.ID).SetKind(operation.KindFieldIndex).SetCreatedBy(subject).Exec(ctx); err != nil {
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
	for {
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

// operationStep advances the oldest pending operation by one batch. A batch
// error other than cancellation fails the operation and its field.
func (s *Service) operationStep(ctx context.Context) (bool, error) {
	var op *ent.Operation
	err := s.write(ctx, func(t *Service) error {
		var e error
		op, e = t.Client.Operation.Query().Where(operation.StatusEQ(operation.StatusPending)).Order(ent.Asc(operation.FieldCreatedAt), ent.Asc(operation.FieldID)).First(ctx)
		if ent.IsNotFound(e) {
			op = nil
			return nil
		}
		if e != nil {
			return e
		}
		return t.fieldIndexBatch(ctx, op)
	})
	if op == nil || err == nil || ctx.Err() != nil {
		return op != nil, err
	}
	failed := s.write(ctx, func(t *Service) error {
		if e := t.Client.Operation.UpdateOneID(op.ID).Where(operation.StatusEQ(operation.StatusPending)).SetStatus(operation.StatusFailed).SetError(err.Error()).Exec(ctx); e != nil {
			return e
		}
		e := t.Client.FieldDefinition.UpdateOneID(op.FieldID).SetIndexStatus(fielddefinition.IndexStatusFailed).Exec(ctx)
		if ent.IsNotFound(e) {
			return nil
		}
		return e
	})
	return true, errors.Join(err, failed)
}

// operationBatch is the surfaces one background batch reindexes while holding
// the writer. Measured with BenchmarkWriteDuringIndexBuild at 2000 items, a
// concurrent edit waits about 50 ms extra behind 500-surface batches, 27 ms
// behind 100 and 20 ms behind 50, where per-commit costs start to dominate.
var operationBatch = 100

// fieldIndexBatch converges the field's rows on the next operationBatch surfaces
// and records the resume point, finishing after the published surfaces.
func (s *Service) fieldIndexBatch(ctx context.Context, op *ent.Operation) error {
	d, err := s.Client.FieldDefinition.Get(ctx, op.FieldID)
	if ent.IsNotFound(err) {
		return s.Client.Operation.UpdateOne(op).SetStatus(operation.StatusSuperseded).Exec(ctx)
	}
	if err != nil {
		return err
	}
	projections, err := s.Client.ItemSurface.Query().Where(itemsurface.ContainerIDEQ(op.ContainerID), itemsurface.SurfaceEQ(itemsurface.Surface(op.Surface)), itemsurface.ItemIDGT(op.AfterItemID)).Order(ent.Asc(itemsurface.FieldItemID)).Limit(operationBatch).All(ctx)
	if err != nil {
		return err
	}
	if len(projections) > 0 {
		if err = s.reindexBatch(ctx, projections, d, map[string][]*ent.FieldDefinition{}); err != nil {
			return err
		}
	}
	b := s.Client.Operation.UpdateOne(op).AddProcessed(len(projections))
	switch {
	case len(projections) == operationBatch:
		return b.SetAfterItemID(projections[len(projections)-1].ItemID).Exec(ctx)
	case op.Surface == operation.SurfaceHead:
		return b.SetSurface(operation.SurfacePublished).SetAfterItemID("").Exec(ctx)
	}
	if err = b.SetStatus(operation.StatusSucceeded).Exec(ctx); err != nil {
		return err
	}
	return s.Client.FieldDefinition.UpdateOne(d).SetIndexStatus(fielddefinition.IndexStatusReady).Exec(ctx)
}

// Operations lists a collection's operations.
func (s *Service) Operations(ctx context.Context, subject, containerID, after string, limit int) (Page[*ent.Operation], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.Operation], error) {
		if _, e := t.authorize(ctx, subject, containerID, "read"); e != nil {
			return Page[*ent.Operation]{}, e
		}
		rows, e := t.Client.Operation.Query().Where(operation.ContainerIDEQ(containerID), operation.IDGT(after)).Order(ent.Asc(operation.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.Operation) string { return v.ID }), e
	})
}

// Operation returns one operation of a collection the caller can read.
func (s *Service) Operation(ctx context.Context, subject, id string) (*ent.Operation, error) {
	return read(ctx, s, func(t *Service) (*ent.Operation, error) {
		op, e := t.Client.Operation.Get(ctx, id)
		if ent.IsNotFound(e) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if _, e = t.authorize(ctx, subject, op.ContainerID, "read"); e != nil {
			return nil, e
		}
		return op, nil
	})
}

// queryable reports whether queries may filter, sort or group by a field.
func queryable(d *ent.FieldDefinition) bool {
	return d.Indexed && d.IndexStatus == fielddefinition.IndexStatusReady
}

// unqueryable explains why a custom field cannot be queried.
func unqueryable(key string, d *ent.FieldDefinition) error {
	if d != nil && d.Indexed {
		return invalid("field index is " + string(d.IndexStatus) + ": " + key)
	}
	return invalid("query fields must exist and be indexed: " + key)
}
