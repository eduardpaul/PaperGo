package sqlite

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dm"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/events"
	"papergo/internal/model"
)

func (q Queries) DomainEvent(ctx context.Context, id string) (events.Event, error) {
	row, err := models.FindDomainEvent(ctx, q.exec, id)
	if err != nil {
		return events.Event{}, dberr.Translate(err)
	}
	return eventRecord(row), nil
}

// eventsMatching selects the events f matches.
func eventsMatching(f events.Filter) *sqlite.ViewQuery[*models.DomainEvent, models.DomainEventSlice] {
	query := models.DomainEvents.Query()
	w := models.SelectWhere.DomainEvents
	if len(f.Types) > 0 {
		query.Apply(w.Type.In(f.Types...))
	}
	if f.ResourceID != "" {
		query.Apply(w.ResourceID.EQ(f.ResourceID))
	}
	if f.Undispatched {
		query.Apply(w.DispatchedAt.IsNull())
	}
	if f.DispatchedThrough != nil {
		query.Apply(w.DispatchedAt.LTE(f.DispatchedThrough.UTC()))
	}
	return query
}

func (q Queries) DomainEvents(ctx context.Context, f events.Filter) ([]events.Event, error) {
	c := models.DomainEvents.Columns
	query := eventsMatching(f)
	query.Apply(sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID))
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]events.Event, len(rows))
	for i, row := range rows {
		out[i] = eventRecord(row)
	}
	return out, nil
}

func (q Queries) CountDomainEvents(ctx context.Context, f events.Filter) (int64, error) {
	n, err := eventsMatching(f).Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) InsertEvent(ctx context.Context, e events.Event) error {
	_, err := models.DomainEvents.Insert(&models.DomainEventSetter{
		ID: omit.From(e.ID), CreatedAt: omit.From(e.CreatedAt), Type: omit.From(e.Type), WorkspaceID: omit.From(e.WorkspaceID),
		CollectionID: nullable.String(e.CollectionID), ResourceID: nullable.String(e.ResourceID), Actor: omit.From(e.Actor),
		Data: omit.From(types.NewJSON(model.JSONMap(e.Data))), Depth: omit.From(int64(e.Depth)), CauseRunID: nullable.String(e.CauseRunID),
	}, im.OnConflict("id").DoNothing()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetEventFanout(ctx context.Context, id, cursor string) error {
	set := &models.DomainEventSetter{FanoutCursor: omitnull.From(cursor)}
	_, err := models.DomainEvents.Update(set.UpdateMod(), models.UpdateWhere.DomainEvents.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetEventsDispatched(ctx context.Context, ids []string, at time.Time) error {
	set := &models.DomainEventSetter{DispatchedAt: omitnull.From(at)}
	_, err := models.DomainEvents.Update(set.UpdateMod(), models.UpdateWhere.DomainEvents.ID.In(ids...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteDispatchedEvents(ctx context.Context, before time.Time, limit int) (int64, error) {
	page := models.DomainEvents.Query(sm.Columns(models.DomainEvents.Columns.ID), models.SelectWhere.DomainEvents.DispatchedAt.LT(before), sm.Limit(limit))
	n, err := models.DomainEvents.Delete(dm.Where(sqlite.Raw(`"id" IN ?`, page))).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

func eventRecord(r *models.DomainEvent) events.Event {
	return events.Event{
		ID: r.ID, Type: r.Type, WorkspaceID: r.WorkspaceID, CollectionID: r.CollectionID.GetOrZero(), ResourceID: r.ResourceID.GetOrZero(),
		Actor: r.Actor, CauseRunID: r.CauseRunID.GetOrZero(), FanoutCursor: r.FanoutCursor.GetOrZero(), Data: r.Data.Val,
		Depth: int(r.Depth), CreatedAt: r.CreatedAt.UTC(), DispatchedAt: nullable.UTC(r.DispatchedAt),
	}
}
