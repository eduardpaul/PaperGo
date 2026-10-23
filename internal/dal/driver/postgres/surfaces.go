package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/dm"
	"github.com/stephenafamo/bob/dialect/psql/im"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/dialect/psql/um"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/surfaces"
)

func (q Queries) Surfaces(ctx context.Context, f surfaces.Filter) ([]surfaces.Surface, error) {
	c := models.ItemSurfaces.Columns
	query := models.ItemSurfaces.Query(sm.OrderBy(c.ItemID), sm.OrderBy(c.Surface))
	if f.Fields != nil {
		query.Apply(sm.Columns(c.Except("payload")))
	}
	if f.IDs != nil {
		query.Apply(sm.Where(c.ID.In(psql.Arg(anys(f.IDs)...))))
	}
	if f.ItemIDs != nil {
		query.Apply(sm.Where(c.ItemID.In(psql.Arg(anys(f.ItemIDs)...))))
	}
	if f.ContainerIDs != nil {
		query.Apply(sm.Where(c.ContainerID.In(psql.Arg(anys(f.ContainerIDs)...))))
	}
	if f.ItemID != "" {
		query.Apply(sm.Where(c.ItemID.EQ(psql.Arg(f.ItemID))))
	}
	if f.ContainerID != "" {
		query.Apply(sm.Where(c.ContainerID.EQ(psql.Arg(f.ContainerID))))
	}
	if f.WorkspaceID != "" {
		query.Apply(sm.Where(c.WorkspaceID.EQ(psql.Arg(f.WorkspaceID))))
	}
	if f.Surface != "" {
		query.Apply(sm.Where(c.Surface.EQ(psql.Arg(string(f.Surface)))))
	}
	if f.RevisionID != "" {
		query.Apply(sm.Where(c.RevisionID.EQ(psql.Arg(f.RevisionID))))
	}
	if f.AfterItemID != "" {
		query.Apply(sm.Where(c.ItemID.GT(psql.Arg(f.AfterItemID))))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]surfaces.Surface, len(rows))
	for i, row := range rows {
		out[i] = surfaceRecord(row)
	}
	return out, nil
}

// SurfaceFields reads each key as text with the -> operator, which returns
// the key's JSON or NULL when the payload lacks it.
func (q Queries) SurfaceFields(ctx context.Context, ids, keys []string) (map[string]json.RawMessage, error) {
	columns := make([]string, len(keys))
	args := make([]any, 0, len(keys)+len(ids))
	for i, key := range keys {
		args = append(args, key)
		columns[i] = fmt.Sprintf(", (payload -> $%d)::text", len(args))
	}
	marks := make([]string, len(ids))
	for i, id := range ids {
		args = append(args, id)
		marks[i] = fmt.Sprintf("$%d", len(args))
	}
	rows, err := q.exec.QueryContext(ctx, "SELECT id"+strings.Join(columns, "")+" FROM item_surfaces WHERE id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	return surfaceFields(rows, keys)
}

func (q Queries) InsertSurface(ctx context.Context, s surfaces.Surface) error {
	_, err := models.ItemSurfaces.Insert(&models.ItemSurfaceSetter{
		ID: omit.From(s.ID), CreatedAt: omit.From(s.CreatedAt), ItemID: omit.From(s.ItemID), ContainerID: omit.From(s.ContainerID),
		WorkspaceID: omit.From(s.WorkspaceID), Surface: omit.From(string(s.Surface)), RevisionID: omit.From(s.RevisionID), Name: omit.From(s.Name), FoldedName: omit.From(refs.FoldName(s.Name)),
		Keywords: omit.From(types.NewJSON(s.Keywords)), Payload: omit.From(types.NewJSON(s.Payload)), ItemCreatedAt: omit.From(s.ItemCreatedAt),
		ItemCreatedBy: omit.From(s.ItemCreatedBy), ModifiedAt: omit.From(s.ModifiedAt), ModifiedBy: omit.From(s.ModifiedBy),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateSurface(ctx context.Context, s surfaces.Surface) error {
	set := &models.ItemSurfaceSetter{
		ContainerID: omit.From(s.ContainerID), WorkspaceID: omit.From(s.WorkspaceID), RevisionID: omit.From(s.RevisionID), Name: omit.From(s.Name), FoldedName: omit.From(refs.FoldName(s.Name)),
		Keywords: omit.From(types.NewJSON(s.Keywords)), Payload: omit.From(types.NewJSON(s.Payload)), ModifiedAt: omit.From(s.ModifiedAt), ModifiedBy: omit.From(s.ModifiedBy),
	}
	_, err := models.ItemSurfaces.Update(set.UpdateMod(), models.UpdateWhere.ItemSurfaces.ID.EQ(s.ID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteSurface(ctx context.Context, id string) error {
	_, err := models.ItemSurfaces.Delete(models.DeleteWhere.ItemSurfaces.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) TouchSurfaces(ctx context.Context, itemIDs []string, at time.Time, actor string) error {
	set := &models.ItemSurfaceSetter{ModifiedAt: omit.From(at), ModifiedBy: omit.From(actor)}
	_, err := models.ItemSurfaces.Update(set.UpdateMod(), models.UpdateWhere.ItemSurfaces.ItemID.In(itemIDs...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) FieldValues(ctx context.Context, f surfaces.ValueFilter) ([]surfaces.FieldValue, error) {
	c := models.FieldValues.Columns
	query := models.FieldValues.Query(sm.OrderBy(c.FieldKey), sm.OrderBy(c.Ordinal))
	for _, w := range valueFilter(f) {
		query.Apply(sm.Where(w))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]surfaces.FieldValue, len(rows))
	for i, row := range rows {
		out[i] = surfaces.FieldValue{
			ID: row.ID, SurfaceID: row.SurfaceID, ContainerID: row.ContainerID, ItemID: row.ItemID, Surface: refs.Surface(row.Surface),
			FieldKey: row.FieldKey, FieldType: catalog.FieldType(row.FieldType), Scale: row.Scale, Ordinal: row.Ordinal,
			ValueText: row.ValueText.Ptr(), ValueInteger: row.ValueInteger.Ptr(), ValueNumeric: row.ValueNumeric.Ptr(), ValueBoolean: row.ValueBoolean.Ptr(),
			AppliesParents: row.AppliesParents,
		}
	}
	return out, nil
}

func valueFilter(f surfaces.ValueFilter) []bob.Expression {
	c := models.FieldValues.Columns
	var out []bob.Expression
	if f.SurfaceID != "" {
		out = append(out, c.SurfaceID.EQ(psql.Arg(f.SurfaceID)))
	}
	if f.ItemID != "" {
		out = append(out, c.ItemID.EQ(psql.Arg(f.ItemID)))
	}
	if f.ContainerID != "" {
		out = append(out, c.ContainerID.EQ(psql.Arg(f.ContainerID)))
	}
	if f.FieldKey != "" {
		out = append(out, c.FieldKey.EQ(psql.Arg(f.FieldKey)))
	}
	if f.Surface != "" {
		out = append(out, c.Surface.EQ(psql.Arg(string(f.Surface))))
	}
	if f.ChoiceHeads {
		out = append(out, c.FieldType.EQ(psql.Arg(string(catalog.FieldChoice))), c.Ordinal.EQ(psql.Arg(0)), c.ValueText.IsNotNull())
	}
	return out
}

// fieldValueBatch keeps a multi-row insert of field values (15 columns)
// below both engines' parameter limits.
const fieldValueBatch = 500

func (q Queries) InsertFieldValues(ctx context.Context, values []surfaces.FieldValue) error {
	at := time.Now().UTC()
	for start := 0; start < len(values); start += fieldValueBatch {
		batch := values[start:min(start+fieldValueBatch, len(values))]
		rows := make([]bob.Mod[*dialect.InsertQuery], len(batch))
		for i, v := range batch {
			rows[i] = &models.FieldValueSetter{
				ID: omit.From(v.ID), CreatedAt: omit.From(at), SurfaceID: omit.From(v.SurfaceID), ContainerID: omit.From(v.ContainerID),
				ItemID: omit.From(v.ItemID), Surface: omit.From(string(v.Surface)), FieldKey: omit.From(v.FieldKey), FieldType: omit.From(string(v.FieldType)),
				Scale: omit.From(v.Scale), Ordinal: omit.From(v.Ordinal), ValueText: omitnull.FromPtr(v.ValueText), ValueInteger: omitnull.FromPtr(v.ValueInteger),
				ValueNumeric: omitnull.FromPtr(v.ValueNumeric), ValueBoolean: omitnull.FromPtr(v.ValueBoolean), AppliesParents: omit.From(v.AppliesParents),
			}
		}
		if _, err := models.FieldValues.Insert(rows...).Exec(ctx, q.exec); err != nil {
			return dberr.Translate(err)
		}
	}
	return nil
}

func (q Queries) DeleteFieldValues(ctx context.Context, f surfaces.ValueFilter) error {
	query := models.FieldValues.Delete()
	for _, w := range valueFilter(f) {
		query.Apply(dm.Where(w))
	}
	_, err := query.Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) AddGroupCount(ctx context.Context, g surfaces.GroupCount, delta int) error {
	if delta > 0 {
		_, err := models.GroupCounts.Insert(&models.GroupCountSetter{
			ContainerID: omit.From(g.ContainerID), Surface: omit.From(string(g.Surface)), FieldKey: omit.From(g.FieldKey), Value: omit.From(g.Value), Count: omit.From(int64(delta)),
		}, im.OnConflict("container_id", "surface", "field_key", "value").DoUpdate(
			im.SetCol("count").To(models.GroupCounts.Columns.Count.OP("+", psql.Arg(delta))),
		)).Exec(ctx, q.exec)
		return dberr.Translate(err)
	}
	w := models.UpdateWhere.GroupCounts
	if _, err := models.GroupCounts.Update(um.SetCol("count").To(models.GroupCounts.Columns.Count.OP("+", psql.Arg(delta))),
		w.ContainerID.EQ(g.ContainerID), w.Surface.EQ(string(g.Surface)), w.FieldKey.EQ(g.FieldKey), w.Value.EQ(g.Value)).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	d := models.DeleteWhere.GroupCounts
	_, err := models.GroupCounts.Delete(d.ContainerID.EQ(g.ContainerID), d.Surface.EQ(string(g.Surface)), d.FieldKey.EQ(g.FieldKey), d.Value.EQ(g.Value), d.Count.LTE(0)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) GroupCounts(ctx context.Context, containerID string, surface refs.Surface, keys []string) ([]surfaces.GroupCount, error) {
	w := models.SelectWhere.GroupCounts
	rows, err := models.GroupCounts.Query(w.ContainerID.EQ(containerID), w.Surface.EQ(string(surface)), w.FieldKey.In(keys...), w.Count.GT(0),
		sm.OrderBy(models.GroupCounts.Columns.FieldKey), sm.OrderBy(models.GroupCounts.Columns.Value)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]surfaces.GroupCount, len(rows))
	for i, row := range rows {
		out[i] = surfaces.GroupCount{ContainerID: row.ContainerID, Surface: refs.Surface(row.Surface), FieldKey: row.FieldKey, Value: row.Value, Count: int(row.Count)}
	}
	return out, nil
}

func (q Queries) DeleteGroupCounts(ctx context.Context, containerID, fieldKey string) error {
	d := models.DeleteWhere.GroupCounts
	_, err := models.GroupCounts.Delete(d.ContainerID.EQ(containerID), d.FieldKey.EQ(fieldKey)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func surfaceRecord(r *models.ItemSurface) surfaces.Surface {
	keywords := r.Keywords.Val
	if keywords == nil {
		keywords = []string{}
	}
	return surfaces.Surface{
		ID: r.ID, CreatedAt: r.CreatedAt.UTC(), ItemID: r.ItemID, ContainerID: r.ContainerID, WorkspaceID: r.WorkspaceID,
		Surface: refs.Surface(r.Surface), RevisionID: r.RevisionID, Name: r.Name, Keywords: keywords, Payload: r.Payload.Val,
		ItemCreatedAt: r.ItemCreatedAt.UTC(), ItemCreatedBy: r.ItemCreatedBy, ModifiedAt: r.ModifiedAt.UTC(), ModifiedBy: r.ModifiedBy,
	}
}

// surfaceFields reads rows of a surface ID and one JSON value or NULL per
// key into an object of the keys present, by surface ID.
func surfaceFields(rows scan.Rows, keys []string) (map[string]json.RawMessage, error) {
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var id string
		values := make([]sql.NullString, len(keys))
		dest := []any{&id}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, dberr.Translate(err)
		}
		object := map[string]json.RawMessage{}
		for i, v := range values {
			if v.Valid {
				object[keys[i]] = json.RawMessage(v.String)
			}
		}
		raw, err := json.Marshal(object)
		if err != nil {
			return nil, err
		}
		out[id] = raw
	}
	return out, dberr.Translate(rows.Err())
}

func (q Queries) DropSurface(ctx context.Context, containerID string, surface refs.Surface) error {
	d := models.DeleteWhere.ItemSurfaces
	if _, err := models.ItemSurfaces.Delete(d.ContainerID.EQ(containerID), d.Surface.EQ(string(surface))).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	g := models.DeleteWhere.GroupCounts
	_, err := models.GroupCounts.Delete(g.ContainerID.EQ(containerID), g.Surface.EQ(string(surface))).Exec(ctx, q.exec)
	return dberr.Translate(err)
}
