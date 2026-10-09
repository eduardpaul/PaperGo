package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/term"
	"strings"
	"time"
)

// EventTermMerged is raised when a term is merged into another; data has
// source_term_id, target_term_id and term_set_id (the target's set).
const EventTermMerged = "term.merged"

type MoveTermInput struct {
	// ParentID is the new parent in the same set; null moves the term to the root.
	ParentID *string `json:"parent_id"`
}

// MoveTerm moves a term and its subtree under another parent of its set.
func (s *Service) MoveTerm(ctx context.Context, subject, id string, version int, in MoveTermInput) (out TermView, err error) {
	err = s.write(ctx, func(t *Service) error {
		v, set, w, e := t.termForChange(ctx, subject, id, version)
		if e != nil {
			return e
		}
		parent, e := t.moveParent(ctx, set, in.ParentID)
		if e != nil {
			return e
		}
		moved, e := t.relocate(ctx, v, set, parent)
		if e != nil {
			return e
		}
		views, e := t.termViews(ctx, []*ent.Term{moved})
		if e != nil {
			return e
		}
		out = views[0]
		return t.audit(ctx, subject, "term.move", w, map[string]any{"term_id": id, "parent_id": in.ParentID})
	})
	return
}
func (s *Service) moveParent(ctx context.Context, set *ent.TermSet, id *string) (*ent.Term, error) {
	if id == nil {
		return nil, nil
	}
	p, e := s.Client.Term.Get(ctx, *id)
	if ent.IsNotFound(e) || e == nil && (p.TermSetID != set.ID || p.Deprecated) {
		return nil, invalid("parent_id must identify an active term of the set")
	}
	return p, e
}

// relocate moves v and its subtree under parent (nil for a root) of set,
// which may be another set of the workspace. Paths of the subtree follow.
func (s *Service) relocate(ctx context.Context, v *ent.Term, set *ent.TermSet, parent *ent.Term) (*ent.Term, error) {
	path := "/" + v.ID + "/"
	var parentID *string
	if parent != nil {
		if parent.ID == v.ID || strings.HasPrefix(parent.Path, v.Path) {
			return nil, invalid("a term cannot move below itself")
		}
		path, parentID = parent.Path+v.ID+"/", &parent.ID
	}
	lo, hi := subtree(v.Path)
	rows, e := s.Client.QueryContext(ctx, `SELECT coalesce(max(length(path)-length(replace(path,'/',''))),0) FROM terms WHERE path>? AND path<?`, lo, hi)
	if e != nil {
		return nil, e
	}
	slashes := 0
	if rows.Next() {
		e = rows.Scan(&slashes)
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	below := 0
	if slashes > 0 {
		below = slashes - 1 - termLevels(v.Path)
	}
	if termLevels(path)+below > MaxDepth {
		return nil, invalid("term hierarchy depth exceeded")
	}
	old := v.Path
	u := s.Client.Term.UpdateOne(v).SetPath(path).AddVersion(1)
	if parentID == nil {
		u.ClearParentID()
	} else {
		u.SetParentID(*parentID)
	}
	if set.ID != v.TermSetID {
		u.SetTermSetID(set.ID)
	}
	moved, e := u.Save(ctx)
	if e != nil {
		return nil, e
	}
	if old == path && set.ID == v.TermSetID {
		return moved, nil
	}
	now := time.Now()
	if set.ID == v.TermSetID {
		_, e = s.Client.ExecContext(ctx, `UPDATE terms SET path=?||substr(path,?),updated_at=? WHERE path>? AND path<?`, path, len(old)+1, now, lo, hi)
		return moved, e
	}
	// A parent must already be in the set its children move to, so the
	// subtree changes set one level at a time.
	base := termLevels(old)
	for level := base + 1; level <= base+below; level++ {
		if _, e = s.Client.ExecContext(ctx, `UPDATE terms SET term_set_id=?,path=?||substr(path,?),updated_at=? WHERE path>? AND path<? AND length(path)-length(replace(path,'/',''))=?`,
			set.ID, path, len(old)+1, now, lo, hi, level+1); e != nil {
			return nil, e
		}
	}
	return moved, nil
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
		source, set, w, e := t.termForChange(ctx, subject, id, version)
		if e != nil {
			return e
		}
		target, e := t.Client.Term.Get(ctx, in.TargetTermID)
		if ent.IsNotFound(e) || e == nil && target.TermSetID != set.ID {
			return invalid("target_term_id must identify a term of the same set")
		}
		if e != nil {
			return e
		}
		merged, e := t.mergeInto(ctx, subject, source, target, set, false)
		if e != nil {
			return e
		}
		views, e := t.termViews(ctx, []*ent.Term{merged})
		if e != nil {
			return e
		}
		out = views[0]
		return t.audit(ctx, subject, "term.merge", w, map[string]any{"term_id": id, "target_term_id": target.ID})
	})
	return
}

// mergeInto merges source into target, an active term of targetSet, and
// returns the updated target.
func (s *Service) mergeInto(ctx context.Context, subject string, source, target *ent.Term, targetSet *ent.TermSet, keyword bool) (*ent.Term, error) {
	if target.ID == source.ID || strings.HasPrefix(target.Path, source.Path) {
		return nil, invalid("a term cannot merge into itself or its descendants")
	}
	if target.Deprecated {
		return nil, invalid("the target term must be active")
	}
	children, e := s.Client.Term.Query().Where(term.ParentIDEQ(source.ID)).Order(ent.Asc(term.FieldID)).All(ctx)
	if e != nil {
		return nil, e
	}
	for _, c := range children {
		if c.MergedIntoID == nil {
			taken, e := s.Client.Term.Query().Where(term.ParentIDEQ(target.ID), term.NormalizedNameEQ(c.NormalizedName), term.MergedIntoIDIsNil()).Exist(ctx)
			if e != nil {
				return nil, e
			}
			if taken {
				return nil, invalid("the target already has a child named " + c.Name)
			}
		}
		if _, e = s.relocate(ctx, c, targetSet, target); e != nil {
			return nil, e
		}
	}
	if _, e = s.Client.Term.Update().Where(term.MergedIntoIDEQ(source.ID)).SetMergedIntoID(target.ID).Save(ctx); e != nil {
		return nil, e
	}
	synonyms := append([]string{}, target.Synonyms...)
	seen := map[string]bool{target.NormalizedName: true}
	for _, v := range synonyms {
		seen[strings.ToLower(v)] = true
	}
	for _, v := range append([]string{source.Name}, source.Synonyms...) {
		if key := strings.ToLower(v); !seen[key] && len(synonyms) < 50 {
			seen[key] = true
			synonyms = append(synonyms, v)
		}
	}
	if _, e = s.Client.Term.UpdateOneID(source.ID).SetMergedIntoID(target.ID).SetDeprecated(true).SetAvailableAsKeyword(false).AddVersion(1).Save(ctx); e != nil {
		return nil, e
	}
	u := s.Client.Term.UpdateOneID(target.ID).SetSynonyms(synonyms).AddVersion(1)
	if keyword && !targetSet.IsKeywords {
		u.SetAvailableAsKeyword(true)
	}
	merged, e := u.Save(ctx)
	if e != nil {
		return nil, e
	}
	s.emit(ctx, EventTermMerged, subject, targetSet.WorkspaceID, nil, "", map[string]any{"source_term_id": source.ID, "target_term_id": target.ID, "term_set_id": targetSet.ID})
	return merged, nil
}
