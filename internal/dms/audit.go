package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/auditevent"
	"papergo/ent/resource"
)

// TaxonomyAudit exposes organization curation without requiring workspace access.
func (s *Service) TaxonomyAudit(ctx context.Context, subject, after string, limit int) (Page[*ent.AuditEvent], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.AuditEvent], error) {
		if err := t.requireTaxonomy(ctx, subject, "manage"); err != nil {
			return Page[*ent.AuditEvent]{}, err
		}
		rows, err := t.Client.AuditEvent.Query().Where(auditevent.WorkspaceIDIsNil(), auditevent.ResourceIDIsNil(), auditevent.IDGT(after)).Order(ent.Asc(auditevent.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.AuditEvent) string { return v.ID }), err
	})
}

func (s *Service) Audit(ctx context.Context, subject, id, after string, limit int) (Page[*ent.AuditEvent], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[*ent.AuditEvent], error) { return t.Audit(ctx, subject, id, after, limit) })
	}
	root, err := s.authorize(ctx, subject, id, "manage")
	if err != nil {
		return Page[*ent.AuditEvent]{}, err
	}
	if root.Kind != resource.KindWorkspace {
		return Page[*ent.AuditEvent]{}, invalid("audit scope must be a workspace")
	}
	q := s.Client.AuditEvent.Query().Where(auditevent.WorkspaceIDEQ(id))
	if after != "" {
		q.Where(auditevent.IDGT(after))
	}
	rows, err := q.Order(ent.Asc(auditevent.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
	return entityPage(rows, limit, func(v *ent.AuditEvent) string { return v.ID }), err
}
