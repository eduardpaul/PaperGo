package dms

import (
	"bytes"
	"context"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"papergo/ent"
	"papergo/ent/term"
	"papergo/ent/termgroup"
	"papergo/ent/termset"
	"strings"
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
		if d.Type == "term" || d.Type == "keywords" || d.Type == "lookup" || d.Type == "person" || d.Options.Multiple || d.Indexed {
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
const RelationshipTypesTermSetKey = "relationship_types"

type RelationshipTypeView struct {
	*ent.RelationshipType
	Term *ent.Term `json:"term"`
}

func (s *Service) relationshipSet(ctx context.Context, create bool) (*ent.TermSet, error) {
	set, err := s.Client.TermSet.Query().Where(termset.KeyEQ(RelationshipTypesTermSetKey)).Only(ctx)
	if !ent.IsNotFound(err) || !create {
		return set, err
	}
	group, err := s.Client.TermGroup.Query().Where(termgroup.NameEqualFold("Relationships")).Only(ctx)
	if ent.IsNotFound(err) {
		group, err = s.Client.TermGroup.Create().SetName("Relationships").Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	return s.Client.TermSet.Create().SetGroupID(group.ID).SetKey(RelationshipTypesTermSetKey).SetName("Types").SetDescription("Item relationship predicates").SetIsOpen(true).Save(ctx)
}

func (s *Service) resolveRelationshipTerm(ctx context.Context, subject, value string, create bool) (*ent.Term, error) {
	if err := s.requireTaxonomy(ctx, subject, "read"); err != nil {
		return nil, err
	}
	value = strings.TrimSpace(value)
	if err := validateName(value); err != nil {
		return nil, err
	}
	set, err := s.relationshipSet(ctx, create)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := s.Client.Term.Get(ctx, value)
	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}
	if v != nil {
		for hops := 0; v.MergedIntoID != nil && hops < MaxDepth; hops++ {
			v, err = s.Client.Term.Get(ctx, *v.MergedIntoID)
			if err != nil {
				return nil, err
			}
		}
		if v.TermSetID != set.ID || v.Deprecated {
			return nil, invalid("relationship type must be an active predicate term")
		}
		return v, nil
	}
	if _, err := uuid.Parse(value); err == nil {
		return nil, invalid("unknown relationship type term")
	}
	key := strings.ToLower(value)
	for _, match := range []string{"normalized_name=?", "EXISTS(SELECT 1 FROM json_each(labels) WHERE unicode_lower(value)=?)", "EXISTS(SELECT 1 FROM json_each(synonyms) WHERE unicode_lower(value)=?)"} {
		rows, err := s.Client.Term.Query().Where(term.TermSetIDEQ(set.ID), term.DeprecatedEQ(false), term.MergedIntoIDIsNil(), func(sel *entsql.Selector) { sel.Where(entsql.ExprP(match, key)) }).Limit(2).All(ctx)
		if err != nil {
			return nil, err
		}
		if len(rows) > 1 {
			return nil, invalid("ambiguous relationship type label; use its term ID")
		}
		if len(rows) == 1 {
			return rows[0], nil
		}
	}
	if !create {
		return nil, nil
	}
	if !set.IsOpen {
		return nil, invalid("unknown relationship type in closed term set")
	}
	v, err = s.insertTerm(ctx, set, nil, TermInput{Name: value, Labels: map[string]string{}, Synonyms: []string{}})
	if err != nil {
		return nil, err
	}
	return v, s.auditTaxonomy(ctx, subject, "term.create", map[string]any{"term_id": v.ID, "term_set_id": set.ID})
}

func (s *Service) ensureRelationshipPolicy(ctx context.Context, subject string, in RelationshipTypeInput) (*ent.RelationshipType, error) {
	in.Name = strings.TrimSpace(in.Name)
	raw, err := relationshipPolicy(in)
	if err != nil {
		return nil, err
	}
	term, err := s.resolveRelationshipTerm(ctx, subject, in.Name, true)
	if err != nil {
		return nil, err
	}
	old, err := s.Client.RelationshipType.Get(ctx, term.ID)
	if err == nil {
		if old.Directed != in.Directed || old.InverseLabel != in.InverseLabel || !sameLimit(old.MaxIncoming, in.MaxIncoming) || !sameLimit(old.MaxOutgoing, in.MaxOutgoing) || !bytes.Equal(old.Attributes, raw) {
			return nil, invalid("relationship type already has different behavior")
		}
		return old, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	out, err := s.Client.RelationshipType.Create().SetID(term.ID).SetInverseLabel(in.InverseLabel).SetDirected(in.Directed).SetNillableMaxIncoming(in.MaxIncoming).SetNillableMaxOutgoing(in.MaxOutgoing).SetAttributes(raw).Save(ctx)
	if err != nil {
		return nil, err
	}
	return out, s.auditCatalog(ctx, subject, "relationship_type.create", map[string]any{"relationship_type_id": out.ID})
}
func sameLimit(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func (s *Service) CreateRelationshipType(ctx context.Context, subject string, in RelationshipTypeInput) (out RelationshipTypeView, err error) {
	err = s.write(ctx, func(t *Service) error {
		if err := t.RequireScope(ctx, subject, "list.write"); err != nil {
			return err
		}
		policy, err := t.ensureRelationshipPolicy(ctx, subject, in)
		if err != nil {
			return err
		}
		term, err := t.Client.Term.Get(ctx, policy.ID)
		out = RelationshipTypeView{policy, term}
		return err
	})
	return
}

func (s *Service) describeRelationshipTerm(ctx context.Context, v *ent.Term) (RelationshipTypeView, error) {
	policy, err := s.Client.RelationshipType.Get(ctx, v.ID)
	if ent.IsNotFound(err) {
		raw, _ := relationshipPolicy(RelationshipTypeInput{Name: v.Name})
		policy = &ent.RelationshipType{ID: v.ID, Attributes: raw, Version: 1}
		err = nil
	}
	return RelationshipTypeView{policy, v}, err
}
func (s *Service) RelationshipTypes(ctx context.Context, subject, after string, limit int) (Page[RelationshipTypeView], error) {
	return read(ctx, s, func(t *Service) (Page[RelationshipTypeView], error) {
		if err := t.RequireScope(ctx, subject, "list.read"); err != nil {
			return Page[RelationshipTypeView]{}, err
		}
		if err := t.requireTaxonomy(ctx, subject, "read"); err != nil {
			return Page[RelationshipTypeView]{}, err
		}
		set, err := t.relationshipSet(ctx, false)
		if ent.IsNotFound(err) {
			return Page[RelationshipTypeView]{Data: []RelationshipTypeView{}}, nil
		}
		if err != nil {
			return Page[RelationshipTypeView]{}, err
		}
		terms, err := t.Client.Term.Query().Where(term.TermSetIDEQ(set.ID), term.MergedIntoIDIsNil(), term.DeprecatedEQ(false), term.IDGT(after)).Order(ent.Asc(term.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		if err != nil {
			return Page[RelationshipTypeView]{}, err
		}
		page := entityPage(terms, limit, func(v *ent.Term) string { return v.ID })
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
		v, err := t.resolveRelationshipTerm(ctx, subject, id, false)
		if err != nil {
			return RelationshipTypeView{}, err
		}
		if v == nil {
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

func (s *Service) UpdateRelationship(ctx context.Context, subject, itemID, linkID string, version int, in UpdateRelationship) (out *ent.Relationship, err error) {
	if in.Metadata == nil {
		return nil, invalid("metadata is required")
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
		typ, e := t.Client.RelationshipType.Get(ctx, old.TypeID)
		if e != nil {
			return e
		}
		if e = normalizeMetadata(typ.Attributes, values, old.Metadata); e != nil {
			return e
		}
		out, e = t.Client.Relationship.UpdateOne(old).SetMetadata(values).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship.update", source, map[string]any{"relationship_id": linkID, "version": out.Version})
	})
	return
}
