package postgres

import (
	"context"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/postgres/models"
)

// keyBatch bounds the business keys one statement inserts.
const keyBatch = 500

func (q Queries) BusinessKeys(ctx context.Context, f catalog.KeyFilter) ([]catalog.BusinessKey, error) {
	w := models.SelectWhere.BusinessKeys
	query := models.BusinessKeys.Query(sm.OrderBy(models.BusinessKeys.Columns.ID))
	if f.CollectionID != "" {
		query.Apply(w.ContainerID.EQ(f.CollectionID))
	}
	if f.ItemID != "" {
		query.Apply(w.ItemID.EQ(f.ItemID))
	}
	if f.FieldKey != "" {
		query.Apply(w.FieldKey.EQ(f.FieldKey))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]catalog.BusinessKey, len(rows))
	for i, row := range rows {
		out[i] = catalog.BusinessKey{ID: row.ID, CollectionID: row.ContainerID, ItemID: row.ItemID, FieldKey: row.FieldKey, Value: row.Value, CreatedAt: row.CreatedAt.UTC()}
	}
	return out, nil
}

func (q Queries) InsertBusinessKeys(ctx context.Context, keys []catalog.BusinessKey) error {
	for len(keys) > 0 {
		n := min(len(keys), keyBatch)
		rows := make([]bob.Mod[*dialect.InsertQuery], n)
		for i, k := range keys[:n] {
			rows[i] = &models.BusinessKeySetter{ID: omit.From(k.ID), CreatedAt: omit.From(k.CreatedAt), ContainerID: omit.From(k.CollectionID), ItemID: omit.From(k.ItemID), FieldKey: omit.From(k.FieldKey), Value: omit.From(k.Value)}
		}
		if _, err := models.BusinessKeys.Insert(rows...).Exec(ctx, q.exec); err != nil {
			return dberr.Translate(err)
		}
		keys = keys[n:]
	}
	return nil
}

func (q Queries) DeleteBusinessKeys(ctx context.Context, f catalog.KeyFilter) error {
	w := models.DeleteWhere.BusinessKeys
	query := models.BusinessKeys.Delete()
	if f.CollectionID != "" {
		query.Apply(w.ContainerID.EQ(f.CollectionID))
	}
	if f.ItemID != "" {
		query.Apply(w.ItemID.EQ(f.ItemID))
	}
	if f.FieldKey != "" {
		query.Apply(w.FieldKey.EQ(f.FieldKey))
	}
	_, err := query.Exec(ctx, q.exec)
	return dberr.Translate(err)
}
