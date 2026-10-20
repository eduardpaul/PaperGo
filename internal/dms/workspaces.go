package dms

import (
	"context"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/grants"
	"papergo/internal/identity"
)

// Workspace membership is the workspace's root ACL: exactly one grant per
// subject. Owners manage, members contribute, and visitors read published
// content. Collection/item ACLs can restrict members; owners retain control.
type WorkspaceMember struct {
	Subject string `json:"subject"`
	Role    string `json:"role"`
}

func validateContainerMetadata(name, description string) error {
	if !utf8.ValidString(name) || len(utf16.Encode([]rune(name))) > 200 {
		return invalid("workspace and collection names are limited to 200 characters")
	}
	if !utf8.ValidString(description) || len(utf16.Encode([]rune(description))) > 2000 {
		return invalid("workspace and collection descriptions are limited to 2000 characters")
	}
	return validateName(name)
}

func workspaceRole(action grants.Action) string {
	switch action {
	case grants.ActionManage:
		return "owner"
	case grants.ActionWrite:
		return "member"
	default:
		return "visitor"
	}
}

func (s *Service) workspace(ctx context.Context, subject, id, action string) (*resources.Resource, error) {
	r, err := s.authorize(ctx, subject, id, action)
	if err != nil {
		return nil, err
	}
	if r.Kind != refs.KindWorkspace {
		return nil, ErrNotFound
	}
	return r, nil
}

func (s *Service) WorkspaceMembers(ctx context.Context, subject, id string) ([]WorkspaceMember, error) {
	return read(ctx, s, func(t *Service) ([]WorkspaceMember, error) {
		if _, err := t.workspace(ctx, subject, id, "read"); err != nil {
			return nil, err
		}
		rows, err := t.DAL.Grants().List(ctx, grants.Filter{ResourceID: id, ByPrincipal: true})
		if err != nil {
			return nil, err
		}
		members := make([]WorkspaceMember, 0, len(rows))
		for _, g := range rows {
			members = append(members, WorkspaceMember{Subject: g.PrincipalID, Role: workspaceRole(g.Action)})
		}
		return members, nil
	})
}

// WorkspaceManagers returns the users granted manage access on the
// workspace itself, inside the caller's transaction.
func (s *Service) WorkspaceManagers(ctx context.Context, id string) ([]string, error) {
	rows, err := s.DAL.Grants().List(ctx, grants.Filter{ResourceID: id, Action: grants.ActionManage, PrincipalType: grants.PrincipalUser, ByPrincipal: true})
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, g := range rows {
		out[i] = g.PrincipalID
	}
	return out, nil
}

// SetWorkspaceMember adds a member or replaces their role. A retry is a no-op.
// Membership changes do not consume the workspace metadata ETag.
func (s *Service) SetWorkspaceMember(ctx context.Context, subject, id string, in WorkspaceMember) error {
	if in.Subject == "" || in.Subject != strings.TrimSpace(in.Subject) || len(in.Subject) > 255 || !utf8.ValidString(in.Subject) {
		return invalid("member subject must contain 1 to 255 bytes without surrounding whitespace")
	}
	if in.Role == "" {
		in.Role = "member"
	}
	action := grants.ActionRead
	switch in.Role {
	case "owner":
		action = grants.ActionManage
	case "member":
		action = grants.ActionWrite
	case "visitor":
	default:
		return invalid("workspace role must be owner, member or visitor")
	}
	return s.write(ctx, func(t *Service) error {
		w, err := t.workspace(ctx, subject, id, "manage")
		if err != nil {
			return err
		}
		if w.PersonalOwnerID != nil {
			return ErrProtectedHome
		}
		if err = identity.ValidatePrincipal(ctx, dal.NewStore(t.db).WithTx(t.tx), identity.Principal{Type: directory.PrincipalUser, ID: in.Subject}); err != nil {
			return invalid(err.Error())
		}
		active, err := t.DAL.Directory().ActiveUser(ctx, in.Subject)
		if err != nil {
			return err
		}
		if !active {
			return invalid("workspace members must be active users of the organization")
		}
		existing, err := t.DAL.Grants().List(ctx, grants.Filter{ResourceID: id, PrincipalID: in.Subject, PrincipalType: grants.PrincipalUser})
		if err != nil {
			return err
		}
		previousRole := ""
		if len(existing) == 0 {
			_, err = t.DAL.Grants().Create(ctx, grants.Grant{ResourceID: id, PrincipalType: grants.PrincipalUser, PrincipalID: in.Subject, Action: action})
		} else {
			old := existing[0]
			if old.Action == action {
				return nil
			}
			previousRole = workspaceRole(old.Action)
			_, err = t.DAL.Grants().ChangeAction(ctx, old.ID, action)
		}
		if err != nil {
			return lastOwner(err)
		}
		t.emitPermissionsChanged(ctx, subject, w, map[string]any{"action": "member.set", "member_subject": in.Subject, "previous_role": previousRole, "role": in.Role})
		return t.audit(ctx, subject, "workspace.member.set", w, map[string]any{"subject": in.Subject, "role": in.Role})
	})
}

// lastOwner reports removing a workspace's last active owner as ErrLastOwner.
func lastOwner(err error) error {
	if errors.Is(err, dberr.ErrLastWorkspaceOwner) {
		return ErrLastOwner
	}
	return err
}

func (s *Service) RemoveWorkspaceMember(ctx context.Context, subject, id, member string) error {
	return s.write(ctx, func(t *Service) error {
		w, err := t.workspace(ctx, subject, id, "manage")
		if err != nil {
			return err
		}
		if w.PersonalOwnerID != nil {
			return ErrProtectedHome
		}
		existing, err := t.DAL.Grants().List(ctx, grants.Filter{ResourceID: id, PrincipalID: member, PrincipalType: grants.PrincipalUser})
		if err != nil {
			return err
		}
		if len(existing) == 0 {
			return ErrNotFound
		}
		g := existing[0]
		if err = t.DAL.Grants().Revoke(ctx, g.ID); err != nil {
			return lastOwner(err)
		}
		t.emitPermissionsChanged(ctx, subject, w, map[string]any{"action": "member.remove", "member_subject": member, "previous_role": workspaceRole(g.Action), "role": ""})
		return t.audit(ctx, subject, "workspace.member.remove", w, map[string]any{"subject": member})
	})
}
