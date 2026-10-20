package postgres

import (
	"context"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/relationships"
)

func (q Queries) RelationshipType(ctx context.Context, id string) (relationships.Type, error) {
	row, err := models.FindRelationshipType(ctx, q.exec, id)
	if err != nil {
		return relationships.Type{}, dberr.Translate(err)
	}
	return relationships.Type{
		ID: row.ID, InverseLabel: row.InverseLabel, Directed: row.Directed, MaxIncoming: limit(row.MaxIncoming.Ptr()), MaxOutgoing: limit(row.MaxOutgoing.Ptr()),
		Attributes: row.Attributes.Val, Version: int(row.Version), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}, nil
}

func (q Queries) InsertRelationshipType(ctx context.Context, t relationships.Type) error {
	_, err := models.RelationshipTypes.Insert(&models.RelationshipTypeSetter{
		ID: omit.From(t.ID), CreatedAt: omit.From(t.CreatedAt), UpdatedAt: omit.From(t.UpdatedAt), InverseLabel: omit.From(t.InverseLabel),
		Directed: omit.From(t.Directed), MaxIncoming: limitSetter(t.MaxIncoming), MaxOutgoing: limitSetter(t.MaxOutgoing),
		Attributes: omit.From(types.NewJSON(t.Attributes)), Version: omit.From(int64(t.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteRelationshipType(ctx context.Context, id string) error {
	_, err := models.RelationshipTypes.Delete(models.DeleteWhere.RelationshipTypes.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

// LockRelationshipType locks the type's row until the transaction ends.
func (q Queries) LockRelationshipType(ctx context.Context, id string) error {
	_, err := q.exec.ExecContext(ctx, `SELECT id FROM relationship_types WHERE id = $1 FOR UPDATE`, id)
	return dberr.Translate(err)
}

func (q Queries) Relationship(ctx context.Context, id string) (relationships.Relationship, error) {
	row, err := models.FindRelationship(ctx, q.exec, id)
	if err != nil {
		return relationships.Relationship{}, dberr.Translate(err)
	}
	return relationshipRecord(row), nil
}

func (q Queries) Relationships(ctx context.Context, f relationships.Filter) ([]relationships.Relationship, error) {
	query := models.Relationships.Query(sm.Where(relationshipWhere(f)), sm.OrderBy(models.Relationships.Columns.ID))
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]relationships.Relationship, len(rows))
	for i, row := range rows {
		out[i] = relationshipRecord(row)
	}
	return out, nil
}

func (q Queries) CountRelationships(ctx context.Context, f relationships.Filter) (int, error) {
	n, err := models.Relationships.Query(sm.Where(relationshipWhere(f))).Count(ctx, q.exec)
	return int(n), dberr.Translate(err)
}

func (q Queries) InsertRelationship(ctx context.Context, r relationships.Relationship) error {
	_, err := models.Relationships.Insert(&models.RelationshipSetter{
		ID: omit.From(r.ID), CreatedAt: omit.From(r.CreatedAt), Metadata: omit.From(types.NewJSON(r.Metadata)), SourceID: omit.From(r.SourceID),
		TargetID: omit.From(r.TargetID), TypeID: nullable.String(r.TypeID), Directed: omit.From(r.Directed), Version: omit.From(int64(r.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateRelationship(ctx context.Context, r relationships.Relationship) (bool, error) {
	set := &models.RelationshipSetter{TypeID: nullable.String(r.TypeID), Metadata: omit.From(types.NewJSON(r.Metadata)), Version: omit.From(int64(r.Version))}
	w := models.UpdateWhere.Relationships
	n, err := models.Relationships.Update(set.UpdateMod(), w.ID.EQ(r.ID), w.Version.EQ(int64(r.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DeleteRelationship(ctx context.Context, id string) error {
	_, err := models.Relationships.Delete(models.DeleteWhere.Relationships.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func relationshipWhere(f relationships.Filter) bob.Expression {
	c := models.Relationships.Columns
	where := []bob.Expression{psql.Raw("TRUE")}
	if len(f.IDs) > 0 {
		where = append(where, c.ID.In(psql.Arg(anys(f.IDs)...)))
	}
	if f.Endpoints != nil {
		where = append(where, psql.Or(c.SourceID.In(psql.Arg(anys(f.Endpoints)...)), c.TargetID.In(psql.Arg(anys(f.Endpoints)...))))
	}
	if f.Endpoint != "" {
		where = append(where, psql.Or(c.SourceID.EQ(psql.Arg(f.Endpoint)), c.TargetID.EQ(psql.Arg(f.Endpoint))))
	}
	if f.SourceID != "" {
		where = append(where, c.SourceID.EQ(psql.Arg(f.SourceID)))
	}
	if f.TargetID != "" {
		where = append(where, c.TargetID.EQ(psql.Arg(f.TargetID)))
	}
	if len(f.TypeIDs) > 0 {
		where = append(where, c.TypeID.In(psql.Arg(anys(f.TypeIDs)...)))
	}
	if f.Untyped {
		where = append(where, c.TypeID.IsNull())
	}
	if f.Directed != nil {
		where = append(where, c.Directed.EQ(psql.Arg(*f.Directed)))
	}
	if f.Workspace != "" {
		where = append(where, psql.Raw(`EXISTS(SELECT 1 FROM resources peer WHERE peer.workspace_id = ? AND peer.id IN ("relationships"."source_id", "relationships"."target_id"))`, f.Workspace))
	}
	if f.AfterID != "" {
		where = append(where, c.ID.GT(psql.Arg(f.AfterID)))
	}
	return psql.And(where...)
}

func relationshipRecord(r *models.Relationship) relationships.Relationship {
	metadata := r.Metadata.Val
	if metadata == nil {
		metadata = map[string]any{}
	}
	return relationships.Relationship{
		ID: r.ID, SourceID: r.SourceID, TargetID: r.TargetID, TypeID: r.TypeID.GetOrZero(), Directed: r.Directed,
		Metadata: metadata, Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(),
	}
}

func limit(n *int64) *int {
	if n == nil {
		return nil
	}
	v := int(*n)
	return &v
}

func limitSetter(n *int) omitnull.Val[int64] {
	if n == nil {
		return omitnull.Val[int64]{}
	}
	return omitnull.From(int64(*n))
}
