package dms

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/taxonomy"
)

type RelationshipTypeInput struct {
	Name         string        `json:"name"`
	InverseLabel string        `json:"inverse_label"`
	Directed     bool          `json:"directed"`
	MaxIncoming  *int          `json:"max_incoming,omitempty"`
	MaxOutgoing  *int          `json:"max_outgoing,omitempty"`
	Attributes   []CreateField `json:"attributes"`
}

func relationshipPolicy(in RelationshipTypeInput) (json.RawMessage, error) {
	if e := validateName(in.Name); e != nil {
		return nil, e
	}
	if in.InverseLabel != "" {
		if e := validateName(in.InverseLabel); e != nil {
			return nil, e
		}
	}
	for _, n := range []*int{in.MaxIncoming, in.MaxOutgoing} {
		if n != nil && (*n < 1 || *n > 1000) {
			return nil, invalid("cardinality must be 1..1000")
		}
	}
	if !in.Directed && (in.InverseLabel != "" || in.MaxIncoming != nil || in.MaxOutgoing != nil) {
		return nil, invalid("symmetric relationships cannot have inverse labels or directional cardinality")
	}
	if len(in.Attributes) > 64 {
		return nil, invalid("at most 64 attribute definitions")
	}
	schema := SchemaDefinition{Fields: []SchemaField{}}
	seen := map[string]bool{}
	for _, f := range in.Attributes {
		if f.ContentTypeID != "" || f.Options.Unique {
			return nil, invalid("relationship attributes cannot select content types or unique business keys")
		}
		d := fieldFromInput(f)
		if seen[d.Key] {
			return nil, invalid("duplicate attribute")
		}
		seen[d.Key] = true
		if d.Type == catalog.FieldTerm || d.Type == catalog.FieldKeywords || d.Type == catalog.FieldLookup || d.Type == catalog.FieldPerson || d.Options.Multiple || d.Indexed {
			return nil, invalid("relationship attributes must be unindexed scalar fields")
		}
		if d.Choices == nil {
			d.Choices = []string{}
		}
		if e := validateFieldDefinition(d); e != nil {
			return nil, e
		}
		schema.Fields = append(schema.Fields, schemaField(d))
	}
	return json.Marshal(schema)
}

// Relationship policy IDs are taxonomy term IDs; labels live only in the term store.
type RelationshipTypeView struct {
	ID           string          `json:"id"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Version      int             `json:"version"`
	InverseLabel string          `json:"inverse_label"`
	Directed     bool            `json:"directed"`
	MaxIncoming  *int            `json:"max_incoming"`
	MaxOutgoing  *int            `json:"max_outgoing"`
	Attributes   json.RawMessage `json:"attributes"`
	Term         TermView        `json:"term"`
}

func viewRelationshipType(t relationships.Type, term taxonomy.Term) RelationshipTypeView {
	return RelationshipTypeView{ID: t.ID, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Version: t.Version, InverseLabel: t.InverseLabel,
		Directed: t.Directed, MaxIncoming: t.MaxIncoming, MaxOutgoing: t.MaxOutgoing, Attributes: t.Attributes, Term: viewTerm(term)}
}

// relationshipSet returns the relationship types set, creating it in a
// Relationships group when create is set. found is false when it is missing.
func (s *Service) relationshipSet(ctx context.Context, create bool) (set taxonomy.Set, found bool, err error) {
	store := s.DAL.Taxonomy()
	set, err = store.SetByKey(ctx, taxonomy.RelationshipTypesSetKey)
	if !dberr.IsNotFound(err) {
		return set, err == nil, err
	}
	if !create {
		return set, false, nil
	}
	groups, err := store.Groups(ctx, taxonomy.GroupFilter{Name: "Relationships", Limit: 1})
	if err != nil {
		return set, false, err
	}
	var group taxonomy.Group
	if len(groups) > 0 {
		group = groups[0]
	} else if group, err = store.CreateGroup(ctx, taxonomy.Group{Name: "Relationships"}); err != nil {
		return set, false, err
	}
	set, err = store.CreateSet(ctx, taxonomy.Set{GroupID: group.ID, Key: taxonomy.RelationshipTypesSetKey, Name: "Types", Description: "Item relationship predicates", Open: true})
	return set, err == nil, taxonomyError(err)
}

// resolveRelationshipTerm finds the predicate term value names; found is
// false when none matches and create is unset.
func (s *Service) resolveRelationshipTerm(ctx context.Context, subject, value string, create bool) (v taxonomy.Term, found bool, err error) {
	if err = s.requireTaxonomy(ctx, subject, "read"); err != nil {
		return v, false, err
	}
	value = strings.TrimSpace(value)
	if err = validateName(value); err != nil {
		return v, false, err
	}
	set, found, err := s.relationshipSet(ctx, create)
	if err != nil || !found {
		return v, false, err
	}
	store := s.DAL.Taxonomy()
	v, err = store.Canonical(ctx, value)
	if err == nil {
		if v.SetID != set.ID || v.Deprecated {
			return v, false, invalid("relationship type must be an active predicate term")
		}
		return v, true, nil
	}
	if !dberr.IsNotFound(err) {
		return v, false, err
	}
	if _, err := uuid.Parse(value); err == nil {
		return v, false, invalid("unknown relationship type term")
	}
	rows, err := store.Lookup(ctx, set.ID, value)
	if err != nil {
		return v, false, err
	}
	if len(rows) > 1 {
		return v, false, invalid("ambiguous relationship type label; use its term ID")
	}
	if len(rows) == 1 {
		return rows[0], true, nil
	}
	if !create {
		return v, false, nil
	}
	if !set.Open {
		return v, false, invalid("unknown relationship type in closed term set")
	}
	if v, err = s.insertTerm(ctx, set.ID, TermInput{Name: value, Labels: map[string]string{}, Synonyms: []string{}}); err != nil {
		return v, false, err
	}
	return v, true, s.auditTaxonomy(ctx, subject, "term.create", map[string]any{"term_id": v.ID, "term_set_id": set.ID})
}

func (s *Service) ensureRelationshipPolicy(ctx context.Context, subject string, in RelationshipTypeInput) (relationships.Type, error) {
	in.Name = strings.TrimSpace(in.Name)
	raw, err := relationshipPolicy(in)
	if err != nil {
		return relationships.Type{}, err
	}
	term, _, err := s.resolveRelationshipTerm(ctx, subject, in.Name, true)
	if err != nil {
		return relationships.Type{}, err
	}
	want := relationships.Type{ID: term.ID, InverseLabel: in.InverseLabel, Directed: in.Directed, MaxIncoming: in.MaxIncoming, MaxOutgoing: in.MaxOutgoing, Attributes: raw}
	old, err := s.DAL.Relationships().Type(ctx, term.ID)
	if err == nil {
		if !old.Same(want) {
			return old, invalid("relationship type already has different behavior")
		}
		return old, nil
	}
	if !dberr.IsNotFound(err) {
		return old, err
	}
	out, err := s.DAL.Relationships().CreateType(ctx, want)
	if err != nil {
		return out, err
	}
	return out, s.auditCatalog(ctx, subject, "relationship_type.create", map[string]any{"relationship_type_id": out.ID})
}
func (s *Service) CreateRelationshipType(ctx context.Context, subject string, in RelationshipTypeInput) (out RelationshipTypeView, err error) {
	err = s.write(ctx, func(t *Service) error {
		if err := t.RequireScope(ctx, subject, "list.write"); err != nil {
			return err
		}
		policy, err := t.ensureRelationshipPolicy(ctx, subject, in)
		if err != nil {
			return err
		}
		term, err := t.DAL.Taxonomy().Term(ctx, policy.ID)
		out = viewRelationshipType(policy, term)
		return err
	})
	return
}

func (s *Service) describeRelationshipTerm(ctx context.Context, v taxonomy.Term) (RelationshipTypeView, error) {
	policy, err := s.DAL.Relationships().Type(ctx, v.ID)
	if dberr.IsNotFound(err) {
		raw, _ := relationshipPolicy(RelationshipTypeInput{Name: v.Name})
		policy, err = relationships.Type{ID: v.ID, Attributes: raw, Version: 1}, nil
	}
	return viewRelationshipType(policy, v), err
}
func (s *Service) RelationshipTypes(ctx context.Context, subject, after string, limit int) (Page[RelationshipTypeView], error) {
	return read(ctx, s, func(t *Service) (Page[RelationshipTypeView], error) {
		if err := t.RequireScope(ctx, subject, "list.read"); err != nil {
			return Page[RelationshipTypeView]{}, err
		}
		if err := t.requireTaxonomy(ctx, subject, "read"); err != nil {
			return Page[RelationshipTypeView]{}, err
		}
		set, found, err := t.relationshipSet(ctx, false)
		if err != nil || !found {
			return Page[RelationshipTypeView]{Data: []RelationshipTypeView{}}, err
		}
		terms, err := t.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{SetID: set.ID, Unmerged: true, Current: true, AfterID: after, Order: taxonomy.OrderID, Limit: pageSize(limit) + 1})
		if err != nil {
			return Page[RelationshipTypeView]{}, err
		}
		page := entityPage(terms, limit, func(v taxonomy.Term) string { return v.ID })
		out := Page[RelationshipTypeView]{Data: []RelationshipTypeView{}, NextCursor: page.NextCursor}
		for _, v := range page.Data {
			view, err := t.describeRelationshipTerm(ctx, v)
			if err != nil {
				return out, err
			}
			out.Data = append(out.Data, view)
		}
		return out, nil
	})
}
func (s *Service) RelationshipType(ctx context.Context, subject, id string) (RelationshipTypeView, error) {
	return read(ctx, s, func(t *Service) (RelationshipTypeView, error) {
		if err := t.RequireScope(ctx, subject, "list.read"); err != nil {
			return RelationshipTypeView{}, err
		}
		v, found, err := t.resolveRelationshipTerm(ctx, subject, id, false)
		if err != nil {
			return RelationshipTypeView{}, err
		}
		if !found {
			return RelationshipTypeView{}, ErrNotFound
		}
		return t.describeRelationshipTerm(ctx, v)
	})
}

func normalizeMetadata(raw json.RawMessage, values, previous map[string]any) error {
	defs, e := definitions(raw)
	if e != nil {
		return e
	}
	if e = normalizeRevisionValues(defs, values, previous, false); e != nil {
		return e
	}
	b, e := json.Marshal(values)
	if e != nil || len(b) > 16*1024 {
		return invalid("relationship metadata exceeds 16 KiB")
	}
	return nil
}

type UpdateRelationship struct {
	Metadata map[string]any `json:"metadata"`
}

func (s *Service) UpdateRelationship(ctx context.Context, subject, itemID, linkID string, version int, in UpdateRelationship) (out RelationshipRecord, err error) {
	if in.Metadata == nil {
		return out, invalid("metadata is required")
	}
	err = s.write(ctx, func(t *Service) error {
		source, e := t.authorize(ctx, subject, itemID, "write")
		if e != nil {
			return e
		}
		old, e := t.itemLink(ctx, itemID, linkID)
		if e != nil {
			return e
		}
		for _, id := range []string{old.SourceID, old.TargetID} {
			if _, e = t.authorize(ctx, subject, id, "write"); e != nil {
				return e
			}
		}
		if old.Version != version {
			return ErrPreconditionFailed
		}
		values := patchValues(old.Metadata, in.Metadata)
		if b, e := json.Marshal(values); e != nil || len(b) > 16*1024 {
			return invalid("metadata exceeds 16 KiB")
		}
		if e = validateRelationshipAttributes(values); e != nil {
			return e
		}
		if old.TypeID != "" {
			typ, e := t.DAL.Relationships().Type(ctx, old.TypeID)
			if e != nil {
				return e
			}
			if e = normalizeMetadata(typ.Attributes, values, old.Metadata); e != nil {
				return e
			}
		}
		old.Metadata = values
		saved, e := t.DAL.Relationships().Save(ctx, old, version)
		if errors.Is(e, dberr.ErrConcurrentUpdate) {
			return ErrPreconditionFailed
		}
		if e != nil {
			return e
		}
		out = viewRelationship(saved)
		if e = t.touchRelationship(ctx, subject, saved, "updated"); e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship.update", source, map[string]any{"relationship_id": linkID, "version": saved.Version})
	})
	return
}
