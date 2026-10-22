package resources

import (
	"context"
	"fmt"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/live"
	"papergo/internal/dal/refs"
)

// SetArchived changes discovery state without changing content or its timestamps.
func (r *Repo) SetArchived(ctx context.Context, id string, version int, actor string, archived bool) (changed bool, err error) {
	err = r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, id); err != nil {
			return err
		}
		row, err := q.FindResource(ctx, id)
		if err != nil {
			return err
		}
		if row.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if row.DeletedAt != nil || row.Kind != refs.KindItem {
			return fmt.Errorf("%w: only live items can be archived", dberr.ErrCheckViolation)
		}
		if (row.ArchivedAt != nil) == archived {
			return nil
		}
		state := &Deletion{}
		if archived {
			state.At, state.By = now(), actor
		}
		ok, err := q.UpdateResource(ctx, id, version, Set{Archived: state, Version: version + 1, UpdatedAt: row.UpdatedAt})
		if err != nil {
			return err
		}
		if !ok {
			return dberr.ErrConcurrentUpdate
		}
		changed = true
		return live.Notify(ctx, q, live.KindItem, id, live.ActionUpdated)
	})
	return
}
