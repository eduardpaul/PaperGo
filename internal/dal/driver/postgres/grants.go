package postgres

import (
	"context"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/grants"
	"papergo/internal/dal/refs"
)

// LockDirectory takes the transaction-scoped advisory lock that serializes
// directory and grant changes.
func (q Queries) LockDirectory(ctx context.Context) error {
	_, err := q.exec.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1346848591)`)
	return dberr.Translate(err)
}

func (q Queries) GrantTarget(ctx context.Context, resourceID string) (grants.Target, error) {
	row, err := models.FindResource(ctx, q.exec, resourceID, "kind", "workspace_id", "personal_owner_id", "inherit_permissions", "deleted_at")
	if err != nil {
		return grants.Target{}, dberr.Translate(err)
	}
	return grants.Target{
		Kind: refs.ResourceKind(row.Kind), WorkspaceID: row.WorkspaceID, PersonalOwnerID: row.PersonalOwnerID.GetOrZero(),
		Inherit: row.InheritPermissions, Deleted: row.DeletedAt.IsValue(),
	}, nil
}

func (q Queries) UserActive(ctx context.Context, id string) (bool, error) {
	w := models.SelectWhere.IdentityUsers
	ok, err := models.IdentityUsers.Query(w.ID.EQ(id), w.Active.EQ(true), w.DeletedAt.IsNull()).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) FindGrant(ctx context.Context, id string) (grants.Grant, error) {
	row, err := models.FindGrant(ctx, q.exec, id)
	if err != nil {
		return grants.Grant{}, dberr.Translate(err)
	}
	return grantRecord(row), nil
}

func (q Queries) Grants(ctx context.Context, f grants.Filter) ([]grants.Grant, error) {
	c := models.Grants.Columns
	query := models.Grants.Query()
	w := models.SelectWhere.Grants
	if f.ResourceID != "" {
		query.Apply(w.ResourceID.EQ(f.ResourceID))
	}
	if f.PrincipalID != "" {
		query.Apply(w.PrincipalID.EQ(f.PrincipalID))
	}
	if f.ExcludePrincipalID != "" {
		query.Apply(w.PrincipalID.NE(f.ExcludePrincipalID))
	}
	if f.PrincipalType != "" {
		query.Apply(w.PrincipalType.EQ(string(f.PrincipalType)))
	}
	if f.Action != "" {
		query.Apply(w.Action.EQ(string(f.Action)))
	}
	if f.ByPrincipal {
		query.Apply(sm.OrderBy(c.PrincipalID), sm.OrderBy(c.PrincipalType), sm.OrderBy(c.Action))
	} else {
		query.Apply(sm.OrderBy(c.PrincipalType), sm.OrderBy(c.PrincipalID), sm.OrderBy(c.Action))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]grants.Grant, len(rows))
	for i, row := range rows {
		out[i] = grantRecord(row)
	}
	return out, nil
}

func (q Queries) InsertGrant(ctx context.Context, g grants.Grant) error {
	_, err := models.Grants.Insert(&models.GrantSetter{
		ID: omit.From(g.ID), CreatedAt: omit.From(g.CreatedAt), PrincipalType: omit.From(string(g.PrincipalType)), PrincipalID: omit.From(g.PrincipalID),
		Action: omit.From(string(g.Action)), Effect: omit.From("allow"), ResourceID: omit.From(g.ResourceID),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetGrantAction(ctx context.Context, id string, action grants.Action) error {
	set := &models.GrantSetter{Action: omit.From(string(action))}
	_, err := models.Grants.Update(set.UpdateMod(), models.UpdateWhere.Grants.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteGrant(ctx context.Context, id string) error {
	_, err := models.Grants.Delete(models.DeleteWhere.Grants.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteGrants(ctx context.Context, f grants.Filter) error {
	w := models.DeleteWhere.Grants
	query := models.Grants.Delete()
	if f.ResourceID != "" {
		query.Apply(w.ResourceID.EQ(f.ResourceID))
	}
	if f.PrincipalID != "" {
		query.Apply(w.PrincipalID.EQ(f.PrincipalID))
	}
	if f.PrincipalType != "" {
		query.Apply(w.PrincipalType.EQ(string(f.PrincipalType)))
	}
	if f.ResourceID == "" && f.PrincipalID == "" {
		return dberr.ErrCheckViolation
	}
	_, err := query.Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) OtherWorkspaceOwners(ctx context.Context, workspaceID, excludedUserID string) (int64, error) {
	w := models.SelectWhere.Grants
	users := models.IdentityUsers.Columns
	n, err := models.Grants.Query(
		w.ResourceID.EQ(workspaceID), w.PrincipalID.NE(excludedUserID), w.Action.EQ(string(grants.ActionManage)),
		w.PrincipalType.EQ(string(grants.PrincipalUser)),
		sm.InnerJoin(models.IdentityUsers.Name()).OnEQ(users.ID, models.Grants.Columns.PrincipalID),
		models.SelectWhere.IdentityUsers.Active.EQ(true), models.SelectWhere.IdentityUsers.DeletedAt.IsNull(),
	).Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func grantRecord(r *models.Grant) grants.Grant {
	return grants.Grant{
		ID: r.ID, ResourceID: r.ResourceID, PrincipalID: r.PrincipalID, PrincipalType: grants.PrincipalType(r.PrincipalType),
		Action: grants.Action(r.Action), CreatedAt: r.CreatedAt.UTC(),
	}
}
