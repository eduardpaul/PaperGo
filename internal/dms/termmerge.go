package dms

import (
	"context"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/taxonomy"
)

// EventTermMerged is raised when a term is merged into another; data has
// source_term_id, target_term_id and term_set_id (the target's set).
const EventTermMerged = "term.merged"

// EventTermMoved is raised when a term moves under another parent or into
// another set, which changes the parents it applies; data has term_id,
// term_set_id, previous_term_set_id, parent_id and previous_parent_id
// (empty at the root).
const EventTermMoved = "term.moved"

type MoveTermInput struct {
	// ParentID is the new parent in the same set; null moves the term to the root.
	ParentID *string `json:"parent_id"`
}

// MoveTerm moves a term and its subtree under another parent of its set.
func (s *Service) MoveTerm(ctx context.Context, subject, id string, version int, in MoveTermInput) (out TermView, err error) {
	err = s.write(ctx, func(t *Service) error {
		before, set, e := t.termForChange(ctx, subject, id, version)
		if e != nil {
			return e
		}
		parent := ""
		if in.ParentID != nil {
			parent = *in.ParentID
		}
		moved, e := t.DAL.Taxonomy().Move(ctx, id, version, set.ID, parent, false)
		if e != nil {
			return taxonomyError(e)
		}
		if e = t.emitTermMoved(ctx, subject, before, moved); e != nil {
			return e
		}
		views, e := t.termViews(ctx, []taxonomy.Term{moved})
		if e != nil {
			return e
		}
		out = views[0]
		return t.auditTaxonomy(ctx, subject, "term.move", map[string]any{"term_id": id, "parent_id": in.ParentID})
	})
	return
}

type MergeTermInput struct {
	TargetTermID string `json:"target_term_id"`
}

// MergeTerm merges a term into another term of its set: the source's
// children move to the target, its name and synonyms become the target's
// synonyms, terms merged into it now point to the target, and the source
// stays as a deprecated term whose merged_into_id names the target. Values
// that hold the source keep meaning the target: queries, filters and new
// writes follow merged_into_id.
func (s *Service) MergeTerm(ctx context.Context, subject, id string, version int, in MergeTermInput) (out TermView, err error) {
	err = s.write(ctx, func(t *Service) error {
		source, set, e := t.termForChange(ctx, subject, id, version)
		if e != nil {
			return e
		}
		target, e := t.DAL.Taxonomy().Term(ctx, in.TargetTermID)
		if dberr.IsNotFound(e) || e == nil && target.SetID != set.ID {
			return invalid("target_term_id must identify a term of the same set")
		}
		if e != nil {
			return e
		}
		merged, e := t.mergeInto(ctx, subject, source, target, false)
		if e != nil {
			return e
		}
		views, e := t.termViews(ctx, []taxonomy.Term{merged})
		if e != nil {
			return e
		}
		out = views[0]
		return t.auditTaxonomy(ctx, subject, "term.merge", map[string]any{"term_id": id, "target_term_id": target.ID})
	})
	return
}

// emitTermMoved delivers a move to every live workspace's subscriptions.
func (s *Service) emitTermMoved(ctx context.Context, subject string, before, after taxonomy.Term) error {
	workspaces, err := s.DAL.Resources().IDs(ctx, resources.Filter{Kinds: []refs.ResourceKind{refs.KindWorkspace}, State: resources.Live})
	if err != nil {
		return err
	}
	for _, workspaceID := range workspaces {
		s.emit(ctx, EventTermMoved, subject, workspaceID, nil, "", map[string]any{
			"term_id": after.ID, "term_set_id": after.SetID, "previous_term_set_id": before.SetID, "parent_id": after.ParentID, "previous_parent_id": before.ParentID,
		})
	}
	return nil
}

// mergeInto merges source into target with the relationship types they
// name, and returns the updated target.
func (s *Service) mergeInto(ctx context.Context, subject string, source, target taxonomy.Term, keyword bool) (taxonomy.Term, error) {
	merged, err := s.DAL.Taxonomy().Merge(ctx, source.ID, source.Version, target.ID, keyword)
	if err != nil {
		return merged, taxonomyError(err)
	}
	if err := s.mergeRelationshipTerms(ctx, subject, source, target); err != nil {
		return merged, err
	}
	// Deliver organization curation to workspace workflow subscriptions.
	workspaces, err := s.DAL.Resources().Resources(ctx, resources.Filter{Kinds: []refs.ResourceKind{refs.KindWorkspace}, State: resources.Live})
	if err != nil {
		return merged, err
	}
	for _, w := range workspaces {
		s.emit(ctx, EventTermMerged, subject, w.ID, nil, "", map[string]any{"source_term_id": source.ID, "target_term_id": target.ID, "term_set_id": merged.SetID})
	}
	return merged, nil
}
