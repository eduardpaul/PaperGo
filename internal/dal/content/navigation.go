package content

import (
	"context"
	"strconv"

	"github.com/stephenafamo/scan"
	"papergo/internal/dal/catalog"
)

// TermSource is where the items a navigation counts hold their terms: the
// values of Field, or without one their keywords: own keywords, keyword
// field values and keyword terms in term fields.
type TermSource struct {
	Field *catalog.FieldDefinition
	// Subterms counts an item at every term above one of Field's values it
	// holds. Own keywords and fields that apply parents always count there.
	Subterms bool
}

// TermLevel is one level of a term set: the children of ParentID, whose
// path is ParentPath, or the roots when it is empty.
type TermLevel struct {
	SetID, ParentID, ParentPath string
	// HideEmpty leaves out terms no counted item holds at or below them.
	HideEmpty bool
	// After pages by sort order, normalized name and ID.
	After *TermNodeKey
	Limit int
}

// TermNodeKey orders the terms of a level.
type TermNodeKey struct {
	SortOrder      int
	NormalizedName string
	ID             string
}

// TermNode is a term of a level with the items counted at it: Listed hold
// the term, or one below it where that counts there; Subtree hold the term
// or one below it; Below hold one below it.
type TermNode struct {
	Key                    TermNodeKey
	Name                   string
	Children               bool
	Listed, Subtree, Below int
}

// held selects the terms the eligible rows e hold through src, as rows of
// item, term and whether the term counts at its ancestors.
func (r *Repo) held(src TermSource) (string, []any) {
	lineage := "f.applies_parents"
	if src.Subterms {
		lineage = "TRUE"
	}
	if src.Field != nil {
		return "SELECT e.id AS item,f.value_text AS term," + lineage + " AS lineage FROM eligible e JOIN field_values f ON f.surface_id=e.surface_id AND f.field_key=? AND f.field_type=?",
			[]any{src.Field.Key, string(src.Field.Type)}
	}
	// Keyword field values count above as their field says, as keyword
	// filters match them.
	return "SELECT e.id AS item,k.keyword AS term,TRUE AS lineage FROM eligible e JOIN item_surface_keywords k ON k.surface_id=e.surface_id" +
		" UNION ALL SELECT e.id,f.value_text,f.applies_parents FROM eligible e JOIN field_values f ON f.surface_id=e.surface_id WHERE " + keywordValue, nil
}

// TermNodes counts q's eligible rows at each term of a level. Each term an
// item holds counts at the term of the level it lies under, read from its
// path, so the items are counted in one pass whatever the level's size. A
// merged term counts as the term it was merged into; deprecated terms
// appear only while items hold them.
func (r *Repo) TermNodes(ctx context.Context, q *Query, src TermSource, l TermLevel) ([]TermNode, error) {
	query, args, err := r.eligible(ctx, q)
	if err != nil {
		return nil, err
	}
	held, heldArgs := r.held(src)
	args = append(args, heldArgs...)
	parent := l.ParentPath
	if parent == "" {
		parent = "/"
	}
	child := r.d.FirstSegment("substr(x.path," + strconv.Itoa(len(parent)+1) + ")")
	level := "t.term_set_id=? AND t.merged_into_id IS NULL AND "
	args = append(args, parent, parent[:len(parent)-1]+"0", l.SetID)
	if l.ParentID == "" {
		level += "t.parent_id IS NULL"
	} else {
		level += "t.parent_id=?"
		args = append(args, l.ParentID)
	}
	if a := l.After; a != nil {
		level += " AND (t.sort_order,t.normalized_name,t.id)>(?,?,?)"
		args = append(args, a.SortOrder, a.NormalizedName, a.ID)
	}
	having := "NOT t.deprecated OR coalesce(n.subtree,0)>0"
	if l.HideEmpty {
		having = "coalesce(n.subtree,0)>0"
	}
	statement := "WITH eligible AS (" + query + "),held AS (" + held + `),
placed AS (SELECT h.item,c.id AS term,c.path,h.lineage FROM held h JOIN terms s ON s.id=h.term JOIN terms c ON c.id=coalesce(s.merged_into_id,s.id)),
under AS (SELECT x.item,x.term,x.lineage,` + child + ` AS child FROM placed x WHERE x.path>? AND x.path<?),
counts AS (SELECT child,count(DISTINCT CASE WHEN term=child OR lineage THEN item END) AS listed,count(DISTINCT item) AS subtree,count(DISTINCT CASE WHEN term<>child THEN item END) AS below FROM under GROUP BY child)
SELECT t.id,t.name,t.normalized_name,t.sort_order,EXISTS(SELECT 1 FROM terms k WHERE k.parent_id=t.id AND k.merged_into_id IS NULL AND NOT k.deprecated),
coalesce(n.listed,0),coalesce(n.subtree,0),coalesce(n.below,0)
FROM terms t LEFT JOIN counts n ON n.child=t.id WHERE ` + level + ` AND (` + having + `) ORDER BY t.sort_order,t.normalized_name,t.id LIMIT ?`
	out := []TermNode{}
	err = r.each(ctx, func(rows scan.Rows) error {
		var n TermNode
		if err := rows.Scan(&n.Key.ID, &n.Name, &n.Key.NormalizedName, &n.Key.SortOrder, &n.Children, &n.Listed, &n.Subtree, &n.Below); err != nil {
			return err
		}
		out = append(out, n)
		return nil
	}, statement, append(args, l.Limit)...)
	return out, err
}

// Termless counts q's eligible rows that hold no term through src.
func (r *Repo) Termless(ctx context.Context, q *Query, src TermSource) (int, error) {
	query, args, err := r.eligible(ctx, q)
	if err != nil {
		return 0, err
	}
	held, heldArgs := r.held(src)
	var n int
	err = r.scan(ctx, []any{&n}, "WITH eligible AS ("+query+"),held AS ("+held+") SELECT count(*) FROM eligible e WHERE e.id NOT IN (SELECT item FROM held)", append(args, heldArgs...)...)
	return n, err
}
