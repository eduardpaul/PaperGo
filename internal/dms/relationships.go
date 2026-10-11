package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/relationship"
	"papergo/ent/resource"
	"strings"
	"unicode"
)

// A typed relationship takes its direction, attributes and cardinality from its policy.
// Untyped relationships accept a bounded scalar property bag.
type CreateRelationship struct {
	Type     string         `json:"type,omitempty"`
	Directed *bool          `json:"directed,omitempty"`
	TargetID string         `json:"target_id"`
	Metadata map[string]any `json:"metadata"`
}

func (s *Service) Link(ctx context.Context, subject, sourceID string, in CreateRelationship) (out *ent.Relationship, err error) {
	if sourceID == in.TargetID {
		return nil, invalid("self relationships are not supported")
	}
	if in.Metadata == nil {
		in.Metadata = map[string]any{}
	}
	in.Metadata = patchValues(map[string]any{}, in.Metadata)
	if e := validateRelationshipAttributes(in.Metadata); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		source, e := t.authorize(ctx, subject, sourceID, "write")
		if e != nil {
			return e
		}
		target, e := t.authorize(ctx, subject, in.TargetID, "write")
		if e != nil {
			return e
		}
		if source.Kind != resource.KindItem || target.Kind != resource.KindItem {
			return invalid("relationships connect items")
		}
		var typ *ent.RelationshipType
		directed := false
		if in.Directed != nil {
			directed = *in.Directed
		}
		if in.Type != "" {
			predicate, e := t.resolveRelationshipTerm(ctx, subject, in.Type, true)
			if e != nil {
				return e
			}
			typ, e = t.Client.RelationshipType.Get(ctx, predicate.ID)
			if ent.IsNotFound(e) {
				typ, e = t.ensureRelationshipPolicy(ctx, subject, RelationshipTypeInput{Name: predicate.ID, Directed: directed})
			}
			if e != nil {
				return e
			}
			if in.Directed != nil && *in.Directed != typ.Directed {
				return invalid("direction differs from the relationship type")
			}
			directed = typ.Directed
		}
		sourceKey, targetKey := sourceID, in.TargetID
		if !directed && sourceKey > targetKey {
			sourceKey, targetKey = targetKey, sourceKey
		}
		q := t.Client.Relationship.Query().Where(relationship.SourceIDEQ(sourceKey), relationship.TargetIDEQ(targetKey), relationship.DirectedEQ(directed))
		if typ == nil {
			q.Where(relationship.TypeIDIsNil())
		} else {
			q.Where(relationship.TypeIDEQ(typ.ID))
		}
		existing, e := q.Only(ctx)
		if e == nil {
			out = existing
			return nil
		}
		if !ent.IsNotFound(e) {
			return e
		}
		if typ != nil {
			if e = normalizeMetadata(typ.Attributes, in.Metadata, nil); e != nil {
				return e
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
		}
		builder := t.Client.Relationship.Create().SetSourceID(sourceKey).SetTargetID(targetKey).SetMetadata(in.Metadata).SetDirected(directed)
		if typ != nil {
			builder.SetTypeID(typ.ID)
		}
		out, e = builder.Save(ctx)
		if e != nil {
			return e
		}
		if e = t.touchRelationship(ctx, subject, out, "created"); e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship.create", source, map[string]any{"relationship_id": out.ID, "target_id": in.TargetID})
	})
	return
}
func (s *Service) Relationships(ctx context.Context, subject, itemID, direction, typeValue, after string, limit int) (Page[RelationshipView], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[RelationshipView], error) {
			return t.Relationships(ctx, subject, itemID, direction, typeValue, after, limit)
		})
	}
	if _, err := s.Get(ctx, subject, itemID); err != nil {
		return Page[RelationshipView]{}, err
	}
	q := s.Client.Relationship.Query()
	switch direction {
	case "", "both":
		q.Where(relationship.Or(relationship.SourceIDEQ(itemID), relationship.TargetIDEQ(itemID)))
	case "outgoing":
		q.Where(relationship.DirectedEQ(true), relationship.SourceIDEQ(itemID))
	case "incoming":
		q.Where(relationship.DirectedEQ(true), relationship.TargetIDEQ(itemID))
	default:
		return Page[RelationshipView]{}, invalid("direction must be both, outgoing or incoming")
	}
	if typeValue != "" {
		predicate, err := s.resolveRelationshipTerm(ctx, subject, typeValue, false)
		if err != nil {
			return Page[RelationshipView]{}, err
		}
		if predicate == nil {
			return Page[RelationshipView]{Data: []RelationshipView{}}, nil
		}
		q.Where(relationship.TypeIDEQ(predicate.ID))
	}
	if after != "" {
		q.Where(relationship.IDGT(after))
	}
	q.Where(relationshipVisibility(ctx, subject))
	rows, err := q.Order(ent.Asc(relationship.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
	if err != nil {
		return Page[RelationshipView]{}, err
	}
	page := entityPage(rows, limit, func(v *ent.Relationship) string { return v.ID })
	out := Page[RelationshipView]{Data: []RelationshipView{}, NextCursor: page.NextCursor}
	for _, edge := range page.Data {
		view, err := s.relationshipView(ctx, subject, itemID, edge)
		if err != nil {
			return out, err
		}
		out.Data = append(out.Data, view)
	}
	return out, nil
}

// itemLink loads a relationship the item participates in at either endpoint.
func (s *Service) itemLink(ctx context.Context, itemID, linkID string) (*ent.Relationship, error) {
	link, err := s.Client.Relationship.Get(ctx, linkID)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if link.SourceID != itemID && link.TargetID != itemID {
		return nil, ErrNotFound
	}
	return link, nil
}

// filenameValid accepts names usable as file names: no path separators,
// control characters or dot segments.
func filenameValid(name string) bool {
	return validateTitle(name) == nil && !strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl) && name != "." && name != ".."
}
