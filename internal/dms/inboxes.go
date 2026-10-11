package dms

import (
	"context"
	"errors"
	"papergo/ent"
	"papergo/ent/groupinbox"
	"papergo/ent/resource"
	"papergo/internal/identity"
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
	binding, err := s.Client.GroupInbox.Query().Where(groupinbox.GroupIDEQ(groupID)).Only(ctx)
	if ent.IsNotFound(err) {
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
	group, err := s.Client.IdentityGroup.Get(ctx, groupID)
	if ent.IsNotFound(err) {
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
	lib, err := s.Client.Resource.Get(ctx, libraryID)
	if ent.IsNotFound(err) {
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
func (s *Service) auditInbox(ctx context.Context, subject, action, groupID string, lib *ent.Resource) error {
	return s.Client.AuditEvent.Create().SetSubject(subject).SetAction(action).SetEntityType("group_inbox").SetEntityID(groupID).SetWorkspaceID(lib.WorkspaceID).SetResourceID(lib.ID).SetProperties([]string{"library_id"}).SetTraceID(s.traceID).SetDetails(map[string]any{"group_id": groupID, "library_id": lib.ID}).Exec(ctx)
}
func (s *Service) SetGroupInbox(ctx context.Context, subject, groupID string, in GroupInboxInput) (out GroupInbox, err error) {
	if in.LibraryID == "" {
		return out, invalid("library_id is required")
	}
	err = s.write(ctx, func(t *Service) error {
		if err := t.RequireScope(ctx, subject, "document.write"); err != nil {
			return err
		}
		group, err := t.Client.IdentityGroup.Get(ctx, groupID)
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		lib, err := t.authorize(ctx, subject, in.LibraryID, "manage")
		if err != nil {
			return err
		}
		if lib.Kind != resource.KindLibrary {
			return invalid("Inbox must identify a library")
		}
		binding, err := t.Client.GroupInbox.Query().Where(groupinbox.GroupIDEQ(groupID)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if binding != nil {
			if err = t.manageInboxLibrary(ctx, subject, binding.LibraryID); err != nil {
				return err
			}
			if binding.LibraryID != lib.ID {
				if err = t.Client.GroupInbox.UpdateOne(binding).SetLibraryID(lib.ID).Exec(ctx); err != nil {
					return err
				}
			}
		} else {
			if err = t.Client.GroupInbox.Create().SetGroupID(groupID).SetLibraryID(lib.ID).Exec(ctx); err != nil {
				return err
			}
		}
		out = GroupInbox{group.ID, group.Name, lib.WorkspaceID, lib.ID, lib.Name}
		if binding != nil && binding.LibraryID == lib.ID {
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
		binding, err := t.Client.GroupInbox.Query().Where(groupinbox.GroupIDEQ(groupID)).Only(ctx)
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err = t.manageInboxLibrary(ctx, subject, binding.LibraryID); err != nil {
			return err
		}
		lib, err := t.Client.Resource.Get(ctx, binding.LibraryID)
		if err != nil {
			return err
		}
		if err = t.Client.GroupInbox.DeleteOne(binding).Exec(ctx); err != nil {
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
		lib, err := t.Client.Resource.Get(ctx, home.InboxLibraryID)
		if err != nil {
			return err
		}
		out = []Inbox{{Kind: "personal", WorkspaceID: home.WorkspaceID, LibraryID: lib.ID, LibraryName: lib.Name}}
		groups, err := t.tx.QueryContext(ctx, identity.GroupIDsSQL, subject)
		if err != nil {
			return err
		}
		var ids []string
		for groups.Next() {
			var id string
			if err = groups.Scan(&id); err != nil {
				groups.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = groups.Err()
		groups.Close()
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
	if err := identity.RequireCredential(ctx, s.tx, subject); err != nil {
		if errors.Is(err, identity.ErrInvalidRESTToken) {
			return "", ErrForbidden
		}
		return "", err
	}
	for _, scope := range []string{"document.write", "list.read", "list.write"} {
		if err := s.RequireScope(ctx, subject, scope); err != nil {
			return "", err
		}
	}
	if groupID == "" {
		home, err := s.ensureHome(ctx, subject)
		return home.InboxLibraryID, err
	}
	var member bool
	if err := s.scan(ctx, []any{&member}, "SELECT EXISTS(SELECT 1 FROM ("+identity.GroupIDsSQL+") WHERE id=?)", subject, groupID); err != nil {
		return "", err
	}
	if !member {
		return "", ErrNotFound
	}
	binding, err := s.Client.GroupInbox.Query().Where(groupinbox.GroupIDEQ(groupID)).Only(ctx)
	if ent.IsNotFound(err) {
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
