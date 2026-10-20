// Package refs declares the statements domains share to check the records
// they refer to and to serialize changes within a workspace. A domain embeds
// Queries in its own, so every driver implements them once.
package refs

import (
	"context"
	"strings"
	"time"
)

// ResourceKind is what a resource is.
type ResourceKind string

const (
	KindWorkspace ResourceKind = "workspace"
	KindList      ResourceKind = "list"
	KindLibrary   ResourceKind = "library"
	KindFolder    ResourceKind = "folder"
	KindItem      ResourceKind = "item"
)

func (k ResourceKind) Valid() bool {
	switch k {
	case KindWorkspace, KindList, KindLibrary, KindFolder, KindItem:
		return true
	}
	return false
}

// Resource is the containment state of a resource another record refers to.
// An empty ContainerID means the resource is not in a collection.
type Resource struct {
	Kind                     ResourceKind
	WorkspaceID, ContainerID string
	Deleted                  bool
	CreatedAt                time.Time
	CreatedBy                string
}

// Workflow is the ownership state of a workflow another record refers to.
type Workflow struct {
	WorkspaceID string
	Deleted     bool
}

// Queries are the shared reference statements. Each method executes on the
// transaction or connection the driver was bound to.
type Queries interface {
	// LockWorkspace serializes changes within a workspace.
	LockWorkspace(ctx context.Context, id string) error
	Resource(ctx context.Context, id string) (Resource, error)
	Workflow(ctx context.Context, id string) (Workflow, error)
	// RunInWorkflow reports whether a run belongs to a workflow and workspace.
	RunInWorkflow(ctx context.Context, runID, workflowID, workspaceID string) (bool, error)
	// RunInWorkspace reports whether a run belongs to a workspace.
	RunInWorkspace(ctx context.Context, runID, workspaceID string) (bool, error)
	// PrincipalExists reports whether a user or group exists.
	PrincipalExists(ctx context.Context, kind, id string) (bool, error)
}

// Surface is which revision of an item a projection or index reflects.
type Surface string

const (
	SurfaceHead      Surface = "head"
	SurfacePublished Surface = "published"
)

func (s Surface) Valid() bool { return s == SurfaceHead || s == SurfacePublished }

// FoldName is the case-insensitive form of a name: item surfaces store it as
// folded_name, and name searches fold their text the same way.
func FoldName(name string) string { return strings.ToLower(name) }
