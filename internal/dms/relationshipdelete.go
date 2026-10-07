package dms

import (
	"context"
)

func (s *Service) UnlinkVersion(ctx context.Context, subject, itemID, linkID string, version int) error {
	return s.write(ctx, func(t *Service) error {
		source, e := t.authorize(ctx, subject, itemID, "write")
		if e != nil {
			return e
		}
		link, e := t.itemLink(ctx, itemID, linkID)
		if e != nil {
			return e
		}
		if link.Version != version {
			return ErrConflict
		}
		if e = t.Client.Relationship.DeleteOne(link).Exec(ctx); e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship.delete", source, map[string]any{"relationship_id": linkID})
	})
}
