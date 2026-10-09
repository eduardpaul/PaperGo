package dms

import (
	"context"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent"
	"papergo/ent/relationship"
	"papergo/ent/resource"
	"strings"
	"unicode"
)

// Every relationship has a type; its name, direction, attributes and
// cardinality all come from that type.
type CreateRelationship struct {
	TypeID   string         `json:"type_id" format:"uuid" doc:"Relationship type; it supplies the name, direction, attributes and cardinality."`
	TargetID string         `json:"target_id" format:"uuid"`
	Metadata map[string]any `json:"metadata" required:"false" doc:"Attribute values validated by the relationship type."`
}

func (s *Service) Link(ctx context.Context, subject, sourceID string, in CreateRelationship) (out *ent.Relationship, err error) {
	if in.TypeID == "" {
		return nil, invalid("type_id is required")
	}
	if sourceID == in.TargetID {
		return nil, invalid("self relationships are not supported")
	}
	if in.Metadata == nil {
		in.Metadata = map[string]any{}
	}
	encoded, e := json.Marshal(in.Metadata)
	if e != nil || len(encoded) > 16*1024 {
		return nil, invalid("relationship metadata is limited to 16 KiB")
	}
	err = s.write(ctx, func(t *Service) error {
		source, e := t.authorize(ctx, subject, sourceID, "write")
		if e != nil {
			return e
		}
		target, e := t.Get(ctx, subject, in.TargetID)
		if e != nil {
			return e
		}
		if source.Kind != resource.KindItem || target.Kind != resource.KindItem {
			return invalid("relationships connect items")
		}
		if source.WorkspaceID != target.WorkspaceID {
			return invalid("relationships must remain within one workspace")
		}
		typ, e := t.Client.RelationshipType.Get(ctx, in.TypeID)
		if ent.IsNotFound(e) {
			return invalid("unknown relationship type")
		}
		if e != nil {
			return e
		}
		if typ.WorkspaceID != source.WorkspaceID {
			return invalid("relationship type belongs to another workspace")
		}
		if e = normalizeMetadata(typ.Attributes, in.Metadata); e != nil {
			return e
		}
		sourceKey, targetKey := sourceID, in.TargetID
		if !typ.Directed && sourceKey > targetKey {
			sourceKey, targetKey = targetKey, sourceKey
		}
		for i, max := range []*int{typ.MaxOutgoing, typ.MaxIncoming} {
			if max == nil {
				continue
			}
			q := t.Client.Relationship.Query().Where(relationship.TypeIDEQ(typ.ID))
			if i == 0 {
				q.Where(relationship.SourceIDEQ(sourceKey))
			} else {
				q.Where(relationship.TargetIDEQ(targetKey))
			}
			n, e := q.Count(ctx)
			if e != nil {
				return e
			}
			if n >= *max {
				return ErrConflict
			}
		}
		out, e = t.Client.Relationship.Create().SetWorkspaceID(source.WorkspaceID).SetSourceID(sourceKey).SetTargetID(targetKey).SetTypeID(typ.ID).SetName(typ.Key).SetMetadata(in.Metadata).SetDirected(typ.Directed).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship.create", source, map[string]any{"relationship_id": out.ID, "target_id": in.TargetID})
	})
	return
}
func (s *Service) Relationships(ctx context.Context, subject, itemID, direction, name, after string, limit int) (Page[*ent.Relationship], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[*ent.Relationship], error) {
			return t.Relationships(ctx, subject, itemID, direction, name, after, limit)
		})
	}
	if _, err := s.Get(ctx, subject, itemID); err != nil {
		return Page[*ent.Relationship]{}, err
	}
	q := s.Client.Relationship.Query()
	switch direction {
	case "", "outgoing":
		q.Where(relationship.Or(relationship.SourceIDEQ(itemID), relationship.And(relationship.DirectedEQ(false), relationship.TargetIDEQ(itemID))))
		if name != "" {
			q.Where(relationship.NameEQ(name))
		}
	case "incoming":
		q.Where(relationship.Or(relationship.TargetIDEQ(itemID), relationship.And(relationship.DirectedEQ(false), relationship.SourceIDEQ(itemID))))
		if name != "" {
			q.Where(relationship.NameEQ(name))
		}
	default:
		return Page[*ent.Relationship]{}, invalid("direction must be outgoing or incoming")
	}
	if after != "" {
		q.Where(relationship.IDGT(after))
	}
	q.Where(func(sel *entsql.Selector) {
		for _, field := range []string{relationship.FieldSourceID, relationship.FieldTargetID} {
			id := sel.C(field)
			acl, args := permissionSQL(id, subject, "read")
			visible, visibleArgs := visibleSQL(id, subject, "auto")
			args = append(args, visibleArgs...)
			sel.Where(entsql.ExprP(acl+" AND "+visible, args...))
		}
	})
	rows, err := q.Order(ent.Asc(relationship.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
	return entityPage(rows, limit, func(v *ent.Relationship) string { return v.ID }), err
}

// itemLink loads a relationship the item participates in: as source, or as
// either endpoint of an undirected link.
func (s *Service) itemLink(ctx context.Context, itemID, linkID string) (*ent.Relationship, error) {
	link, err := s.Client.Relationship.Get(ctx, linkID)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if link.SourceID != itemID && (link.Directed || link.TargetID != itemID) {
		return nil, ErrNotFound
	}
	return link, nil
}

// filenameValid accepts names usable as file names: no path separators,
// control characters or dot segments.
func filenameValid(name string) bool {
	return strings.TrimSpace(name) != "" && len(name) <= 255 && !strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl) && name != "." && name != ".."
}
