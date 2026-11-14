package content

import (
	"encoding/json"
	"strings"

	"context"
)

// TermUsage counts what names a term: deleting the term would leave each
// of these with an ID that names nothing.
type TermUsage struct {
	// Items are the live items and folders whose head or published content
	// holds the term, as a keyword or in a field.
	Items int
	// DeletedItems are the items and folders in a recycle bin whose head or
	// published content holds the term; restoring one brings that content
	// back as it is.
	DeletedItems int
	// Workspaces and Collections, deleted ones included, carry the term as
	// a keyword.
	Workspaces, Collections int
	// Fields give the term as their default.
	Fields int
	// ContentTypes give the term as a field's default or test for it in a
	// rule.
	ContentTypes int
	// Templates are collection templates whose fields give the term as a
	// default.
	Templates int
	// ListViews filter on the term.
	ListViews int
	// SmartFolders select or filter on the term.
	SmartFolders int
	// Workflows are the workflows, not deleted, whose current version names
	// the term in a trigger or a form.
	Workflows int
	// Relationships have the type the term names.
	Relationships int
}

// TermUsage counts what names any of ids, which are a term and the terms
// merged into it. Keyword links and relationship types are found by their
// indexes; everything else holds term IDs inside JSON documents, which are
// read as text for the quoted ID, as TermHolders reads surfaces.
func (r *Repo) TermUsage(ctx context.Context, ids []string) (TermUsage, error) {
	var out TermUsage
	if len(ids) == 0 {
		return out, nil
	}
	list, _ := json.Marshal(ids)
	var args []any
	in := func(alias string) string {
		args = append(args, string(list))
		return "(SELECT value FROM " + r.d.Elements(r.d.JSONArg(), alias) + ")"
	}
	// names tests that one of the JSON columns holds one of the IDs.
	names := func(columns ...string) string {
		tests := []string{}
		for _, column := range columns {
			for _, id := range ids {
				tests = append(tests, r.d.Contains(r.d.Text(column), "?"))
				args = append(args, `"`+id+`"`)
			}
		}
		return "(" + strings.Join(tests, " OR ") + ")"
	}
	// Each count is written, and so takes its arguments, in query order.
	counts := []string{
		"SELECT count(DISTINCT p.item_id) FROM item_surfaces p WHERE p.id IN (SELECT k.surface_id FROM item_surface_keywords k WHERE k.keyword IN " + in("held") + ") OR " + names("p.payload"),
		"SELECT count(*) FROM resources r WHERE r.deleted_at IS NOT NULL AND r.kind IN ('item','folder') AND EXISTS (SELECT 1 FROM item_revisions v WHERE (v.id=r.head_revision_id OR v.id=r.published_revision_id) AND " + names("v.keywords", "v.payload") + ")",
		"SELECT count(*) FROM resources r WHERE r.kind='workspace' AND " + names("r.keywords"),
		"SELECT count(*) FROM resources r WHERE r.kind IN ('list','library') AND " + names("r.keywords"),
		"SELECT count(*) FROM field_definitions f WHERE " + names("f.options"),
		"SELECT count(*) FROM content_types c WHERE " + names("c.field_settings", "c.rules"),
		"SELECT count(*) FROM schema_templates s WHERE " + names("s.definition"),
		"SELECT count(*) FROM list_views l WHERE " + names(`l."query"`, "l.settings"),
		"SELECT count(*) FROM smart_folders s WHERE " + names("s.definition"),
		"SELECT count(*) FROM workflows w JOIN workflow_versions v ON v.workflow_id=w.id AND v.number=w.current_version WHERE w.deleted_at IS NULL AND " + names("v.definition"),
		"SELECT count(*) FROM relationships l WHERE l.type_id IN " + in("typed"),
	}
	err := r.scan(ctx, []any{&out.Items, &out.DeletedItems, &out.Workspaces, &out.Collections, &out.Fields, &out.ContentTypes, &out.Templates, &out.ListViews, &out.SmartFolders, &out.Workflows, &out.Relationships},
		"SELECT ("+strings.Join(counts, "), (")+")", args...)
	return out, err
}
