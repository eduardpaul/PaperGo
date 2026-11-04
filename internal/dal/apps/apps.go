// Package apps owns the apps of workspaces for every database driver: the
// apps and their immutable versions. Drivers implement Queries; this package
// decides what those statements mean.
package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/jsonutil"
)

// State is where an app is in its authoring lifecycle: a draft is seen only
// by those who write apps, a published app by the workspace's readers.
type State string

const (
	StateDraft     State = "draft"
	StatePublished State = "published"
)

func (s State) Valid() bool { return s == StateDraft || s == StatePublished }

// App is a stored app. Parameters keep numeric literals exactly.
type App struct {
	ID, WorkspaceID, Key, Name, Description, CreatedBy, UpdatedBy string
	State                                                         State
	Parameters                                                    map[string]any
	CurrentVersion, Version                                       int
	CreatedAt, UpdatedAt                                          time.Time
}

// Content is what a version holds: the source, its compiled bundle with that
// bundle's hash, and the parameters form the source declares.
type Content struct {
	Source, Bundle, BundleHash string
	ParametersSchema           json.RawMessage
}

func (c Content) equal(other Content) bool {
	return c.Source == other.Source && c.BundleHash == other.BundleHash && jsonutil.Equal(c.ParametersSchema, other.ParametersSchema)
}

// Version is an immutable version of an app.
type Version struct {
	ID, AppID, CreatedBy string
	Number               int
	Content
	CreatedAt time.Time
}

// CreateInput describes an app and its first version. A missing ID is
// generated, a missing state is draft and missing parameters are an empty
// object.
type CreateInput struct {
	ID, WorkspaceID, Key, Name, Description string
	State                                   State
	Parameters                              map[string]any
	Content                                 Content
	CreatedBy                               string
}

// Changes names the mutable settings of an app; nil leaves a setting. The
// key and workspace never change.
type Changes struct {
	Name, Description *string
	State             *State
	Parameters        *map[string]any
}

// Filter selects apps, ordered by name.
type Filter struct {
	WorkspaceID, Key, Name, ExcludeID string
	State                             State
}

// Queries are the statements a driver runs for apps. Each method executes on
// the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	FindApp(ctx context.Context, id string) (App, error)
	Apps(ctx context.Context, f Filter) ([]App, error)
	InsertApp(ctx context.Context, a App) error
	// UpdateApp writes a's mutable state when the stored version is from.
	UpdateApp(ctx context.Context, a App, from int64) error
	// DeleteApp deletes an app at version from, and its versions with it.
	DeleteApp(ctx context.Context, id string, from int64) error
	// AppVersions returns an app's versions, newest first.
	AppVersions(ctx context.Context, appID string) ([]Version, error)
	AppVersion(ctx context.Context, appID string, number int64) (Version, error)
	InsertAppVersion(ctx context.Context, v Version) error
}

// Repo runs app reads on its connection and changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds app rules to a driver. write runs fn atomically, inside the
// caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func (r *Repo) Find(ctx context.Context, id string) (App, error) { return r.read.FindApp(ctx, id) }

func (r *Repo) List(ctx context.Context, f Filter) ([]App, error) { return r.read.Apps(ctx, f) }

// Versions returns an app's versions, newest first.
func (r *Repo) Versions(ctx context.Context, id string) ([]Version, error) {
	return r.read.AppVersions(ctx, id)
}

func (r *Repo) Version(ctx context.Context, id string, number int64) (Version, error) {
	return r.read.AppVersion(ctx, id, number)
}

func check(name string, state State, content Content) error {
	if name == "" || !state.Valid() || content.Source == "" || content.Bundle == "" || len(content.BundleHash) != 64 || len(content.ParametersSchema) == 0 {
		return fmt.Errorf("%w: an app needs a name, a valid state, a source, its bundle and its parameters form", dberr.ErrCheckViolation)
	}
	return nil
}

// Create records an app in a live workspace with its first version.
func (r *Repo) Create(ctx context.Context, in CreateInput) (App, error) {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	if in.State == "" {
		in.State = StateDraft
	}
	if in.Parameters == nil {
		in.Parameters = map[string]any{}
	}
	if err := check(in.Name, in.State, in.Content); err != nil {
		return App{}, err
	}
	at := now()
	a := App{
		ID: in.ID, WorkspaceID: in.WorkspaceID, Key: in.Key, Name: in.Name, Description: in.Description, CreatedBy: in.CreatedBy, UpdatedBy: in.CreatedBy,
		State: in.State, Parameters: in.Parameters, CurrentVersion: 1, Version: 1, CreatedAt: at, UpdatedAt: at,
	}
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, in.WorkspaceID); err != nil {
			return err
		}
		ws, err := q.Resource(ctx, in.WorkspaceID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || ws.Kind != refs.KindWorkspace || ws.Deleted {
			return fmt.Errorf("%w: an app belongs to a live workspace", dberr.ErrInvalidContainment)
		}
		if err := q.InsertApp(ctx, a); err != nil {
			return err
		}
		return q.InsertAppVersion(ctx, Version{ID: uuid.NewString(), AppID: a.ID, CreatedBy: in.CreatedBy, Number: 1, Content: in.Content, CreatedAt: at})
	})
	if err != nil {
		return App{}, err
	}
	return a, nil
}

// lock returns the current app after serializing writers of its workspace.
func lock(ctx context.Context, q Queries, id string) (App, error) {
	a, err := q.FindApp(ctx, id)
	if err != nil {
		return App{}, err
	}
	if err := q.LockWorkspace(ctx, a.WorkspaceID); err != nil {
		return App{}, err
	}
	return q.FindApp(ctx, id)
}

// Revise applies changes to an app read at version, as subject, and returns
// it with its current version. Content that differs from the current one
// becomes the next version; nil content keeps it.
func (r *Repo) Revise(ctx context.Context, id string, version int, subject string, content *Content, in Changes) (App, Version, error) {
	var a App
	var current Version
	err := r.write(ctx, func(q Queries) error {
		var err error
		if a, err = lock(ctx, q, id); err != nil {
			return err
		}
		if a.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if current, err = q.AppVersion(ctx, id, int64(a.CurrentVersion)); err != nil {
			return err
		}
		at := now()
		from := int64(a.Version)
		a.Version++
		a.UpdatedAt, a.UpdatedBy = at, subject
		if in.Name != nil {
			a.Name = *in.Name
		}
		if in.Description != nil {
			a.Description = *in.Description
		}
		if in.State != nil {
			a.State = *in.State
		}
		if in.Parameters != nil {
			a.Parameters = *in.Parameters
		}
		if content != nil && !content.equal(current.Content) {
			current = Version{ID: uuid.NewString(), AppID: id, CreatedBy: subject, Number: a.CurrentVersion + 1, Content: *content, CreatedAt: at}
			if err = q.InsertAppVersion(ctx, current); err != nil {
				return err
			}
			a.CurrentVersion = current.Number
		}
		if err = check(a.Name, a.State, current.Content); err != nil {
			return err
		}
		return q.UpdateApp(ctx, a, from)
	})
	if err != nil {
		return App{}, Version{}, err
	}
	return a, current, nil
}

// Delete deletes an app read at version, with its versions.
func (r *Repo) Delete(ctx context.Context, id string, version int) error {
	return r.write(ctx, func(q Queries) error {
		a, err := lock(ctx, q, id)
		if err != nil {
			return err
		}
		if a.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		return q.DeleteApp(ctx, id, int64(version))
	})
}
