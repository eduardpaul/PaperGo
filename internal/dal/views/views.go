// Package views owns saved ways of reading content: the list views of a
// collection and smart folders, personal or shared in a workspace, that
// gather items across collections. Drivers implement Queries; this package
// decides what the records may hold.
package views

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/access"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

// Layout is how a list view shows its items.
type Layout string

const (
	LayoutTable    Layout = "table"
	LayoutBoard    Layout = "board"
	LayoutCalendar Layout = "calendar"
	LayoutGallery  Layout = "gallery"
)

func (l Layout) Valid() bool {
	switch l {
	case LayoutTable, LayoutBoard, LayoutCalendar, LayoutGallery:
		return true
	}
	return false
}

// ListView is a saved query of a collection with the columns it shows. At
// most one view of a collection is its default.
// SystemKey identifies a protected built-in collection view.
type SystemKey string

const SystemArchived SystemKey = "archived"

type ListView struct {
	SystemKey   SystemKey       `json:"system_key,omitempty"`
	ID          string          `json:"id,omitempty"`
	CreatedAt   time.Time       `json:"created_at,omitempty"`
	ContainerID string          `json:"container_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Columns     []string        `json:"columns,omitempty"`
	Query       json.RawMessage `json:"query,omitempty"`
	Layout      Layout          `json:"layout,omitempty"`
	IsDefault   bool            `json:"is_default,omitempty"`
	Version     int             `json:"version,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at,omitempty"`
}

// SmartFolder gathers items across collections. A personal folder has an
// owner; a shared one belongs to a workspace.
type SmartFolder struct {
	ID          string          `json:"id,omitempty"`
	CreatedAt   time.Time       `json:"created_at,omitempty"`
	WorkspaceID *string         `json:"workspace_id,omitempty"`
	OwnerID     *string         `json:"owner_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Definition  json.RawMessage `json:"definition,omitempty"`
	CreatedBy   string          `json:"created_by,omitempty"`
	UpdatedBy   string          `json:"updated_by,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at,omitempty"`
	Version     int             `json:"version,omitempty"`
}

// ViewFilter selects a collection's views ordered by ID. Zero fields do not
// filter.
type ViewFilter struct {
	ContainerID string
	AfterID     string
	Limit       int
}

// FolderOrder sorts selected smart folders.
type FolderOrder uint8

const (
	FoldersByID FolderOrder = iota
	FoldersByName
)

// FolderFilter selects smart folders. Zero fields do not filter.
type FolderFilter struct {
	WorkspaceID string
	// Shared selects workspace folders, those without an owner.
	Shared bool
	// Reader selects the folders a principal owns and the shared folders of
	// workspaces it may read.
	Reader  *access.Principal
	AfterID string
	Order   FolderOrder
	Limit   int
}

// Queries are the statements a driver runs for views. Each method executes
// on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	ListView(ctx context.Context, id string) (ListView, error)
	ListViews(ctx context.Context, f ViewFilter) ([]ListView, error)
	InsertListView(ctx context.Context, v ListView) error
	// UpdateListView writes v's name, columns, query, layout and default
	// flag when its version still is v.Version-1.
	UpdateListView(ctx context.Context, v ListView) (bool, error)
	DeleteListView(ctx context.Context, id string) error
	// ClearDefaultView unsets a collection's default view other than keep.
	ClearDefaultView(ctx context.Context, containerID, keep string, at time.Time) error
	SmartFolder(ctx context.Context, id string) (SmartFolder, error)
	SmartFolders(ctx context.Context, f FolderFilter) ([]SmartFolder, error)
	CountSmartFolders(ctx context.Context, f FolderFilter) (int, error)
	InsertSmartFolder(ctx context.Context, f SmartFolder) error
	// UpdateSmartFolder writes f's name, description, definition and editor
	// when its version still is f.Version-1.
	UpdateSmartFolder(ctx context.Context, f SmartFolder) (bool, error)
	DeleteSmartFolder(ctx context.Context, id string) error
}

// Repo applies the view rules for every engine. read runs single
// statements; write runs a function in one transaction, joining the
// caller's when there is one.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

// ListView returns a view, or dberr.ErrNotFound.
func (r *Repo) ListView(ctx context.Context, id string) (ListView, error) {
	return r.read.ListView(ctx, id)
}

func (r *Repo) ListViews(ctx context.Context, f ViewFilter) ([]ListView, error) {
	return r.read.ListViews(ctx, f)
}

func validView(v ListView) error {
	if v.SystemKey != "" && v.SystemKey != SystemArchived {
		return fmt.Errorf("%w: invalid view system key", dberr.ErrCheckViolation)
	}
	if v.SystemKey == SystemArchived && v.IsDefault {
		return fmt.Errorf("%w: Archived cannot be the opening default", dberr.ErrCheckViolation)
	}
	if !v.Layout.Valid() {
		return fmt.Errorf("%w: invalid view layout %q", dberr.ErrCheckViolation, v.Layout)
	}
	return nil
}

// CreateListView saves a view of a live list or library. A default view
// replaces the collection's previous default.
func (r *Repo) CreateListView(ctx context.Context, v ListView) (ListView, error) {
	if v.Layout == "" {
		v.Layout = LayoutTable
	}
	if err := validView(v); err != nil {
		return ListView{}, err
	}
	err := r.write(ctx, func(q Queries) error {
		c, err := q.Resource(ctx, v.ContainerID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || c.Deleted || c.Kind != refs.KindList && c.Kind != refs.KindLibrary {
			return fmt.Errorf("%w: views belong to lists and libraries", dberr.ErrInvalidContainment)
		}
		if v.ID == "" {
			v.ID = uuid.NewString()
		}
		if v.Columns == nil {
			v.Columns = []string{}
		}
		if len(v.Query) == 0 {
			v.Query = json.RawMessage(`{}`)
		}
		v.CreatedAt = time.Now().UTC()
		v.UpdatedAt, v.Version = v.CreatedAt, 1
		if v.IsDefault {
			if err := q.ClearDefaultView(ctx, v.ContainerID, v.ID, v.CreatedAt); err != nil {
				return err
			}
		}
		return q.InsertListView(ctx, v)
	})
	return v, err
}

// UpdateListView saves v over the view at version. A default view replaces
// the collection's previous default.
func (r *Repo) UpdateListView(ctx context.Context, v ListView, version int) (ListView, error) {
	if err := validView(v); err != nil {
		return ListView{}, err
	}
	var out ListView
	err := r.write(ctx, func(q Queries) error {
		old, err := q.ListView(ctx, v.ID)
		if err != nil {
			return err
		}
		if old.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if old.SystemKey != "" {
			return fmt.Errorf("%w: built-in views cannot be changed", dberr.ErrCheckViolation)
		}
		out = old
		out.Name, out.Columns, out.Query, out.Layout, out.IsDefault = v.Name, v.Columns, v.Query, v.Layout, v.IsDefault
		if out.Columns == nil {
			out.Columns = []string{}
		}
		if len(out.Query) == 0 {
			out.Query = json.RawMessage(`{}`)
		}
		out.Version, out.UpdatedAt = version+1, time.Now().UTC()
		if out.IsDefault {
			if err := q.ClearDefaultView(ctx, out.ContainerID, out.ID, out.UpdatedAt); err != nil {
				return err
			}
		}
		ok, err := q.UpdateListView(ctx, out)
		if err == nil && !ok {
			err = dberr.ErrConcurrentUpdate
		}
		return err
	})
	return out, err
}

// DeleteListView deletes the view at version.
func (r *Repo) DeleteListView(ctx context.Context, id string, version int) error {
	return r.write(ctx, func(q Queries) error {
		v, err := q.ListView(ctx, id)
		if err != nil {
			return err
		}
		if v.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if v.SystemKey != "" {
			return fmt.Errorf("%w: built-in views cannot be deleted", dberr.ErrCheckViolation)
		}
		return q.DeleteListView(ctx, id)
	})
}

// SmartFolder returns a smart folder, or dberr.ErrNotFound.
func (r *Repo) SmartFolder(ctx context.Context, id string) (SmartFolder, error) {
	return r.read.SmartFolder(ctx, id)
}

func (r *Repo) SmartFolders(ctx context.Context, f FolderFilter) ([]SmartFolder, error) {
	return r.read.SmartFolders(ctx, f)
}

// CountSmartFolders counts the smart folders f selects.
func (r *Repo) CountSmartFolders(ctx context.Context, f FolderFilter) (int, error) {
	return r.read.CountSmartFolders(ctx, f)
}

// CreateSmartFolder saves a personal folder, which has an owner, or a
// folder shared in a live workspace.
func (r *Repo) CreateSmartFolder(ctx context.Context, f SmartFolder) (SmartFolder, error) {
	if f.WorkspaceID == nil && f.OwnerID == nil {
		return SmartFolder{}, fmt.Errorf("%w: a smart folder has an owner or a workspace", dberr.ErrCheckViolation)
	}
	err := r.write(ctx, func(q Queries) error {
		if f.WorkspaceID != nil {
			w, err := q.Resource(ctx, *f.WorkspaceID)
			if err != nil && !dberr.IsNotFound(err) {
				return err
			}
			if err != nil || w.Deleted || w.Kind != refs.KindWorkspace {
				return fmt.Errorf("%w: smart folders belong to a workspace", dberr.ErrInvalidContainment)
			}
		}
		if f.ID == "" {
			f.ID = uuid.NewString()
		}
		f.CreatedAt = time.Now().UTC()
		f.UpdatedAt, f.UpdatedBy, f.Version = f.CreatedAt, f.CreatedBy, 1
		return q.InsertSmartFolder(ctx, f)
	})
	return f, err
}

// UpdateSmartFolder saves f's name, description and definition over the
// folder at version. Ownership and workspace do not change.
func (r *Repo) UpdateSmartFolder(ctx context.Context, f SmartFolder, version int) (SmartFolder, error) {
	var out SmartFolder
	err := r.write(ctx, func(q Queries) error {
		old, err := q.SmartFolder(ctx, f.ID)
		if err != nil {
			return err
		}
		if old.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		out = old
		out.Name, out.Description, out.Definition, out.UpdatedBy = f.Name, f.Description, f.Definition, f.UpdatedBy
		out.Version, out.UpdatedAt = version+1, time.Now().UTC()
		ok, err := q.UpdateSmartFolder(ctx, out)
		if err == nil && !ok {
			err = dberr.ErrConcurrentUpdate
		}
		return err
	})
	return out, err
}

// DeleteSmartFolder deletes the folder at version.
func (r *Repo) DeleteSmartFolder(ctx context.Context, id string, version int) error {
	return r.write(ctx, func(q Queries) error {
		f, err := q.SmartFolder(ctx, id)
		if err != nil {
			return err
		}
		if f.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		return q.DeleteSmartFolder(ctx, id)
	})
}
