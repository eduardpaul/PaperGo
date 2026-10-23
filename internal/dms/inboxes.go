package dms

import (
	"context"
	"errors"
	"papergo/internal/dal"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/identity"
	"slices"
	"sort"
	"strings"
)

type GroupInboxInput struct {
	LibraryID string `json:"library_id"`
}
type GroupInbox struct {
	GroupID     string `json:"group_id"`
	GroupName   string `json:"group_name"`
	WorkspaceID string `json:"workspace_id"`
	LibraryID   string `json:"library_id"`
	LibraryName string `json:"library_name"`
}
type Inbox struct {
	Kind        string  `json:"kind"`
	WorkspaceID string  `json:"workspace_id"`
	LibraryID   string  `json:"library_id"`
	LibraryName string  `json:"library_name"`
	GroupID     *string `json:"group_id"`
	GroupName   *string `json:"group_name"`
}

func (s *Service) groupInbox(ctx context.Context, subject, groupID string) (GroupInbox, error) {
	var out GroupInbox
	binding, err := s.DAL.Directory().Inbox(ctx, groupID)
	if dberr.IsNotFound(err) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	lib, err := s.authorize(ctx, subject, binding.LibraryID, "read")
	if errors.Is(err, ErrForbidden) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	group, err := s.DAL.Directory().Group(ctx, groupID)
	if dberr.IsNotFound(err) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	return GroupInbox{group.ID, group.Name, lib.WorkspaceID, lib.ID, lib.Name}, nil
}
func (s *Service) GroupInbox(ctx context.Context, subject, groupID string) (GroupInbox, error) {
	return read(ctx, s, func(t *Service) (GroupInbox, error) {
		if err := t.RequireScope(ctx, subject, "document.read"); err != nil {
			return GroupInbox{}, err
		}
		return t.groupInbox(ctx, subject, groupID)
	})
}
func (s *Service) manageInboxLibrary(ctx context.Context, subject, libraryID string) error {
	lib, err := s.DAL.Resources().Resource(ctx, libraryID)
	if dberr.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if lib.DeletedAt != nil {
		return nil
	}
	_, err = s.authorize(ctx, subject, libraryID, "manage")
	return err
}
func (s *Service) auditInbox(ctx context.Context, subject, action, groupID string, lib *resources.Resource) error {
	_, err := s.DAL.Audit().Append(ctx, auditstore.Event{
		WorkspaceID: lib.WorkspaceID, ResourceID: lib.ID, Subject: subject, Action: action, EntityType: "group_inbox", EntityID: groupID,
		TraceID: s.traceID, Properties: []string{"library_id"}, Details: map[string]any{"group_id": groupID, "library_id": lib.ID},
	})
	return err
}
func (s *Service) SetGroupInbox(ctx context.Context, subject, groupID string, in GroupInboxInput) (out GroupInbox, err error) {
	if in.LibraryID == "" {
		return out, invalid("library_id is required")
	}
	err = s.write(ctx, func(t *Service) error {
		if err := t.RequireScope(ctx, subject, "document.write"); err != nil {
			return err
		}
		group, err := t.DAL.Directory().Group(ctx, groupID)
		if dberr.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		lib, err := t.authorize(ctx, subject, in.LibraryID, "manage")
		if err != nil {
			return err
		}
		if lib.Kind != refs.KindLibrary {
			return invalid("Inbox must identify a library")
		}
		binding, err := t.DAL.Directory().Inbox(ctx, groupID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err == nil {
			if err = t.manageInboxLibrary(ctx, subject, binding.LibraryID); err != nil {
				return err
			}
		}
		changed, err := t.DAL.Directory().BindInbox(ctx, groupID, lib.ID)
		if err != nil {
			return err
		}
		out = GroupInbox{group.ID, group.Name, lib.WorkspaceID, lib.ID, lib.Name}
		if !changed {
			return nil
		}
		return t.auditInbox(ctx, subject, "group_inbox.set", groupID, lib)
	})
	return
}
func (s *Service) RemoveGroupInbox(ctx context.Context, subject, groupID string) error {
	return s.write(ctx, func(t *Service) error {
		if err := t.RequireScope(ctx, subject, "document.write"); err != nil {
			return err
		}
		binding, err := t.DAL.Directory().Inbox(ctx, groupID)
		if dberr.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err = t.manageInboxLibrary(ctx, subject, binding.LibraryID); err != nil {
			return err
		}
		lib, err := t.DAL.Resources().Resource(ctx, binding.LibraryID)
		if err != nil {
			return err
		}
		if err = t.DAL.Directory().UnbindInbox(ctx, groupID); err != nil {
			return err
		}
		return t.auditInbox(ctx, subject, "group_inbox.delete", groupID, lib)
	})
}
func (s *Service) Inboxes(ctx context.Context, subject string) (out []Inbox, err error) {
	err = s.write(ctx, func(t *Service) error {
		if err := t.RequireScope(ctx, subject, "document.read"); err != nil {
			return err
		}
		home, err := t.ensureHome(ctx, subject)
		if err != nil {
			return err
		}
		lib, err := t.DAL.Resources().Resource(ctx, home.InboxLibraryID)
		if err != nil {
			return err
		}
		out = []Inbox{{Kind: "personal", WorkspaceID: home.WorkspaceID, LibraryID: lib.ID, LibraryName: lib.Name}}
		ids, err := t.DAL.Directory().EffectiveGroups(ctx, subject)
		if err != nil {
			return err
		}
		var shared []Inbox
		for _, id := range ids {
			binding, err := t.groupInbox(ctx, subject, id)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			groupID, groupName := binding.GroupID, binding.GroupName
			shared = append(shared, Inbox{Kind: "group", WorkspaceID: binding.WorkspaceID, LibraryID: binding.LibraryID, LibraryName: binding.LibraryName, GroupID: &groupID, GroupName: &groupName})
		}
		sort.Slice(shared, func(i, j int) bool {
			a, b := strings.ToLower(*shared[i].GroupName), strings.ToLower(*shared[j].GroupName)
			if a == b {
				return *shared[i].GroupID < *shared[j].GroupID
			}
			return a < b
		})
		out = append(out, shared...)
		return nil
	})
	return
}

// inboxUploadDestination runs both before streaming and in the commit transaction.
func (s *Service) inboxUploadDestination(ctx context.Context, subject, groupID string) (string, error) {
	if err := identity.RequireCredential(ctx, dal.NewStore(s.db).WithTx(s.tx), subject); err != nil {
		if errors.Is(err, identity.ErrInvalidAccessToken) {
			return "", ErrForbidden
		}
		return "", err
	}
	for _, scope := range []string{"document.write", "item.read", "item.write"} {
		if err := s.RequireScope(ctx, subject, scope); err != nil {
			return "", err
		}
	}
	if groupID == "" {
		home, err := s.ensureHome(ctx, subject)
		return home.InboxLibraryID, err
	}
	groups, err := s.DAL.Directory().EffectiveGroups(ctx, subject)
	if err != nil {
		return "", err
	}
	if !slices.Contains(groups, groupID) {
		return "", ErrNotFound
	}
	binding, err := s.DAL.Directory().Inbox(ctx, groupID)
	if dberr.IsNotFound(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err = s.documentParent(ctx, subject, binding.LibraryID, ""); err != nil {
		return "", err
	}
	return binding.LibraryID, nil
}
func (s *Service) InboxUploadDestination(ctx context.Context, subject, groupID string) (out string, err error) {
	err = s.write(ctx, func(t *Service) error { out, err = t.inboxUploadDestination(ctx, subject, groupID); return err })
	return
}
func (s *Service) CreateInboxDocument(ctx context.Context, subject, groupID, libraryID string, metadata DocumentMetadata, content BlobInput) (out *DocumentUpload, err error) {
	if metadata.FolderID != "" || metadata.ContentTypeID != "" {
		return nil, invalid("Inbox uploads use the library root and default content type")
	}
	err = s.write(ctx, func(t *Service) error {
		current, err := t.inboxUploadDestination(ctx, subject, groupID)
		if err != nil {
			return err
		}
		if current != libraryID {
			return ErrConflict
		}
		out, err = t.CreateDocument(ctx, subject, current, metadata, content)
		return err
	})
	return
}
