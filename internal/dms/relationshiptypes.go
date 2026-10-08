package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/relationship"
	"papergo/ent/relationshiptype"
)

type RelationshipTypeInput struct {
	Key          string        `json:"key" pattern:"^[a-z][a-z0-9_]{0,63}$" doc:"Immutable key; relationships report it as their name."`
	Label        string        `json:"label" minLength:"1" maxLength:"255"`
	InverseLabel string        `json:"inverse_label" required:"false" maxLength:"255" doc:"Directed types only."`
	Directed     bool          `json:"directed"`
	MaxIncoming  *int          `json:"max_incoming,omitempty" minimum:"1" maximum:"1000" doc:"Directed types only."`
	MaxOutgoing  *int          `json:"max_outgoing,omitempty" minimum:"1" maximum:"1000" doc:"Directed types only."`
	Attributes   []CreateField `json:"attributes" required:"false" maxItems:"64" doc:"Relationship metadata definitions."`
}

func relationshipPolicy(in RelationshipTypeInput) (json.RawMessage, error) {
	if !fieldKey.MatchString(in.Key) {
		return nil, invalid("invalid relationship type key")
	}
	if e := validateName(in.Label); e != nil {
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
		if d.Type == "term" || d.Type == "lookup" || d.Options.Multiple || d.Indexed {
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
func (s *Service) CreateRelationshipType(ctx context.Context, subject, workspaceID string, in RelationshipTypeInput) (out *ent.RelationshipType, err error) {
	raw, e := relationshipPolicy(in)
	if e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		w, e := t.workspace(ctx, subject, workspaceID, "manage")
		if e != nil {
			return e
		}
		out, e = t.Client.RelationshipType.Create().SetWorkspaceID(w.ID).SetKey(in.Key).SetLabel(in.Label).SetInverseLabel(in.InverseLabel).SetDirected(in.Directed).SetNillableMaxIncoming(in.MaxIncoming).SetNillableMaxOutgoing(in.MaxOutgoing).SetAttributes(raw).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship_type.create", w, map[string]any{"relationship_type_id": out.ID})
	})
	return
}
func (s *Service) RelationshipTypes(ctx context.Context, subject, workspaceID, after string, limit int) (Page[*ent.RelationshipType], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.RelationshipType], error) {
		if _, e := t.workspace(ctx, subject, workspaceID, "read"); e != nil {
			return Page[*ent.RelationshipType]{}, e
		}
		rows, e := t.Client.RelationshipType.Query().Where(relationshiptype.WorkspaceIDEQ(workspaceID), relationshiptype.IDGT(after)).Order(ent.Asc(relationshiptype.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.RelationshipType) string { return v.ID }), e
	})
}
func (s *Service) UpdateRelationshipType(ctx context.Context, subject, id string, version int, in RelationshipTypeInput) (out *ent.RelationshipType, err error) {
	err = s.write(ctx, func(t *Service) error {
		old, e := t.Client.RelationshipType.Get(ctx, id)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		w, e := t.workspace(ctx, subject, old.WorkspaceID, "manage")
		if e != nil {
			return e
		}
		if old.Version != version {
			return ErrConflict
		}
		if in.Key == "" {
			in.Key = old.Key
		}
		if in.Key != old.Key || in.Directed != old.Directed {
			return invalid("key and direction are immutable")
		}
		raw, e := relationshipPolicy(in)
		if e != nil {
			return e
		}
		used, e := t.Client.Relationship.Query().Where(relationship.TypeIDEQ(id)).Exist(ctx)
		if e != nil {
			return e
		}
		if used && !bytes.Equal(raw, old.Attributes) {
			return invalid("attribute schema cannot change while relationships use this type")
		}
		for i, max := range []*int{in.MaxIncoming, in.MaxOutgoing} {
			if max == nil {
				continue
			}
			column := "target_id"
			if i == 1 {
				column = "source_id"
			}
			rows, e := t.Client.QueryContext(ctx, "SELECT 1 FROM relationships WHERE type_id=? GROUP BY "+column+" HAVING count(*)>? LIMIT 1", id, *max)
			if e != nil {
				return e
			}
			exceeded := rows.Next()
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if exceeded {
				return invalid("cardinality would invalidate existing relationships")
			}
		}
		b := t.Client.RelationshipType.UpdateOne(old).SetLabel(in.Label).SetInverseLabel(in.InverseLabel).SetAttributes(raw).ClearMaxIncoming().ClearMaxOutgoing().AddVersion(1)
		if in.MaxIncoming != nil {
			b.SetMaxIncoming(*in.MaxIncoming)
		}
		if in.MaxOutgoing != nil {
			b.SetMaxOutgoing(*in.MaxOutgoing)
		}
		out, e = b.Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship_type.update", w, map[string]any{"relationship_type_id": id})
	})
	return
}
func normalizeMetadata(raw json.RawMessage, values map[string]any) error {
	defs, e := definitions(raw)
	if e != nil {
		return e
	}
	if e = normalizeValues(defs, values); e != nil {
		return e
	}
	b, e := json.Marshal(values)
	if e != nil || len(b) > 16*1024 {
		return invalid("relationship metadata exceeds 16 KiB")
	}
	return nil
}

type UpdateRelationship struct {
	Metadata map[string]any `json:"metadata" doc:"Replaces all attribute values."`
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
		if old.Version != version {
			return ErrConflict
		}
		for _, id := range []string{old.SourceID, old.TargetID} {
			if _, e = t.Get(ctx, subject, id); e != nil {
				return e
			}
		}
		if b, e := json.Marshal(in.Metadata); e != nil || len(b) > 16*1024 {
			return invalid("metadata exceeds 16 KiB")
		}
		typ, e := t.Client.RelationshipType.Get(ctx, old.TypeID)
		if e != nil {
			return e
		}
		if e = normalizeMetadata(typ.Attributes, in.Metadata); e != nil {
			return e
		}
		out, e = t.Client.Relationship.UpdateOne(old).SetMetadata(in.Metadata).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "relationship.update", source, map[string]any{"relationship_id": linkID, "version": out.Version})
	})
	return
}
