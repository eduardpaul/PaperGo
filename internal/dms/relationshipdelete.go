package dms

import (
	"context"
)

func (s *Service) Unlink(ctx context.Context, subject, itemID, linkID string) error {
	return s.write(ctx, func(t *Service) error {
		source, e := t.authorize(ctx, subject, itemID, "write")
		if e != nil {
			return e
		}
		link, e := t.itemLink(ctx, itemID, linkID)
		if e != nil {
			return e
		}
		for _, id := range []string{link.SourceID, link.TargetID} {
			if _, e = t.authorize(ctx, subject, id, "write"); e != nil {
				return e
			}
		}
		if e = t.Client.Relationship.DeleteOne(link).Exec(ctx); e != nil {
			return e
		}
		if e = t.touchRelationship(ctx, subject, link, "deleted"); e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship.delete", source, map[string]any{"relationship_id": linkID})
	})
}
