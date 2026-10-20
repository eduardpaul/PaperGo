package postgres

import (
	"context"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/audit"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
)

// auditMatching selects the events f matches, without its page.
func auditMatching(f audit.Filter) *psql.ViewQuery[*models.AuditEvent, models.AuditEventSlice] {
	query := models.AuditEvents.Query()
	w := models.SelectWhere.AuditEvents
	if f.Actor != "" {
		query.Apply(w.Subject.EQ(f.Actor))
	}
	if f.EntityType != "" {
		query.Apply(w.EntityType.EQ(f.EntityType))
	}
	if f.EntityID != "" {
		query.Apply(w.EntityID.EQ(f.EntityID))
	}
	if f.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(f.WorkspaceID))
	}
	if f.ResourceID != "" {
		query.Apply(w.ResourceID.EQ(f.ResourceID))
	}
	if f.Action != "" {
		query.Apply(w.Action.EQ(f.Action))
	}
	if f.From != nil {
		query.Apply(w.CreatedAt.GTE(f.From.UTC()))
	}
	if f.To != nil {
		query.Apply(w.CreatedAt.LT(f.To.UTC()))
	}
	return query
}

func (q Queries) CountAuditEvents(ctx context.Context, f audit.Filter) (int64, error) {
	n, err := auditMatching(f).Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) AuditEvents(ctx context.Context, f audit.Filter) ([]audit.Event, error) {
	query := auditMatching(f)
	c := models.AuditEvents.Columns
	if f.AfterAt != nil {
		query.Apply(sm.Where(psql.Group(c.CreatedAt, c.ID).LT(psql.ArgGroup(f.AfterAt.UTC(), f.AfterID))))
	}
	query.Apply(sm.OrderBy(c.CreatedAt).Desc(), sm.OrderBy(c.ID).Desc())
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]audit.Event, len(rows))
	for i, r := range rows {
		out[i] = audit.Event{
			ID: r.ID, WorkspaceID: r.WorkspaceID.GetOrZero(), ResourceID: r.ResourceID.GetOrZero(), Subject: r.Subject, Action: r.Action,
			EntityType: r.EntityType, EntityID: r.EntityID.GetOrZero(), TraceID: r.TraceID, Properties: r.Properties.Val, Details: r.Details.Val,
			CreatedAt: r.CreatedAt.UTC(),
		}
	}
	return out, nil
}

func (q Queries) InsertAuditEvent(ctx context.Context, e audit.Event) error {
	_, err := models.AuditEvents.Insert(&models.AuditEventSetter{
		ID: omit.From(e.ID), CreatedAt: omit.From(e.CreatedAt), WorkspaceID: nullable.String(e.WorkspaceID), ResourceID: nullable.String(e.ResourceID),
		Subject: omit.From(e.Subject), Action: omit.From(e.Action), EntityType: omit.From(e.EntityType), EntityID: nullable.String(e.EntityID),
		Properties: omit.From(types.NewJSON(e.Properties)), TraceID: omit.From(e.TraceID), Details: omit.From(types.NewJSON(e.Details)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) AuditRecorded(ctx context.Context, entityType, entityID, action string) (bool, error) {
	w := models.SelectWhere.AuditEvents
	ok, err := models.AuditEvents.Query(w.EntityType.EQ(entityType), w.EntityID.EQ(entityID), w.Action.EQ(action)).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}
