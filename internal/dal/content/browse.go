package content

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"papergo/internal/dal/access"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/refs"
)

// Browse selects the live resources a reader lists below a parent, in a
// workspace or, with neither, the workspaces, in ID order.
type Browse struct {
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

// SearchMatch matches workspaces and collections whose name contains Name,
// and the items and folders the search store found on each surface.
type SearchMatch struct {
	Name            string
	Head, Published []string
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

// SurfaceKeywords selects the keywords of the item surface aliased p: its
// own keywords and its keyword and keyword-term field values.
const SurfaceKeywords = "SELECT keyword FROM item_surface_keywords WHERE surface_id=p.id UNION SELECT value_text FROM field_values WHERE surface_id=p.id AND (field_type='keywords' OR field_type='term' AND value_text IN (SELECT t.id FROM terms t JOIN term_sets ts ON ts.id=t.term_set_id WHERE ts.is_keywords OR t.available_as_keyword))"

// BrowseIDs returns the IDs of the resources b selects.
func (r *Repo) BrowseIDs(ctx context.Context, b Browse) ([]string, error) {
	where := []string{"r.deleted_at IS NULL"}
	args := []any{}
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	switch {
	case b.ParentID != "":
		add("r.parent_id=?", b.ParentID)
	case b.WorkspaceID != "":
		add("r.workspace_id=?", b.WorkspaceID)
	default:
		add("r.kind='workspace'")
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
		aliases := "SELECT id FROM terms WHERE id=? OR merged_into_id=?"
		candidates = append(candidates, candidate{
			"SELECT resource_id AS id FROM resource_keywords WHERE keyword IN (" + aliases + ") UNION ALL SELECT p.item_id FROM item_surface_keywords k JOIN item_surfaces p ON p.id=k.surface_id WHERE k.keyword IN (" + aliases + ") UNION ALL SELECT item_id FROM field_values WHERE field_type IN ('keywords','term') AND value_text IN (" + aliases + ")",
			[]any{b.Keyword, b.Keyword, b.Keyword, b.Keyword, b.Keyword, b.Keyword},
		})
	}
	var head, published string
	if b.Search != nil {
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
		cond := "(r.kind NOT IN ('item','folder') AND EXISTS(SELECT 1 FROM resource_keywords k JOIN terms kt ON kt.id=k.keyword WHERE k.resource_id=r.id AND coalesce(kt.merged_into_id,kt.id)=?) OR r.kind IN ('item','folder') AND EXISTS (SELECT 1 FROM item_surfaces p WHERE p.item_id=r.id AND p.surface=" + selected + " AND EXISTS(SELECT 1 FROM (" + SurfaceKeywords + ") k JOIN terms kt ON kt.id=k.keyword WHERE coalesce(kt.merged_into_id,kt.id)=?)))"
		a := append([]any{b.Keyword}, selectedArgs...)
		add(cond, append(a, b.Keyword)...)
	}
	if b.Search != nil {
		selected, selectedArgs := r.acl.Surface("r.id", b.Reader, b.Surface)
		// An entry matches on the surface the reader reads.
		cond := "(r.kind NOT IN ('item','folder') AND " + r.d.Contains(r.d.Fold("r.name"), "?") + " OR r.kind IN ('item','folder') AND r.id IN (SELECT value FROM " + r.d.Elements("CASE "+selected+" WHEN 'head' THEN "+r.d.JSONArg()+" ELSE "+r.d.JSONArg()+" END", "matched") + "))"
		a := append([]any{b.Search.Name}, selectedArgs...)
		add(cond, append(a, head, published)...)
	}
	if f := b.Field; f != nil {
		selected, selectedArgs := r.acl.Surface("r.id", b.Reader, b.Surface)
		cond := `EXISTS (SELECT 1 FROM field_values f WHERE f.container_id=? AND f.item_id=r.id AND f.surface=` + selected + ` AND f.field_key=? AND f.field_type=? AND f.scale=? AND f.` + string(f.Column) + string(f.Op) + `?)`
		a := append([]any{f.ContainerID}, selectedArgs...)
		add(cond, append(a, f.Key, string(f.Type), f.Scale, f.Value)...)
	}
	query := "SELECT r.id FROM resources r WHERE " + strings.Join(where, " AND ") + " ORDER BY r.id"
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
