package sqlite

import (
	"context"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
)

func (q Queries) DirectoryUser(ctx context.Context, id string) (directory.User, error) {
	row, err := models.FindIdentityUser(ctx, q.exec, id)
	if err != nil {
		return directory.User{}, dberr.Translate(err)
	}
	return userRecord(row), nil
}

func (q Queries) DirectoryUsers(ctx context.Context, f directory.UserFilter) ([]directory.User, error) {
	w := models.SelectWhere.IdentityUsers
	query := models.IdentityUsers.Query(sm.OrderBy(models.IdentityUsers.Columns.ID))
	if len(f.IDs) > 0 {
		query.Apply(w.ID.In(f.IDs...))
	}
	if f.Active {
		query.Apply(w.Active.EQ(true))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]directory.User, len(rows))
	for i, row := range rows {
		out[i] = userRecord(row)
	}
	return out, nil
}

func (q Queries) DirectoryGroup(ctx context.Context, id string) (directory.Group, error) {
	row, err := models.FindIdentityGroup(ctx, q.exec, id)
	if err != nil {
		return directory.Group{}, dberr.Translate(err)
	}
	return directory.Group{ID: row.ID, Name: row.Name, Description: row.Description, Source: directory.GroupSource(row.Source), Version: int(row.Version), CreatedAt: row.CreatedAt.UTC()}, nil
}

func (q Queries) GroupInbox(ctx context.Context, groupID string) (directory.Inbox, error) {
	row, err := models.GroupInboxes.Query(models.SelectWhere.GroupInboxes.GroupID.EQ(groupID)).One(ctx, q.exec)
	if err != nil {
		return directory.Inbox{}, dberr.Translate(err)
	}
	return directory.Inbox{ID: row.ID, GroupID: row.GroupID, LibraryID: row.LibraryID, CreatedAt: row.CreatedAt.UTC()}, nil
}

func (q Queries) InsertGroupInbox(ctx context.Context, i directory.Inbox) error {
	_, err := models.GroupInboxes.Insert(&models.GroupInboxSetter{
		ID: omit.From(i.ID), CreatedAt: omit.From(i.CreatedAt), GroupID: omit.From(i.GroupID), LibraryID: omit.From(i.LibraryID),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetGroupInboxLibrary(ctx context.Context, id, libraryID string) error {
	set := &models.GroupInboxSetter{LibraryID: omit.From(libraryID)}
	_, err := models.GroupInboxes.Update(set.UpdateMod(), models.UpdateWhere.GroupInboxes.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteGroupInbox(ctx context.Context, id string) error {
	_, err := models.GroupInboxes.Delete(models.DeleteWhere.GroupInboxes.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func userRecord(r *models.IdentityUser) directory.User {
	return directory.User{
		ID: r.ID, DisplayName: r.DisplayName, Email: r.Email, Active: r.Active, Service: r.Service,
		DeletedAt: nullable.UTC(r.DeletedAt), CreatedAt: r.CreatedAt.UTC(), SyncHash: r.SyncHash, PasswordHash: r.PasswordHash,
	}
}
