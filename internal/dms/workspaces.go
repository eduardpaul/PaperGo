package dms

import (
	"context"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"papergo/ent"
	"papergo/ent/grant"
	"papergo/ent/resource"
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

func workspaceRole(action grant.Action) string {
	switch action {
	case grant.ActionManage:
		return "owner"
	case grant.ActionWrite:
		return "member"
	default:
		return "visitor"
	}
}

func (s *Service) workspace(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	r, err := s.authorize(ctx, subject, id, action)
	if err != nil {
		return nil, err
	}
	if r.Kind != resource.KindWorkspace {
		return nil, ErrNotFound
	}
	return r, nil
}

func (s *Service) WorkspaceMembers(ctx context.Context, subject, id string) ([]WorkspaceMember, error) {
	return read(ctx, s, func(t *Service) ([]WorkspaceMember, error) {
		if _, err := t.workspace(ctx, subject, id, "read"); err != nil {
			return nil, err
		}
		grants, err := t.Client.Grant.Query().Where(grant.ResourceIDEQ(id)).Order(ent.Asc(grant.FieldPrincipalID)).All(ctx)
		if err != nil {
			return nil, err
		}
		members := make([]WorkspaceMember, 0, len(grants))
		for _, g := range grants {
			members = append(members, WorkspaceMember{Subject: g.PrincipalID, Role: workspaceRole(g.Action)})
		}
		return members, nil
	})
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
	action := grant.ActionRead
	switch in.Role {
	case "owner":
		action = grant.ActionManage
	case "member":
		action = grant.ActionWrite
	case "visitor":
	default:
		return invalid("workspace role must be owner, member or visitor")
	}
	return s.write(ctx, func(t *Service) error {
		w, err := t.workspace(ctx, subject, id, "manage")
		if err != nil {
			return err
		}
		if err = identity.ValidatePrincipal(ctx, t.tx, identity.Principal{Type: "user", ID: in.Subject}); err != nil {
			return invalid(err.Error())
		}
		old, err := t.Client.Grant.Query().Where(grant.ResourceIDEQ(id), grant.PrincipalIDEQ(in.Subject), grant.PrincipalTypeEQ(grant.PrincipalTypeUser)).Only(ctx)
		if ent.IsNotFound(err) {
			_, err = t.Client.Grant.Create().SetResourceID(id).SetPrincipalType(grant.PrincipalTypeUser).SetPrincipalID(in.Subject).SetAction(action).Save(ctx)
		} else if err == nil {
			if old.Action == action {
				return nil
			}
			if old.Action == grant.ActionManage && action != grant.ActionManage {
				if err = t.checkOtherOwner(ctx, id, in.Subject); err != nil {
					return err
				}
			}
			_, err = t.Client.Grant.UpdateOne(old).SetAction(action).Save(ctx)
		}
		if err != nil {
			return err
		}
		return t.audit(ctx, subject, "workspace.member.set", w, map[string]any{"subject": in.Subject, "role": in.Role})
	})
}

func (s *Service) checkOtherOwner(ctx context.Context, id, member string) error {
	other, err := s.Client.Grant.Query().Where(grant.ResourceIDEQ(id), grant.PrincipalIDNEQ(member), grant.ActionEQ(grant.ActionManage)).Exist(ctx)
	if err != nil {
		return err
	}
	if !other {
		return ErrLastOwner
	}
	return nil
}

func (s *Service) RemoveWorkspaceMember(ctx context.Context, subject, id, member string) error {
	return s.write(ctx, func(t *Service) error {
		w, err := t.workspace(ctx, subject, id, "manage")
		if err != nil {
			return err
		}
		g, err := t.Client.Grant.Query().Where(grant.ResourceIDEQ(id), grant.PrincipalIDEQ(member), grant.PrincipalTypeEQ(grant.PrincipalTypeUser)).Only(ctx)
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if g.Action == grant.ActionManage {
			if err = t.checkOtherOwner(ctx, id, member); err != nil {
				return err
			}
		}
		if err = t.Client.Grant.DeleteOne(g).Exec(ctx); err != nil {
			return err
		}
		return t.audit(ctx, subject, "workspace.member.remove", w, map[string]any{"subject": member})
	})
}
