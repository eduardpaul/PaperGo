package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/google/uuid"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
)

func directoryRows[T any](ctx context.Context, exec bob.Executor, query string, read func(scan.Rows) (T, error), args ...any) ([]T, error) {
	rows, e := exec.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, dberr.Translate(e)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, e := read(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, dberr.Translate(rows.Err())
}
func directoryExec(ctx context.Context, exec bob.Executor, query string, args ...any) error {
	_, e := exec.ExecContext(ctx, query, args...)
	return dberr.Translate(e)
}
func directoryChanged(ctx context.Context, exec bob.Executor, query string, args ...any) (bool, error) {
	res, e := exec.ExecContext(ctx, query, args...)
	if e != nil {
		return false, dberr.Translate(e)
	}
	n, e := res.RowsAffected()
	return n == 1, e
}
func directoryBool(ctx context.Context, exec bob.Executor, query string, args ...any) (bool, error) {
	rows, e := directoryRows(ctx, exec, query, func(r scan.Rows) (bool, error) { var b bool; e := r.Scan(&b); return b, e }, args...)
	if e != nil {
		return false, e
	}
	if len(rows) == 0 {
		return false, dberr.ErrNotFound
	}
	return rows[0], nil
}
func (q Queries) InsertDirectoryUser(ctx context.Context, u directory.User) error {
	_, err := models.IdentityUsers.Insert(&models.IdentityUserSetter{ID: omit.From(u.ID), CreatedAt: omit.From(u.CreatedAt), DisplayName: omit.From(u.DisplayName), Email: omit.From(u.Email), Active: omit.From(u.Active), Service: omit.From(u.Service), SyncHash: omit.From(u.SyncHash), PasswordHash: omit.From(u.PasswordHash)}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateDirectoryUser(ctx context.Context, u directory.User) error {
	set := &models.IdentityUserSetter{DisplayName: omit.From(u.DisplayName), Email: omit.From(u.Email), SyncHash: omit.From(u.SyncHash)}
	_, err := models.IdentityUsers.Update(set.UpdateMod(), models.UpdateWhere.IdentityUsers.ID.EQ(u.ID), models.UpdateWhere.IdentityUsers.DeletedAt.IsNull()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetDirectoryUserActive(ctx context.Context, id string, active bool) error {
	set := &models.IdentityUserSetter{Active: omit.From(active)}
	n, err := models.IdentityUsers.Update(set.UpdateMod(), models.UpdateWhere.IdentityUsers.ID.EQ(id), models.UpdateWhere.IdentityUsers.DeletedAt.IsNull()).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrNotFound
	}
	return nil
}

func (q Queries) SetDirectoryPassword(ctx context.Context, id, hash string) error {
	set := &models.IdentityUserSetter{PasswordHash: omit.From(hash)}
	_, err := models.IdentityUsers.Update(set.UpdateMod(), models.UpdateWhere.IdentityUsers.ID.EQ(id), models.UpdateWhere.IdentityUsers.PasswordHash.EQ(""), models.UpdateWhere.IdentityUsers.DeletedAt.IsNull()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UserIsLastWorkspaceOwner(ctx context.Context, id string) (bool, error) {
	return directoryBool(ctx, q.exec, `SELECT EXISTS(SELECT 1 FROM grants g JOIN resources w ON w.id=g.resource_id WHERE w.kind='workspace' AND w.personal_owner_id IS NULL AND w.deleted_at IS NULL AND g.principal_type=$1 AND g.principal_id=$2 AND g.action='manage' AND NOT EXISTS(SELECT 1 FROM grants other JOIN identity_users u ON u.id=other.principal_id WHERE other.resource_id=w.id AND other.principal_type=$1 AND other.action='manage' AND other.principal_id<>$2 AND u.active AND u.deleted_at IS NULL))`, directory.PrincipalUser, id)
}
func (q Queries) UserHasHome(ctx context.Context, id string) (bool, error) {
	return directoryBool(ctx, q.exec, `SELECT EXISTS(SELECT 1 FROM resources WHERE personal_owner_id=$1 AND deleted_at IS NULL)`, id)
}
func (q Queries) DeleteDirectoryUserData(ctx context.Context, id string, now time.Time) error {
	if e := directoryExec(ctx, q.exec, `DELETE FROM identity_preferences WHERE owner_type=$1 AND owner_id=$2`, directory.OwnerUser, id); e != nil {
		return e
	}
	for _, query := range []string{
		`DELETE FROM identity_memberships WHERE principal_type=$2 AND principal_id=$1`,
		`DELETE FROM identity_assignments WHERE principal_type=$2 AND principal_id=$1`,
		`DELETE FROM grants WHERE principal_type=$2 AND principal_id=$1`,
	} {
		if e := directoryExec(ctx, q.exec, query, id, directory.PrincipalUser); e != nil {
			return e
		}
	}
	for _, query := range []string{
		`DELETE FROM smart_folders WHERE owner_id=$1`, `DELETE FROM access_tokens WHERE subject=$1`, `DELETE FROM identity_sessions WHERE subject=$1`,
		`DELETE FROM sync_tokens WHERE view_id IN (SELECT id FROM sync_views WHERE subject=$1)`,
		`DELETE FROM sync_states WHERE view_id IN (SELECT id FROM sync_views WHERE subject=$1)`, `DELETE FROM sync_views WHERE subject=$1`,
	} {
		if e := directoryExec(ctx, q.exec, query, id); e != nil {
			return e
		}
	}
	return directoryExec(ctx, q.exec, `UPDATE identity_users SET active=FALSE,display_name='Deleted user',email='',password_hash='',sync_hash='',deleted_at=$1 WHERE id=$2`, now, id)
}
func (q Queries) DirectoryGroups(ctx context.Context) ([]directory.Group, error) {
	rows, err := models.IdentityGroups.Query(sm.OrderBy(models.IdentityGroups.Columns.Name), sm.OrderBy(models.IdentityGroups.Columns.ID)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]directory.Group, len(rows))
	for i, g := range rows {
		out[i] = directory.Group{ID: g.ID, Name: g.Name, Description: g.Description, Source: directory.GroupSource(g.Source), Version: int(g.Version), CreatedAt: g.CreatedAt.UTC()}
	}
	return out, nil
}

func (q Queries) InsertDirectoryGroup(ctx context.Context, g directory.Group) error {
	_, err := models.IdentityGroups.Insert(&models.IdentityGroupSetter{ID: omit.From(g.ID), CreatedAt: omit.From(g.CreatedAt), Name: omit.From(g.Name), Description: omit.From(g.Description), Source: omit.From(string(g.Source)), Version: omit.From(int64(g.Version))}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateDirectoryGroup(ctx context.Context, g directory.Group, version int) (bool, error) {
	set := &models.IdentityGroupSetter{Name: omit.From(g.Name), Description: omit.From(g.Description), Version: omit.From(int64(version + 1))}
	n, err := models.IdentityGroups.Update(set.UpdateMod(), models.UpdateWhere.IdentityGroups.ID.EQ(g.ID), models.UpdateWhere.IdentityGroups.Version.EQ(int64(version))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DeleteDirectoryGroup(ctx context.Context, id string, version int) (bool, error) {
	ok, e := directoryChanged(ctx, q.exec, `DELETE FROM identity_groups WHERE id=$1 AND version=$2`, id, version)
	if e != nil || !ok {
		return ok, e
	}
	for _, query := range []string{`DELETE FROM identity_memberships WHERE principal_type=$1 AND principal_id=$2`, `DELETE FROM identity_assignments WHERE principal_type=$1 AND principal_id=$2`, `DELETE FROM grants WHERE principal_type=$1 AND principal_id=$2`} {
		if e := directoryExec(ctx, q.exec, query, directory.PrincipalGroup, id); e != nil {
			return false, e
		}
	}
	return true, nil
}
func (q Queries) DirectoryMemberships(ctx context.Context, f directory.MembershipFilter) ([]directory.Membership, error) {
	query := `SELECT id,group_id,principal_type,principal_id FROM identity_memberships WHERE TRUE`
	args := []any{}
	if f.GroupID != "" {
		args = append(args, f.GroupID)
		query += ` AND group_id=$1`
	}
	if f.Principal != nil {
		args = append(args, f.Principal.Type)
		query += ` AND principal_type=$` + strconv.Itoa(len(args))
		args = append(args, f.Principal.ID)
		query += ` AND principal_id=$` + strconv.Itoa(len(args))
	}
	query += ` ORDER BY principal_type,principal_id,group_id`
	return directoryRows(ctx, q.exec, query, func(r scan.Rows) (directory.Membership, error) {
		var m directory.Membership
		e := r.Scan(&m.ID, &m.GroupID, &m.Principal.Type, &m.Principal.ID)
		return m, e
	}, args...)
}
func (q Queries) InsertDirectoryMembership(ctx context.Context, m directory.Membership) error {
	_, err := models.IdentityMemberships.Insert(&models.IdentityMembershipSetter{ID: omit.From(m.ID), GroupID: omit.From(m.GroupID), PrincipalType: omit.From(string(m.Principal.Type)), PrincipalID: omit.From(m.Principal.ID)}, im.OnConflict("group_id", "principal_type", "principal_id").DoNothing()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteDirectoryMembership(ctx context.Context, id string, p directory.Principal) error {
	_, err := models.IdentityMemberships.Delete(models.DeleteWhere.IdentityMemberships.GroupID.EQ(id), models.DeleteWhere.IdentityMemberships.PrincipalType.EQ(string(p.Type)), models.DeleteWhere.IdentityMemberships.PrincipalID.EQ(p.ID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DirectoryRoles(ctx context.Context) ([]directory.Role, error) {
	rows, err := models.IdentityRoles.Query(sm.OrderBy(models.IdentityRoles.Columns.Name), sm.OrderBy(models.IdentityRoles.Columns.ID)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]directory.Role, len(rows))
	for i, r := range rows {
		out[i] = directory.Role{ID: r.ID, Name: r.Name, Description: r.Description, Scopes: r.Scopes.Val, Builtin: r.Builtin, Version: int(r.Version)}
	}
	return out, nil
}

func (q Queries) InsertDirectoryRole(ctx context.Context, r directory.Role) error {
	_, err := models.IdentityRoles.Insert(&models.IdentityRoleSetter{ID: omit.From(r.ID), Name: omit.From(r.Name), Description: omit.From(r.Description), Scopes: omit.From(types.NewJSON(r.Scopes)), Builtin: omit.From(r.Builtin), Version: omit.From(int64(1))}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateDirectoryRole(ctx context.Context, r directory.Role, version int) (bool, error) {
	set := &models.IdentityRoleSetter{Name: omit.From(r.Name), Description: omit.From(r.Description), Scopes: omit.From(types.NewJSON(r.Scopes)), Version: omit.From(int64(version + 1))}
	n, err := models.IdentityRoles.Update(set.UpdateMod(), models.UpdateWhere.IdentityRoles.ID.EQ(r.ID), models.UpdateWhere.IdentityRoles.Version.EQ(int64(version)), models.UpdateWhere.IdentityRoles.Builtin.EQ(false)).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DeleteDirectoryRole(ctx context.Context, id string, version int) (bool, error) {
	n, err := models.IdentityRoles.Delete(models.DeleteWhere.IdentityRoles.ID.EQ(id), models.DeleteWhere.IdentityRoles.Version.EQ(int64(version)), models.DeleteWhere.IdentityRoles.Builtin.EQ(false)).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) RolePrincipals(ctx context.Context, id string) ([]directory.Principal, error) {
	rows, err := models.IdentityAssignments.Query(models.SelectWhere.IdentityAssignments.RoleID.EQ(id), sm.OrderBy(models.IdentityAssignments.Columns.PrincipalType), sm.OrderBy(models.IdentityAssignments.Columns.PrincipalID)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]directory.Principal, len(rows))
	for i, r := range rows {
		out[i] = directory.Principal{Type: directory.PrincipalType(r.PrincipalType), ID: r.PrincipalID}
	}
	return out, nil
}

func (q Queries) InsertDirectoryAssignment(ctx context.Context, id string, p directory.Principal) error {
	_, err := models.IdentityAssignments.Insert(&models.IdentityAssignmentSetter{ID: omit.From(uuid.NewString()), RoleID: omit.From(id), PrincipalType: omit.From(string(p.Type)), PrincipalID: omit.From(p.ID)}, im.OnConflict("role_id", "principal_type", "principal_id").DoNothing()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteDirectoryAssignment(ctx context.Context, id string, p directory.Principal) error {
	_, err := models.IdentityAssignments.Delete(models.DeleteWhere.IdentityAssignments.RoleID.EQ(id), models.DeleteWhere.IdentityAssignments.PrincipalType.EQ(string(p.Type)), models.DeleteWhere.IdentityAssignments.PrincipalID.EQ(p.ID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DirectoryOrganization(ctx context.Context) (directory.Organization, error) {
	r, err := models.IdentityOrganizations.Query(models.SelectWhere.IdentityOrganizations.Singleton.EQ(true)).One(ctx, q.exec)
	if err != nil {
		return directory.Organization{}, dberr.Translate(err)
	}
	return directory.Organization{ID: r.ID, Identifier: r.Identifier, DisplayName: r.DisplayName, Description: r.Description, Version: r.Version}, nil
}

func (q Queries) InsertDirectoryOrganization(ctx context.Context) error {
	_, err := models.IdentityOrganizations.Insert(&models.IdentityOrganizationSetter{ID: omit.From(uuid.NewString()), Singleton: omit.From(true), Identifier: omit.From("papergo"), DisplayName: omit.From("PaperGo")}, im.OnConflict("singleton").DoNothing()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateDirectoryOrganization(ctx context.Context, o directory.Organization, version int64) (bool, error) {
	set := &models.IdentityOrganizationSetter{Identifier: omit.From(o.Identifier), DisplayName: omit.From(o.DisplayName), Description: omit.From(o.Description), Version: omit.From(version + 1)}
	n, err := models.IdentityOrganizations.Update(set.UpdateMod(), models.UpdateWhere.IdentityOrganizations.ID.EQ(o.ID), models.UpdateWhere.IdentityOrganizations.Version.EQ(version)).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DirectoryPreferences(ctx context.Context, owner directory.PreferenceOwner, id string) (directory.Preferences, error) {
	r, err := models.IdentityPreferences.Query(models.SelectWhere.IdentityPreferences.OwnerType.EQ(string(owner)), models.SelectWhere.IdentityPreferences.OwnerID.EQ(id)).One(ctx, q.exec)
	if err != nil {
		return directory.Preferences{}, dberr.Translate(err)
	}
	var values map[string]string
	if err := json.Unmarshal(r.Values.Val, &values); err != nil {
		return directory.Preferences{}, err
	}
	return directory.Preferences{Values: values, Version: r.Version}, nil
}

func (q Queries) WriteDirectoryPreferences(ctx context.Context, owner directory.PreferenceOwner, id string, version int64, values map[string]string) (bool, error) {
	raw, err := json.Marshal(values)
	if err != nil {
		return false, err
	}
	set := &models.IdentityPreferenceSetter{Values: omit.From(types.NewJSON(json.RawMessage(raw))), Version: omit.From(version + 1)}
	if version == 0 {
		set.ID = omit.From(uuid.NewString())
		set.OwnerType = omit.From(string(owner))
		set.OwnerID = omit.From(id)
		n, err := models.IdentityPreferences.Insert(set).Exec(ctx, q.exec)
		return n == 1, dberr.Translate(err)
	}
	n, err := models.IdentityPreferences.Update(set.UpdateMod(), models.UpdateWhere.IdentityPreferences.OwnerType.EQ(string(owner)), models.UpdateWhere.IdentityPreferences.OwnerID.EQ(id), models.UpdateWhere.IdentityPreferences.Version.EQ(version)).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DirectoryAccessTokens(ctx context.Context, f directory.AccessTokenFilter) ([]directory.AccessToken, error) {
	query := `SELECT id,subject,name,prefix,secret_hash,created_by,scopes,resources,integration,created_at,expires_at,last_used_at,revoked_at FROM access_tokens WHERE TRUE`
	args := []any{}
	add := func(clause string, v any) { args = append(args, v); query += clause + "$" + strconv.Itoa(len(args)) }
	if f.Subject != "" {
		add(" AND subject=", f.Subject)
	}
	if f.ID != "" {
		add(" AND id=", f.ID)
	}
	if f.SecretHash != "" {
		add(" AND secret_hash=", f.SecretHash)
	}
	if f.Personal {
		query += " AND NOT integration"
	}
	if f.Live {
		args = append(args, f.Now)
		query += ` AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>$` + strconv.Itoa(len(args)) + `)`
	}
	query += ` ORDER BY created_at DESC,id`
	return directoryRows(ctx, q.exec, query, func(r scan.Rows) (directory.AccessToken, error) {
		var t directory.AccessToken
		var raw, limit []byte
		var expires, used, revoked sql.NullTime
		e := r.Scan(&t.ID, &t.Subject, &t.Name, &t.Prefix, &t.SecretHash, &t.CreatedBy, &raw, &limit, &t.Integration, &t.CreatedAt, &expires, &used, &revoked)
		if expires.Valid {
			t.ExpiresAt = &expires.Time
		}
		if used.Valid {
			t.LastUsedAt = &used.Time
		}
		if revoked.Valid {
			t.RevokedAt = &revoked.Time
		}
		if e == nil {
			e = json.Unmarshal(raw, &t.Scopes)
		}
		if e == nil {
			e = json.Unmarshal(limit, &t.Resources)
		}
		return t, e
	}, args...)
}
func (q Queries) InsertDirectoryAccessToken(ctx context.Context, t directory.AccessToken) error {
	_, err := models.AccessTokens.Insert(&models.AccessTokenSetter{ID: omit.From(t.ID), Subject: omit.From(t.Subject), Name: omit.From(t.Name), Prefix: omit.From(t.Prefix), SecretHash: omit.From(t.SecretHash), Scopes: omit.From(types.NewJSON(t.Scopes)), Resources: omit.From(types.NewJSON(t.Resources)), Integration: omit.From(t.Integration), CreatedBy: omit.From(t.CreatedBy), CreatedAt: omit.From(t.CreatedAt), ExpiresAt: nullable.Time(t.ExpiresAt)}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) RevokeDirectoryAccessToken(ctx context.Context, id, subject string, admin bool, now time.Time) (bool, error) {
	if admin {
		return directoryChanged(ctx, q.exec, `UPDATE access_tokens SET revoked_at=$1 WHERE id=$2 AND revoked_at IS NULL`, now, id)
	}
	return directoryChanged(ctx, q.exec, `UPDATE access_tokens SET revoked_at=$1 WHERE id=$2 AND subject=$3 AND NOT integration AND revoked_at IS NULL`, now, id, subject)
}
func (q Queries) TouchDirectoryAccessToken(ctx context.Context, id string, now time.Time) error {
	_, err := models.AccessTokens.Update((&models.AccessTokenSetter{LastUsedAt: nullable.Time(&now)}).UpdateMod(), models.UpdateWhere.AccessTokens.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DirectoryEffectiveGroups(ctx context.Context, subject string) ([]string, error) {
	return directoryRows(ctx, q.exec, `WITH RECURSIVE effective_groups(id) AS (
 SELECT group_id FROM identity_memberships WHERE principal_type=$1 AND principal_id=$2
 UNION SELECT m.group_id FROM identity_memberships m JOIN effective_groups e ON m.principal_type=$3 AND m.principal_id=e.id
 ) SELECT id FROM effective_groups ORDER BY id`, func(r scan.Rows) (string, error) { var id string; e := r.Scan(&id); return id, e }, directory.PrincipalUser, subject, directory.PrincipalGroup)
}
func (q Queries) DirectoryHasAdministrator(ctx context.Context, roleID string) (bool, error) {
	return directoryBool(ctx, q.exec, `WITH RECURSIVE admins(principal_type,principal_id) AS (
 SELECT principal_type,principal_id FROM identity_assignments WHERE role_id=$1
 UNION SELECT m.principal_type,m.principal_id FROM identity_memberships m JOIN admins a ON a.principal_type=$2 AND a.principal_id=m.group_id
 ) SELECT EXISTS(SELECT 1 FROM admins a JOIN identity_users u ON a.principal_type=$3 AND a.principal_id=u.id WHERE u.active AND u.deleted_at IS NULL)`, roleID, directory.PrincipalGroup, directory.PrincipalUser)
}
func (q Queries) DirectoryEffectiveScopes(ctx context.Context, subject, tokenID string, now time.Time) ([]string, error) {
	return directoryRows(ctx, q.exec, `WITH RECURSIVE effective_groups(id) AS (
 SELECT group_id FROM identity_memberships WHERE principal_type=$3 AND principal_id=$1
 UNION SELECT m.group_id FROM identity_memberships m JOIN effective_groups e ON m.principal_type=$4 AND m.principal_id=e.id
 ) SELECT DISTINCT sc.value FROM identity_assignments a JOIN identity_roles r ON r.id=a.role_id JOIN json_each(r.scopes) sc
 WHERE EXISTS(SELECT 1 FROM identity_users u WHERE u.id=$1 AND u.active AND u.deleted_at IS NULL)
 AND ((a.principal_type=$3 AND a.principal_id=$1) OR (a.principal_type=$4 AND a.principal_id IN (SELECT id FROM effective_groups)))
 UNION SELECT sc.value FROM access_tokens t JOIN identity_users u ON u.id=t.subject JOIN json_each(t.scopes) sc
 WHERE t.id=$2 AND t.subject=$1 AND t.integration AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>$5) AND u.active AND u.deleted_at IS NULL ORDER BY 1`, func(r scan.Rows) (string, error) { var v string; e := r.Scan(&v); return v, e }, subject, tokenID, directory.PrincipalUser, directory.PrincipalGroup, now)
}

func (q Queries) InsertSession(ctx context.Context, s directory.Session) error {
	_, err := models.IdentitySessions.Insert(&models.IdentitySessionSetter{ID: omit.From(s.ID), CreatedAt: omit.From(s.CreatedAt), Subject: omit.From(s.Subject), SecretHash: omit.From(s.SecretHash), ExpiresAt: omit.From(s.ExpiresAt)}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) FindSession(ctx context.Context, id string) (directory.Session, error) {
	row, err := models.FindIdentitySession(ctx, q.exec, id)
	if err != nil {
		return directory.Session{}, dberr.Translate(err)
	}
	return directory.Session{ID: row.ID, Subject: row.Subject, SecretHash: row.SecretHash, CreatedAt: row.CreatedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC()}, nil
}

func (q Queries) DeleteSession(ctx context.Context, id string) error {
	_, err := models.IdentitySessions.Delete(models.DeleteWhere.IdentitySessions.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}
