package dms

import (
	"context"
	"papergo/ent"
)

func (s *Service) Template(ctx context.Context, subject, id string) (*ent.SchemaTemplate, error) {
	return read(ctx, s, func(t *Service) (*ent.SchemaTemplate, error) {
		v, e := t.Client.SchemaTemplate.Get(ctx, id)
		if ent.IsNotFound(e) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if _, e = t.workspace(ctx, subject, v.WorkspaceID, "read"); e != nil {
			return nil, e
		}
		return v, nil
	})
}
func (s *Service) TermSet(ctx context.Context, subject, id string) (*ent.TermSet, error) {
	return read(ctx, s, func(t *Service) (*ent.TermSet, error) {
		v, e := t.Client.TermSet.Get(ctx, id)
		if ent.IsNotFound(e) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if _, e = t.workspace(ctx, subject, v.WorkspaceID, "read"); e != nil {
			return nil, e
		}
		return v, nil
	})
}
func (s *Service) Term(ctx context.Context, subject, id string) (*ent.Term, error) {
	return read(ctx, s, func(t *Service) (*ent.Term, error) {
		v, e := t.Client.Term.Get(ctx, id)
		if ent.IsNotFound(e) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if _, e = t.TermSet(ctx, subject, v.TermSetID); e != nil {
			return nil, e
		}
		return v, nil
	})
}
func (s *Service) RelationshipType(ctx context.Context, subject, id string) (*ent.RelationshipType, error) {
	return read(ctx, s, func(t *Service) (*ent.RelationshipType, error) {
		v, e := t.Client.RelationshipType.Get(ctx, id)
		if ent.IsNotFound(e) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if _, e = t.workspace(ctx, subject, v.WorkspaceID, "read"); e != nil {
			return nil, e
		}
		return v, nil
	})
}
