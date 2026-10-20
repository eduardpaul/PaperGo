package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dialect"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/access"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
)

func (q Queries) FindResource(ctx context.Context, id string) (resources.Resource, error) {
	row, err := models.FindResource(ctx, q.exec, id)
	if err != nil {
		return resources.Resource{}, dberr.Translate(err)
	}
	return resourceRecord(row)
}

// resourceWhere is the selection of f, without order or limit.
func resourceWhere(f resources.Filter) []bob.Mod[*dialect.SelectQuery] {
	c := models.Resources.Columns
	var mods []bob.Mod[*dialect.SelectQuery]
	where := func(e bob.Expression) { mods = append(mods, sm.Where(e)) }
	if f.IDs != nil {
		where(c.ID.In(sqlite.Arg(anys(f.IDs)...)))
	}
	if len(f.Kinds) > 0 {
		kinds := make([]any, len(f.Kinds))
		for i, k := range f.Kinds {
			kinds[i] = string(k)
		}
		where(c.Kind.In(sqlite.Arg(kinds...)))
	}
	for _, eq := range []struct{ column, value string }{{"workspace_id", f.WorkspaceID}, {"parent_id", f.ParentID}, {"container_id", f.ContainerID},
		{"content_type_id", f.ContentTypeID}, {"personal_owner_id", f.PersonalOwnerID}, {"system_key", string(f.SystemKey)}, {"name", f.Name}, {"name_key", f.NameKey}} {
		if eq.value != "" {
			where(sqlite.Quote("resources", eq.column).EQ(sqlite.Arg(eq.value)))
		}
	}
	if f.FoldedName != "" {
		where(sqlite.Raw(`unicode_lower("resources"."name") = ?`, f.FoldedName))
	}
	if f.PublishedName != "" {
		where(sqlite.Raw(`("resources"."name_key" = ? OR "resources"."id" IN (SELECT p.item_id FROM item_surfaces p WHERE p.surface = 'published' AND unicode_lower(p.name) = ?))`, f.PublishedName, f.PublishedName))
	}
	if f.ExceptID != "" {
		where(c.ID.NE(sqlite.Arg(f.ExceptID)))
	}
	if f.ScopeID != nil {
		if *f.ScopeID == "" {
			where(c.ScopeID.IsNull())
		} else {
			where(c.ScopeID.EQ(sqlite.Arg(*f.ScopeID)))
		}
	}
	if f.Below != "" {
		where(sqlite.Raw(`"resources"."id" IN (WITH RECURSIVE subtree(id) AS (SELECT ? UNION ALL SELECT c.id FROM resources c JOIN subtree ON c.parent_id = subtree.id) SELECT id FROM subtree)`, f.Below))
	}
	switch f.State {
	case resources.Live:
		where(c.DeletedAt.IsNull())
	case resources.Deleted:
		where(c.DeletedAt.IsNotNull())
	}
	if f.DeletedAt != nil {
		where(c.DeletedAt.EQ(sqlite.Arg(*f.DeletedAt)))
	}
	if f.WithHead {
		where(c.HeadRevisionID.IsNotNull())
	}
	if f.Unpublished {
		where(sqlite.Raw(`("resources"."published_revision_id" IS NULL OR "resources"."head_revision_id" IS NULL OR "resources"."published_revision_id" <> "resources"."head_revision_id")`))
	}
	if f.Publishing != nil {
		where(c.PublishingEnabled.EQ(sqlite.Arg(*f.Publishing)))
	}
	if f.Reader != nil {
		allowed, args := access.SQLite.Rows(`"resources"`, f.Reader.Principal, f.Reader.Action)
		where(sqlite.Raw(allowed, args...))
		if f.Reader.Surface != "" {
			visible, args := access.SQLite.Visible(`"resources"."id"`, f.Reader.Principal, f.Reader.Surface)
			where(sqlite.Raw(visible, args...))
		}
	}
	if f.DeletionRoots {
		where(sqlite.Raw(`NOT EXISTS (SELECT 1 FROM resources folder WHERE folder.id = "resources"."parent_id" AND folder.deleted_at = "resources"."deleted_at")`))
	}
	if f.BeforeDeletion != nil {
		where(sqlite.Or(c.DeletedAt.LT(sqlite.Arg(f.BeforeDeletion.At)), sqlite.And(c.DeletedAt.EQ(sqlite.Arg(f.BeforeDeletion.At)), c.ID.LT(sqlite.Arg(f.BeforeDeletion.ID)))))
	}
	if f.AfterID != "" {
		where(c.ID.GT(sqlite.Arg(f.AfterID)))
	}
	return mods
}

// resourceOrder sorts by f.Order.
func resourceOrder(f resources.Filter) []bob.Mod[*dialect.SelectQuery] {
	c := models.Resources.Columns
	if f.Order == resources.NewestDeletion {
		return []bob.Mod[*dialect.SelectQuery]{sm.OrderBy(c.DeletedAt).Desc(), sm.OrderBy(c.ID).Desc()}
	}
	return []bob.Mod[*dialect.SelectQuery]{sm.OrderBy(c.ID)}
}

func (q Queries) FindResources(ctx context.Context, f resources.Filter) ([]resources.Resource, error) {
	query := models.Resources.Query(resourceWhere(f)...)
	if f.Summary {
		query.Apply(sm.Columns(models.Resources.Columns.Except("values")))
	}
	query.Apply(resourceOrder(f)...)
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]resources.Resource, len(rows))
	for i, row := range rows {
		if out[i], err = resourceRecord(row); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (q Queries) ResourceIDs(ctx context.Context, f resources.Filter) ([]string, error) {
	c := models.Resources.Columns
	mods := append([]bob.Mod[*dialect.SelectQuery]{sm.Columns(c.ID), sm.From(models.Resources.NameAsExpr())}, resourceWhere(f)...)
	mods = append(mods, resourceOrder(f)...)
	if f.Limit > 0 {
		mods = append(mods, sm.Limit(f.Limit))
	}
	ids, err := bob.All(ctx, q.exec, sqlite.Select(mods...), scan.SingleColumnMapper[string])
	return ids, dberr.Translate(err)
}

func (q Queries) CountResources(ctx context.Context, f resources.Filter) (int, error) {
	n, err := models.Resources.Query(resourceWhere(f)...).Count(ctx, q.exec)
	return int(n), dberr.Translate(err)
}

func (q Queries) InsertResource(ctx context.Context, r resources.Resource, values json.RawMessage) error {
	set := &models.ResourceSetter{
		ID: omit.From(r.ID), CreatedAt: omit.From(r.CreatedAt), WorkspaceID: omit.From(r.WorkspaceID), PersonalOwnerID: omitnull.FromPtr(r.PersonalOwnerID),
		Kind: omit.From(string(r.Kind)), Name: omit.From(r.Name), DefaultCurrency: omit.From(r.DefaultCurrency), Description: omit.From(r.Description),
		AllowFolders: omit.From(r.AllowFolders), Keywords: omit.From(types.NewJSON(r.Keywords)), Values: omit.From(types.NewJSON(values)),
		InheritPermissions: omit.From(r.InheritPermissions), Version: omit.From(int64(r.Version)), UpdatedAt: omit.From(r.UpdatedAt),
		ScopeID: omitnull.FromPtr(r.ScopeID), ParentID: omitnull.FromPtr(r.ParentID), ContainerID: omitnull.FromPtr(r.ContainerID),
		NextRevisionNumber: omit.From(int64(r.NextRevisionNumber)), PublishingEnabled: omit.From(r.PublishingEnabled), Versioning: omit.From(string(r.Versioning)),
		MaxVersions: omit.From(int64(r.MaxVersions)), WebdavEnabled: omit.From(r.WebdavEnabled), DuplicatePolicy: omit.From(string(r.DuplicatePolicy)),
		NameKey: omitnull.FromPtr(r.NameKey), CreatedBy: omit.From(r.CreatedBy), UpdatedBy: omit.From(r.UpdatedBy),
		ContentTypeID: omitnull.FromPtr(r.ContentTypeID), TemplateKeys: omit.From(types.NewJSON(r.TemplateKeys)),
	}
	if r.SystemKey != nil {
		set.SystemKey = omitnull.From(string(*r.SystemKey))
	}
	_, err := models.Resources.Insert(set).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateResource(ctx context.Context, id string, version int, s resources.Set) (bool, error) {
	set := resourceSetter(s)
	w := models.UpdateWhere.Resources
	mods := []bob.Mod[*dialect.UpdateQuery]{set.UpdateMod(), w.ID.EQ(id)}
	if version > 0 {
		mods = append(mods, w.Version.EQ(int64(version)))
	}
	n, err := models.Resources.Update(mods...).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

// resourceSetter converts the columns a write changes.
func resourceSetter(s resources.Set) *models.ResourceSetter {
	set := &models.ResourceSetter{UpdatedAt: omit.From(s.UpdatedAt)}
	reference := func(v *string) omitnull.Val[string] {
		if v == nil {
			return omitnull.Val[string]{}
		}
		return nullable.String(*v)
	}
	set.WorkspaceID = omit.FromPtr(s.WorkspaceID)
	set.ParentID, set.ContainerID, set.ScopeID, set.ContentTypeID = reference(s.ParentID), reference(s.ContainerID), reference(s.ScopeID), reference(s.ContentTypeID)
	set.HeadRevisionID, set.PublishedRevisionID, set.SchemaHeadID = reference(s.HeadRevisionID), reference(s.PublishedRevisionID), reference(s.SchemaHeadID)
	set.NameKey = reference(s.NameKey)
	set.Name, set.Description, set.DefaultCurrency = omit.FromPtr(s.Name), omit.FromPtr(s.Description), omit.FromPtr(s.DefaultCurrency)
	set.AllowFolders, set.InheritPermissions = omit.FromPtr(s.AllowFolders), omit.FromPtr(s.InheritPermissions)
	set.PublishingEnabled, set.WebdavEnabled = omit.FromPtr(s.PublishingEnabled), omit.FromPtr(s.WebdavEnabled)
	set.UpdatedBy = omit.FromPtr(s.UpdatedBy)
	if s.Versioning != nil {
		set.Versioning = omit.From(string(*s.Versioning))
	}
	if s.DuplicatePolicy != nil {
		set.DuplicatePolicy = omit.From(string(*s.DuplicatePolicy))
	}
	if s.MaxVersions != nil {
		set.MaxVersions = omit.From(int64(*s.MaxVersions))
	}
	if s.NextRevisionNumber != nil {
		set.NextRevisionNumber = omit.From(int64(*s.NextRevisionNumber))
	}
	if s.Keywords != nil {
		set.Keywords = omit.From(types.NewJSON(*s.Keywords))
	}
	if s.TemplateKeys != nil {
		set.TemplateKeys = omit.From(types.NewJSON(*s.TemplateKeys))
	}
	if s.Values != nil {
		set.Values = omit.From(types.NewJSON(s.Values))
	}
	if s.Deleted != nil {
		if s.Deleted.At.IsZero() {
			set.DeletedAt, set.DeletedBy = omitnull.FromPtr[time.Time](nil), omitnull.FromPtr[string](nil)
		} else {
			set.DeletedAt, set.DeletedBy = omitnull.From(s.Deleted.At), nullable.String(s.Deleted.By)
		}
	}
	if s.Version > 0 {
		set.Version = omit.From(int64(s.Version))
	}
	return set
}

func (q Queries) DeleteResource(ctx context.Context, id string) error {
	_, err := models.Resources.Delete(models.DeleteWhere.Resources.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) Subtree(ctx context.Context, id string, f resources.TreeFilter) ([]string, error) {
	cond, args := "", []any{id}
	switch f.State {
	case resources.Live:
		cond += ` AND c.deleted_at IS NULL`
	case resources.Deleted:
		cond += ` AND c.deleted_at IS NOT NULL`
	}
	if f.DeletedAt != nil {
		cond += ` AND c.deleted_at = ?`
		args = append(args, *f.DeletedAt)
	}
	query := sqlite.RawQuery(`WITH RECURSIVE sub(id, depth) AS (SELECT ?, 0 UNION ALL SELECT c.id, sub.depth + 1 FROM resources c JOIN sub ON c.parent_id = sub.id WHERE TRUE`+cond+`) SELECT id FROM sub ORDER BY depth, id`, args...)
	ids, err := bob.All(ctx, q.exec, query, scan.SingleColumnMapper[string])
	return ids, dberr.Translate(err)
}

func (q Queries) IsAncestor(ctx context.Context, id, ancestor string) (bool, error) {
	query := sqlite.RawQuery(`WITH RECURSIVE up(id) AS (SELECT parent_id FROM resources WHERE id = ? UNION ALL SELECT r.parent_id FROM resources r JOIN up ON r.id = up.id WHERE r.parent_id IS NOT NULL) SELECT EXISTS(SELECT 1 FROM up WHERE id = ?)`, id, ancestor)
	ok, err := bob.One(ctx, q.exec, query, scan.SingleColumnMapper[bool])
	return ok, dberr.Translate(err)
}

func (q Queries) InheritScope(ctx context.Context, id, scopeID string) error {
	_, err := q.exec.ExecContext(ctx, `WITH RECURSIVE sub(id) AS (SELECT id FROM resources WHERE parent_id = ? AND inherit_permissions UNION ALL SELECT c.id FROM resources c JOIN sub ON c.parent_id = sub.id WHERE c.inherit_permissions) UPDATE resources SET scope_id = ? WHERE id IN (SELECT id FROM sub)`, id, scopeID)
	return dberr.Translate(err)
}

func (q Queries) FolderHeight(ctx context.Context, id string, limit int) (int, error) {
	query := sqlite.RawQuery(`WITH RECURSIVE sub(id, kind, depth) AS (SELECT id, kind, 1 FROM resources WHERE id = ? UNION ALL SELECT c.id, c.kind, sub.depth + 1 FROM resources c JOIN sub ON c.parent_id = sub.id WHERE c.deleted_at IS NULL AND sub.depth <= ?) SELECT coalesce(max(CASE WHEN kind = 'folder' THEN depth ELSE depth - 1 END), 0) FROM sub`, id, limit)
	n, err := bob.One(ctx, q.exec, query, scan.SingleColumnMapper[int])
	return n, dberr.Translate(err)
}

func (q Queries) MoveItemRuns(ctx context.Context, itemID, workspaceID string) error {
	set := &models.WorkflowRunSetter{WorkspaceID: omit.From(workspaceID)}
	_, err := models.WorkflowRuns.Update(set.UpdateMod(), models.UpdateWhere.WorkflowRuns.ItemID.EQ(itemID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func resourceRecord(r *models.Resource) (resources.Resource, error) {
	out := resources.Resource{
		ID: r.ID, CreatedAt: r.CreatedAt.UTC(), WorkspaceID: r.WorkspaceID, PersonalOwnerID: r.PersonalOwnerID.Ptr(), ParentID: r.ParentID.Ptr(),
		ContainerID: r.ContainerID.Ptr(), ContentTypeID: r.ContentTypeID.Ptr(), Kind: refs.ResourceKind(r.Kind), Name: r.Name, Description: r.Description,
		DefaultCurrency: r.DefaultCurrency, AllowFolders: r.AllowFolders, Keywords: r.Keywords.Val, TemplateKeys: r.TemplateKeys.Val,
		InheritPermissions: r.InheritPermissions, Version: int(r.Version), HeadRevisionID: r.HeadRevisionID.Ptr(), PublishedRevisionID: r.PublishedRevisionID.Ptr(),
		SchemaHeadID: r.SchemaHeadID.Ptr(), NextRevisionNumber: int(r.NextRevisionNumber), PublishingEnabled: r.PublishingEnabled,
		Versioning: resources.Versioning(r.Versioning), MaxVersions: int(r.MaxVersions), WebdavEnabled: r.WebdavEnabled,
		DuplicatePolicy: resources.DuplicatePolicy(r.DuplicatePolicy), UpdatedAt: r.UpdatedAt.UTC(), CreatedBy: r.CreatedBy, UpdatedBy: r.UpdatedBy,
		ScopeID: r.ScopeID.Ptr(), NameKey: r.NameKey.Ptr(), DeletedAt: nullable.UTC(r.DeletedAt), DeletedBy: r.DeletedBy.Ptr(),
	}
	if key, ok := r.SystemKey.Get(); ok {
		k := resources.SystemKey(key)
		out.SystemKey = &k
	}
	if out.Keywords == nil {
		out.Keywords = []string{}
	}
	if out.TemplateKeys == nil {
		out.TemplateKeys = []string{}
	}
	if len(r.Values.Val) > 0 {
		if err := json.Unmarshal(r.Values.Val, &out.Values); err != nil {
			return resources.Resource{}, err
		}
	}
	if out.Values == nil {
		out.Values = map[string]any{}
	}
	return out, nil
}

func (q Queries) ClearWorkspace(ctx context.Context, workspaceID, actor string, at time.Time) error {
	sub := "SELECT id FROM resources WHERE workspace_id = ?"
	runs := "SELECT id FROM workflow_runs WHERE workspace_id = ?"
	views := "SELECT id FROM sync_views WHERE container_id IN (" + sub + ")"
	statements := []string{
		"DELETE FROM relationships WHERE source_id IN (" + sub + ") OR target_id IN (" + sub + ")",
		"DELETE FROM runner_step_results WHERE run_id IN (" + runs + ")",
		"DELETE FROM workflow_run_items WHERE run_id IN (" + runs + ")",
		"DELETE FROM workflow_runs WHERE workspace_id = ?",
		"DELETE FROM workflow_triggers WHERE workspace_id = ?",
		"DELETE FROM workflow_versions WHERE workflow_id IN (SELECT id FROM workflows WHERE workspace_id = ?)",
		"DELETE FROM workflows WHERE workspace_id = ?",
		"DELETE FROM domain_events WHERE workspace_id = ?",
		"DELETE FROM operation_items WHERE item_id IN (" + sub + ")",
		"DELETE FROM operations WHERE container_id IN (" + sub + ")",
		"DELETE FROM sync_tokens WHERE view_id IN (" + views + ")",
		"DELETE FROM sync_states WHERE view_id IN (" + views + ")",
		"DELETE FROM sync_views WHERE container_id IN (" + sub + ")",
		"DELETE FROM group_inboxes WHERE library_id IN (" + sub + ")",
		"DELETE FROM smart_folders WHERE workspace_id = ?",
		"DELETE FROM schema_templates WHERE workspace_id = ?",
		"DELETE FROM list_views WHERE container_id IN (" + sub + ")",
		"DELETE FROM field_values WHERE item_id IN (" + sub + ")",
		"DELETE FROM business_keys WHERE item_id IN (" + sub + ")",
		"DELETE FROM item_surfaces WHERE item_id IN (" + sub + ")",
		"DELETE FROM publications WHERE item_id IN (" + sub + ")",
		"DELETE FROM item_histories WHERE item_id IN (" + sub + ")",
		"DELETE FROM item_revisions WHERE item_id IN (" + sub + ")",
		"DELETE FROM blobs WHERE item_id IN (" + sub + ")",
		"DELETE FROM grants WHERE resource_id IN (" + sub + ")",
		"DELETE FROM collection_types WHERE container_id IN (" + sub + ")",
		"DELETE FROM schema_revisions WHERE container_id IN (" + sub + ")",
	}
	// Tombstones release retention and last-owner guards during the purge.
	if _, err := q.exec.ExecContext(ctx, "UPDATE resources SET deleted_at = ?, deleted_by = ?, name_key = NULL, head_revision_id = NULL, published_revision_id = NULL, schema_head_id = NULL WHERE workspace_id = ?", at, actor, workspaceID); err != nil {
		return dberr.Translate(err)
	}
	if _, err := q.exec.ExecContext(ctx, "INSERT INTO blob_deletions (id, created_at, object_key) SELECT b.id, ?, b.object_key FROM blobs b JOIN resources r ON r.id = b.item_id WHERE r.workspace_id = ? ON CONFLICT (object_key) DO NOTHING", at, workspaceID); err != nil {
		return dberr.Translate(err)
	}
	for _, statement := range statements {
		args := make([]any, strings.Count(statement, "?"))
		for i := range args {
			args[i] = workspaceID
		}
		if _, err := q.exec.ExecContext(ctx, statement, args...); err != nil {
			return dberr.Translate(err)
		}
	}
	return nil
}
