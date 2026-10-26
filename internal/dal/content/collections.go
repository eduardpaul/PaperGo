package content

import (
	"context"
	"strings"

	"papergo/internal/dal/access"
	"papergo/internal/dal/refs"
)

// Collections selects the live lists and libraries a reader may read, in
// ID order. Names, Templates and ContentTypes each match, ignoring case,
// a collection's name, an adopted template key, or the key or name of a
// content type it accepts. Empty fields do not filter.
type Collections struct {
	Reader                         access.Principal
	IDs                            []string
	Kind                           refs.ResourceKind
	WorkspaceID                    string
	Names, Templates, ContentTypes []string
	Limit                          int
}

// CollectionIDs returns the IDs of the collections c selects.
func (r *Repo) CollectionIDs(ctx context.Context, c Collections) ([]string, error) {
	where := []string{"r.kind IN ('list','library')", "r.deleted_at IS NULL"}
	args := []any{}
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	allowed, allowedArgs := r.acl.Rows("r", c.Reader, access.Read)
	add(allowed, allowedArgs...)
	if c.IDs != nil {
		if len(c.IDs) == 0 {
			return []string{}, nil
		}
		args := make([]any, len(c.IDs))
		for i, id := range c.IDs {
			args[i] = id
		}
		add("r.id IN ("+marks(len(args))+")", args...)
	}
	if c.Kind != "" {
		add("r.kind=?", string(c.Kind))
	}
	if c.WorkspaceID != "" {
		add("r.workspace_id=?", c.WorkspaceID)
	}
	if len(c.Names) > 0 {
		text, a := r.named(c.Names, "r.name")
		add(text, a...)
	}
	if len(c.Templates) > 0 {
		text, a := r.named(c.Templates, "tk.value")
		add("EXISTS(SELECT 1 FROM "+r.d.Elements("r.template_keys", "tk")+" WHERE "+text+")", a...)
	}
	if len(c.ContentTypes) > 0 {
		key, keyArgs := r.named(c.ContentTypes, "ct.key")
		name, nameArgs := r.named(c.ContentTypes, "ct.name")
		add("EXISTS(SELECT 1 FROM content_types lt JOIN content_type_ancestors a ON a.content_type_id=lt.id JOIN content_types ct ON ct.id=a.ancestor_id AND ct.collection_id IS NULL WHERE lt.collection_id=r.id AND lt.attached AND ("+key+" OR "+name+"))", append(keyArgs, nameArgs...)...)
	}
	query := "SELECT r.id FROM resources r WHERE " + strings.Join(where, " AND ") + " ORDER BY r.id"
	if c.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, c.Limit)
	}
	return r.strings(ctx, query, args...)
}

// named matches column against values, ignoring case.
func (r *Repo) named(values []string, column string) (string, []any) {
	marks := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		marks[i] = "?"
		args[i] = strings.ToLower(v)
	}
	return r.d.Fold(column) + " IN (" + strings.Join(marks, ",") + ")", args
}
