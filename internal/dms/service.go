package dms

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/grant"
	"papergo/ent/resource"
	"papergo/internal/model"
	"strings"
	"time"
)

type Service struct {
	Client      *ent.Client
	writeMu     chan struct{}
	transaction bool
}

func NewService(client *ent.Client) *Service {
	return &Service{Client: client, writeMu: make(chan struct{}, 1)}
}

// Mutations include authorization, version checks and audit records in one
// transaction. SQLite WAL allows concurrent readers; one writer per process
// avoids deferred-transaction upgrades racing with another application writer.
// Waiting for the writer slot honors ctx, so a timed-out request leaves the queue.
func (s *Service) write(ctx context.Context, fn func(*Service) error) error {
	select {
	case s.writeMu <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.writeMu }()
	tx, err := s.Client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	err = fn(&Service{Client: tx.Client(), writeMu: s.writeMu, transaction: true})
	if err != nil {
		_ = tx.Rollback()
	} else {
		err = tx.Commit()
	}
	if ent.IsConstraintError(err) {
		return ErrConflict
	}
	return err
}

// A consistent snapshot binds authorization, surface selection and hydration.
func read[T any](ctx context.Context, s *Service, fn func(*Service) (T, error)) (out T, err error) {
	if s.transaction {
		return fn(s)
	}
	tx, err := s.Client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = fn(&Service{Client: tx.Client(), writeMu: s.writeMu, transaction: true})
	if err != nil {
		return out, err
	}
	err = tx.Commit()
	return out, err
}
func (s *Service) audit(ctx context.Context, subject, action string, r *ent.Resource, details map[string]any) error {
	_, err := s.Client.AuditEvent.Create().SetWorkspaceID(r.WorkspaceID).SetResourceID(r.ID).SetSubject(subject).SetAction(action).SetDetails(details).Save(ctx)
	return err
}
func (s *Service) Get(ctx context.Context, subject, id string) (*ent.Resource, error) {
	return s.GetSurface(ctx, subject, id, "auto")
}

type CreateResource struct {
	Kind              string         `json:"kind"`
	Name              string         `json:"name"`
	Tags              []string       `json:"tags"`
	Values            map[string]any `json:"values"`
	PublishingEnabled bool           `json:"publishing_enabled"`
	WebDAVEnabled     bool           `json:"webdav_enabled"`
}

func (s *Service) Create(ctx context.Context, subject, parentID string, in CreateResource) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		var e error
		out, e = t.create(ctx, subject, parentID, in, nil)
		return e
	})
	return
}

// create inserts a resource inside a write transaction. A library item may be
// created with its first blob, so its first revision already carries content.
func (s *Service) create(ctx context.Context, subject, parentID string, in CreateResource, content *BlobInput) (*ent.Resource, error) {
	if err := validateName(in.Name); err != nil {
		return nil, err
	}
	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	if in.Values == nil {
		in.Values = map[string]any{}
	}
	if parentID == "" && in.Kind != "workspace" {
		return nil, invalid("root resource must be a workspace")
	}
	if in.PublishingEnabled && in.Kind != "list" && in.Kind != "library" {
		return nil, invalid("publishing_enabled belongs to lists and libraries")
	}
	if in.WebDAVEnabled && in.Kind != "library" {
		return nil, invalid("webdav_enabled belongs to libraries")
	}
	if content != nil && in.Kind != "item" {
		return nil, invalid("only items have content")
	}
	id := uuid.NewString()
	workspaceID := id
	var parent *ent.Resource
	var containerID *string
	if parentID != "" {
		var e error
		parent, e = s.authorize(ctx, subject, parentID, "write")
		if e != nil {
			return nil, e
		}
		workspaceID = parent.WorkspaceID
		valid := false
		switch string(parent.Kind) {
		case "workspace":
			valid = in.Kind == "list" || in.Kind == "library"
		case "list", "library":
			valid = in.Kind == "folder" || in.Kind == "item"
			v := parent.ID
			containerID = &v
		case "folder":
			valid = in.Kind == "folder" || in.Kind == "item"
			containerID = parent.ContainerID
		}
		if !valid {
			return nil, invalid("invalid parent for resource kind")
		}
		depth, e := s.depth(ctx, parent)
		if e != nil {
			return nil, e
		}
		if depth >= MaxDepth {
			return nil, invalid("maximum hierarchy depth reached")
		}
	}
	key, e := s.fileNameKey(ctx, resource.Kind(in.Kind), containerID, in.Name)
	if e != nil {
		return nil, e
	}
	if in.Kind == "item" {
		if containerID == nil {
			return nil, invalid("item must belong to a list or library")
		}
		defs, e := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(*containerID)).All(ctx)
		if e != nil {
			return nil, e
		}
		if e = normalizeValues(defs, in.Values); e != nil {
			return nil, e
		}
	} else if len(in.Values) > 0 {
		return nil, invalid("custom values are supported on items only")
	}
	builder := s.Client.Resource.Create().SetID(id).SetWorkspaceID(workspaceID).SetKind(resource.Kind(in.Kind)).SetName(in.Name).SetTags(in.Tags).SetValues(in.Values).SetNillableContainerID(containerID).SetPublishingEnabled(in.PublishingEnabled).SetWebdavEnabled(in.WebDAVEnabled).SetNillableNameKey(key)
	if parent != nil {
		builder.SetParentID(parent.ID)
	} else {
		builder.SetInheritPermissions(false)
	}
	out, e := builder.Save(ctx)
	if e != nil {
		return nil, e
	}
	if parent == nil {
		_, e = s.Client.Grant.Create().SetResourceID(out.ID).SetSubject(subject).SetAction(grant.ActionManage).SetEffect(grant.EffectAllow).Save(ctx)
		if e != nil {
			return nil, e
		}
	}
	if out.Kind == resource.KindList || out.Kind == resource.KindLibrary {
		if e = s.recordSchema(ctx, subject, out); e != nil {
			return nil, e
		}
	}
	details := map[string]any{"kind": in.Kind}
	if out.Kind == resource.KindItem {
		var blobID *string
		if content != nil {
			b, e := s.createBlob(ctx, out.ID, *content)
			if e != nil {
				return nil, e
			}
			blobID = &b.ID
			details["blob_id"] = b.ID
		}
		if _, e = s.recordRevision(ctx, subject, out, blobID); e != nil {
			return nil, e
		}
	}
	out, e = s.Client.Resource.Get(ctx, out.ID)
	if e != nil {
		return nil, e
	}
	if out.Kind == resource.KindItem {
		if e = s.overlayHead(ctx, out); e != nil {
			return nil, e
		}
	}
	return out, s.audit(ctx, subject, "resource.create", out, details)
}

// depth counts r and its ancestors.
func (s *Service) depth(ctx context.Context, r *ent.Resource) (int, error) {
	depth := 1
	for ; r.ParentID != nil; depth++ {
		if depth > MaxDepth {
			return depth, invalid("invalid hierarchy depth")
		}
		var err error
		if r, err = s.Client.Resource.Get(ctx, *r.ParentID); err != nil {
			return depth, err
		}
	}
	return depth, nil
}

type UpdateResource struct {
	Name              *string         `json:"name,omitempty"`
	Tags              *[]string       `json:"tags,omitempty"`
	Values            *map[string]any `json:"values,omitempty"`
	PublishingEnabled *bool           `json:"publishing_enabled,omitempty"`
	WebDAVEnabled     *bool           `json:"webdav_enabled,omitempty"`
	// ParentID moves a folder or item to another folder of the same collection.
	ParentID *string `json:"parent_id,omitempty"`
}

func (s *Service) Update(ctx context.Context, subject, id string, version int, in UpdateResource) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		var e error
		out, e = t.update(ctx, subject, id, version, in)
		return e
	})
	return
}
func (s *Service) update(ctx context.Context, subject, id string, version int, in UpdateResource) (*ent.Resource, error) {
	content := in.Name != nil || in.Tags != nil || in.Values != nil
	if !content && in.PublishingEnabled == nil && in.WebDAVEnabled == nil && in.ParentID == nil {
		return nil, invalid("at least one change is required")
	}
	if in.Name != nil {
		if err := validateName(*in.Name); err != nil {
			return nil, err
		}
	}
	if in.Tags != nil {
		if err := validateTags(*in.Tags); err != nil {
			return nil, err
		}
	}
	r, e := s.authorize(ctx, subject, id, "write")
	if e != nil {
		return nil, e
	}
	if r.Version != version {
		return nil, ErrConflict
	}
	if in.PublishingEnabled != nil || in.WebDAVEnabled != nil {
		if in.PublishingEnabled != nil && r.Kind != resource.KindList && r.Kind != resource.KindLibrary {
			return nil, invalid("publishing_enabled belongs to lists and libraries")
		}
		if in.WebDAVEnabled != nil && r.Kind != resource.KindLibrary {
			return nil, invalid("webdav_enabled belongs to libraries")
		}
		if _, e = s.authorize(ctx, subject, id, "manage"); e != nil {
			return nil, e
		}
	}
	if r.Kind == resource.KindItem {
		if e = s.overlayHead(ctx, r); e != nil {
			return nil, e
		}
	}
	if in.Values != nil && (r.Kind != resource.KindItem || r.ContainerID == nil) {
		return nil, invalid("only items support custom values")
	}
	moved := in.ParentID != nil && (r.ParentID == nil || *in.ParentID != *r.ParentID)
	b := s.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).AddVersion(1).SetUpdatedAt(time.Now().UTC())
	if moved {
		if e = s.checkMove(ctx, subject, r, *in.ParentID); e != nil {
			return nil, e
		}
		b.SetParentID(*in.ParentID)
	}
	if in.Name != nil {
		key, e := s.fileNameKey(ctx, r.Kind, r.ContainerID, *in.Name)
		if e != nil {
			return nil, e
		}
		b.SetName(*in.Name).SetNillableNameKey(key)
	}
	// Item values are validated and normalized once, by recordRevision below.
	if r.Kind == resource.KindItem {
		if in.Name != nil {
			r.Name = *in.Name
		}
		if in.Tags != nil {
			r.Tags = *in.Tags
		}
		if in.Values != nil {
			r.Values = *in.Values
		}
		b.SetValues(r.Values)
	}
	if in.Tags != nil {
		b.SetTags(*in.Tags)
	}
	if in.PublishingEnabled != nil {
		b.SetPublishingEnabled(*in.PublishingEnabled)
	}
	if in.WebDAVEnabled != nil {
		b.SetWebdavEnabled(*in.WebDAVEnabled)
	}
	n, e := b.Save(ctx)
	if e != nil {
		return nil, e
	}
	if n != 1 {
		return nil, ErrConflict
	}
	out, e := s.Client.Resource.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if out.Kind == resource.KindItem {
		// A move alone changes location, not content, so it records no revision.
		if content {
			out.Values = r.Values
			if _, e = s.recordRevision(ctx, subject, out, nil); e != nil {
				return nil, e
			}
			if out, e = s.Client.Resource.Get(ctx, id); e != nil {
				return nil, e
			}
		}
		if e = s.overlayHead(ctx, out); e != nil {
			return nil, e
		}
	}
	if in.PublishingEnabled != nil && !*in.PublishingEnabled {
		if e = s.publishAllHeads(ctx, subject, out); e != nil {
			return nil, e
		}
	}
	details := map[string]any{"version": out.Version}
	if moved {
		details["from_parent_id"] = *r.ParentID
		details["parent_id"] = *in.ParentID
	}
	return out, s.audit(ctx, subject, "resource.update", out, details)
}

// checkMove validates moving r below parentID: both ends need write access,
// the parent must belong to r's collection, and the subtree must stay within
// MaxDepth without entering itself. Triggers repeat the containment rules.
func (s *Service) checkMove(ctx context.Context, subject string, r *ent.Resource, parentID string) error {
	if r.Kind != resource.KindFolder && r.Kind != resource.KindItem {
		return invalid("only folders and items can move")
	}
	p, err := s.authorize(ctx, subject, parentID, "write")
	if err != nil {
		return err
	}
	if r.ContainerID == nil || (p.ID != *r.ContainerID && (p.Kind != resource.KindFolder || p.ContainerID == nil || *p.ContainerID != *r.ContainerID)) {
		return invalid("folders and items move only within their collection")
	}
	// One walk up from the new parent both rejects entering r and counts depth.
	depth := 1
	for a := p; a.ParentID != nil; depth++ {
		if a.ID == r.ID {
			return invalid("a folder cannot move into itself")
		}
		if depth > MaxDepth {
			return invalid("invalid hierarchy depth")
		}
		if a, err = s.Client.Resource.Get(ctx, *a.ParentID); err != nil {
			return err
		}
	}
	height, err := s.subtreeHeight(ctx, r.ID)
	if err != nil {
		return err
	}
	if depth+height > MaxDepth {
		return invalid("maximum hierarchy depth reached")
	}
	return nil
}

// subtreeHeight counts the levels of id and its live descendants.
func (s *Service) subtreeHeight(ctx context.Context, id string) (int, error) {
	var height int
	err := s.scan(ctx, []any{&height}, `WITH RECURSIVE sub(id,depth) AS (SELECT ?,1 UNION ALL SELECT c.id,sub.depth+1 FROM resources c JOIN sub ON c.parent_id=sub.id WHERE c.deleted_at IS NULL AND sub.depth<=?) SELECT max(depth) FROM sub`, id, MaxDepth)
	return height, err
}

// scan reads the single row of an aggregate query.
func (s *Service) scan(ctx context.Context, dest []any, query string, args ...any) error {
	rows, err := s.Client.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err == nil {
			err = sql.ErrNoRows
		}
		return err
	}
	if err = rows.Scan(dest...); err != nil {
		return err
	}
	return rows.Close()
}

// Delete turns a folder or item, with everything below it, into retained
// tombstones. Their revisions, publications and audit history stay; derived
// read projections and live relationships go, so they vanish from every read.
func (s *Service) Delete(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error { return t.delete(ctx, subject, id, version) })
}
func (s *Service) delete(ctx context.Context, subject, id string, version int) error {
	r, err := s.authorize(ctx, subject, id, "write")
	if err != nil {
		return err
	}
	if r.Version != version {
		return ErrConflict
	}
	if r.Kind != resource.KindFolder && r.Kind != resource.KindItem {
		return invalid("only folders and items can be deleted")
	}
	subtree := `WITH RECURSIVE sub(id) AS (SELECT ? UNION ALL SELECT c.id FROM resources c JOIN sub ON c.parent_id=sub.id WHERE c.deleted_at IS NULL) SELECT id FROM sub`
	writable, args := permissionSQL("r.id", subject, "write")
	var total, allowed int
	if err = s.scan(ctx, []any{&total, &allowed}, `SELECT count(*), coalesce(sum(CASE WHEN `+writable+` THEN 1 ELSE 0 END),0) FROM resources r WHERE r.id IN (`+subtree+`)`, append(args, id)...); err != nil {
		return err
	}
	// Removing a folder removes content the caller may not otherwise change.
	if allowed != total {
		return ErrForbidden
	}
	for _, statement := range []string{
		`DELETE FROM field_values WHERE item_id IN (` + subtree + `)`,
		`DELETE FROM item_surfaces WHERE item_id IN (` + subtree + `)`,
	} {
		if _, err = s.Client.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	unlinked, err := s.Client.ExecContext(ctx, `DELETE FROM relationships WHERE source_id IN (`+subtree+`) OR target_id IN (`+subtree+`)`, id, id)
	if err != nil {
		return err
	}
	links, err := unlinked.RowsAffected()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err = s.Client.ExecContext(ctx, `UPDATE resources SET deleted_at=?, name_key=NULL, version=version+1, updated_at=? WHERE id IN (`+subtree+`)`, now, now, id); err != nil {
		return err
	}
	return s.audit(ctx, subject, "resource.delete", r, map[string]any{"version": version + 1, "descendants": total - 1, "relationships": links})
}

// fileNameKey is the unique, case-insensitive name of a library folder or item,
// and nil for other resources. Library names must be usable as file names.
func (s *Service) fileNameKey(ctx context.Context, kind resource.Kind, containerID *string, name string) (*string, error) {
	if (kind != resource.KindFolder && kind != resource.KindItem) || containerID == nil {
		return nil, nil
	}
	c, err := s.Client.Resource.Get(ctx, *containerID)
	if err != nil {
		return nil, err
	}
	if c.Kind != resource.KindLibrary {
		return nil, nil
	}
	if !filenameValid(name) {
		return nil, invalid("library folder and file names cannot contain / \\ or control characters")
	}
	key := nameKey(name)
	return &key, nil
}

// nameKey folds case like Windows and macOS file systems, which treat names
// differing only in case as the same file. It matches SQL unicode_lower.
func nameKey(name string) string { return strings.ToLower(name) }

type CreateField struct {
	Options  model.FieldOptions `json:"options,omitempty"`
	Key      string             `json:"key"`
	Label    string             `json:"label"`
	Type     string             `json:"type"`
	Required bool               `json:"required"`
	Choices  []string           `json:"choices"`
	Indexed  bool               `json:"indexed"`
	Scale    int                `json:"scale"`
}

func (s *Service) CreateField(ctx context.Context, subject, containerID string, in CreateField) (out *ent.FieldDefinition, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.authorize(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		out, e = t.createField(ctx, subject, c, in)
		if e != nil {
			return e
		}
		if e = t.recordSchema(ctx, subject, c); e != nil {
			return e
		}
		return t.audit(ctx, subject, "field.create", c, map[string]any{"key": in.Key})
	})
	return
}
func (s *Service) createField(ctx context.Context, subject string, c *ent.Resource, in CreateField) (*ent.FieldDefinition, error) {
	if c.Kind != "list" && c.Kind != "library" {
		return nil, invalid("fields belong to collections")
	}
	d := fieldFromInput(in)
	if d.Choices == nil {
		d.Choices = []string{}
	}
	if e := validateFieldDefinition(d); e != nil {
		return nil, e
	}
	if e := s.validateReferenceScopes(ctx, subject, c.WorkspaceID, d); e != nil {
		return nil, e
	}
	if len(d.Options.DefaultValue) > 0 {
		if e := s.normalizeItemValues(ctx, subject, c.WorkspaceID, []*ent.FieldDefinition{d}, map[string]any{}, nil); e != nil {
			return nil, e
		}
	}
	n, e := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(c.ID)).Count(ctx)
	if e != nil {
		return nil, e
	}
	if n >= 200 {
		return nil, invalid("at most 200 fields per collection")
	}
	if d.Required && len(d.Options.DefaultValue) == 0 {
		exists, e := s.Client.Resource.Query().Where(resource.ContainerIDEQ(c.ID), resource.KindEQ(resource.KindItem), resource.DeletedAtIsNil()).Exist(ctx)
		if e != nil {
			return nil, e
		}
		if exists {
			return nil, invalid("required fields on populated collections need a default")
		}
	}
	return s.Client.FieldDefinition.Create().SetContainerID(c.ID).SetKey(d.Key).SetLabel(d.Label).SetType(d.Type).SetRequired(d.Required).SetChoices(d.Choices).SetIndexed(d.Indexed).SetScale(d.Scale).SetOptions(d.Options).Save(ctx)
}
func (s *Service) Fields(ctx context.Context, subject, id string) ([]*ent.FieldDefinition, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]*ent.FieldDefinition, error) { return t.Fields(ctx, subject, id) })
	}
	if _, err := s.authorize(ctx, subject, id, "read"); err != nil {
		return nil, err
	}
	return s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(id)).Order(ent.Asc(fielddefinition.FieldKey)).All(ctx)
}

type Permission struct {
	Subject string `json:"subject"`
	Action  string `json:"action"`
	Effect  string `json:"effect"`
}
type Permissions struct {
	CopyInherited   bool         `json:"copy_inherited,omitempty"`
	ScopeID         string       `json:"scope_id,omitempty"`
	EffectiveGrants []Permission `json:"effective_grants,omitempty"`
	Inherit         bool         `json:"inherit"`
	Grants          []Permission `json:"grants"`
}

func (s *Service) SetPermissions(ctx context.Context, subject, id string, version int, in Permissions) (out *ent.Resource, err error) {
	if in.Inherit && (len(in.Grants) > 0 || in.CopyInherited) {
		return nil, invalid("inherited resources cannot have local grants; create an exclusive scope first")
	}
	if len(in.Grants) > 200 {
		return nil, invalid("at most 200 grants per resource")
	}
	seen := map[string]bool{}
	for _, g := range in.Grants {
		if strings.TrimSpace(g.Subject) == "" || len(g.Subject) > 255 {
			return nil, invalid("invalid permission subject")
		}
		if g.Action != "read" && g.Action != "read_draft" && g.Action != "write" && g.Action != "publish" && g.Action != "manage" {
			return nil, invalid("invalid permission action")
		}
		if g.Effect != "" && g.Effect != "allow" {
			return nil, invalid("only additive allow grants are supported")
		}
		key := g.Subject + "\x00" + g.Action
		if seen[key] {
			return nil, invalid("duplicate permission")
		}
		seen[key] = true
	}
	err = s.write(ctx, func(t *Service) error {
		r, e := t.authorize(ctx, subject, id, "manage")
		if e != nil {
			return e
		}
		if r.Version != version {
			return ErrConflict
		}
		if r.ParentID == nil && in.Inherit {
			return invalid("workspace cannot inherit permissions")
		}
		if in.CopyInherited {
			if !r.InheritPermissions {
				return invalid("copy_inherited requires an inherited resource")
			}
			_, copied, e := t.effectivePermissions(ctx, r)
			if e != nil {
				return e
			}
			merged := map[string]bool{}
			for _, g := range in.Grants {
				merged[g.Subject+"\x00"+g.Action] = true
			}
			for _, g := range copied {
				key := g.Subject + "\x00" + g.Action
				if !merged[key] {
					in.Grants = append(in.Grants, g)
					merged[key] = true
				}
			}
			if len(in.Grants) > 200 {
				return invalid("copied scope exceeds 200 grants")
			}
		}
		if _, e = t.Client.Grant.Delete().Where(grant.ResourceIDEQ(id)).Exec(ctx); e != nil {
			return e
		}
		n, e := t.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).SetInheritPermissions(in.Inherit).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		for _, g := range in.Grants {
			if _, e = t.Client.Grant.Create().SetResourceID(id).SetSubject(g.Subject).SetAction(grant.Action(g.Action)).SetEffect(grant.EffectAllow).Save(ctx); e != nil {
				return e
			}
		}
		out, e = t.Client.Resource.Get(ctx, id)
		if e != nil {
			return e
		}
		if _, e = t.authorize(ctx, subject, id, "manage"); e != nil {
			return invalid("permission change would remove your manage access")
		}
		if out.Kind == resource.KindItem {
			if e = t.overlayHead(ctx, out); e != nil {
				return e
			}
		}
		return t.audit(ctx, subject, "permissions.replace", out, map[string]any{"inherit": in.Inherit, "grant_count": len(in.Grants)})
	})
	return
}
func (s *Service) Permissions(ctx context.Context, subject, id string) (Permissions, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Permissions, error) { return t.Permissions(ctx, subject, id) })
	}
	r, err := s.authorize(ctx, subject, id, "manage")
	if err != nil {
		return Permissions{}, err
	}
	scope, effective, err := s.effectivePermissions(ctx, r)
	if err != nil {
		return Permissions{}, err
	}
	out := Permissions{Inherit: r.InheritPermissions, ScopeID: scope, EffectiveGrants: effective, Grants: []Permission{}}
	if !r.InheritPermissions {
		out.Grants = effective
	}
	return out, nil
}
func (s *Service) effectivePermissions(ctx context.Context, r *ent.Resource) (string, []Permission, error) {
	for depth := 0; r.InheritPermissions; depth++ {
		if r.ParentID == nil || depth >= MaxDepth {
			return "", nil, invalid("invalid permission ancestry")
		}
		parent, e := s.Client.Resource.Get(ctx, *r.ParentID)
		if e != nil {
			return "", nil, e
		}
		r = parent
	}
	rows, e := s.Client.Grant.Query().Where(grant.ResourceIDEQ(r.ID)).Order(ent.Asc(grant.FieldSubject, grant.FieldAction)).All(ctx)
	if e != nil {
		return "", nil, e
	}
	permissions := []Permission{}
	for _, g := range rows {
		permissions = append(permissions, Permission{g.Subject, string(g.Action), string(g.Effect)})
	}
	return r.ID, permissions, nil
}

func checkVersion(version int) error {
	if version < 1 {
		return invalid(fmt.Sprintf("version must be positive: %d", version))
	}
	return nil
}
