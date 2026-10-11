package dms

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"papergo/ent"
	"papergo/ent/auditevent"
	"time"
)

type AuditQuery struct {
	Actor, EntityType, EntityID, WorkspaceID, After string
	From, To                                        *time.Time
	Limit                                           int
}
type auditCursor struct {
	At time.Time
	ID string
}

func (s *Service) OrganizationAudit(ctx context.Context, subject string, in AuditQuery) (Page[*ent.AuditEvent], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.AuditEvent], error) {
		if err := t.RequireScope(ctx, subject, "audit.read"); err != nil {
			return Page[*ent.AuditEvent]{}, err
		}
		if in.From != nil && in.To != nil && !in.From.Before(*in.To) {
			return Page[*ent.AuditEvent]{}, invalid("from must precede to")
		}
		q := t.Client.AuditEvent.Query()
		if in.Actor != "" {
			q.Where(auditevent.SubjectEQ(in.Actor))
		}
		if in.EntityType != "" {
			q.Where(auditevent.EntityTypeEQ(in.EntityType))
		}
		if in.EntityID != "" {
			q.Where(auditevent.EntityIDEQ(in.EntityID))
		}
		if in.WorkspaceID != "" {
			q.Where(auditevent.WorkspaceIDEQ(in.WorkspaceID))
		}
		if in.From != nil {
			q.Where(auditevent.CreatedAtGTE(in.From.UTC()))
		}
		if in.To != nil {
			q.Where(auditevent.CreatedAtLT(in.To.UTC()))
		}
		if in.After != "" {
			var cursor auditCursor
			raw, err := base64.RawURLEncoding.DecodeString(in.After)
			if err != nil || len(raw) > 256 || json.Unmarshal(raw, &cursor) != nil || cursor.At.IsZero() {
				return Page[*ent.AuditEvent]{}, invalid("invalid audit continuation")
			}
			if _, err = uuid.Parse(cursor.ID); err != nil {
				return Page[*ent.AuditEvent]{}, invalid("invalid audit continuation")
			}
			q.Where(auditevent.Or(auditevent.CreatedAtLT(cursor.At.UTC()), auditevent.And(auditevent.CreatedAtEQ(cursor.At.UTC()), auditevent.IDLT(cursor.ID))))
		}
		rows, err := q.Order(ent.Desc(auditevent.FieldCreatedAt), ent.Desc(auditevent.FieldID)).Limit(pageSize(in.Limit) + 1).All(ctx)
		if err != nil {
			return Page[*ent.AuditEvent]{}, err
		}
		return entityPage(rows, in.Limit, func(v *ent.AuditEvent) string {
			raw, _ := json.Marshal(auditCursor{At: v.CreatedAt.UTC(), ID: v.ID})
			return base64.RawURLEncoding.EncodeToString(raw)
		}), nil
	})
}
