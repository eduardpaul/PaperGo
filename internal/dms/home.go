package dms

import (
	"context"
	"errors"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resources"
)

var ErrProtectedHome = errors.New("Home and its default libraries are protected")

type Home struct {
	WorkspaceID        string `json:"workspace_id"`
	DocumentsLibraryID string `json:"documents_library_id"`
	InboxLibraryID     string `json:"inbox_library_id"`
}

func (s *Service) Home(ctx context.Context, subject string) (out Home, err error) {
	err = s.write(ctx, func(t *Service) error {
		if err := t.RequireScope(ctx, subject, "item.read"); err != nil {
			return err
		}
		out, err = t.ensureHome(ctx, subject)
		return err
	})
	return
}

// ensureHome creates only fixed Home resources for the active caller, inside
// the owning write transaction; it grants no general creation capability.
func (s *Service) ensureHome(ctx context.Context, subject string) (Home, error) {
	var out Home
	user, err := s.DAL.Directory().User(ctx, subject)
	if err != nil && !dberr.IsNotFound(err) {
		return out, err
	}
	if err != nil || !user.Live() {
		return out, ErrForbidden
	}
	s.homeProvisioning = true
	defer func() { s.homeProvisioning = false }()
	w, err := s.DAL.Resources().One(ctx, resources.Filter{PersonalOwnerID: subject})
	if dberr.IsNotFound(err) {
		w, err = s.create(ctx, subject, "", CreateResource{Kind: "workspace", Name: "Home", Description: "Personal workspace."}, nil)
	}
	if err != nil {
		return out, err
	}
	if w.DeletedAt != nil {
		return out, ErrNotFound
	}
	out.WorkspaceID = w.ID
	for _, recipe := range []struct {
		key, name, description string
		target                 *string
	}{
		{"home.documents", "Documents", "Your documents.", &out.DocumentsLibraryID},
		{"home.inbox", "Inbox", "New uploads land here for triage.", &out.InboxLibraryID},
	} {
		lib, err := s.DAL.Resources().One(ctx, resources.Filter{WorkspaceID: w.ID, SystemKey: resources.SystemKey(recipe.key)})
		if dberr.IsNotFound(err) {
			lib, err = s.create(ctx, subject, w.ID, CreateResource{Name: recipe.name, Description: recipe.description, TemplateKey: "documents"}, nil)
		}
		if err != nil {
			return out, err
		}
		if lib.DeletedAt != nil {
			return out, ErrNotFound
		}
		*recipe.target = lib.ID
	}
	return out, nil
}
