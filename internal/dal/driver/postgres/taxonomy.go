package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/taxonomy"
)

func (q Queries) TermGroup(ctx context.Context, id string) (taxonomy.Group, error) {
	row, err := models.FindTermGroup(ctx, q.exec, id)
	if err != nil {
		return taxonomy.Group{}, dberr.Translate(err)
	}
	return groupRecord(row), nil
}

func (q Queries) TermGroups(ctx context.Context, f taxonomy.GroupFilter) ([]taxonomy.Group, error) {
	w := models.SelectWhere.TermGroups
	query := models.TermGroups.Query(sm.OrderBy(models.TermGroups.Columns.ID))
	if f.Name != "" {
		query.Apply(w.Name.EQ(f.Name))
	}
	if f.System {
		query.Apply(w.IsSystem.EQ(true))
	}
	if f.AfterID != "" {
		query.Apply(w.ID.GT(f.AfterID))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]taxonomy.Group, len(rows))
	for i, row := range rows {
		out[i] = groupRecord(row)
	}
	return out, nil
}

func (q Queries) InsertTermGroup(ctx context.Context, g taxonomy.Group) error {
	_, err := models.TermGroups.Insert(&models.TermGroupSetter{
		ID: omit.From(g.ID), CreatedAt: omit.From(g.CreatedAt), UpdatedAt: omit.From(g.UpdatedAt),
		Name: omit.From(g.Name), Description: omit.From(g.Description), IsSystem: omit.From(g.System), Version: omit.From(int64(g.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateTermGroup(ctx context.Context, g taxonomy.Group) (bool, error) {
	set := &models.TermGroupSetter{Name: omit.From(g.Name), Description: omit.From(g.Description), UpdatedAt: omit.From(g.UpdatedAt), Version: omit.From(int64(g.Version))}
	w := models.UpdateWhere.TermGroups
	n, err := models.TermGroups.Update(set.UpdateMod(), w.ID.EQ(g.ID), w.Version.EQ(int64(g.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DeleteTermGroup(ctx context.Context, id string) error {
	_, err := models.TermGroups.Delete(models.DeleteWhere.TermGroups.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) TermSet(ctx context.Context, id string) (taxonomy.Set, error) {
	row, err := models.FindTermSet(ctx, q.exec, id)
	if err != nil {
		return taxonomy.Set{}, dberr.Translate(err)
	}
	return setRecord(row), nil
}

func (q Queries) TermSets(ctx context.Context, f taxonomy.SetFilter) ([]taxonomy.Set, error) {
	w := models.SelectWhere.TermSets
	query := models.TermSets.Query(sm.OrderBy(models.TermSets.Columns.ID))
	if f.GroupID != "" {
		query.Apply(w.GroupID.EQ(f.GroupID))
	}
	if f.Key != "" {
		query.Apply(w.Key.EQ(f.Key))
	}
	if f.Name != "" {
		query.Apply(w.Name.EQ(f.Name))
	}
	if f.Keywords {
		query.Apply(w.IsKeywords.EQ(true))
	}
	if f.AfterID != "" {
		query.Apply(w.ID.GT(f.AfterID))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]taxonomy.Set, len(rows))
	for i, row := range rows {
		out[i] = setRecord(row)
	}
	return out, nil
}

// LockTermSet locks the set's row until the transaction ends.
func (q Queries) LockTermSet(ctx context.Context, id string) error {
	_, err := q.exec.ExecContext(ctx, `SELECT id FROM term_sets WHERE id = $1 FOR UPDATE`, id)
	return dberr.Translate(err)
}

func (q Queries) InsertTermSet(ctx context.Context, s taxonomy.Set) error {
	_, err := models.TermSets.Insert(&models.TermSetSetter{
		ID: omit.From(s.ID), CreatedAt: omit.From(s.CreatedAt), UpdatedAt: omit.From(s.UpdatedAt), GroupID: omit.From(s.GroupID),
		Key: omit.From(s.Key), Name: omit.From(s.Name), Description: omit.From(s.Description),
		IsOpen: omit.From(s.Open), IsKeywords: omit.From(s.Keywords), Version: omit.From(int64(s.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateTermSet(ctx context.Context, s taxonomy.Set) (bool, error) {
	set := &models.TermSetSetter{Name: omit.From(s.Name), Description: omit.From(s.Description), IsOpen: omit.From(s.Open), UpdatedAt: omit.From(s.UpdatedAt), Version: omit.From(int64(s.Version))}
	w := models.UpdateWhere.TermSets
	n, err := models.TermSets.Update(set.UpdateMod(), w.ID.EQ(s.ID), w.Version.EQ(int64(s.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DeleteTermSet(ctx context.Context, id string) error {
	_, err := models.TermSets.Delete(models.DeleteWhere.TermSets.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) TermSetFields(ctx context.Context, id string) (bool, error) {
	return directoryBool(ctx, q.exec, `SELECT EXISTS(SELECT 1 FROM field_definitions WHERE options ->> 'term_set_id' = $1)`, id)
}

func (q Queries) Term(ctx context.Context, id string) (taxonomy.Term, error) {
	row, err := models.FindTerm(ctx, q.exec, id)
	if err != nil {
		return taxonomy.Term{}, dberr.Translate(err)
	}
	return termRecord(row), nil
}

func (q Queries) Terms(ctx context.Context, f taxonomy.TermFilter) ([]taxonomy.Term, error) {
	query := models.Terms.Query(sm.Where(termWhere(f)))
	for _, order := range termOrder(f) {
		query.Apply(sm.OrderBy(order))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]taxonomy.Term, len(rows))
	for i, row := range rows {
		out[i] = termRecord(row)
	}
	return out, nil
}

func (q Queries) TermsExist(ctx context.Context, f taxonomy.TermFilter) (bool, error) {
	ok, err := models.Terms.Query(sm.Where(termWhere(f))).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) TermParents(ctx context.Context, ids []string) ([]string, error) {
	c := models.Terms.Columns
	query := models.Terms.Query(sm.Columns(c.ParentID), sm.Distinct(), sm.Where(c.ParentID.In(psql.Arg(anys(ids)...))), sm.Where(c.MergedIntoID.IsNull()))
	out, err := bob.All(ctx, q.exec, query, scan.SingleColumnMapper[string])
	return out, dberr.Translate(err)
}

func (q Queries) SubtreeDepth(ctx context.Context, path string) (int, error) {
	lo, hi := subtree(path)
	rows, err := directoryRows(ctx, q.exec, `SELECT coalesce(max(length(path) - length(replace(path, '/', ''))), 0) FROM terms WHERE path > $1 AND path < $2`,
		func(r scan.Rows) (int, error) { var n int; err := r.Scan(&n); return n, err }, lo, hi)
	if err != nil || len(rows) == 0 || rows[0] == 0 {
		return 0, err
	}
	return rows[0] - 1, nil
}

func (q Queries) InsertTerm(ctx context.Context, t taxonomy.Term) error {
	set := termSetter(t)
	set.ID, set.CreatedAt, set.TermSetID, set.Path = omit.From(t.ID), omit.From(t.CreatedAt), omit.From(t.SetID), omit.From(t.Path)
	_, err := models.Terms.Insert(set).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateTerm(ctx context.Context, t taxonomy.Term) (bool, error) {
	set := termSetter(t)
	set.TermSetID, set.Path = omit.From(t.SetID), omit.From(t.Path)
	w := models.UpdateWhere.Terms
	n, err := models.Terms.Update(set.UpdateMod(), w.ID.EQ(t.ID), w.Version.EQ(int64(t.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) MoveSubtree(ctx context.Context, from, to, set string, at time.Time) error {
	lo, hi := subtree(from)
	_, err := q.exec.ExecContext(ctx, `UPDATE terms SET path = $1::text || substr(path, $2::integer), term_set_id = $3, updated_at = $4 WHERE path > $5 AND path < $6`, to, len(from)+1, set, at, lo, hi)
	return dberr.Translate(err)
}

func (q Queries) RepointMerged(ctx context.Context, from, to string, at time.Time) error {
	set := &models.TermSetter{MergedIntoID: nullable.String(to), UpdatedAt: omit.From(at)}
	_, err := models.Terms.Update(set.UpdateMod(), models.UpdateWhere.Terms.MergedIntoID.EQ(from)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) MatchTerms(ctx context.Context, terms, values []string) ([]string, error) {
	roots, _ := json.Marshal(terms)
	held, _ := json.Marshal(values)
	return directoryRows(ctx, q.exec, `SELECT DISTINCT j.value FROM jsonb_array_elements_text($1::jsonb) j(value)
JOIN terms r ON r.id = coalesce((SELECT merged_into_id FROM terms WHERE id = j.value), j.value)
CROSS JOIN jsonb_array_elements_text($2::jsonb) v(value) JOIN terms t ON t.id = coalesce((SELECT merged_into_id FROM terms WHERE id = v.value), v.value)
WHERE t.path >= r.path AND t.path < substr(r.path, 1, length(r.path) - 1) || '0'`,
		func(r scan.Rows) (string, error) { var id string; err := r.Scan(&id); return id, err }, string(roots), string(held))
}

func (q Queries) TermFamily(ctx context.Context, id string) ([]string, error) {
	subtree := `terms r JOIN terms t ON t.path >= r.path AND t.path < substr(r.path, 1, length(r.path) - 1) || '0' WHERE r.id = $1`
	return directoryRows(ctx, q.exec, `SELECT t.id FROM `+subtree+` UNION SELECT m.id FROM terms m WHERE m.merged_into_id IN (SELECT t.id FROM `+subtree+`)`,
		func(r scan.Rows) (string, error) { var v string; err := r.Scan(&v); return v, err }, id)
}

func (q Queries) TagResource(ctx context.Context, resourceID string, termIDs []string) error {
	if _, err := models.ResourceKeywords.Delete(models.DeleteWhere.ResourceKeywords.ResourceID.EQ(resourceID)).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	if len(termIDs) == 0 {
		return nil
	}
	rows := make([]bob.Mod[*dialect.InsertQuery], len(termIDs))
	for i, id := range termIDs {
		rows[i] = &models.ResourceKeywordSetter{ResourceID: omit.From(resourceID), Keyword: omit.From(id)}
	}
	_, err := models.ResourceKeywords.Insert(rows...).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) TagSurface(ctx context.Context, surfaceID string, termIDs []string) error {
	if _, err := models.ItemSurfaceKeywords.Delete(models.DeleteWhere.ItemSurfaceKeywords.SurfaceID.EQ(surfaceID)).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	if len(termIDs) == 0 {
		return nil
	}
	rows := make([]bob.Mod[*dialect.InsertQuery], len(termIDs))
	for i, id := range termIDs {
		rows[i] = &models.ItemSurfaceKeywordSetter{SurfaceID: omit.From(surfaceID), Keyword: omit.From(id)}
	}
	_, err := models.ItemSurfaceKeywords.Insert(rows...).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

// subtree bounds the paths strictly below path: every descendant's path
// starts with path, and paths compare as bytes, where '0' is the byte after
// '/'.
func subtree(path string) (lo, hi string) { return path, path[:len(path)-1] + "0" }

func termWhere(f taxonomy.TermFilter) bob.Expression {
	c := models.Terms.Columns
	where := []bob.Expression{psql.Raw("TRUE")}
	if len(f.IDs) > 0 {
		where = append(where, c.ID.In(psql.Arg(anys(f.IDs)...)))
	}
	if f.SetID != "" {
		where = append(where, c.TermSetID.EQ(psql.Arg(f.SetID)))
	}
	if f.ParentID != "" {
		where = append(where, c.ParentID.EQ(psql.Arg(f.ParentID)))
	}
	if f.Roots {
		where = append(where, c.ParentID.IsNull())
	}
	if f.Under != "" {
		lo, hi := subtree(f.Under)
		where = append(where, c.Path.GT(psql.Arg(lo)), c.Path.LT(psql.Arg(hi)))
	}
	if s := f.Scope; s != nil {
		either := []bob.Expression{psql.Raw("FALSE")}
		if s.Path != "" {
			_, hi := subtree(s.Path)
			either = append(either, psql.And(c.Path.GTE(psql.Arg(s.Path)), c.Path.LT(psql.Arg(hi))))
		}
		if len(s.IDs) > 0 {
			either = append(either, c.ID.In(psql.Arg(anys(s.IDs)...)))
		}
		if len(s.ParentIDs) > 0 {
			either = append(either, psql.And(c.ParentID.In(psql.Arg(anys(s.ParentIDs)...)), c.Deprecated.EQ(psql.Arg(false)), c.MergedIntoID.IsNull()))
		}
		where = append(where, psql.Or(either...))
	}
	if f.Unmerged {
		where = append(where, c.MergedIntoID.IsNull())
	}
	if f.Current {
		where = append(where, c.Deprecated.EQ(psql.Arg(false)))
	}
	if f.MergedInto != "" {
		where = append(where, c.MergedIntoID.EQ(psql.Arg(f.MergedInto)))
	}
	if f.KeywordSet != "" {
		where = append(where, psql.Or(c.TermSetID.EQ(psql.Arg(f.KeywordSet)), c.AvailableAsKeyword.EQ(psql.Arg(true))))
	}
	if f.Search != "" {
		where = append(where, psql.Raw(`(strpos("terms"."normalized_name", ?) > 0
 OR EXISTS(SELECT 1 FROM jsonb_array_elements_text("terms"."synonyms") v(value) WHERE strpos(lower(v.value COLLATE "pg_c_utf8"), ?) > 0)
 OR EXISTS(SELECT 1 FROM jsonb_each_text("terms"."labels") l WHERE strpos(lower(l.value COLLATE "pg_c_utf8"), ?) > 0))`, f.Search, f.Search, f.Search))
	}
	if f.Match != "" {
		either := []bob.Expression{}
		if f.In&taxonomy.MatchName != 0 {
			either = append(either, c.NormalizedName.EQ(psql.Arg(f.Match)))
		}
		if f.In&taxonomy.MatchLabels != 0 {
			either = append(either, psql.Raw(`EXISTS(SELECT 1 FROM jsonb_each_text("terms"."labels") l WHERE lower(l.value COLLATE "pg_c_utf8") = ?)`, f.Match))
		}
		if f.In&taxonomy.MatchSynonyms != 0 {
			either = append(either, psql.Raw(`EXISTS(SELECT 1 FROM jsonb_array_elements_text("terms"."synonyms") v(value) WHERE lower(v.value COLLATE "pg_c_utf8") = ?)`, f.Match))
		}
		where = append(where, psql.Or(either...))
	}
	if a := f.After; a != nil {
		where = append(where, psql.Group(c.SortOrder, c.NormalizedName, c.ID).GT(psql.ArgGroup(a.SortOrder, a.Name, a.ID)))
	}
	if f.AfterID != "" {
		where = append(where, c.ID.GT(psql.Arg(f.AfterID)))
	}
	return psql.And(where...)
}

func termOrder(f taxonomy.TermFilter) []bob.Expression {
	c := models.Terms.Columns
	switch f.Order {
	case taxonomy.OrderID:
		return []bob.Expression{c.ID}
	case taxonomy.OrderPrefix:
		return []bob.Expression{psql.Raw(`strpos("terms"."normalized_name", ?) <> 1`, f.Search), c.NormalizedName, c.ID}
	case taxonomy.OrderExact:
		return []bob.Expression{c.NormalizedName.NE(psql.Arg(f.Match)), c.Path}
	case taxonomy.OrderKeyword:
		return []bob.Expression{psql.Or(c.TermSetID.NE(psql.Arg(f.KeywordSet)), c.ParentID.IsNotNull(), c.NormalizedName.NE(psql.Arg(f.Match))), c.ID}
	}
	return []bob.Expression{c.SortOrder, c.NormalizedName, c.ID}
}

func termSetter(t taxonomy.Term) *models.TermSetter {
	return &models.TermSetter{
		UpdatedAt: omit.From(t.UpdatedAt), ParentID: nullable.String(t.ParentID), Name: omit.From(t.Name), NormalizedName: omit.From(t.NormalizedName),
		Labels: omit.From(types.NewJSON(t.Labels)), Synonyms: omit.From(types.NewJSON(t.Synonyms)), Description: omit.From(t.Description),
		Color: nullable.String(t.Color), SortOrder: omit.From(int64(t.SortOrder)), MergedIntoID: nullable.String(t.MergedIntoID),
		AvailableAsKeyword: omit.From(t.AvailableAsKeyword), Deprecated: omit.From(t.Deprecated), Version: omit.From(int64(t.Version)),
	}
}

func groupRecord(r *models.TermGroup) taxonomy.Group {
	return taxonomy.Group{ID: r.ID, Name: r.Name, Description: r.Description, System: r.IsSystem, Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
}

func setRecord(r *models.TermSet) taxonomy.Set {
	return taxonomy.Set{ID: r.ID, GroupID: r.GroupID, Key: r.Key, Name: r.Name, Description: r.Description, Open: r.IsOpen, Keywords: r.IsKeywords,
		Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
}

func termRecord(r *models.Term) taxonomy.Term {
	labels, synonyms := r.Labels.Val, r.Synonyms.Val
	if labels == nil {
		labels = map[string]string{}
	}
	if synonyms == nil {
		synonyms = []string{}
	}
	return taxonomy.Term{
		ID: r.ID, SetID: r.TermSetID, ParentID: r.ParentID.GetOrZero(), Name: r.Name, NormalizedName: r.NormalizedName,
		Description: r.Description, Color: r.Color.GetOrZero(), Path: r.Path, MergedIntoID: r.MergedIntoID.GetOrZero(),
		SortOrder: int(r.SortOrder), Labels: labels, Synonyms: synonyms, AvailableAsKeyword: r.AvailableAsKeyword, Deprecated: r.Deprecated,
		Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}
