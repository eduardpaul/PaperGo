package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/relationship"
)

// A taxonomy merge also merges predicate policies and edges. Cardinality-bound
// predicates cannot merge while used, and conflicting semantics or attributes
// reject the whole curation transaction.
func (s *Service) mergeRelationshipTerms(ctx context.Context, subject string, source, target *ent.Term) error {
	a, err := s.Client.RelationshipType.Get(ctx, source.ID)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := s.Client.RelationshipType.Get(ctx, target.ID)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if source.TermSetID != target.TermSetID {
		return invalid("relationship predicates must stay in their term set")
	}
	if b != nil && (a.Directed != b.Directed || a.InverseLabel != b.InverseLabel || !sameLimit(a.MaxIncoming, b.MaxIncoming) || !sameLimit(a.MaxOutgoing, b.MaxOutgoing) || !bytes.Equal(a.Attributes, b.Attributes)) {
		return invalid("relationship predicates have different behavior")
	}
	used, err := s.Client.Relationship.Query().Where(relationship.TypeIDIn(source.ID, target.ID)).Exist(ctx)
	if err != nil {
		return err
	}
	if used && (a.MaxIncoming != nil || a.MaxOutgoing != nil) {
		return invalid("remove relationships before merging predicates with endpoint limits")
	}
	if b == nil {
		b, err = s.Client.RelationshipType.Create().SetID(target.ID).SetDirected(a.Directed).SetInverseLabel(a.InverseLabel).SetNillableMaxIncoming(a.MaxIncoming).SetNillableMaxOutgoing(a.MaxOutgoing).SetAttributes(a.Attributes).Save(ctx)
		if err != nil {
			return err
		}
	}
	edges, err := s.Client.Relationship.Query().Where(relationship.TypeIDEQ(source.ID)).All(ctx)
	if err != nil {
		return err
	}
	for _, edge := range edges {
		duplicate, err := s.Client.Relationship.Query().Where(relationship.TypeIDEQ(target.ID), relationship.SourceIDEQ(edge.SourceID), relationship.TargetIDEQ(edge.TargetID)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if duplicate != nil {
			values := patchValues(duplicate.Metadata, map[string]any{})
			for key, value := range edge.Metadata {
				if existing, ok := values[key]; ok {
					x, _ := json.Marshal(existing)
					y, _ := json.Marshal(value)
					if !bytes.Equal(x, y) {
						return invalid("matching relationships have conflicting attributes")
					}
				} else {
					values[key] = value
				}
			}
			if err = normalizeMetadata(b.Attributes, values, duplicate.Metadata); err != nil {
				return err
			}
			if err = s.Client.Relationship.UpdateOne(duplicate).SetMetadata(values).AddVersion(1).Exec(ctx); err != nil {
				return err
			}
		}
		if err = s.Client.Relationship.DeleteOne(edge).Exec(ctx); err != nil {
			return err
		}
		if duplicate == nil {
			if err = s.Client.Relationship.Create().SetID(edge.ID).SetCreatedAt(edge.CreatedAt).SetSourceID(edge.SourceID).SetTargetID(edge.TargetID).SetTypeID(target.ID).SetDirected(edge.Directed).SetMetadata(edge.Metadata).SetVersion(edge.Version + 1).Exec(ctx); err != nil {
				return err
			}
		}
		if err = s.touchRelationship(ctx, subject, edge, "updated"); err != nil {
			return err
		}
	}
	return s.Client.RelationshipType.DeleteOne(a).Exec(ctx)
}
