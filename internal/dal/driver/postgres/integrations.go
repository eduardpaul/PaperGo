package postgres

import (
	"context"
	"time"

	"github.com/aarondl/opt/null"
	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/integrations"
)

func (q Queries) FindIntegration(ctx context.Context, id string) (integrations.Integration, error) {
	row, err := models.FindListIntegration(ctx, q.exec, id)
	if err != nil {
		return integrations.Integration{}, dberr.Translate(err)
	}
	return integrationRecord(row), nil
}

func (q Queries) Integrations(ctx context.Context, f integrations.Filter) ([]integrations.Integration, error) {
	c := models.ListIntegrations.Columns
	query := models.ListIntegrations.Query(sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID))
	w := models.SelectWhere.ListIntegrations
	if f.ContainerID != "" {
		query.Apply(w.ContainerID.EQ(f.ContainerID))
	}
	if f.Unpaused {
		query.Apply(w.Paused.EQ(false))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]integrations.Integration, len(rows))
	for i, row := range rows {
		out[i] = integrationRecord(row)
	}
	return out, nil
}

func (q Queries) CountIntegrations(ctx context.Context, f integrations.Filter) (int64, error) {
	query := models.ListIntegrations.Query()
	w := models.SelectWhere.ListIntegrations
	if f.ContainerID != "" {
		query.Apply(w.ContainerID.EQ(f.ContainerID))
	}
	if f.Unpaused {
		query.Apply(w.Paused.EQ(false))
	}
	n, err := query.Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) InsertIntegration(ctx context.Context, i integrations.Integration) error {
	_, err := models.ListIntegrations.Insert(&models.ListIntegrationSetter{
		ID: omit.From(i.ID), CreatedAt: omit.From(i.CreatedAt), UpdatedAt: omit.From(i.UpdatedAt), ContainerID: omit.From(i.ContainerID),
		Kind: omit.From(i.Kind), Name: omit.From(i.Name), Paused: omit.From(i.Paused), Version: omit.From(int64(i.Version)),
		Generation: omit.From(i.Generation), ConfigCiphertext: omit.From(i.ConfigCiphertext), ConfigHash: omit.From(i.ConfigHash),
		ConfiguredBy: omit.From(i.ConfiguredBy), CredentialID: omit.From(i.CredentialID), CredentialScopes: omit.From(types.NewJSON(i.CredentialScopes)),
		State: omit.From(types.NewJSON(i.State)), LastAttemptAt: nullable.Time(i.LastAttemptAt), LastSuccessAt: nullable.Time(i.LastSuccessAt),
		Error: omit.From(i.Error), ItemCount: omit.From(int64(i.ItemCount)), LeaseToken: omit.From(i.LeaseToken), LeaseUntil: nullable.Time(i.LeaseUntil),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateIntegration(ctx context.Context, i integrations.Integration) error {
	set := &models.ListIntegrationSetter{
		UpdatedAt: omit.From(i.UpdatedAt), Name: omit.From(i.Name), Paused: omit.From(i.Paused), Version: omit.From(int64(i.Version)),
		Generation: omit.From(i.Generation), ConfigCiphertext: omit.From(i.ConfigCiphertext), ConfigHash: omit.From(i.ConfigHash),
		ConfiguredBy: omit.From(i.ConfiguredBy), CredentialID: omit.From(i.CredentialID), CredentialScopes: omit.From(types.NewJSON(i.CredentialScopes)),
		State: omit.From(types.NewJSON(i.State)), LastAttemptAt: nullable.Time(i.LastAttemptAt), LastSuccessAt: nullable.Time(i.LastSuccessAt),
		Error: omit.From(i.Error), ItemCount: omit.From(int64(i.ItemCount)), LeaseToken: omit.From(i.LeaseToken), LeaseUntil: nullable.Time(i.LeaseUntil),
	}
	n, err := models.ListIntegrations.Update(set.UpdateMod(), models.UpdateWhere.ListIntegrations.ID.EQ(i.ID)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n == 0 {
		return dberr.ErrNotFound
	}
	return nil
}

func (q Queries) DeleteIntegration(ctx context.Context, id string) error {
	_, err := models.ListIntegrations.Delete(models.DeleteWhere.ListIntegrations.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) ReleaseIntegrationLease(ctx context.Context, id, token string) error {
	set := &models.ListIntegrationSetter{LeaseToken: omit.From(""), LeaseUntil: omitnull.FromNull(null.Val[time.Time]{})}
	w := models.UpdateWhere.ListIntegrations
	_, err := models.ListIntegrations.Update(set.UpdateMod(), w.ID.EQ(id), w.LeaseToken.EQ(token)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) IntegrationItem(ctx context.Context, itemID string) (integrations.Item, error) {
	row, err := models.IntegrationItems.Query(models.SelectWhere.IntegrationItems.ItemID.EQ(itemID)).One(ctx, q.exec)
	if err != nil {
		return integrations.Item{}, dberr.Translate(err)
	}
	return integrationItemRecord(row), nil
}

func (q Queries) IntegrationItems(ctx context.Context, integrationID string) ([]integrations.Item, error) {
	w := models.SelectWhere.IntegrationItems
	query := models.IntegrationItems.Query(sm.OrderBy(models.IntegrationItems.Columns.ID))
	if integrationID == "" {
		query.Apply(w.IntegrationID.IsNull())
	} else {
		query.Apply(w.IntegrationID.EQ(integrationID))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]integrations.Item, len(rows))
	for i, row := range rows {
		out[i] = integrationItemRecord(row)
	}
	return out, nil
}

func (q Queries) InsertIntegrationItem(ctx context.Context, i integrations.Item) error {
	_, err := models.IntegrationItems.Insert(&models.IntegrationItemSetter{
		ID: omit.From(i.ID), CreatedAt: omit.From(i.CreatedAt), ItemID: omit.From(i.ItemID), IntegrationID: nullable.String(i.IntegrationID),
		ExternalID: omit.From(i.ExternalID), ProviderKind: omit.From(i.ProviderKind), ManagedFields: omit.From(types.NewJSON(i.ManagedFields)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DetachIntegrationItems(ctx context.Context, integrationID string) error {
	set := &models.IntegrationItemSetter{IntegrationID: omitnull.FromNull(null.Val[string]{})}
	_, err := models.IntegrationItems.Update(set.UpdateMod(), models.UpdateWhere.IntegrationItems.IntegrationID.EQ(integrationID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) RevisionData(ctx context.Context, revisionID string) (integrations.RevisionData, error) {
	row, err := models.IntegrationRevisionData.Query(models.SelectWhere.IntegrationRevisionData.RevisionID.EQ(revisionID)).One(ctx, q.exec)
	if err != nil {
		return integrations.RevisionData{}, dberr.Translate(err)
	}
	return integrations.RevisionData{ID: row.ID, RevisionID: row.RevisionID, ProviderKind: row.ProviderKind, Data: row.Data.Val, CreatedAt: row.CreatedAt.UTC()}, nil
}

func (q Queries) InsertRevisionData(ctx context.Context, d integrations.RevisionData) error {
	_, err := models.IntegrationRevisionData.Insert(&models.IntegrationRevisionDatumSetter{
		ID: omit.From(d.ID), CreatedAt: omit.From(d.CreatedAt), RevisionID: omit.From(d.RevisionID), ProviderKind: omit.From(d.ProviderKind),
		Data: omit.From(types.NewJSON(d.Data)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func integrationRecord(r *models.ListIntegration) integrations.Integration {
	return integrations.Integration{
		ID: r.ID, ContainerID: r.ContainerID, Kind: r.Kind, Name: r.Name, Generation: r.Generation, ConfigHash: r.ConfigHash, ConfiguredBy: r.ConfiguredBy,
		CredentialID: r.CredentialID, LeaseToken: r.LeaseToken, Error: r.Error, ConfigCiphertext: r.ConfigCiphertext, CredentialScopes: r.CredentialScopes.Val,
		State: r.State.Val, Paused: r.Paused, Version: int(r.Version), ItemCount: int(r.ItemCount), LastAttemptAt: nullable.UTC(r.LastAttemptAt),
		LastSuccessAt: nullable.UTC(r.LastSuccessAt), LeaseUntil: nullable.UTC(r.LeaseUntil), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

func integrationItemRecord(r *models.IntegrationItem) integrations.Item {
	return integrations.Item{
		ID: r.ID, ItemID: r.ItemID, IntegrationID: r.IntegrationID.GetOrZero(), ExternalID: r.ExternalID, ProviderKind: r.ProviderKind,
		ManagedFields: r.ManagedFields.Val, CreatedAt: r.CreatedAt.UTC(),
	}
}
