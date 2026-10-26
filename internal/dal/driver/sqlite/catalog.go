package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/dialect/sqlite/um"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
)

func (q Queries) FieldDefinitions(ctx context.Context, f catalog.FieldFilter) ([]*catalog.FieldDefinition, error) {
	c := models.FieldDefinitions.Columns
	query := models.FieldDefinitions.Query(sm.OrderBy(c.Key), sm.OrderBy(c.ID))
	for _, in := range []struct {
		values []string
		col    string
	}{{f.IDs, "id"}, {f.Keys, "key"}, {fieldTypes(f.Types), "type"}} {
		if in.values == nil {
			continue
		}
		if len(in.values) == 0 {
			return []*catalog.FieldDefinition{}, nil
		}
		query.Apply(sm.Where(sqlite.Quote("field_definitions", in.col).In(sqlite.Arg(anys(in.values)...))))
	}
	if f.ExcludeType != "" {
		query.Apply(sm.Where(c.Type.NE(sqlite.Arg(string(f.ExcludeType)))))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]*catalog.FieldDefinition, len(rows))
	for i, row := range rows {
		if out[i], err = fieldRecord(row); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func fieldTypes(types []catalog.FieldType) []string {
	if types == nil {
		return nil
	}
	out := make([]string, len(types))
	for i, t := range types {
		out[i] = string(t)
	}
	return out
}

func (q Queries) FieldDefinition(ctx context.Context, id string) (*catalog.FieldDefinition, error) {
	row, err := models.FindFieldDefinition(ctx, q.exec, id)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	return fieldRecord(row)
}

func (q Queries) InsertFieldDefinition(ctx context.Context, d *catalog.FieldDefinition) error {
	set, err := fieldSetter(d)
	if err != nil {
		return err
	}
	set.ID, set.CreatedAt, set.Key, set.Type = omit.From(d.ID), omit.From(d.CreatedAt), omit.From(d.Key), omit.From(string(d.Type))
	set.IndexStatus, set.Scale = omit.From(string(d.IndexStatus)), omit.From(int64(d.Scale))
	_, err = models.FieldDefinitions.Insert(set).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateFieldDefinition(ctx context.Context, d *catalog.FieldDefinition) (bool, error) {
	set, err := fieldSetter(d)
	if err != nil {
		return false, err
	}
	w := models.UpdateWhere.FieldDefinitions
	n, err := models.FieldDefinitions.Update(set.UpdateMod(), w.ID.EQ(d.ID), w.Version.EQ(int64(d.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DeleteFieldDefinition(ctx context.Context, id string) error {
	_, err := models.FieldDefinitions.Delete(models.DeleteWhere.FieldDefinitions.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetFieldIndexStatus(ctx context.Context, ids []string, status catalog.IndexStatus) (int, error) {
	set := &models.FieldDefinitionSetter{IndexStatus: omit.From(string(status))}
	n, err := models.FieldDefinitions.Update(set.UpdateMod(), models.UpdateWhere.FieldDefinitions.ID.In(ids...)).Exec(ctx, q.exec)
	return int(n), dberr.Translate(err)
}

func (q Queries) ContentTypes(ctx context.Context, f catalog.ContentTypeFilter) ([]*catalog.ContentType, error) {
	c := models.ContentTypes.Columns
	query := models.ContentTypes.Query(sm.OrderBy(c.Key), sm.OrderBy(c.ID))
	if f.IDs != nil {
		if len(f.IDs) == 0 {
			return []*catalog.ContentType{}, nil
		}
		query.Apply(sm.Where(c.ID.In(sqlite.Arg(anys(f.IDs)...))))
	}
	if f.TemplateKey != "" {
		query.Apply(sm.Where(c.TemplateKey.EQ(sqlite.Arg(f.TemplateKey))))
	}
	if f.ParentID != "" {
		query.Apply(sm.Where(c.ParentID.EQ(sqlite.Arg(f.ParentID))))
	}
	if f.Catalog {
		query.Apply(sm.Where(c.CollectionID.IsNull()))
	}
	if f.CollectionIDs != nil {
		if len(f.CollectionIDs) == 0 {
			return []*catalog.ContentType{}, nil
		}
		query.Apply(sm.Where(c.CollectionID.In(sqlite.Arg(anys(f.CollectionIDs)...))))
	}
	if f.CollectionID != "" {
		query.Apply(sm.Where(c.CollectionID.EQ(sqlite.Arg(f.CollectionID))))
	}
	if (f.CollectionID != "" || f.CollectionIDs != nil) && !f.Detached {
		query.Apply(sm.Where(c.Attached.EQ(sqlite.Arg(true))))
	}
	if f.FieldID != "" {
		query.Apply(sm.Where(sqlite.Raw(`EXISTS(SELECT 1 FROM json_each("content_types"."field_ids") f WHERE f.value = ?)`, f.FieldID)))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]*catalog.ContentType, len(rows))
	for i, row := range rows {
		if out[i], err = contentTypeRecord(row); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (q Queries) ContentType(ctx context.Context, id string) (*catalog.ContentType, error) {
	row, err := models.FindContentType(ctx, q.exec, id)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	return contentTypeRecord(row)
}

func (q Queries) InsertContentType(ctx context.Context, t *catalog.ContentType) error {
	set, err := contentTypeSetter(t)
	if err != nil {
		return err
	}
	set.ID, set.CreatedAt, set.Key, set.Attached, set.IsDefault = omit.From(t.ID), omit.From(t.CreatedAt), omit.From(t.Key), omit.From(t.Attached), omit.From(t.IsDefault)
	if t.TemplateKey != nil {
		set.TemplateKey = nullable.String(*t.TemplateKey)
	}
	if t.ParentID != nil {
		set.ParentID = nullable.String(*t.ParentID)
	}
	if t.CollectionID != nil {
		set.CollectionID = nullable.String(*t.CollectionID)
	}
	if _, err = models.ContentTypes.Insert(set).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	return q.InsertLineage(ctx, t.ID, t.AncestorIDs)
}

func (q Queries) InsertLineage(ctx context.Context, id string, ancestorIDs []string) error {
	lineage := make([]*models.ContentTypeAncestorSetter, 0, len(ancestorIDs)+1)
	for depth, ancestor := range append([]string{id}, ancestorIDs...) {
		lineage = append(lineage, &models.ContentTypeAncestorSetter{ContentTypeID: omit.From(id), AncestorID: omit.From(ancestor), Depth: omit.From(int64(depth))})
	}
	_, err := models.ContentTypeAncestors.Insert(bob.ToMods(lineage...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateContentType(ctx context.Context, t *catalog.ContentType) (bool, error) {
	set, err := contentTypeSetter(t)
	if err != nil {
		return false, err
	}
	w := models.UpdateWhere.ContentTypes
	n, err := models.ContentTypes.Update(set.UpdateMod(), w.ID.EQ(t.ID), w.Version.EQ(int64(t.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) BumpContentTypes(ctx context.Context, ids []string, at time.Time) error {
	c := models.ContentTypes.Columns
	_, err := models.ContentTypes.Update(um.SetCol("version").To(c.Version.OP("+", sqlite.Arg(1))), um.SetCol("updated_at").ToArg(at),
		models.UpdateWhere.ContentTypes.ID.In(ids...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) Lineages(ctx context.Context, ids []string) (map[string][]string, error) {
	c := models.ContentTypeAncestors.Columns
	rows, err := models.ContentTypeAncestors.Query(sm.Where(c.ContentTypeID.In(sqlite.Arg(anys(ids)...))), sm.Where(c.Depth.GT(sqlite.Arg(0))),
		sm.OrderBy(c.ContentTypeID), sm.OrderBy(c.Depth)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := map[string][]string{}
	for _, row := range rows {
		out[row.ContentTypeID] = append(out[row.ContentTypeID], row.AncestorID)
	}
	return out, nil
}

func (q Queries) Descendants(ctx context.Context, ids []string) ([]string, error) {
	c := models.ContentTypeAncestors.Columns
	query := models.ContentTypeAncestors.Query(sm.Columns(c.ContentTypeID), sm.Distinct(), sm.Where(c.AncestorID.In(sqlite.Arg(anys(ids)...))), sm.OrderBy(c.ContentTypeID))
	out, err := bob.All(ctx, q.exec, query, scan.SingleColumnMapper[string])
	return out, dberr.Translate(err)
}

func (q Queries) SetAttached(ctx context.Context, id string, attached bool) error {
	set := &models.ContentTypeSetter{Attached: omit.From(attached)}
	if !attached {
		set.IsDefault = omit.From(false)
	}
	_, err := models.ContentTypes.Update(set.UpdateMod(), models.UpdateWhere.ContentTypes.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

// SetDefaultContentType clears the old default first: a collection has at
// most one default at any time.
func (q Queries) SetDefaultContentType(ctx context.Context, collectionID, id string) error {
	w := models.UpdateWhere.ContentTypes
	off := &models.ContentTypeSetter{IsDefault: omit.From(false)}
	if _, err := models.ContentTypes.Update(off.UpdateMod(), w.CollectionID.EQ(collectionID), w.IsDefault.EQ(true)).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	on := &models.ContentTypeSetter{IsDefault: omit.From(true)}
	_, err := models.ContentTypes.Update(on.UpdateMod(), w.CollectionID.EQ(collectionID), w.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) AttachedCollections(ctx context.Context, contentTypeIDs []string) ([]string, error) {
	c := models.ContentTypes.Columns
	query := models.ContentTypes.Query(sm.Columns(c.CollectionID), sm.Distinct(), sm.Where(c.CollectionID.IsNotNull()), sm.Where(c.Attached.EQ(sqlite.Arg(true))),
		sm.Where(sqlite.Raw(`"content_types"."id" IN (SELECT content_type_id FROM content_type_ancestors WHERE ancestor_id IN ?)`, sqlite.ArgGroup(anys(contentTypeIDs)...))),
		sm.OrderBy(c.CollectionID))
	out, err := bob.All(ctx, q.exec, query, scan.SingleColumnMapper[string])
	return out, dberr.Translate(err)
}

func (q Queries) ContentTypeInUse(ctx context.Context, collectionID, contentTypeID string) (bool, error) {
	w := models.SelectWhere.Resources
	ok, err := models.Resources.Query(w.ContainerID.EQ(collectionID), w.ContentTypeID.EQ(contentTypeID), w.DeletedAt.IsNull()).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) DeleteUnusedListTypes(ctx context.Context, collectionID string) error {
	_, err := q.exec.ExecContext(ctx, `DELETE FROM content_types WHERE collection_id = ?
 AND NOT EXISTS (SELECT 1 FROM resources r WHERE r.content_type_id = content_types.id)
 AND NOT EXISTS (SELECT 1 FROM item_revisions v WHERE v.content_type_id = content_types.id)`, collectionID)
	return dberr.Translate(err)
}

func fieldSetter(d *catalog.FieldDefinition) (*models.FieldDefinitionSetter, error) {
	choices, err := json.Marshal(d.Choices)
	if err != nil {
		return nil, err
	}
	options, err := json.Marshal(d.Options)
	if err != nil {
		return nil, err
	}
	return &models.FieldDefinitionSetter{Label: omit.From(d.Label), Required: omit.From(d.Required), Indexed: omit.From(d.Indexed),
		Choices: omit.From(types.NewJSON(json.RawMessage(choices))), Options: omit.From(types.NewJSON(json.RawMessage(options))), Version: omit.From(int64(d.Version))}, nil
}

func fieldRecord(row *models.FieldDefinition) (*catalog.FieldDefinition, error) {
	out := &catalog.FieldDefinition{ID: row.ID, CreatedAt: row.CreatedAt.UTC(), Key: row.Key, Label: row.Label, Type: catalog.FieldType(row.Type), Required: row.Required,
		Indexed: row.Indexed, Version: int(row.Version), IndexStatus: catalog.IndexStatus(row.IndexStatus), Scale: int(row.Scale)}
	if err := json.Unmarshal(row.Choices.Val, &out.Choices); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(row.Options.Val, &out.Options); err != nil {
		return nil, err
	}
	return out, nil
}

func contentTypeSetter(t *catalog.ContentType) (*models.ContentTypeSetter, error) {
	rules, err := json.Marshal(t.Rules)
	if err != nil {
		return nil, err
	}
	return &models.ContentTypeSetter{Name: omit.From(t.Name), FieldIds: omit.From(types.NewJSON(t.FieldIds)), HiddenFieldIds: omit.From(types.NewJSON(t.HiddenFieldIds)),
		Rules: omit.From(types.NewJSON(json.RawMessage(rules))), UpdatedAt: omit.From(t.UpdatedAt), Version: omit.From(int64(t.Version))}, nil
}

func contentTypeRecord(row *models.ContentType) (*catalog.ContentType, error) {
	out := &catalog.ContentType{ID: row.ID, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(), Key: row.Key, Name: row.Name, TemplateKey: row.TemplateKey.Ptr(),
		ParentID: row.ParentID.Ptr(), CollectionID: row.CollectionID.Ptr(), Attached: row.Attached, IsDefault: row.IsDefault,
		FieldIds: row.FieldIds.Val, HiddenFieldIds: row.HiddenFieldIds.Val, Version: int(row.Version)}
	if out.FieldIds == nil {
		out.FieldIds = []string{}
	}
	if out.HiddenFieldIds == nil {
		out.HiddenFieldIds = []string{}
	}
	if err := json.Unmarshal(row.Rules.Val, &out.Rules); err != nil {
		return nil, err
	}
	return out, nil
}
