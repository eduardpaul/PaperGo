package content

import (
	"context"
	"strings"

	"github.com/stephenafamo/scan"
	"papergo/internal/dal/access"
	"papergo/internal/dal/refs"
)

// KeywordCount is how many resources hold a keyword.
type KeywordCount struct {
	ID, Name string
	Count    int
}

// PopularKeywords counts the live items whose current content holds each
// keyword of the keywords set, or term available as a keyword, most used
// first.
func (r *Repo) PopularKeywords(ctx context.Context, keywordsSetID string, top int) ([]KeywordCount, error) {
	out := []KeywordCount{}
	err := r.each(ctx, func(rows scan.Rows) error {
		var c KeywordCount
		if err := rows.Scan(&c.ID, &c.Count); err != nil {
			return err
		}
		out = append(out, c)
		return nil
	}, `WITH usage AS (
 SELECT p.item_id,t.id AS keyword FROM item_surfaces p JOIN resources c ON c.id=p.container_id AND c.deleted_at IS NULL JOIN resources r ON r.id=p.item_id AND r.deleted_at IS NULL JOIN terms t ON t.id IN (`+r.surfaceKeywords()+`) WHERE p.surface=CASE WHEN c.publishing_enabled THEN 'head' ELSE 'published' END
 ) SELECT u.keyword,count(DISTINCT u.item_id) AS n FROM usage u JOIN terms t ON t.id=u.keyword WHERE NOT t.deprecated AND (t.term_set_id=? OR t.available_as_keyword) GROUP BY u.keyword ORDER BY n DESC,u.keyword LIMIT ?`, keywordsSetID, top)
	return out, err
}

// WorkspaceKeywords selects the keywords on what a reader reads in a
// workspace or one of its collections, counting each resource once per
// keyword: collections and workspaces by their keywords, items and
// folders on the surface the reader reads.
type WorkspaceKeywords struct {
	Archive                   refs.ArchiveSelection
	Reader                    access.Principal
	WorkspaceID, CollectionID string
	// Prefix starts the normalized names of the keywords.
	Prefix string
	// After pages by normalized name and ID.
	AfterName, AfterID string
	Limit              int
}

// WorkspaceKeywords counts the keywords w selects by normalized name.
func (r *Repo) WorkspaceKeywords(ctx context.Context, w WorkspaceKeywords) ([]KeywordCount, error) {
	scope, args := "r.workspace_id=?", []any{w.WorkspaceID}
	if w.CollectionID != "" {
		scope, args = "r.container_id=?", []any{w.CollectionID}
	}
	scope += " AND " + w.Archive.Predicate("r")
	readable, readArgs := r.acl.Resource("r.id", w.Reader, access.Read)
	selected, surfaceArgs := r.acl.Surface("r.id", w.Reader, "auto")
	query := `WITH usage AS (
 SELECT r.id,t.id AS keyword FROM resources r JOIN terms t ON t.id IN (` + r.resourceKeywords("r.id") + `)
 WHERE r.kind NOT IN ('item','folder') AND r.deleted_at IS NULL AND ` + scope + ` AND ` + readable + `
 UNION
 SELECT r.id,t.id FROM resources r JOIN item_surfaces p ON p.item_id=r.id JOIN terms t ON t.id IN (` + r.surfaceKeywords() + `)
 WHERE r.deleted_at IS NULL AND ` + scope + ` AND p.surface=` + selected + ` AND ` + readable + `
 ) SELECT t.id,t.name,count(*) FROM usage u JOIN terms t ON t.id=u.keyword WHERE TRUE`
	values := append(append([]any{}, args...), readArgs...)
	values = append(values, args...)
	values = append(values, surfaceArgs...)
	values = append(values, readArgs...)
	if w.Prefix != "" {
		query += " AND " + r.d.HasPrefix("t.normalized_name", "?")
		values = append(values, strings.ToLower(w.Prefix))
	}
	if w.AfterID != "" {
		query += " AND (t.normalized_name,t.id)>(?,?)"
		values = append(values, w.AfterName, w.AfterID)
	}
	query += " GROUP BY t.id,t.name,t.normalized_name ORDER BY t.normalized_name,t.id"
	if w.Limit > 0 {
		query += " LIMIT ?"
		values = append(values, w.Limit)
	}
	out := []KeywordCount{}
	err := r.each(ctx, func(rows scan.Rows) error {
		var c KeywordCount
		if err := rows.Scan(&c.ID, &c.Name, &c.Count); err != nil {
			return err
		}
		out = append(out, c)
		return nil
	}, query, values...)
	return out, err
}

// CurrentFiles pages, by ID, the live items other than except whose head
// or published revision holds a file with these bytes.
func (r *Repo) CurrentFiles(ctx context.Context, sha256, except, after string, limit int) ([]string, error) {
	return r.strings(ctx, `SELECT DISTINCT r.id FROM blobs b
JOIN item_revisions v ON v.blob_id=b.id JOIN resources r ON r.id=v.item_id
WHERE b.sha256=? AND r.deleted_at IS NULL AND r.id<>? AND r.id>?
AND (v.id=r.head_revision_id OR v.id=r.published_revision_id) ORDER BY r.id LIMIT ?`, sha256, except, after, limit)
}
