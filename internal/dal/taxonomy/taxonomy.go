// Package taxonomy owns the organization term store: term groups, the term
// sets they hold and the terms of each set, kept as trees by materialized
// paths. Merged terms stay as deprecated pointers to the term that replaces
// them. Drivers implement Queries; this package decides what those
// statements mean.
package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
)

const (
	// SystemGroupID and KeywordsSetID are seeded by the schema.
	SystemGroupID = "00000000-0000-4000-8000-000000000001"
	KeywordsSetID = "00000000-0000-4000-8000-000000000002"
	// RelationshipTypesSetKey is the set whose terms name relationship types.
	RelationshipTypesSetKey = "relationship_types"
	// MaxDepth bounds term hierarchies and merge chains.
	MaxDepth = 32
	// MaxSynonyms bounds the synonyms a term keeps, merges included.
	MaxSynonyms = 50
)

// Violation is a taxonomy rule a change breaks; its text explains it.
type Violation string

func (v Violation) Error() string { return string(v) }

// Group is a term group. The system group holds only the keywords set.
type Group struct {
	ID, Name, Description string
	System                bool
	Version               int
	CreatedAt, UpdatedAt  time.Time
}

// Set is a term set. Open sets accept terms from readers; the keywords set
// is open and holds the organization's free keywords.
type Set struct {
	ID, GroupID, Key, Name, Description string
	Open, Keywords                      bool
	Version                             int
	CreatedAt, UpdatedAt                time.Time
}

// Term is a term. Path lists the ids from its root to itself as
// /root/…/term/. Empty ParentID, Color and MergedIntoID are NULL.
type Term struct {
	ID, SetID, ParentID, Name, NormalizedName string
	Description, Color, Path, MergedIntoID    string
	SortOrder                                 int
	Labels                                    map[string]string
	Synonyms                                  []string
	AvailableAsKeyword, Deprecated            bool
	Version                                   int
	CreatedAt, UpdatedAt                      time.Time
}

// Levels is how deep the term sits: 1 for a root term.
func (t Term) Levels() int { return Levels(t.Path) }

// Merged reports whether the term was merged into another.
func (t Term) Merged() bool { return t.MergedIntoID != "" }

// Lineage returns the ids from the term's root to the term.
func (t Term) Lineage() []string { return strings.Split(strings.Trim(t.Path, "/"), "/") }

// Levels is how deep a term with path sits.
func Levels(path string) int { return strings.Count(path, "/") - 1 }

// GroupFilter selects groups ordered by id.
type GroupFilter struct {
	Name, AfterID string
	System        bool
	Limit         int
}

// SetFilter selects sets ordered by id.
type SetFilter struct {
	GroupID, Key, Name, AfterID string
	Keywords                    bool
	Limit                       int
}

// Field selects the term texts a Match compares.
type Field uint8

const (
	MatchName Field = 1 << iota
	MatchLabels
	MatchSynonyms
)

// Order sorts selected terms.
type Order uint8

const (
	// OrderTree sorts by sort order, normalized name and id.
	OrderTree Order = iota
	OrderID
	// OrderPrefix puts names that start with Search first, then sorts by
	// normalized name and id.
	OrderPrefix
	// OrderExact puts the term named Match first, then sorts by path.
	OrderExact
	// OrderKeyword puts a root of KeywordSet named Match first, then sorts by id.
	OrderKeyword
)

// Cursor is the position after a term in OrderTree.
type Cursor struct {
	SortOrder int
	Name, ID  string
}

// Scope selects the terms below Path (Path's term included), the terms in
// IDs and the active children of the terms in ParentIDs.
type Scope struct {
	Path           string
	IDs, ParentIDs []string
}

// TermFilter selects terms. Zero fields do not filter.
type TermFilter struct {
	IDs   []string
	SetID string
	// ParentID selects children; Roots selects terms without a parent.
	ParentID string
	Roots    bool
	// Under selects terms strictly below a path.
	Under string
	Scope *Scope
	// Unmerged leaves merged terms out; Current leaves deprecated terms out.
	Unmerged, Current bool
	MergedInto        string
	// KeywordSet selects the terms of that keywords set and the terms
	// available as keywords.
	KeywordSet string
	// Search is a lowercase needle found in names, labels or synonyms.
	Search string
	// Match is a lowercase text equal to one of the In fields.
	Match string
	In    Field
	After *Cursor
	// AfterID selects ids after it.
	AfterID string
	Order   Order
	Limit   int
}

// Queries are the statements a driver runs for the term store. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	TermGroup(ctx context.Context, id string) (Group, error)
	TermGroups(ctx context.Context, f GroupFilter) ([]Group, error)
	InsertTermGroup(ctx context.Context, g Group) error
	// UpdateTermGroup writes g's name and description when its version
	// still is g.Version-1.
	UpdateTermGroup(ctx context.Context, g Group) (bool, error)
	DeleteTermGroup(ctx context.Context, id string) error
	TermSet(ctx context.Context, id string) (Set, error)
	TermSets(ctx context.Context, f SetFilter) ([]Set, error)
	// LockTermSet serializes tree changes of a set.
	LockTermSet(ctx context.Context, id string) error
	InsertTermSet(ctx context.Context, s Set) error
	// UpdateTermSet writes s's name, description and openness when its
	// version still is s.Version-1.
	UpdateTermSet(ctx context.Context, s Set) (bool, error)
	DeleteTermSet(ctx context.Context, id string) error
	// TermSetFields reports whether a term field draws from the set.
	TermSetFields(ctx context.Context, id string) (bool, error)
	Term(ctx context.Context, id string) (Term, error)
	Terms(ctx context.Context, f TermFilter) ([]Term, error)
	TermsExist(ctx context.Context, f TermFilter) (bool, error)
	// TermParents returns which of ids have unmerged children.
	TermParents(ctx context.Context, ids []string) ([]string, error)
	// SubtreeDepth is the deepest level below path, or 0.
	SubtreeDepth(ctx context.Context, path string) (int, error)
	InsertTerm(ctx context.Context, t Term) error
	// UpdateTerm writes every mutable column of t when its version still
	// is t.Version-1.
	UpdateTerm(ctx context.Context, t Term) (bool, error)
	// MoveSubtree rewrites the terms strictly below from to sit below to,
	// in set.
	MoveSubtree(ctx context.Context, from, to, set string, at time.Time) error
	// RepointMerged points the terms merged into from at to.
	RepointMerged(ctx context.Context, from, to string, at time.Time) error
	// MatchTerms returns which of terms values hold, themselves or below,
	// following merges on both sides.
	MatchTerms(ctx context.Context, terms, values []string) ([]string, error)
	// TermFamily returns a term, its descendants and the terms merged into
	// any of them.
	TermFamily(ctx context.Context, id string) ([]string, error)
	// TagResource replaces the keywords of a container resource.
	TagResource(ctx context.Context, resourceID string, termIDs []string) error
	// TagSurface replaces the intrinsic keywords of an item surface.
	TagSurface(ctx context.Context, surfaceID string, termIDs []string) error
}

// Repo applies the term store's rules for every engine. read runs single
// statements; write runs a function in one transaction, joining the
// caller's when there is one.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func (r *Repo) Group(ctx context.Context, id string) (Group, error) {
	return r.read.TermGroup(ctx, id)
}

// GroupExists reports whether a group exists.
func (r *Repo) GroupExists(ctx context.Context, id string) (bool, error) {
	return found(r.read.TermGroup(ctx, id))
}

func (r *Repo) Groups(ctx context.Context, f GroupFilter) ([]Group, error) {
	return r.read.TermGroups(ctx, f)
}

// CreateGroup adds a group; names are unique.
func (r *Repo) CreateGroup(ctx context.Context, g Group) (Group, error) {
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	g.Version = 1
	g.CreatedAt = time.Now().UTC()
	g.UpdatedAt = g.CreatedAt
	return g, r.write(ctx, func(q Queries) error { return q.InsertTermGroup(ctx, g) })
}

// SaveGroup renames a group or changes its description.
func (r *Repo) SaveGroup(ctx context.Context, in Group, version int) (Group, error) {
	var out Group
	err := r.write(ctx, func(q Queries) error {
		g, err := current(ctx, q.TermGroup, in.ID, func(g Group) int { return g.Version }, version)
		if err != nil {
			return err
		}
		if g.System {
			return Violation("the system term group is managed by PaperGo")
		}
		g.Name, g.Description, g.Version, g.UpdatedAt = in.Name, in.Description, version+1, time.Now().UTC()
		out = g
		return saved(q.UpdateTermGroup(ctx, g))
	})
	return out, err
}

// DeleteGroup deletes an empty group other than the system group.
func (r *Repo) DeleteGroup(ctx context.Context, id string, version int) error {
	return r.write(ctx, func(q Queries) error {
		g, err := current(ctx, q.TermGroup, id, func(g Group) int { return g.Version }, version)
		if err != nil {
			return err
		}
		if g.System {
			return Violation("the system term group cannot be deleted")
		}
		sets, err := q.TermSets(ctx, SetFilter{GroupID: id, Limit: 1})
		if err != nil {
			return err
		}
		if len(sets) > 0 {
			return Violation("delete or empty the group's term sets first")
		}
		return q.DeleteTermGroup(ctx, id)
	})
}

func (r *Repo) Set(ctx context.Context, id string) (Set, error) { return r.read.TermSet(ctx, id) }

// SetExists reports whether a set exists.
func (r *Repo) SetExists(ctx context.Context, id string) (bool, error) {
	return found(r.read.TermSet(ctx, id))
}

func (r *Repo) Sets(ctx context.Context, f SetFilter) ([]Set, error) {
	return r.read.TermSets(ctx, f)
}

// SetByKey returns the set with key, or dberr.ErrNotFound.
func (r *Repo) SetByKey(ctx context.Context, key string) (Set, error) {
	return only(r.read.TermSets(ctx, SetFilter{Key: key, Limit: 1}))
}

// KeywordsSet returns the organization's keywords set.
func (r *Repo) KeywordsSet(ctx context.Context) (Set, error) {
	return r.read.TermSet(ctx, KeywordsSetID)
}

// CreateSet adds a set to a group other than the system group; keys are
// unique, and names are unique within a group.
func (r *Repo) CreateSet(ctx context.Context, s Set) (Set, error) {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.Keywords, s.Version = false, 1
	s.CreatedAt = time.Now().UTC()
	s.UpdatedAt = s.CreatedAt
	return s, r.write(ctx, func(q Queries) error {
		g, err := q.TermGroup(ctx, s.GroupID)
		if dberr.IsNotFound(err) {
			return Violation("group_id must identify an organization term group")
		}
		if err != nil {
			return err
		}
		if g.System {
			return Violation("the system term group holds only the keywords set")
		}
		return q.InsertTermSet(ctx, s)
	})
}

// SaveSet changes a set's name, description and openness. Its key and
// group never change, and the keywords set stays open.
func (r *Repo) SaveSet(ctx context.Context, in Set, version int) (Set, error) {
	var out Set
	err := r.write(ctx, func(q Queries) error {
		s, err := current(ctx, q.TermSet, in.ID, func(s Set) int { return s.Version }, version)
		if err != nil {
			return err
		}
		if in.Key != "" && in.Key != s.Key || in.GroupID != "" && in.GroupID != s.GroupID {
			return Violation("term set key and group are immutable")
		}
		if s.Keywords && !in.Open {
			return Violation("the keywords set stays open")
		}
		s.Name, s.Description, s.Open, s.Version, s.UpdatedAt = in.Name, in.Description, in.Open, version+1, time.Now().UTC()
		out = s
		return saved(q.UpdateTermSet(ctx, s))
	})
	return out, err
}

// DeleteSet deletes a set that never had terms and that no field uses.
// Terms are retained, so a set that had terms stays.
func (r *Repo) DeleteSet(ctx context.Context, id string, version int) error {
	return r.write(ctx, func(q Queries) error {
		s, err := current(ctx, q.TermSet, id, func(s Set) int { return s.Version }, version)
		if err != nil {
			return err
		}
		if s.Keywords {
			return Violation("the keywords set cannot be deleted")
		}
		if used, err := q.TermsExist(ctx, TermFilter{SetID: id}); err != nil || used {
			return orViolation(err, used, "term sets with terms cannot be deleted")
		}
		if used, err := q.TermSetFields(ctx, id); err != nil || used {
			return orViolation(err, used, "the term set is used by fields")
		}
		return q.DeleteTermSet(ctx, id)
	})
}

func (r *Repo) Term(ctx context.Context, id string) (Term, error) { return r.read.Term(ctx, id) }

// TermExists reports whether a term exists, merged or not.
func (r *Repo) TermExists(ctx context.Context, id string) (bool, error) {
	return found(r.read.Term(ctx, id))
}

// IsRelationshipType reports whether id is a current term of the
// relationship types set.
func (r *Repo) IsRelationshipType(ctx context.Context, id string) (bool, error) {
	t, err := r.read.Term(ctx, id)
	if dberr.IsNotFound(err) || err == nil && t.Deprecated {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	set, err := r.read.TermSet(ctx, t.SetID)
	return err == nil && set.Key == RelationshipTypesSetKey, err
}

func (r *Repo) Terms(ctx context.Context, f TermFilter) ([]Term, error) {
	return r.read.Terms(ctx, f)
}

func (r *Repo) TermsExist(ctx context.Context, f TermFilter) (bool, error) {
	return r.read.TermsExist(ctx, f)
}

// Canonical returns the term id stands for: itself, or the term its merges
// lead to.
func (r *Repo) Canonical(ctx context.Context, id string) (Term, error) {
	return canonical(ctx, r.read, id)
}

func canonical(ctx context.Context, q Queries, id string) (Term, error) {
	t, err := q.Term(ctx, id)
	for hops := 0; err == nil && t.Merged(); hops++ {
		if hops == MaxDepth {
			return Term{}, dberr.ErrCycleDetected
		}
		t, err = q.Term(ctx, t.MergedIntoID)
	}
	return t, err
}

// Parents reports which of ids have unmerged children.
func (r *Repo) Parents(ctx context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	parents, err := r.read.TermParents(ctx, ids)
	for _, id := range parents {
		out[id] = true
	}
	return out, err
}

// AddTerm adds t to its set, below t.ParentID when set. The parent must be
// an active term of the set.
func (r *Repo) AddTerm(ctx context.Context, t Term) (Term, error) {
	err := r.write(ctx, func(q Queries) error {
		var err error
		t, err = addTerm(ctx, q, t)
		return err
	})
	return t, err
}

func addTerm(ctx context.Context, q Queries, t Term) (Term, error) {
	if err := q.LockTermSet(ctx, t.SetID); err != nil {
		return Term{}, err
	}
	set, err := q.TermSet(ctx, t.SetID)
	if err != nil {
		return Term{}, err
	}
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	t.Path = "/" + t.ID + "/"
	if t.ParentID != "" {
		parent, err := q.Term(ctx, t.ParentID)
		if dberr.IsNotFound(err) {
			return Term{}, Violation("unknown parent term")
		}
		if err != nil {
			return Term{}, err
		}
		if parent.SetID != set.ID || parent.Deprecated {
			return Term{}, Violation("parent must be active and in the same term set")
		}
		t.Path = parent.Path + t.ID + "/"
	}
	if t.Levels() > MaxDepth {
		return Term{}, Violation("term hierarchy depth exceeded")
	}
	if t.AvailableAsKeyword && set.Keywords {
		return Term{}, Violation("keywords are always available as keywords")
	}
	t.NormalizedName = strings.ToLower(t.Name)
	if t.Labels == nil {
		t.Labels = map[string]string{}
	}
	if t.Synonyms == nil {
		t.Synonyms = []string{}
	}
	t.MergedIntoID, t.Version = "", 1
	t.CreatedAt = time.Now().UTC()
	t.UpdatedAt = t.CreatedAt
	return t, q.InsertTerm(ctx, t)
}

// SaveTerm changes a term's texts, color, order and flags. Merged terms are
// read-only, and moving a term is Move.
func (r *Repo) SaveTerm(ctx context.Context, in Term, version int) (Term, error) {
	var out Term
	err := r.write(ctx, func(q Queries) error {
		t, err := changeable(ctx, q, in.ID, version)
		if err != nil {
			return err
		}
		set, err := q.TermSet(ctx, t.SetID)
		if err != nil {
			return err
		}
		if in.AvailableAsKeyword && set.Keywords {
			return Violation("keywords are always available as keywords")
		}
		t.Name, t.NormalizedName, t.Description, t.Color = in.Name, strings.ToLower(in.Name), in.Description, in.Color
		t.SortOrder, t.Labels, t.Synonyms = in.SortOrder, in.Labels, in.Synonyms
		t.Deprecated, t.AvailableAsKeyword = in.Deprecated, in.AvailableAsKeyword
		out, err = update(ctx, q, t)
		return err
	})
	return out, err
}

// Move moves a term and its subtree below parentID (a root when empty) of
// setID, which may be another set. keyword makes the moved term available
// as a keyword.
func (r *Repo) Move(ctx context.Context, id string, version int, setID, parentID string, keyword bool) (Term, error) {
	var out Term
	err := r.write(ctx, func(q Queries) error {
		t, err := lockedTerm(ctx, q, id, version, setID)
		if err != nil {
			return err
		}
		t.AvailableAsKeyword = t.AvailableAsKeyword || keyword
		out, err = move(ctx, q, t, setID, parentID)
		return err
	})
	return out, err
}

// move moves t, loaded while its set and setID were locked.
func move(ctx context.Context, q Queries, t Term, setID, parentID string) (Term, error) {
	path := "/" + t.ID + "/"
	if parentID != "" {
		parent, err := q.Term(ctx, parentID)
		if dberr.IsNotFound(err) || err == nil && (parent.SetID != setID || parent.Deprecated) {
			return Term{}, Violation("parent_id must identify an active term of the set")
		}
		if err != nil {
			return Term{}, err
		}
		if parent.ID == t.ID || strings.HasPrefix(parent.Path, t.Path) {
			return Term{}, Violation("a term cannot move below itself")
		}
		path = parent.Path + t.ID + "/"
	}
	deepest, err := q.SubtreeDepth(ctx, t.Path)
	if err != nil {
		return Term{}, err
	}
	below := 0
	if deepest > 0 {
		below = deepest - t.Levels()
	}
	if Levels(path)+below > MaxDepth {
		return Term{}, Violation("term hierarchy depth exceeded")
	}
	old, oldSet := t.Path, t.SetID
	t.ParentID, t.Path, t.SetID = parentID, path, setID
	moved, err := update(ctx, q, t)
	if err != nil || old == path && oldSet == setID {
		return moved, err
	}
	return moved, q.MoveSubtree(ctx, old, path, setID, moved.UpdatedAt)
}

// Merge merges source into target, an active term outside source's
// subtree: the source's children move below the target, terms merged into
// the source now point to the target, the source's name and synonyms join
// the target's synonyms, and the source stays as a deprecated term whose
// MergedIntoID names the target. keyword makes the target available as a
// keyword. It returns the updated target.
func (r *Repo) Merge(ctx context.Context, sourceID string, version int, targetID string, keyword bool) (Term, error) {
	var out Term
	err := r.write(ctx, func(q Queries) error {
		target, err := q.Term(ctx, targetID)
		if err != nil {
			return err
		}
		source, err := lockedTerm(ctx, q, sourceID, version, target.SetID)
		if err != nil {
			return err
		}
		if target, err = q.Term(ctx, targetID); err != nil {
			return err
		}
		if target.ID == source.ID || strings.HasPrefix(target.Path, source.Path) {
			return Violation("a term cannot merge into itself or its descendants")
		}
		if target.Deprecated {
			return Violation("the target term must be active")
		}
		children, err := q.Terms(ctx, TermFilter{ParentID: source.ID, Order: OrderID})
		if err != nil {
			return err
		}
		for _, c := range children {
			if !c.Merged() {
				taken, err := q.TermsExist(ctx, TermFilter{ParentID: target.ID, Match: c.NormalizedName, In: MatchName, Unmerged: true})
				if err != nil {
					return err
				}
				if taken {
					return Violation("the target already has a child named " + c.Name)
				}
			}
			if _, err = move(ctx, q, c, target.SetID, target.ID); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		if err = q.RepointMerged(ctx, source.ID, target.ID, now); err != nil {
			return err
		}
		synonyms := append([]string{}, target.Synonyms...)
		seen := map[string]bool{target.NormalizedName: true}
		for _, v := range synonyms {
			seen[strings.ToLower(v)] = true
		}
		for _, v := range append([]string{source.Name}, source.Synonyms...) {
			if key := strings.ToLower(v); !seen[key] && len(synonyms) < MaxSynonyms {
				seen[key] = true
				synonyms = append(synonyms, v)
			}
		}
		source.MergedIntoID, source.Deprecated, source.AvailableAsKeyword = target.ID, true, false
		if _, err = update(ctx, q, source); err != nil {
			return err
		}
		set, err := q.TermSet(ctx, target.SetID)
		if err != nil {
			return err
		}
		target.Synonyms = synonyms
		if keyword && !set.Keywords {
			target.AvailableAsKeyword = true
		}
		out, err = update(ctx, q, target)
		return err
	})
	return out, err
}

// Lookup returns up to two current terms of a set with a name, label or
// synonym equal to label, comparing the fields in that order and stopping
// at the first that matches.
func (r *Repo) Lookup(ctx context.Context, setID, label string) ([]Term, error) {
	key := strings.ToLower(label)
	for _, field := range []Field{MatchName, MatchLabels, MatchSynonyms} {
		found, err := r.read.Terms(ctx, TermFilter{SetID: setID, Current: true, Match: key, In: field, Limit: 2})
		if err != nil || len(found) > 0 {
			return found, err
		}
	}
	return nil, nil
}

// Keyword returns the active keyword named name, matching names and
// synonyms without regard to case: a root of the keywords set first, then a
// term available as a keyword. found is false when none matches.
func (r *Repo) Keyword(ctx context.Context, name string) (t Term, set Set, found bool, err error) {
	return keyword(ctx, r.read, name)
}

func keyword(ctx context.Context, q Queries, name string) (Term, Set, bool, error) {
	set, err := q.TermSet(ctx, KeywordsSetID)
	if err != nil {
		return Term{}, Set{}, false, err
	}
	rows, err := q.Terms(ctx, TermFilter{KeywordSet: set.ID, Current: true, Match: strings.ToLower(name), In: MatchName | MatchSynonyms, Order: OrderKeyword, Limit: 1})
	if err != nil || len(rows) == 0 {
		return Term{}, set, false, err
	}
	return rows[0], set, true, nil
}

// ResolveKeywords turns keyword ids and labels into the ids of the active
// keywords they stand for, adding a keyword for an unknown label. Merged
// ids stand for their targets, and duplicates collapse.
func ResolveKeywords(ctx context.Context, q Queries, keywords []string) ([]string, error) {
	out := make([]string, 0, len(keywords))
	if len(keywords) == 0 {
		return out, nil
	}
	if err := q.LockTermSet(ctx, KeywordsSetID); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, raw := range keywords {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		t, err := canonical(ctx, q, label)
		if dberr.IsNotFound(err) {
			var set Set
			var found bool
			if t, set, found, err = keyword(ctx, q, label); err == nil && !found {
				if !set.Open {
					return nil, fmt.Errorf("%w: keyword term set is closed", dberr.ErrInvalidReference)
				}
				t, err = addTerm(ctx, q, Term{SetID: set.ID, Name: label})
			}
		}
		if err != nil {
			return nil, err
		}
		if t.Deprecated {
			return nil, fmt.Errorf("%w: deprecated terms cannot be keywords", dberr.ErrInvalidReference)
		}
		if t.SetID != KeywordsSetID && !t.AvailableAsKeyword {
			return nil, fmt.Errorf("%w: term is not available as a keyword", dberr.ErrInvalidReference)
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t.ID)
		}
	}
	return out, nil
}

// Family returns a term, its descendants and the terms merged into any of
// them: the values a filter on the term matches.
func (r *Repo) Family(ctx context.Context, id string) ([]string, error) {
	return r.read.TermFamily(ctx, id)
}

// Match reports which of terms values hold: the term itself, one of its
// descendants, or a term merged into one of those. A merged term in terms
// stands for the term it was merged into.
func (r *Repo) Match(ctx context.Context, terms, values []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(terms) == 0 || len(values) == 0 {
		return out, nil
	}
	ids, err := r.read.MatchTerms(ctx, terms, values)
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// lockedTerm locks the sets of term id and of others, in id order so that
// concurrent changes cannot deadlock, then loads the term as changeable.
func lockedTerm(ctx context.Context, q Queries, id string, version int, others ...string) (Term, error) {
	t, err := changeable(ctx, q, id, version)
	if err != nil {
		return Term{}, err
	}
	sets := append([]string{t.SetID}, others...)
	slices.Sort(sets)
	for _, set := range slices.Compact(sets) {
		if err := q.LockTermSet(ctx, set); err != nil {
			return Term{}, err
		}
	}
	return changeable(ctx, q, id, version)
}

// changeable loads an unmerged term at version.
func changeable(ctx context.Context, q Queries, id string, version int) (Term, error) {
	t, err := current(ctx, q.Term, id, func(t Term) int { return t.Version }, version)
	if err != nil {
		return Term{}, err
	}
	if t.Merged() {
		return Term{}, Violation("the term was merged into " + t.MergedIntoID)
	}
	return t, nil
}

// update writes t as its next version.
func update(ctx context.Context, q Queries, t Term) (Term, error) {
	t.Version++
	t.UpdatedAt = time.Now().UTC()
	return t, saved(q.UpdateTerm(ctx, t))
}

// current loads a record and checks its version.
func current[T any](ctx context.Context, find func(context.Context, string) (T, error), id string, version func(T) int, want int) (T, error) {
	v, err := find(ctx, id)
	if err == nil && version(v) != want {
		err = dberr.ErrConcurrentUpdate
	}
	return v, err
}

func saved(ok bool, err error) error {
	if err == nil && !ok {
		return dberr.ErrConcurrentUpdate
	}
	return err
}

func only[T any](rows []T, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	if len(rows) == 0 {
		return zero, dberr.ErrNotFound
	}
	return rows[0], nil
}

func found[T any](_ T, err error) (bool, error) {
	if dberr.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func orViolation(err error, broken bool, rule string) error {
	if err != nil {
		return err
	}
	if broken {
		return Violation(rule)
	}
	return nil
}

// IsViolation reports whether err breaks a taxonomy rule.
func IsViolation(err error) bool {
	var v Violation
	return errors.As(err, &v)
}
