package dms

import (
	"context"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/taxonomy"
)

// A taxonomy merge also merges predicate policies and edges. Cardinality-bound
// predicates cannot merge while used, and conflicting semantics or attributes
// reject the whole curation transaction.
func (s *Service) mergeRelationshipTerms(ctx context.Context, subject string, source, target taxonomy.Term) error {
	store := s.DAL.Relationships()
	a, err := store.Type(ctx, source.ID)
	if dberr.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := store.Type(ctx, target.ID)
	typed := err == nil
	if err != nil && !dberr.IsNotFound(err) {
		return err
	}
	if source.SetID != target.SetID {
		return invalid("relationship predicates must stay in their term set")
	}
	if typed && !a.Same(b) {
		return invalid("relationship predicates have different behavior")
	}
	used, err := store.Count(ctx, relationships.Filter{TypeIDs: []string{source.ID, target.ID}, Limit: 1})
	if err != nil {
		return err
	}
	if used > 0 && a.Limited() {
		return invalid("remove relationships before merging predicates with endpoint limits")
	}
	if !typed {
		copied := a
		copied.ID = target.ID
		if b, err = store.CreateType(ctx, copied); err != nil {
			return err
		}
	}
	edges, err := store.List(ctx, relationships.Filter{TypeIDs: []string{source.ID}})
	if err != nil {
		return err
	}
	for _, edge := range edges {
		duplicates, err := store.List(ctx, relationships.Filter{TypeIDs: []string{target.ID}, SourceID: edge.SourceID, TargetID: edge.TargetID, Limit: 1})
		if err != nil {
			return err
		}
		if len(duplicates) > 0 {
			duplicate := duplicates[0]
			values := patchValues(duplicate.Metadata, map[string]any{})
			for key, value := range edge.Metadata {
				if existing, ok := values[key]; ok {
					if !sameJSON(existing, value) {
						return invalid("matching relationships have conflicting attributes")
					}
				} else {
					values[key] = value
				}
			}
			if err = normalizeMetadata(b.Attributes, values, duplicate.Metadata); err != nil {
				return err
			}
			duplicate.Metadata = values
			if _, err = store.Save(ctx, duplicate, duplicate.Version); err != nil {
				return err
			}
			if err = store.Unlink(ctx, edge.ID); err != nil {
				return err
			}
		} else {
			edge.TypeID = target.ID
			if edge, err = store.Save(ctx, edge, edge.Version); err != nil {
				return err
			}
		}
		if err = s.touchRelationship(ctx, subject, edge, "updated"); err != nil {
			return err
		}
	}
	return store.DeleteType(ctx, a.ID)
}
