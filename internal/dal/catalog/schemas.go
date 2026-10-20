package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
)

// SchemaRevision freezes a collection's effective schema: its fields and
// content types when the revision was recorded. Revisions number from 1 per
// collection, and the newest is the collection's schema head.
type SchemaRevision struct {
	ID             string          `json:"id"`
	CreatedAt      time.Time       `json:"created_at"`
	ContainerID    string          `json:"container_id"`
	RevisionNumber int             `json:"revision_number"`
	Definition     json.RawMessage `json:"definition"`
	CreatedBy      string          `json:"created_by"`
}

// SchemaFilter selects revisions ordered by collection and number.
type SchemaFilter struct {
	IDs         []string
	ContainerID string
	// AfterNumber selects numbers after it; Latest selects the newest only.
	AfterNumber int
	Latest      bool
	Limit       int
}

// Template is a workspace's reusable schema: fields and content types that
// collections adopt.
type Template struct {
	ID          string          `json:"id"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Version     int             `json:"version"`
	WorkspaceID string          `json:"workspace_id"`
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Definition  json.RawMessage `json:"definition"`
}

// TemplateFilter selects a workspace's templates ordered by id.
type TemplateFilter struct {
	WorkspaceID, AfterID string
	Limit                int
}

// SchemaQueries are the statements a driver runs for schema revisions and
// templates.
type SchemaQueries interface {
	SchemaRevision(ctx context.Context, id string) (*SchemaRevision, error)
	SchemaRevisions(ctx context.Context, f SchemaFilter) ([]*SchemaRevision, error)
	InsertSchemaRevision(ctx context.Context, r *SchemaRevision) error
	// SetSchemaHead points a collection at its newest schema revision.
	SetSchemaHead(ctx context.Context, containerID, revisionID string, at time.Time) error
	Template(ctx context.Context, id string) (*Template, error)
	Templates(ctx context.Context, f TemplateFilter) ([]*Template, error)
	InsertTemplate(ctx context.Context, t *Template) error
	// UpdateTemplate writes t's name, description and definition when its
	// version still is t.Version-1.
	UpdateTemplate(ctx context.Context, t *Template) (bool, error)
}

// Schema returns a schema revision, or dberr.ErrNotFound.
func (r *Repo) Schema(ctx context.Context, id string) (*SchemaRevision, error) {
	return r.read.SchemaRevision(ctx, id)
}

func (r *Repo) Schemas(ctx context.Context, f SchemaFilter) ([]*SchemaRevision, error) {
	if f.IDs != nil && len(f.IDs) == 0 {
		return []*SchemaRevision{}, nil
	}
	return r.read.SchemaRevisions(ctx, f)
}

// RecordSchema freezes definition as a live list's or library's next schema
// revision and makes it the collection's schema head.
func (r *Repo) RecordSchema(ctx context.Context, containerID, createdBy string, definition json.RawMessage) (*SchemaRevision, error) {
	var out *SchemaRevision
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, containerID); err != nil {
			return err
		}
		if err := collection(ctx, q, containerID); err != nil {
			return err
		}
		last, err := q.SchemaRevisions(ctx, SchemaFilter{ContainerID: containerID, Latest: true})
		if err != nil {
			return err
		}
		out = &SchemaRevision{ID: uuid.NewString(), CreatedAt: time.Now().UTC(), ContainerID: containerID, RevisionNumber: 1, Definition: definition, CreatedBy: createdBy}
		if len(last) > 0 {
			out.RevisionNumber = last[0].RevisionNumber + 1
		}
		if err := q.InsertSchemaRevision(ctx, out); err != nil {
			return err
		}
		return q.SetSchemaHead(ctx, containerID, out.ID, out.CreatedAt)
	})
	return out, err
}

// Template returns a template, or dberr.ErrNotFound.
func (r *Repo) Template(ctx context.Context, id string) (*Template, error) {
	return r.read.Template(ctx, id)
}

func (r *Repo) Templates(ctx context.Context, f TemplateFilter) ([]*Template, error) {
	return r.read.Templates(ctx, f)
}

// CreateTemplate adds a template to a workspace; keys are unique within it.
func (r *Repo) CreateTemplate(ctx context.Context, t Template) (*Template, error) {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	t.Version, t.CreatedAt = 1, time.Now().UTC()
	t.UpdatedAt = t.CreatedAt
	return &t, r.write(ctx, func(q Queries) error { return q.InsertTemplate(ctx, &t) })
}

// SaveTemplate changes a template's name, description and definition at
// version.
func (r *Repo) SaveTemplate(ctx context.Context, in Template, version int) (*Template, error) {
	var out *Template
	err := r.write(ctx, func(q Queries) error {
		t, err := q.Template(ctx, in.ID)
		if err != nil {
			return err
		}
		if t.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		t.Name, t.Description, t.Definition, t.Version, t.UpdatedAt = in.Name, in.Description, in.Definition, version+1, time.Now().UTC()
		out = t
		return saved(q.UpdateTemplate(ctx, t))
	})
	return out, err
}
