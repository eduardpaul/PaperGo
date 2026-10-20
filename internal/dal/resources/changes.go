package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/grants"
	"papergo/internal/dal/live"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
	"papergo/internal/dal/taxonomy"
)

// New describes a resource to create. Empty identifiers are absent.
type New struct {
	ID, WorkspaceID, ParentID, ContainerID, ContentTypeID string
	Kind                                                  refs.ResourceKind
	PersonalOwnerID                                       string
	SystemKey                                             SystemKey
	Name, NameKey, Description, DefaultCurrency           string
	// AllowFolders and InheritPermissions default to true.
	AllowFolders, InheritPermissions *bool
	Keywords                         []string
	Values                           json.RawMessage
	PublishingEnabled, WebdavEnabled bool
	Versioning                       Versioning
	MaxVersions                      int
	DuplicatePolicy                  DuplicatePolicy
	TemplateKeys                     []string
	CreatedBy                        string
}

func now() time.Time { return time.Now().UTC() }

func ptr[T any](v T) *T { return &v }

// Create adds a workspace, or a collection, folder or item below a live
// parent of the same workspace. Folders need a collection that allows them;
// folders and items a content type their collection accepts. The resource
// inherits its parent's permission scope unless it keeps its own.
func (r *Repo) Create(ctx context.Context, n New) (*Resource, error) {
	var out *Resource
	err := r.write(ctx, func(q Queries) error {
		created, err := create(ctx, q, n)
		out = created
		return err
	})
	return out, err
}

func create(ctx context.Context, q Queries, n New) (*Resource, error) {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	if n.Kind == refs.KindWorkspace && n.WorkspaceID == "" {
		n.WorkspaceID = n.ID
	}
	if err := q.LockWorkspace(ctx, n.WorkspaceID); err != nil && !dberr.IsNotFound(err) {
		return nil, err
	}
	if !n.Kind.Valid() {
		return nil, fmt.Errorf("%w: unsupported resource kind %q", dberr.ErrInvalidContainment, n.Kind)
	}
	collection := n.Kind == refs.KindList || n.Kind == refs.KindLibrary
	if n.PublishingEnabled && !collection {
		return nil, fmt.Errorf("%w: publishing belongs to lists and libraries", dberr.ErrInvalidContainment)
	}
	if n.WebdavEnabled && n.Kind != refs.KindLibrary {
		return nil, fmt.Errorf("%w: WebDAV belongs to libraries", dberr.ErrInvalidContainment)
	}
	if n.PersonalOwnerID != "" {
		if n.Kind != refs.KindWorkspace {
			return nil, fmt.Errorf("%w: personal owners belong to workspaces", dberr.ErrInvalidContainment)
		}
		active, err := q.UserActive(ctx, n.PersonalOwnerID)
		if err != nil {
			return nil, err
		}
		if !active {
			return nil, fmt.Errorf("%w: a personal owner is an active user", dberr.ErrInvalidReference)
		}
	}
	if n.SystemKey != "" {
		if !n.SystemKey.Valid() || n.Kind != refs.KindLibrary {
			return nil, fmt.Errorf("%w: system keys belong to Home libraries", dberr.ErrInvalidContainment)
		}
		w, err := q.FindResource(ctx, n.WorkspaceID)
		if err != nil || w.PersonalOwnerID == nil || n.ParentID != w.ID {
			return nil, fmt.Errorf("%w: system libraries belong to Home", dberr.ErrInvalidContainment)
		}
	}
	inherit := true
	if n.InheritPermissions != nil {
		inherit = *n.InheritPermissions
	}
	scope := n.ID
	if n.Kind == refs.KindWorkspace {
		if n.WorkspaceID != n.ID || n.ParentID != "" || n.ContainerID != "" {
			return nil, fmt.Errorf("%w: a workspace is its own root", dberr.ErrInvalidContainment)
		}
		if n.InheritPermissions != nil && *n.InheritPermissions {
			return nil, fmt.Errorf("%w: workspaces cannot inherit permissions", dberr.ErrInvalidContainment)
		}
		inherit = false
	} else {
		if n.ParentID == "" {
			return nil, fmt.Errorf("%w: only workspaces have no parent", dberr.ErrInvalidContainment)
		}
		parent, err := q.FindResource(ctx, n.ParentID)
		if err != nil && !dberr.IsNotFound(err) {
			return nil, err
		}
		if err != nil || parent.DeletedAt != nil || parent.WorkspaceID != n.WorkspaceID {
			return nil, fmt.Errorf("%w: the parent is a live resource of the same workspace", dberr.ErrInvalidContainment)
		}
		if err := contained(ctx, q, n.Kind, parent, n.ContainerID); err != nil {
			return nil, err
		}
		if n.Kind == refs.KindFolder || n.Kind == refs.KindItem {
			if n.Kind == refs.KindFolder {
				c, err := q.FindResource(ctx, n.ContainerID)
				if err != nil {
					return nil, fmt.Errorf("%w: the collection does not exist", dberr.ErrInvalidContainment)
				}
				if !c.AllowFolders {
					return nil, fmt.Errorf("%w: the collection does not allow new folders", dberr.ErrInvalidContainment)
				}
			}
			if n.ContentTypeID == "" {
				return nil, fmt.Errorf("%w: folders and items have a content type", dberr.ErrInvalidReference)
			}
			accepted, err := catalog.Accepts(ctx, q, n.ContainerID, n.ContentTypeID)
			if err != nil {
				return nil, err
			}
			if !accepted {
				return nil, fmt.Errorf("%w: the collection does not accept the content type", dberr.ErrIncompatibleCollection)
			}
		}
		if inherit {
			scope = parent.ID
			if parent.ScopeID != nil {
				scope = *parent.ScopeID
			}
		}
	}
	allowFolders := true
	if n.AllowFolders != nil {
		allowFolders = *n.AllowFolders
	}
	if n.Versioning == "" {
		n.Versioning = VersioningOff
	}
	if n.MaxVersions == 0 {
		n.MaxVersions = 50
	}
	if n.DuplicatePolicy == "" {
		n.DuplicatePolicy = DuplicateWarn
	}
	if n.TemplateKeys == nil {
		n.TemplateKeys = []string{}
	}
	if len(n.Values) == 0 {
		n.Values = json.RawMessage(`{}`)
	}
	keywords, err := taxonomy.ResolveKeywords(ctx, q, n.Keywords)
	if err != nil {
		return nil, err
	}
	at := now()
	row := Resource{
		ID: n.ID, CreatedAt: at, WorkspaceID: n.WorkspaceID, PersonalOwnerID: optional(n.PersonalOwnerID), Kind: n.Kind,
		ParentID: optional(n.ParentID), ContainerID: optional(n.ContainerID), ContentTypeID: optional(n.ContentTypeID),
		Name: n.Name, NameKey: optional(n.NameKey), Description: n.Description, DefaultCurrency: n.DefaultCurrency,
		AllowFolders: allowFolders, Keywords: keywords, TemplateKeys: n.TemplateKeys, InheritPermissions: inherit, Version: 1,
		ScopeID: &scope, NextRevisionNumber: 1, PublishingEnabled: n.PublishingEnabled, Versioning: n.Versioning,
		MaxVersions: n.MaxVersions, WebdavEnabled: n.WebdavEnabled, DuplicatePolicy: n.DuplicatePolicy,
		UpdatedAt: at, CreatedBy: n.CreatedBy, UpdatedBy: n.CreatedBy,
	}
	if n.SystemKey != "" {
		row.SystemKey = &n.SystemKey
	}
	if err := q.InsertResource(ctx, row, n.Values); err != nil {
		return nil, err
	}
	if err := q.TagResource(ctx, row.ID, keywords); err != nil {
		return nil, err
	}
	if row.Content() {
		if err := live.Notify(ctx, q, live.KindItem, row.ID, live.ActionCreated); err != nil {
			return nil, err
		}
	}
	created, err := q.FindResource(ctx, row.ID)
	return &created, err
}

// contained checks that a resource of kind belongs below parent in the
// collection containerID: collections directly in a workspace, folders and
// items directly in their collection or in one of its folders.
func contained(ctx context.Context, q Queries, kind refs.ResourceKind, parent Resource, containerID string) error {
	switch kind {
	case refs.KindList, refs.KindLibrary:
		if parent.Kind != refs.KindWorkspace || containerID != "" {
			return fmt.Errorf("%w: collections belong directly to a workspace", dberr.ErrInvalidContainment)
		}
	case refs.KindFolder, refs.KindItem:
		switch {
		case parent.Collection():
			if containerID != parent.ID {
				return fmt.Errorf("%w: content directly in a collection belongs to it", dberr.ErrInvalidContainment)
			}
		case parent.Kind == refs.KindFolder:
			if containerID == "" || parent.ContainerID == nil || containerID != *parent.ContainerID {
				return fmt.Errorf("%w: content in a folder belongs to the folder's collection", dberr.ErrInvalidContainment)
			}
		default:
			return fmt.Errorf("%w: invalid parent kind %q for %s", dberr.ErrInvalidContainment, parent.Kind, kind)
		}
	default:
		return fmt.Errorf("%w: invalid parent for %s", dberr.ErrInvalidContainment, kind)
	}
	return nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Changes are the settings a change writes. Nil fields keep their value;
// the caller has normalized every value.
type Changes struct {
	Name, Description, ParentID, NameKey, ContentTypeID *string
	DefaultCurrency                                     *string
	Versioning                                          *Versioning
	DuplicatePolicy                                     *DuplicatePolicy
	AllowFolders, PublishingEnabled, WebdavEnabled      *bool
	MaxVersions                                         *int
	Keywords                                            *[]string
	Values                                              json.RawMessage
}

// Change writes a new version of a live resource with its keyword
// projection, moving it when its parent changes. A stale version fails with
// dberr.ErrConcurrentUpdate.
func (r *Repo) Change(ctx context.Context, id string, version int, actor string, c Changes) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, id); err != nil {
			return err
		}
		row, err := q.FindResource(ctx, id)
		if err != nil {
			return err
		}
		if row.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if row.DeletedAt != nil {
			return fmt.Errorf("%w: deleted resources are retained", dberr.ErrImmutable)
		}
		content := row.Content()
		if c.Description != nil && *c.Description != "" && content {
			return fmt.Errorf("%w: descriptions belong to workspaces and collections", dberr.ErrCheckViolation)
		}
		if !row.Collection() && (c.AllowFolders != nil || c.Versioning != nil || c.MaxVersions != nil || c.PublishingEnabled != nil || c.DuplicatePolicy != nil) {
			return fmt.Errorf("%w: collection settings require a collection", dberr.ErrCheckViolation)
		}
		if c.DefaultCurrency != nil && row.Kind != refs.KindWorkspace {
			return fmt.Errorf("%w: default currency requires a workspace", dberr.ErrCheckViolation)
		}
		if c.WebdavEnabled != nil && row.Kind != refs.KindLibrary {
			return fmt.Errorf("%w: WebDAV requires a library", dberr.ErrCheckViolation)
		}
		if c.Values != nil && !content {
			return fmt.Errorf("%w: values require an item or folder", dberr.ErrCheckViolation)
		}
		if c.Versioning != nil && !c.Versioning.Valid() || c.DuplicatePolicy != nil && !c.DuplicatePolicy.Valid() {
			return fmt.Errorf("%w: invalid collection setting", dberr.ErrCheckViolation)
		}
		moved := c.ParentID != nil && (row.ParentID == nil || *c.ParentID != *row.ParentID)
		if moved {
			container := ""
			if row.ContainerID != nil {
				container = *row.ContainerID
			}
			if err := move(ctx, q, row, *c.ParentID, container); err != nil {
				return err
			}
		}
		set := Set{Version: version + 1, UpdatedBy: &actor, Description: c.Description, AllowFolders: c.AllowFolders, Versioning: c.Versioning,
			MaxVersions: c.MaxVersions, PublishingEnabled: c.PublishingEnabled, WebdavEnabled: c.WebdavEnabled, DefaultCurrency: c.DefaultCurrency,
			DuplicatePolicy: c.DuplicatePolicy, Values: c.Values}
		if c.Name != nil {
			set.Name, set.NameKey = c.Name, ptr("")
			if c.NameKey != nil {
				set.NameKey = c.NameKey
			}
		}
		if c.ContentTypeID != nil {
			if !content || row.ContainerID == nil {
				return fmt.Errorf("%w: content types belong to items and folders", dberr.ErrInvalidContainment)
			}
			accepted, err := catalog.Accepts(ctx, q, *row.ContainerID, *c.ContentTypeID)
			if err != nil {
				return err
			}
			if !accepted {
				return fmt.Errorf("%w: the collection does not accept the content type", dberr.ErrInvalidReference)
			}
			set.ContentTypeID = c.ContentTypeID
		}
		if c.Keywords != nil {
			ids, err := taxonomy.ResolveKeywords(ctx, q, *c.Keywords)
			if err != nil {
				return err
			}
			set.Keywords = &ids
			if err := q.TagResource(ctx, id, ids); err != nil {
				return err
			}
		}
		if err := update(ctx, q, id, version, set); err != nil {
			return err
		}
		if content {
			return live.Notify(ctx, q, live.KindItem, id, live.ActionUpdated)
		}
		return nil
	})
}

// update writes s to a resource at version, stamping it now.
func update(ctx context.Context, q Queries, id string, version int, s Set) error {
	s.UpdatedAt = now()
	ok, err := q.UpdateResource(ctx, id, version, s)
	switch {
	case err == nil && !ok && version == 0:
		// Without a version to check, only a missing row updates nothing.
		err = dberr.ErrNotFound
	case err == nil && !ok:
		err = dberr.ErrConcurrentUpdate
	}
	return err
}

// move places a live folder or item below another parent in its
// collection, following the new parent's scope when it inherits.
func move(ctx context.Context, q Queries, row Resource, parentID, containerID string) error {
	if err := q.LockWorkspace(ctx, parentID); err != nil {
		return err
	}
	if !row.Content() {
		return fmt.Errorf("%w: only items and folders move", dberr.ErrInvalidContainment)
	}
	parent, err := q.FindResource(ctx, parentID)
	if err != nil && !dberr.IsNotFound(err) {
		return err
	}
	if err != nil || parent.DeletedAt != nil {
		return fmt.Errorf("%w: the target parent is not a live resource", dberr.ErrInvalidContainment)
	}
	if parent.WorkspaceID != row.WorkspaceID {
		return fmt.Errorf("%w: moves stay within their collection", dberr.ErrIncompatibleCollection)
	}
	if err := contained(ctx, q, row.Kind, parent, containerID); err != nil {
		return err
	}
	if parentID == row.ID {
		return dberr.ErrCycleDetected
	}
	cycle, err := q.IsAncestor(ctx, parentID, row.ID)
	if err != nil {
		return err
	}
	if cycle {
		return dberr.ErrCycleDetected
	}
	scope := row.ID
	if row.InheritPermissions {
		scope = parent.ID
		if parent.ScopeID != nil {
			scope = *parent.ScopeID
		}
	}
	if err := update(ctx, q, row.ID, 0, Set{ParentID: &parentID, ScopeID: &scope}); err != nil {
		return err
	}
	return q.InheritScope(ctx, row.ID, scope)
}

// SetInheritPermissions makes a resource inherit its parent's permission
// scope or keep its own, with its inheriting descendants. A resource that
// inherits holds no grants.
func (r *Repo) SetInheritPermissions(ctx context.Context, id string, version int, inherit bool) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, id); err != nil {
			return err
		}
		row, err := q.FindResource(ctx, id)
		if err != nil {
			return err
		}
		if row.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if row.DeletedAt != nil {
			return fmt.Errorf("%w: deleted resources are retained", dberr.ErrImmutable)
		}
		scope := row.ID
		if inherit {
			if row.Kind == refs.KindWorkspace || row.ParentID == nil {
				return fmt.Errorf("%w: workspaces cannot inherit permissions", dberr.ErrInvalidPermissionScope)
			}
			held, err := q.Grants(ctx, grants.Filter{ResourceID: id})
			if err != nil {
				return err
			}
			if len(held) > 0 {
				return fmt.Errorf("%w: inherited resources cannot hold grants", dberr.ErrInvalidPermissionScope)
			}
			parent, err := q.FindResource(ctx, *row.ParentID)
			if err != nil {
				return err
			}
			scope = parent.ID
			if parent.ScopeID != nil {
				scope = *parent.ScopeID
			}
		}
		if err := update(ctx, q, id, version, Set{InheritPermissions: &inherit, ScopeID: &scope, Version: version + 1}); err != nil {
			return err
		}
		return q.InheritScope(ctx, id, scope)
	})
}

// Delete moves a resource and its live descendants to the recycle bin,
// dropping their read projections and business keys. Their revisions,
// publications and history stay for recovery. It returns the deleted
// resources, ancestors first, as they were.
func (r *Repo) Delete(ctx context.Context, id, actor string) ([]*Resource, error) {
	var out []*Resource
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, id); err != nil {
			return err
		}
		ids, err := q.Subtree(ctx, id, TreeFilter{State: Live})
		if err != nil {
			return err
		}
		at := now()
		for _, rid := range ids {
			row, err := q.FindResource(ctx, rid)
			if err != nil {
				return err
			}
			if row.DeletedAt != nil {
				return fmt.Errorf("%w: the resource is already deleted", dberr.ErrImmutable)
			}
			if err := dropProjections(ctx, q, rid, true); err != nil {
				return err
			}
			if err := update(ctx, q, rid, row.Version, Set{Deleted: &Deletion{At: at, By: actor}, NameKey: ptr(""), Version: row.Version + 1, UpdatedBy: &actor}); err != nil {
				return err
			}
			if row.Content() {
				if err := live.Notify(ctx, q, live.KindItem, rid, live.ActionDeleted); err != nil {
					return err
				}
			}
			out = append(out, &row)
		}
		return nil
	})
	return out, err
}

// dropProjections removes an item's surfaces, their counts when it is live,
// and its business keys.
func dropProjections(ctx context.Context, q Queries, id string, counted bool) error {
	if counted {
		if err := surfaces.CountItem(ctx, q, id, false); err != nil {
			return err
		}
	}
	if err := surfaces.Drop(ctx, q, id); err != nil {
		return err
	}
	return q.DeleteBusinessKeys(ctx, catalog.KeyFilter{ItemID: id})
}

// Restore brings a deleted folder or item back below a live parent of its
// live collection and workspace, inheriting the parent's scope when it
// inherits. The caller projects its surfaces again.
func (r *Repo) Restore(ctx context.Context, id, parentID, nameKey, actor string) (*Resource, error) {
	var out *Resource
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, id); err != nil {
			return err
		}
		row, err := q.FindResource(ctx, id)
		if err != nil {
			return err
		}
		if row.DeletedAt == nil {
			return fmt.Errorf("%w: the resource is not deleted", dberr.ErrInvalidStateTransition)
		}
		if !row.Content() || row.ContainerID == nil {
			return fmt.Errorf("%w: only folders and items are restored", dberr.ErrInvalidContainment)
		}
		parent, err := q.FindResource(ctx, parentID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || parent.DeletedAt != nil || parent.WorkspaceID != row.WorkspaceID {
			return fmt.Errorf("%w: restore requires a live parent", dberr.ErrInvalidContainment)
		}
		for _, above := range []string{*row.ContainerID, row.WorkspaceID} {
			a, err := q.FindResource(ctx, above)
			if err != nil && !dberr.IsNotFound(err) {
				return err
			}
			if err != nil || a.DeletedAt != nil {
				return fmt.Errorf("%w: restore requires a live collection and workspace", dberr.ErrInvalidContainment)
			}
		}
		if err := contained(ctx, q, row.Kind, parent, *row.ContainerID); err != nil {
			return err
		}
		scope := row.ID
		if row.InheritPermissions {
			scope = parent.ID
			if parent.ScopeID != nil {
				scope = *parent.ScopeID
			}
		}
		if err := update(ctx, q, id, row.Version, Set{Deleted: &Deletion{}, ParentID: &parentID, ScopeID: &scope, NameKey: &nameKey, Version: row.Version + 1, UpdatedBy: &actor}); err != nil {
			return err
		}
		if err := q.InheritScope(ctx, id, scope); err != nil {
			return err
		}
		if err := live.Notify(ctx, q, live.KindItem, id, live.ActionUpdated); err != nil {
			return err
		}
		restored, err := q.FindResource(ctx, id)
		out = &restored
		return err
	})
	return out, err
}

// Purge permanently deletes a resource and its subtree with everything they
// own, deepest first.
func (r *Repo) Purge(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, id); err != nil {
			return err
		}
		ids, err := q.Subtree(ctx, id, TreeFilter{})
		if err != nil {
			return err
		}
		for i := len(ids) - 1; i >= 0; i-- {
			row, err := q.FindResource(ctx, ids[i])
			if err != nil {
				return err
			}
			if row.DeletedAt == nil {
				if err := surfaces.CountItem(ctx, q, row.ID, false); err != nil {
					return err
				}
			}
			if err := q.DeleteResource(ctx, row.ID); err != nil {
				return err
			}
			if row.Content() {
				if err := live.Notify(ctx, q, live.KindItem, row.ID, live.ActionDeleted); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Transfer moves a live item to a folder or collection of another
// workspace. It leaves its grants, projections and business keys behind and
// inherits the destination's scope; its runs follow it. The caller
// projects its surfaces again.
func (r *Repo) Transfer(ctx context.Context, id, containerID, parentID, nameKey string, values json.RawMessage, actor string) (*Resource, error) {
	var out *Resource
	err := r.write(ctx, func(q Queries) error {
		first, second := id, parentID
		if second < first {
			first, second = second, first
		}
		for _, lock := range []string{first, second} {
			if err := q.LockWorkspace(ctx, lock); err != nil {
				return err
			}
		}
		row, err := q.FindResource(ctx, id)
		if err != nil {
			return err
		}
		if row.DeletedAt != nil || row.Kind != refs.KindItem {
			return fmt.Errorf("%w: only live items transfer", dberr.ErrInvalidContainment)
		}
		parent, err := q.FindResource(ctx, parentID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || parent.DeletedAt != nil {
			return fmt.Errorf("%w: the target parent is not a live resource", dberr.ErrInvalidContainment)
		}
		if err := contained(ctx, q, row.Kind, parent, containerID); err != nil {
			return err
		}
		accepted, err := catalog.Accepts(ctx, q, containerID, *row.ContentTypeID)
		if err != nil {
			return err
		}
		if !accepted {
			return fmt.Errorf("%w: the destination does not accept the content type", dberr.ErrIncompatibleCollection)
		}
		if err := dropProjections(ctx, q, id, true); err != nil {
			return err
		}
		if err := q.DeleteGrants(ctx, grants.Filter{ResourceID: id}); err != nil {
			return err
		}
		scope := parent.ID
		if parent.ScopeID != nil {
			scope = *parent.ScopeID
		}
		if err := update(ctx, q, id, row.Version, Set{WorkspaceID: &parent.WorkspaceID, ContainerID: &containerID, ParentID: &parentID, InheritPermissions: ptr(true),
			ScopeID: &scope, NameKey: &nameKey, Values: values, Version: row.Version + 1, UpdatedBy: &actor}); err != nil {
			return err
		}
		if err := q.MoveItemRuns(ctx, id, parent.WorkspaceID); err != nil {
			return err
		}
		if err := live.Notify(ctx, q, live.KindItem, id, live.ActionUpdated); err != nil {
			return err
		}
		moved, err := q.FindResource(ctx, id)
		out = &moved
		return err
	})
	return out, err
}

// Revise records the next revision of a live item or folder, numbered
// after its last one, and makes it the head with the values it holds.
func (r *Repo) Revise(ctx context.Context, rev revisions.Revision, values json.RawMessage, actor string) (revisions.Revision, error) {
	var out revisions.Revision
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, rev.ItemID); err != nil {
			return err
		}
		row, err := q.FindResource(ctx, rev.ItemID)
		if err != nil {
			return err
		}
		rev.RevisionNumber = row.NextRevisionNumber
		if out, err = revisions.Record(ctx, q, rev); err != nil {
			return err
		}
		return update(ctx, q, rev.ItemID, 0, Set{HeadRevisionID: &out.ID, Values: values, UpdatedBy: &actor, NextRevisionNumber: ptr(row.NextRevisionNumber + 1)})
	})
	return out, err
}

// SetPublished points an item or folder at the revision readers see. An
// explicit publication is a new version of the item.
func (r *Repo) SetPublished(ctx context.Context, id, revisionID string, explicit bool) error {
	return r.write(ctx, func(q Queries) error {
		set := Set{PublishedRevisionID: &revisionID}
		if explicit {
			row, err := q.FindResource(ctx, id)
			if err != nil {
				return err
			}
			set.Version = row.Version + 1
		}
		if err := update(ctx, q, id, 0, set); err != nil {
			return err
		}
		if explicit {
			return live.Notify(ctx, q, live.KindItem, id, live.ActionUpdated)
		}
		return nil
	})
}

// Unpublish withdraws the published revision of the item at version.
func (r *Repo) Unpublish(ctx context.Context, id string, version int) error {
	return r.write(ctx, func(q Queries) error {
		if err := update(ctx, q, id, version, Set{PublishedRevisionID: ptr(""), Version: version + 1}); err != nil {
			return err
		}
		return live.Notify(ctx, q, live.KindItem, id, live.ActionUpdated)
	})
}

// Touch records a new version of a resource whose content did not change,
// as attaching a file or linking it does. A version of 0 accepts any; an
// empty actor keeps the last editor.
func (r *Repo) Touch(ctx context.Context, id string, version int, actor string) error {
	return r.write(ctx, func(q Queries) error {
		row, err := q.FindResource(ctx, id)
		if err != nil {
			return err
		}
		if version != 0 && row.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		set := Set{Version: row.Version + 1}
		if actor != "" {
			set.UpdatedBy = &actor
		}
		if err := update(ctx, q, id, row.Version, set); err != nil {
			return err
		}
		if row.Content() {
			return live.Notify(ctx, q, live.KindItem, id, live.ActionUpdated)
		}
		return nil
	})
}

// SchemaChanged stamps a collection whose schema changed. Replacing an
// existing schema is a new version of the collection.
func (r *Repo) SchemaChanged(ctx context.Context, id, actor string, replaced bool) error {
	return r.write(ctx, func(q Queries) error {
		row, err := q.FindResource(ctx, id)
		if err != nil {
			return err
		}
		set := Set{UpdatedBy: &actor}
		if replaced {
			set.Version = row.Version + 1
		}
		return update(ctx, q, id, 0, set)
	})
}

// SetTemplateKeys records the templates a collection adopted.
func (r *Repo) SetTemplateKeys(ctx context.Context, id string, keys []string) error {
	if len(keys) > 100 {
		return fmt.Errorf("%w: at most 100 template keys", dberr.ErrCheckViolation)
	}
	return r.write(ctx, func(q Queries) error {
		row, err := q.FindResource(ctx, id)
		if err != nil {
			return err
		}
		if !row.Collection() {
			return fmt.Errorf("%w: template keys belong to collections", dberr.ErrCheckViolation)
		}
		return update(ctx, q, id, 0, Set{TemplateKeys: &keys})
	})
}

// ClearWorkspace empties a workspace that is being purged, leaving its
// tombstoned resources for Purge. Only account deletion purges a Home.
func (r *Repo) ClearWorkspace(ctx context.Context, workspaceID, actor string) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, workspaceID); err != nil {
			return err
		}
		return q.ClearWorkspace(ctx, workspaceID, actor, now())
	})
}
