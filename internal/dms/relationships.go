package dms

import (
	"context"
	"errors"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"strings"
	"time"
	"unicode"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/relationships"
)

// A typed relationship takes its direction, attributes and cardinality from its policy.
// Untyped relationships accept a bounded scalar property bag.
type CreateRelationship struct {
	Type     string         `json:"type,omitempty"`
	Directed *bool          `json:"directed,omitempty"`
	TargetID string         `json:"target_id"`
	Metadata map[string]any `json:"metadata"`
}

// RelationshipRecord is a link between items as the API returns it.
// Untyped links have no type_id.
type RelationshipRecord struct {
	ID        string         `json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	SourceID  string         `json:"source_id"`
	TargetID  string         `json:"target_id"`
	TypeID    string         `json:"type_id,omitempty"`
	Directed  bool           `json:"directed"`
	Version   int            `json:"version"`
	Metadata  map[string]any `json:"metadata"`
}

func viewRelationship(r relationships.Relationship) RelationshipRecord {
	return RelationshipRecord{ID: r.ID, CreatedAt: r.CreatedAt, SourceID: r.SourceID, TargetID: r.TargetID, TypeID: r.TypeID,
		Directed: r.Directed, Version: r.Version, Metadata: r.Metadata}
}

func (s *Service) Link(ctx context.Context, subject, sourceID string, in CreateRelationship) (out RelationshipRecord, err error) {
	if sourceID == in.TargetID {
		return out, invalid("self relationships are not supported")
	}
	if in.Metadata == nil {
		in.Metadata = map[string]any{}
	}
	in.Metadata = patchValues(map[string]any{}, in.Metadata)
	if e := validateRelationshipAttributes(in.Metadata); e != nil {
		return out, e
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
		if source.Kind != refs.KindItem || target.Kind != refs.KindItem {
			return invalid("relationships connect items")
		}
		link := relationships.Relationship{SourceID: sourceID, TargetID: in.TargetID, Metadata: in.Metadata}
		if in.Directed != nil {
			link.Directed = *in.Directed
		}
		if in.Type != "" {
			predicate, _, e := t.resolveRelationshipTerm(ctx, subject, in.Type, true)
			if e != nil {
				return e
			}
			typ, e := t.DAL.Relationships().Type(ctx, predicate.ID)
			if dberr.IsNotFound(e) {
				typ, e = t.ensureRelationshipPolicy(ctx, subject, RelationshipTypeInput{Name: predicate.ID, Directed: link.Directed})
			}
			if e != nil {
				return e
			}
			if in.Directed != nil && *in.Directed != typ.Directed {
				return invalid("direction differs from the relationship type")
			}
			link.TypeID, link.Directed = typ.ID, typ.Directed
			// A retry returns the existing link unchanged, even when its
			// metadata no longer validates.
			if old, found, e := t.DAL.Relationships().Existing(ctx, link); e != nil || found {
				out = viewRelationship(old)
				return e
			}
			if e = normalizeMetadata(typ.Attributes, link.Metadata, nil); e != nil {
				return e
			}
		}
		created, linked, e := t.DAL.Relationships().Link(ctx, link)
		if errors.Is(e, dberr.ErrCardinalityExceeded) {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		out = viewRelationship(created)
		if !linked {
			return nil
		}
		if e = t.touchRelationship(ctx, subject, created, "created"); e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship.create", source, map[string]any{"relationship_id": created.ID, "target_id": in.TargetID})
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
	query := content.Links{Reader: principal(ctx, subject), Endpoint: itemID, Direction: direction, AfterID: after}
	switch direction {
	case "", "both", "outgoing", "incoming":
	default:
		return Page[RelationshipView]{}, invalid("direction must be both, outgoing or incoming")
	}
	if typeValue != "" {
		predicate, found, err := s.resolveRelationshipTerm(ctx, subject, typeValue, false)
		if err != nil {
			return Page[RelationshipView]{}, err
		}
		if !found {
			return Page[RelationshipView]{Data: []RelationshipView{}}, nil
		}
		query.TypeID = predicate.ID
	}
	edges, err := s.visibleRelationships(ctx, query, limit)
	if err != nil {
		return Page[RelationshipView]{}, err
	}
	out := Page[RelationshipView]{Data: []RelationshipView{}, NextCursor: edges.NextCursor}
	for _, edge := range edges.Data {
		view, err := s.relationshipView(ctx, subject, itemID, edge)
		if err != nil {
			return out, err
		}
		out.Data = append(out.Data, view)
	}
	return out, nil
}

// visibleRelationships reads a page of the links a query selects.
func (s *Service) visibleRelationships(ctx context.Context, query content.Links, limit int) (Page[relationships.Relationship], error) {
	query.Limit = pageSize(limit) + 1
	ids, err := s.DAL.Content().LinkIDs(ctx, query)
	if err != nil {
		return Page[relationships.Relationship]{}, err
	}
	page := entityPage(ids, limit, func(id string) string { return id })
	if len(page.Data) == 0 {
		return Page[relationships.Relationship]{Data: []relationships.Relationship{}}, nil
	}
	rows, err := s.DAL.Relationships().List(ctx, relationships.Filter{IDs: page.Data})
	return Page[relationships.Relationship]{Data: rows, NextCursor: page.NextCursor}, err
}

// itemLink loads a relationship the item participates in at either endpoint.
func (s *Service) itemLink(ctx context.Context, itemID, linkID string) (relationships.Relationship, error) {
	link, err := s.DAL.Relationships().Find(ctx, linkID)
	if dberr.IsNotFound(err) || err == nil && link.SourceID != itemID && link.TargetID != itemID {
		return link, ErrNotFound
	}
	return link, err
}

// filenameValid accepts names usable as file names: no path separators,
// control characters or dot segments.
func filenameValid(name string) bool {
	return validateTitle(name) == nil && !strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl) && name != "." && name != ".."
}
