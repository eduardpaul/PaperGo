package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"papergo/internal/dal/access"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/refs"
)

// Browse selects the live resources a reader lists below a parent, in a
// workspace or, with neither, the workspaces, in Order.
type Browse struct {
	Archive refs.ArchiveSelection
	// GlobalItems selects items in every collection, or in WorkspaceID.
	GlobalItems                             bool
	CollectionID, ContentTypeID, ArchivedBy string
	ArchivedFrom, ArchivedTo                time.Time
	// BeforeArchivedAt and BeforeArchivedID page NewestArchived: items
	// archived before BeforeArchivedAt, or then with a smaller ID.
	BeforeArchivedAt      time.Time
	BeforeArchivedID      string
	Order                 BrowseOrder
	Reader                access.Principal
	ParentID, WorkspaceID string
	Kind                  refs.ResourceKind
	// Surface is head, published or auto: the surface the reader reads,
	// auto being the head when they may read drafts.
	Surface string
	// Keyword is a canonical keyword term ID.
	Keyword string
	Search  *SearchMatch
	Field   *FieldMatch
	AfterID string
	Limit   int
}

// BrowseOrder sorts browsed resources.
type BrowseOrder uint8

const (
	BrowseByID BrowseOrder = iota
	// NewestArchived sorts archived items by archive time, newest first,
	// then by ID descending.
	NewestArchived
)

// SearchMatch matches workspaces and collections whose folded name
// contains Name, and the items and folders a text matcher found on each
// surface, or with ByName those whose name on the surface read contains
// Name.
type SearchMatch struct {
	Name            string
	Head, Published []string
	ByName          bool
}

// ValueColumn is the column an indexed field value is compared in.
type ValueColumn string

const (
	ValueText    ValueColumn = "value_text"
	ValueInteger ValueColumn = "value_integer"
	ValueBoolean ValueColumn = "value_boolean"
)

// Op compares a field value.
type Op string

const (
	OpEQ  Op = "="
	OpGT  Op = ">"
	OpGTE Op = ">="
	OpLT  Op = "<"
	OpLTE Op = "<="
)

func (o Op) Valid() bool {
	switch o {
	case OpEQ, OpGT, OpGTE, OpLT, OpLTE:
		return true
	}
	return false
}

// FieldMatch compares an indexed field of a collection with a value on the
// surface the reader reads.
type FieldMatch struct {
	ContainerID, Key string
	Type             catalog.FieldType
	Scale            int
	Column           ValueColumn
	Op               Op
	Value            any
}

// candidate is an index-only superset of the IDs a condition can match. A
// small set drives the query, so per-row access checks run only on
// candidates; a large one is left to the ID-ordered scan, which fills a
// page after few rows.
type candidate struct {
	query string
	args  []any
}

// CandidateDriveLimit is the largest candidate set that drives a query. A
// negative limit always scans, which tests compare plans with.
var CandidateDriveLimit = 1000

// selective counts at most CandidateDriveLimit+1 candidates, so it stays
// cheap for broad conditions.
func (r *Repo) selective(ctx context.Context, c candidate) (bool, error) {
	var n int
	err := r.scan(ctx, []any{&n}, "SELECT count(*) FROM ("+c.query+" LIMIT ?) AS candidates", append(append([]any{}, c.args...), CandidateDriveLimit+1)...)
	return n <= CandidateDriveLimit, err
}

// keywordValue tests that the field value aliased f holds a keyword: a
// keywords field value, or a term field value that is a keyword.
const keywordValue = "(f.field_type='keywords' OR f.field_type='term' AND f.value_text IN (SELECT t.id FROM terms t JOIN term_sets ts ON ts.id=t.term_set_id WHERE ts.is_keywords OR t.available_as_keyword))"

// surfaceKeywordRows selects the keyword sources of the item surfaces in
// the table scope, of columns owner and surface, as rows of owner, term
// and lineage. Name scope in a WITH clause: it is read twice.
func surfaceKeywordRows(scope string) string {
	return "SELECT o.owner,k.keyword AS term,TRUE AS lineage FROM " + scope + " o JOIN item_surface_keywords k ON k.surface_id=o.surface" +
		" UNION ALL SELECT o.owner,f.value_text,f.applies_parents FROM " + scope + " o JOIN field_values f ON f.surface_id=o.surface WHERE " + keywordValue
}

// keywordTerms selects from the terms whose holders take on the canonical
// term the expression keyword names, x: as t, the term, one below it as c,
// or one merged into either, joined with holders. Terms below x apply it
// only where their source applies parents. x and its subtree are one range
// of the path index, so holders are found from the keyword.
func (r *Repo) keywordTerms(holders, keyword string) string {
	return "terms x JOIN terms c ON c.id=x.id OR " + r.d.Below("c.path", "x.path") + " JOIN terms t ON t.id=c.id OR t.merged_into_id=c.id " + holders + " WHERE x.id=" + keyword
}

// keywordSurfaces selects the item surfaces that the canonical term the
// expression keyword names applies to: their own keywords hold it or a
// term below it, or a keyword field value holds it, or a term below it
// where the field applies parents. keyword appears twice.
func (r *Repo) keywordSurfaces(keyword string) string {
	return "SELECT k.surface_id FROM " + r.keywordTerms("JOIN item_surface_keywords k ON k.keyword=t.id", keyword) +
		" UNION ALL SELECT f.surface_id FROM " + r.keywordTerms("JOIN field_values f ON f.value_text=t.id AND f.field_type IN ('term','keywords')", keyword) + " AND (c.id=x.id OR f.applies_parents) AND " + keywordValue
}

// keywordResources selects the resources whose own keywords hold the
// canonical term the expression keyword names, or a term below it.
func (r *Repo) keywordResources(keyword string) string {
	return "SELECT k.resource_id FROM " + r.keywordTerms("JOIN resource_keywords k ON k.keyword=t.id", keyword)
}

// appliedKeywords selects each owner and canonical keyword that rows of
// owner, term and lineage apply: a merged term counts as the term it was
// merged into, and that term's path lists the ancestors a row applies. An
// owner may repeat a keyword. Rows are joined, not correlated per owner, so
// each term's ancestors are listed once per row.
func (r *Repo) appliedKeywords(rows string) string {
	return "SELECT s.owner,a.value AS keyword FROM (" + rows + ") s JOIN terms t ON t.id=s.term JOIN terms c ON c.id=coalesce(t.merged_into_id,t.id) CROSS JOIN " + r.d.PathIDs("c.path", "a") + " WHERE s.lineage OR a.value=c.id"
}

// family selects a term, its descendants and the terms merged into those:
// the terms whose holders a parent-applying source tags with the term.
func (r *Repo) family() string {
	subtree := "SELECT t.id FROM terms x JOIN terms t ON " + r.d.Below("t.path", "x.path") + " WHERE x.id=?"
	return subtree + " UNION ALL SELECT m.id FROM terms m WHERE m.merged_into_id IN (" + subtree + ")"
}

// BrowseIDs returns the IDs of the resources b selects.
func (r *Repo) BrowseIDs(ctx context.Context, b Browse) ([]string, error) {
	if b.Order == NewestArchived && b.Archive != refs.ArchiveArchived {
		return nil, fmt.Errorf("archive order needs archived resources, not %q", b.Archive)
	}
	if !b.BeforeArchivedAt.IsZero() && b.Order != NewestArchived {
		return nil, errors.New("an archive cursor needs archive order")
	}
	where := []string{"r.deleted_at IS NULL"}
	args := []any{}
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	switch {
	case b.GlobalItems:
		add("r.kind='item'")
		if b.WorkspaceID != "" {
			add("r.workspace_id=?", b.WorkspaceID)
		}
	case b.ParentID != "":
		add("r.parent_id=?", b.ParentID)
	case b.WorkspaceID != "":
		add("r.workspace_id=?", b.WorkspaceID)
	default:
		add("r.kind='workspace'")
	}
	add(b.Archive.Predicate("r"))
	if b.CollectionID != "" {
		add("r.container_id=?", b.CollectionID)
	}
	if b.ContentTypeID != "" {
		add("r.content_type_id=?", b.ContentTypeID)
	}
	if b.ArchivedBy != "" {
		add("r.archived_by=?", b.ArchivedBy)
	}
	if !b.ArchivedFrom.IsZero() {
		add("r.archived_at>=?", b.ArchivedFrom.UTC())
	}
	if !b.ArchivedTo.IsZero() {
		add("r.archived_at<=?", b.ArchivedTo.UTC())
	}
	if !b.BeforeArchivedAt.IsZero() {
		add("(r.archived_at,r.id)<(?,?)", b.BeforeArchivedAt.UTC(), b.BeforeArchivedID)
	}
	if b.Kind != "" {
		add("r.kind=?", string(b.Kind))
	}
	if b.AfterID != "" {
		add("r.id>?", b.AfterID)
	}
	candidates := []candidate{}
	if b.Field != nil {
		if !b.Field.Op.Valid() {
			return nil, fmt.Errorf("invalid field comparison %q", b.Field.Op)
		}
		// Either surface may match; the exact condition checks the selected one.
		candidates = append(candidates, candidate{
			`SELECT f.item_id FROM field_values f WHERE f.container_id=? AND f.surface IN ('head','published') AND f.field_key=? AND f.` + string(b.Field.Column) + string(b.Field.Op) + `?`,
			[]any{b.Field.ContainerID, b.Field.Key, b.Field.Value},
		})
	}
	if b.Keyword != "" {
		// Holders of a descendant apply the keyword when they apply parents.
		family := r.family()
		candidates = append(candidates, candidate{
			"SELECT resource_id AS id FROM resource_keywords WHERE keyword IN (" + family + ") UNION ALL SELECT p.item_id FROM item_surface_keywords k JOIN item_surfaces p ON p.id=k.surface_id WHERE k.keyword IN (" + family + ") UNION ALL SELECT item_id FROM field_values WHERE field_type IN ('keywords','term') AND value_text IN (" + family + ")",
			[]any{b.Keyword, b.Keyword, b.Keyword, b.Keyword, b.Keyword, b.Keyword},
		})
	}
	var head, published string
	switch {
	case b.Search != nil && b.Search.ByName:
		candidates = append(candidates, candidate{
			"SELECT item_id FROM item_surfaces WHERE " + r.d.Contains("folded_name", "?") + " UNION ALL SELECT id FROM resources WHERE kind IN ('workspace','list','library') AND " + r.d.Contains(r.d.Fold("name"), "?"),
			[]any{b.Search.Name, b.Search.Name},
		})
	case b.Search != nil:
		h, _ := json.Marshal(nonNil(b.Search.Head))
		p, _ := json.Marshal(nonNil(b.Search.Published))
		head, published = string(h), string(p)
		candidates = append(candidates, candidate{
			"SELECT value FROM " + r.d.Elements(r.d.JSONArg(), "h") + " UNION ALL SELECT value FROM " + r.d.Elements(r.d.JSONArg(), "p") + " UNION ALL SELECT id FROM resources WHERE kind IN ('workspace','list','library') AND " + r.d.Contains(r.d.Fold("name"), "?"),
			[]any{head, published, b.Search.Name},
		})
	}
	for _, c := range candidates {
		small, err := r.selective(ctx, c)
		if err != nil {
			return nil, err
		}
		if small {
			add("r.id IN ("+c.query+")", c.args...)
		}
	}
	action := access.Read
	if b.Surface == "head" {
		action = access.ReadDraft
	}
	allowed, allowedArgs := r.acl.Rows("r", b.Reader, action)
	add(allowed, allowedArgs...)
	visible, visibleArgs := r.acl.Visible("r.id", b.Reader, b.Surface)
	add(visible, visibleArgs...)
	if b.Keyword != "" {
		selected, selectedArgs := r.acl.Surface("r.id", b.Reader, b.Surface)
		cond := "(r.kind NOT IN ('item','folder') AND r.id IN (" + r.keywordResources("?") + ") OR r.kind IN ('item','folder') AND EXISTS (SELECT 1 FROM item_surfaces p WHERE p.item_id=r.id AND p.surface=" + selected + " AND p.id IN (" + r.keywordSurfaces("?") + ")))"
		a := append([]any{b.Keyword}, selectedArgs...)
		add(cond, append(a, b.Keyword, b.Keyword)...)
	}
	if b.Search != nil {
		selected, selectedArgs := r.acl.Surface("r.id", b.Reader, b.Surface)
		// An entry matches on the surface the reader reads.
		if b.Search.ByName {
			cond := "(r.kind NOT IN ('item','folder') AND " + r.d.Contains(r.d.Fold("r.name"), "?") + " OR r.kind IN ('item','folder') AND EXISTS(SELECT 1 FROM item_surfaces p WHERE p.item_id=r.id AND p.surface=" + selected + " AND " + r.d.Contains("p.folded_name", "?") + "))"
			a := append([]any{b.Search.Name}, selectedArgs...)
			add(cond, append(a, b.Search.Name)...)
		} else {
			cond := "(r.kind NOT IN ('item','folder') AND " + r.d.Contains(r.d.Fold("r.name"), "?") + " OR r.kind IN ('item','folder') AND r.id IN (SELECT value FROM " + r.d.Elements("CASE "+selected+" WHEN 'head' THEN "+r.d.JSONArg()+" ELSE "+r.d.JSONArg()+" END", "matched") + "))"
			a := append([]any{b.Search.Name}, selectedArgs...)
			add(cond, append(a, head, published)...)
		}
	}
	if f := b.Field; f != nil {
		selected, selectedArgs := r.acl.Surface("r.id", b.Reader, b.Surface)
		cond := `EXISTS (SELECT 1 FROM field_values f WHERE f.container_id=? AND f.item_id=r.id AND f.surface=` + selected + ` AND f.field_key=? AND f.field_type=? AND f.scale=? AND f.` + string(f.Column) + string(f.Op) + `?)`
		a := append([]any{f.ContainerID}, selectedArgs...)
		add(cond, append(a, f.Key, string(f.Type), f.Scale, f.Value)...)
	}
	order := "r.id"
	if b.Order == NewestArchived {
		order = "r.archived_at DESC,r.id DESC"
	}
	query := "SELECT r.id FROM resources r WHERE " + strings.Join(where, " AND ") + " ORDER BY " + order
	if b.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, b.Limit)
	}
	return r.strings(ctx, query, args...)
}

func nonNil(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
