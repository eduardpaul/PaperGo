package sqlite

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
)

func (q Queries) SchemaRevision(ctx context.Context, id string) (*catalog.SchemaRevision, error) {
	row, err := models.FindSchemaRevision(ctx, q.exec, id)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	return schemaRecord(row), nil
}

func (q Queries) SchemaRevisions(ctx context.Context, f catalog.SchemaFilter) ([]*catalog.SchemaRevision, error) {
	c := models.SchemaRevisions.Columns
	query := models.SchemaRevisions.Query(sm.OrderBy(c.ContainerID), sm.OrderBy(c.RevisionNumber))
	if f.IDs != nil {
		query.Apply(sm.Where(c.ID.In(sqlite.Arg(anys(f.IDs)...))))
	}
	if f.ContainerID != "" {
		query.Apply(sm.Where(c.ContainerID.EQ(sqlite.Arg(f.ContainerID))))
	}
	if f.AfterNumber > 0 {
		query.Apply(sm.Where(c.RevisionNumber.GT(sqlite.Arg(f.AfterNumber))))
	}
	if f.Latest {
		query = models.SchemaRevisions.Query(sm.Where(c.ContainerID.EQ(sqlite.Arg(f.ContainerID))), sm.OrderBy(c.RevisionNumber).Desc(), sm.Limit(1))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]*catalog.SchemaRevision, len(rows))
	for i, row := range rows {
		out[i] = schemaRecord(row)
	}
	return out, nil
}

func (q Queries) InsertSchemaRevision(ctx context.Context, r *catalog.SchemaRevision) error {
	_, err := models.SchemaRevisions.Insert(&models.SchemaRevisionSetter{
		ID: omit.From(r.ID), CreatedAt: omit.From(r.CreatedAt), ContainerID: omit.From(r.ContainerID), RevisionNumber: omit.From(int64(r.RevisionNumber)),
		Definition: omit.From(types.NewJSON(r.Definition)), CreatedBy: omit.From(r.CreatedBy),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetSchemaHead(ctx context.Context, containerID, revisionID string, at time.Time) error {
	set := &models.ResourceSetter{SchemaHeadID: nullable.String(revisionID), UpdatedAt: omit.From(at)}
	_, err := models.Resources.Update(set.UpdateMod(), models.UpdateWhere.Resources.ID.EQ(containerID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) Template(ctx context.Context, id string) (*catalog.Template, error) {
	row, err := models.FindSchemaTemplate(ctx, q.exec, id)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	return templateRecord(row), nil
}

func (q Queries) Templates(ctx context.Context, f catalog.TemplateFilter) ([]*catalog.Template, error) {
	c := models.SchemaTemplates.Columns
	query := models.SchemaTemplates.Query(sm.Where(c.WorkspaceID.EQ(sqlite.Arg(f.WorkspaceID))), sm.OrderBy(c.ID))
	if f.AfterID != "" {
		query.Apply(sm.Where(c.ID.GT(sqlite.Arg(f.AfterID))))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]*catalog.Template, len(rows))
	for i, row := range rows {
		out[i] = templateRecord(row)
	}
	return out, nil
}

func (q Queries) InsertTemplate(ctx context.Context, t *catalog.Template) error {
	_, err := models.SchemaTemplates.Insert(&models.SchemaTemplateSetter{
		ID: omit.From(t.ID), CreatedAt: omit.From(t.CreatedAt), UpdatedAt: omit.From(t.UpdatedAt), WorkspaceID: omit.From(t.WorkspaceID), Key: omit.From(t.Key),
		Name: omit.From(t.Name), Description: omit.From(t.Description), Definition: omit.From(types.NewJSON(t.Definition)), Version: omit.From(int64(t.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateTemplate(ctx context.Context, t *catalog.Template) (bool, error) {
	set := &models.SchemaTemplateSetter{Name: omit.From(t.Name), Description: omit.From(t.Description), Definition: omit.From(types.NewJSON(t.Definition)),
		UpdatedAt: omit.From(t.UpdatedAt), Version: omit.From(int64(t.Version))}
	w := models.UpdateWhere.SchemaTemplates
	n, err := models.SchemaTemplates.Update(set.UpdateMod(), w.ID.EQ(t.ID), w.Version.EQ(int64(t.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func schemaRecord(r *models.SchemaRevision) *catalog.SchemaRevision {
	return &catalog.SchemaRevision{ID: r.ID, CreatedAt: r.CreatedAt.UTC(), ContainerID: r.ContainerID, RevisionNumber: int(r.RevisionNumber), Definition: r.Definition.Val, CreatedBy: r.CreatedBy}
}

func templateRecord(r *models.SchemaTemplate) *catalog.Template {
	return &catalog.Template{ID: r.ID, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Version: int(r.Version), WorkspaceID: r.WorkspaceID, Key: r.Key,
		Name: r.Name, Description: r.Description, Definition: r.Definition.Val}
}
