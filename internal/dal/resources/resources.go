// Package resources owns the content hierarchy: workspaces, the lists and
// libraries they hold, and the folders and items inside a collection. It
// keeps containment, permission scopes, keyword projections, tombstones and
// the derived read projections consistent for every engine. Drivers
// implement Queries; this package decides what the hierarchy may hold.
package resources

import (
	"context"
	"encoding/json"
	"time"

	"papergo/internal/dal/access"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/grants"
	"papergo/internal/dal/live"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
	"papergo/internal/dal/taxonomy"
)

// Versioning is whether a collection keeps a numbered history of its items.
type Versioning string

const (
	VersioningOff   Versioning = "off"
	VersioningMajor Versioning = "major"
)

func (v Versioning) Valid() bool { return v == VersioningOff || v == VersioningMajor }

// DuplicatePolicy is what a library does when a file's bytes are already
// stored in it.
type DuplicatePolicy string

const (
	DuplicateAllow DuplicatePolicy = "allow"
	DuplicateWarn  DuplicatePolicy = "warn"
	DuplicateBlock DuplicatePolicy = "block"
)

func (p DuplicatePolicy) Valid() bool {
	return p == DuplicateAllow || p == DuplicateWarn || p == DuplicateBlock
}

// SystemKey names a library Home provisions for its owner.
type SystemKey string

const (
	SystemDocuments SystemKey = "home.documents"
	SystemInbox     SystemKey = "home.inbox"
)

func (k SystemKey) Valid() bool { return k == SystemDocuments || k == SystemInbox }

// Resource is a workspace, collection, folder or item. Items and folders
// carry the content of their head revision; the deletion columns mark a
// tombstone in the recycle bin.
type Resource struct {
	Archived            bool              `json:"archived"`
	ArchivedAt          *time.Time        `json:"archived_at,omitempty"`
	ArchivedBy          *string           `json:"archived_by,omitempty"`
	ID                  string            `json:"id,omitempty"`
	CreatedAt           time.Time         `json:"created_at,omitempty"`
	WorkspaceID         string            `json:"workspace_id,omitempty"`
	PersonalOwnerID     *string           `json:"personal_owner_id,omitempty"`
	SystemKey           *SystemKey        `json:"system_key,omitempty"`
	ParentID            *string           `json:"parent_id,omitempty"`
	ContainerID         *string           `json:"container_id,omitempty"`
	ContentTypeID       *string           `json:"content_type_id,omitempty"`
	Kind                refs.ResourceKind `json:"kind,omitempty"`
	Name                string            `json:"name,omitempty"`
	Description         string            `json:"description,omitempty"`
	DefaultCurrency     string            `json:"default_currency,omitempty"`
	AllowFolders        bool              `json:"allow_folders,omitempty"`
	Keywords            []string          `json:"keywords,omitempty"`
	TemplateKeys        []string          `json:"template_keys,omitempty"`
	Values              map[string]any    `json:"values,omitempty"`
	InheritPermissions  bool              `json:"inherit_permissions,omitempty"`
	Version             int               `json:"version,omitempty"`
	HeadRevisionID      *string           `json:"head_revision_id,omitempty"`
	PublishedRevisionID *string           `json:"published_revision_id,omitempty"`
	SchemaHeadID        *string           `json:"schema_head_id,omitempty"`
	NextRevisionNumber  int               `json:"next_revision_number,omitempty"`
	PublishingEnabled   bool              `json:"publishing_enabled,omitempty"`
	Versioning          Versioning        `json:"versioning,omitempty"`
	MaxVersions         int               `json:"max_versions,omitempty"`
	WebdavEnabled       bool              `json:"webdav_enabled,omitempty"`
	DuplicatePolicy     DuplicatePolicy   `json:"duplicate_policy,omitempty"`
	UpdatedAt           time.Time         `json:"updated_at,omitempty"`
	CreatedBy           string            `json:"created_by,omitempty"`
	UpdatedBy           string            `json:"updated_by,omitempty"`
	ScopeID             *string           `json:"scope_id,omitempty"`
	NameKey             *string           `json:"-"`
	DeletedAt           *time.Time        `json:"-"`
	DeletedBy           *string           `json:"-"`
}

// Collection reports whether r is a list or library.
func (r Resource) Collection() bool { return r.Kind == refs.KindList || r.Kind == refs.KindLibrary }

// Content reports whether r is a folder or item.
func (r Resource) Content() bool { return r.Kind == refs.KindFolder || r.Kind == refs.KindItem }

// State selects resources by deletion.
type State uint8

const (
	AnyState State = iota
	Live
	Deleted
)

// Reader limits a selection to the resources a principal may take an action
// on. A Surface other than empty also requires items to have the surface
// the principal reads: head, published or auto.
type Reader struct {
	Principal access.Principal
	Action    access.Action
	Surface   string
}

// Filter selects resources in ID order. A nil list does not filter; an
// empty one selects nothing. Zero fields do not filter.
type Filter struct {
	Archive                            refs.ArchiveSelection
	IDs                                []string
	Kinds                              []refs.ResourceKind
	WorkspaceID, ParentID, ContainerID string
	ContentTypeID, PersonalOwnerID     string
	SystemKey                          SystemKey
	Name, NameKey                      string
	// FoldedName matches names that fold to it, as nameKey folds them.
	FoldedName string
	// PublishedName matches a name key or the folded name an item shows
	// on its published surface.
	PublishedName string
	ExceptID      string
	// ScopeID selects a permission scope; a pointer to empty selects
	// resources without one.
	ScopeID *string
	// Below selects a resource and its descendants.
	Below string
	State State
	// DeletedAt selects the tombstones of one deletion.
	DeletedAt *time.Time
	// WithHead selects resources with a head revision.
	WithHead bool
	// Unpublished selects items whose head is not their published revision.
	Unpublished bool
	// Publishing selects collections by whether they publish explicitly.
	Publishing *bool
	Reader     *Reader
	// DeletionRoots leaves out tombstones deleted together with their parent.
	DeletionRoots bool
	// BeforeDeletion pages NewestDeletion: tombstones deleted before At, or
	// at At with a smaller ID.
	BeforeDeletion *Cursor
	AfterID        string
	Order          Order
	Limit          int
	// Summary leaves Values empty, without reading them.
	Summary bool
}

// Order sorts selected resources.
type Order uint8

const (
	ByID Order = iota
	// NewestDeletion sorts tombstones by deletion, newest first, then by ID
	// descending.
	NewestDeletion
)

// Cursor is a position in deletion order.
type Cursor struct {
	At time.Time
	ID string
}

func (f Filter) empty() bool { return f.IDs != nil && len(f.IDs) == 0 }

// Set holds the columns a write changes. Nil fields keep their value; an
// empty identifier or name key stores NULL.
type Set struct {
	WorkspaceID, ParentID, ContainerID, ScopeID, ContentTypeID *string
	HeadRevisionID, PublishedRevisionID, SchemaHeadID          *string
	NameKey                                                    *string
	Name, Description, DefaultCurrency                         *string
	AllowFolders, InheritPermissions                           *bool
	PublishingEnabled, WebdavEnabled                           *bool
	Versioning                                                 *Versioning
	MaxVersions, NextRevisionNumber                            *int
	DuplicatePolicy                                            *DuplicatePolicy
	Keywords, TemplateKeys                                     *[]string
	Values                                                     json.RawMessage
	// Archived archives an item; a zero At returns it to active.
	Archived *Archival
	// Deleted marks a tombstone; a zero At restores the resource.
	Deleted *Deletion
	// Version is the version the write stores; 0 keeps it.
	Version   int
	UpdatedBy *string
	UpdatedAt time.Time
}

// Deletion is when and by whom a resource was deleted.
type Deletion struct {
	At time.Time
	By string
}

// Archival is when and by whom an item was archived.
type Archival struct {
	At time.Time
	By string
}

// TreeFilter selects the resources of a subtree, ancestors before
// descendants. Zero fields do not filter.
type TreeFilter struct {
	// State follows only live or only deleted descendants.
	State State
	// DeletedAt follows only the descendants of one deletion.
	DeletedAt *time.Time
}

// Queries are the statements a driver runs for resources. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	taxonomy.Queries
	catalog.Queries
	surfaces.Queries
	grants.Queries
	live.Queries
	revisions.Queries
	FindResource(ctx context.Context, id string) (Resource, error)
	FindResources(ctx context.Context, f Filter) ([]Resource, error)
	ResourceIDs(ctx context.Context, f Filter) ([]string, error)
	// ResourceActions reports which of the access actions the principal may
	// take on a resource, in one statement.
	ResourceActions(ctx context.Context, id string, p access.Principal) (Actions, error)
	CountResources(ctx context.Context, f Filter) (int, error)
	// InsertResource stores r with the values JSON, ignoring r.Values.
	InsertResource(ctx context.Context, r Resource, values json.RawMessage) error
	// UpdateResource writes s to a resource whose version is version, or to
	// the resource whatever its version when version is 0.
	UpdateResource(ctx context.Context, id string, version int, s Set) (bool, error)
	DeleteResource(ctx context.Context, id string) error
	// Subtree returns the IDs of a resource and its descendants f selects,
	// ancestors first.
	Subtree(ctx context.Context, id string, f TreeFilter) ([]string, error)
	// IsAncestor reports whether ancestor is above id.
	IsAncestor(ctx context.Context, id, ancestor string) (bool, error)
	// InheritScope sets the scope of a resource's inheriting descendants.
	InheritScope(ctx context.Context, id, scopeID string) error
	// FolderHeight counts the folder levels of a live subtree, excluding a
	// terminal item, looking at most limit levels down.
	FolderHeight(ctx context.Context, id string, limit int) (int, error)
	// ClearWorkspace tombstones a workspace's resources and deletes what
	// they own: links, runs, workflows, events, operations, synchronization
	// state, views, revisions, files, grants and catalog attachments. It
	// queues the stored files for deletion.
	ClearWorkspace(ctx context.Context, workspaceID, actor string, at time.Time) error
	// MoveItemRuns moves the runs about an item to a workspace.
	MoveItemRuns(ctx context.Context, itemID, workspaceID string) error
}

// Repo applies the hierarchy rules for every engine. read runs single
// statements; write runs a function in one transaction, joining the
// caller's when there is one.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

// Resource returns a resource, deleted or not, or dberr.ErrNotFound.
func (r *Repo) Resource(ctx context.Context, id string) (*Resource, error) {
	row, err := r.read.FindResource(ctx, id)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Resources lists the resources f selects.
func (r *Repo) Resources(ctx context.Context, f Filter) ([]*Resource, error) {
	if f.empty() {
		return []*Resource{}, nil
	}
	rows, err := r.read.FindResources(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]*Resource, len(rows))
	for i := range rows {
		out[i] = &rows[i]
	}
	return out, nil
}

// One returns the only resource f selects, or dberr.ErrNotFound when it
// selects none or several.
func (r *Repo) One(ctx context.Context, f Filter) (*Resource, error) {
	f.Limit = 2
	rows, err := r.Resources(ctx, f)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, dberr.ErrNotFound
	}
	return rows[0], nil
}

// IDs lists the IDs of the resources f selects.
func (r *Repo) IDs(ctx context.Context, f Filter) ([]string, error) {
	if f.empty() {
		return []string{}, nil
	}
	return r.read.ResourceIDs(ctx, f)
}

// Actions are the access actions a principal may take on a resource.
type Actions struct {
	Read, ReadDraft, Write, Publish, Manage bool
}

// Actions reports which access actions the principal may take on the
// resource id, as Rows would answer for each, in one statement.
func (r *Repo) Actions(ctx context.Context, id string, p access.Principal) (Actions, error) {
	return r.read.ResourceActions(ctx, id, p)
}

// Count counts the resources f selects.
func (r *Repo) Count(ctx context.Context, f Filter) (int, error) {
	if f.empty() {
		return 0, nil
	}
	return r.read.CountResources(ctx, f)
}

// Exists reports whether f selects a resource.
func (r *Repo) Exists(ctx context.Context, f Filter) (bool, error) {
	f.Limit = 1
	ids, err := r.IDs(ctx, f)
	return len(ids) > 0, err
}

// Subtree returns a resource's subtree, ancestors first.
func (r *Repo) Subtree(ctx context.Context, id string, f TreeFilter) ([]*Resource, error) {
	ids, err := r.read.Subtree(ctx, id, f)
	if err != nil {
		return nil, err
	}
	rows, err := r.Resources(ctx, Filter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*Resource, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	out := make([]*Resource, 0, len(ids))
	for _, id := range ids {
		if row := byID[id]; row != nil {
			out = append(out, row)
		}
	}
	return out, nil
}

// SubtreeIDs returns the IDs of a resource's subtree, ancestors first.
func (r *Repo) SubtreeIDs(ctx context.Context, id string, f TreeFilter) ([]string, error) {
	return r.read.Subtree(ctx, id, f)
}

// FolderHeight counts the folder levels of a live subtree, excluding a
// terminal item, looking at most limit levels down.
func (r *Repo) FolderHeight(ctx context.Context, id string, limit int) (int, error) {
	return r.read.FolderHeight(ctx, id, limit)
}
