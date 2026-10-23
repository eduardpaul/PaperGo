package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type UserProfile struct {
	Subject     string     `json:"subject"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email"`
	Active      bool       `json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}
type UserInput struct {
	Subject     string `json:"subject"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}
type UserUpdate struct {
	DisplayName *string `json:"display_name,omitempty"`
	Email       *string `json:"email,omitempty"`
}
type MeProfile struct {
	OrganizationID         string `json:"organization_id"`
	OrganizationIdentifier string `json:"organization_identifier"`
	UserProfile
	Groups []string `json:"groups"`
	Scopes []string `json:"scopes"`
	// Resources are the workspaces, lists and libraries the request's access
	// token is limited to; absent when it reaches everything.
	Resources []string `json:"resources,omitempty"`
}

var ErrCannotDeleteSelf = errors.New("you cannot delete your own account")
var ErrLastWorkspaceOwner = errors.New("a workspace needs at least one active owner")

func RequireActive(ctx context.Context, store *dal.Store, subject string) error {
	user, err := store.Directory().User(ctx, subject)
	if dberr.IsNotFound(err) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if !user.Live() {
		return ErrForbidden
	}
	return nil
}

func validateProfile(name, email string) error {
	if !utf8.ValidString(name) || len([]rune(name)) > 200 || !utf8.ValidString(email) || len(email) > 256 {
		return invalid("display name allows 200 characters; email allows 256 bytes")
	}
	if email != "" {
		address, err := mail.ParseAddress(email)
		if err != nil || address.Address != email {
			return invalid("email must be a plain email address")
		}
	}
	return nil
}
func profileRecord(u directory.User) UserProfile {
	name := u.DisplayName
	if name == "" {
		name = u.ID
	}
	return UserProfile{Subject: u.ID, DisplayName: name, Email: u.Email, Active: u.Active, CreatedAt: u.CreatedAt, DeletedAt: u.DeletedAt}
}
func userProfile(ctx context.Context, store *dal.Store, subject string) (UserProfile, error) {
	u, err := store.Directory().User(ctx, subject)
	return profileRecord(u), identityError(err)
}
func (d Directory) User(ctx context.Context, actor, subject string) (UserProfile, error) {
	if err := RequireScope(ctx, dal.NewStore(d.DB), actor, "user.read"); err != nil {
		return UserProfile{}, err
	}
	return userProfile(ctx, dal.NewStore(d.DB), subject)
}
func (d Directory) Me(ctx context.Context, subject string) (MeProfile, error) {
	var out MeProfile
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		out.UserProfile, err = userProfile(ctx, d.store(tx), subject)
		if err != nil {
			return err
		}
		if !out.Active || out.DeletedAt != nil {
			return ErrForbidden
		}
		org, err := organization(ctx, d.store(tx))
		if err != nil {
			return err
		}
		out.OrganizationID = org.ID
		out.OrganizationIdentifier = org.Identifier
		out.Scopes, err = EffectiveScopes(ctx, d.store(tx), subject)
		if err != nil {
			return err
		}
		if token, ok := AccessTokenContext(ctx); ok && len(token.Resources) > 0 {
			out.Resources = token.Resources
		}
		memberships, err := d.store(tx).Directory().Memberships(ctx, directory.MembershipFilter{Principal: &Principal{Type: directory.PrincipalUser, ID: subject}})
		if err != nil {
			return err
		}
		out.Groups = []string{}
		for _, m := range memberships {
			if m.Principal.Type == directory.PrincipalUser && m.Principal.ID == subject {
				g, e := d.store(tx).Directory().Group(ctx, m.GroupID)
				if e != nil {
					return e
				}
				out.Groups = append(out.Groups, g.Name)
			}
		}
		sort.Strings(out.Groups)
		return nil
	})
	return out, err
}
func (d Directory) UpdateMe(ctx context.Context, subject string, name *string) (MeProfile, error) {
	if name == nil {
		return d.Me(ctx, subject)
	}
	nameValue := strings.TrimSpace(*name)
	if err := validateProfile(nameValue, ""); err != nil {
		return MeProfile{}, err
	}
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), subject, directory.EntityUser, directory.ChangeUpdate, subject, func() error {
			out, err := userProfile(ctx, d.store(tx), subject)
			if err != nil {
				return err
			}
			if !out.Active || out.DeletedAt != nil {
				return ErrForbidden
			}
			user, err := d.store(tx).Directory().User(ctx, subject)
			if err != nil {
				return err
			}
			user.DisplayName = nameValue
			return d.store(tx).Directory().SaveUser(ctx, user)
		})
	})
	if err != nil {
		return MeProfile{}, err
	}
	return d.Me(ctx, subject)
}
func (d Directory) UpdateUser(ctx context.Context, actor, subject string, in UserUpdate) (UserProfile, error) {
	var out UserProfile
	err := d.write(ctx, actor, "user.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityUser, directory.ChangeUpdate, subject, func() error {
			current, err := userProfile(ctx, d.store(tx), subject)
			if err != nil {
				return err
			}
			if current.DeletedAt != nil {
				return ErrNotFound
			}
			if in.DisplayName != nil {
				current.DisplayName = strings.TrimSpace(*in.DisplayName)
			}
			if in.Email != nil {
				current.Email = strings.TrimSpace(*in.Email)
			}
			if err = validateProfile(current.DisplayName, current.Email); err != nil {
				return err
			}
			user, err := d.store(tx).Directory().User(ctx, subject)
			if err != nil {
				return err
			}
			user.DisplayName = current.DisplayName
			user.Email = current.Email
			if err = d.store(tx).Directory().SaveUser(ctx, user); err != nil {
				return err
			}
			out, err = userProfile(ctx, d.store(tx), subject)
			return err
		})
	})
	return out, err
}

// DeleteUserInTx participates in the content service transaction so Home cleanup
// and account anonymization commit together. purgeHome must be supplied by it.
func DeleteUserInTx(ctx context.Context, store *dal.Store, actor, subject string, purgeHome func() error) error {
	if err := store.Directory().Lock(ctx); err != nil {
		return err
	}

	if err := RequireScope(ctx, store, actor, "user.manage"); err != nil {
		return err
	}
	err := auditedIdentity(ctx, store, actor, directory.EntityUser, directory.ChangeDelete, subject, func() error {
		if actor == subject {
			return ErrCannotDeleteSelf
		}
		current, err := userProfile(ctx, store, subject)
		if err != nil {
			return err
		}
		if current.DeletedAt != nil {
			return ErrNotFound
		}
		lastOwner, err := store.Directory().LastWorkspaceOwner(ctx, subject)
		if err != nil {
			return err
		}
		if lastOwner {
			return ErrLastWorkspaceOwner
		}
		if err = purgeHome(); err != nil {
			return err
		}
		return store.Directory().DeleteUser(ctx, subject)
	})
	if err != nil {
		return err
	}
	return checkAdministrator(ctx, store)
}
