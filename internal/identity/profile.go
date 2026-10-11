package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
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
	Scopes []string `json:"scopes"`
}

var ErrCannotDeleteSelf = errors.New("you cannot delete your own account")
var ErrLastWorkspaceOwner = errors.New("a workspace needs at least one active owner")

func RequireActive(ctx context.Context, q Querier, subject string) error {
	var active bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_users WHERE id=? AND active=1 AND deleted_at IS NULL)", subject).Scan(&active); err != nil {
		return err
	}
	if !active {
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
func userProfile(ctx context.Context, q Querier, subject string) (UserProfile, error) {
	out := UserProfile{Subject: subject}
	var deleted sql.NullTime
	err := q.QueryRowContext(ctx, "SELECT display_name,email,active,created_at,deleted_at FROM identity_users WHERE id=?", subject).Scan(&out.DisplayName, &out.Email, &out.Active, &out.CreatedAt, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if deleted.Valid {
		out.DeletedAt = &deleted.Time
	}
	if out.DisplayName == "" {
		out.DisplayName = subject
	}
	return out, err
}
func (d Directory) User(ctx context.Context, actor, subject string) (UserProfile, error) {
	if err := RequireScope(ctx, d.DB, actor, "user.read"); err != nil {
		return UserProfile{}, err
	}
	return userProfile(ctx, d.DB, subject)
}
func (d Directory) Me(ctx context.Context, subject string) (MeProfile, error) {
	var out MeProfile
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		out.UserProfile, err = userProfile(ctx, tx, subject)
		if err != nil {
			return err
		}
		if !out.Active || out.DeletedAt != nil {
			return ErrForbidden
		}
		org, err := organization(ctx, tx)
		if err != nil {
			return err
		}
		out.OrganizationID = org.ID
		out.OrganizationIdentifier = org.Identifier
		out.Scopes, err = EffectiveScopes(ctx, tx, subject)
		return err
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
		return auditedIdentity(ctx, tx, subject, "user.update", "user", subject, func() error {
			out, err := userProfile(ctx, tx, subject)
			if err != nil {
				return err
			}
			if !out.Active || out.DeletedAt != nil {
				return ErrForbidden
			}
			_, err = tx.ExecContext(ctx, "UPDATE identity_users SET display_name=? WHERE id=?", nameValue, subject)
			return err
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
		return auditedIdentity(ctx, tx, actor, "user.update", "user", subject, func() error {
			current, err := userProfile(ctx, tx, subject)
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
			if _, err = tx.ExecContext(ctx, "UPDATE identity_users SET display_name=?,email=? WHERE id=?", current.DisplayName, current.Email, subject); err != nil {
				return err
			}
			out, err = userProfile(ctx, tx, subject)
			return err
		})
	})
	return out, err
}

// DeleteUserInTx participates in the content service transaction so Home cleanup
// and account anonymization commit together. purgeHome must be supplied by it.
func DeleteUserInTx(ctx context.Context, tx *sql.Tx, actor, subject string, purgeHome func() error) error {
	if err := RequireScope(ctx, tx, actor, "user.manage"); err != nil {
		return err
	}
	err := auditedIdentity(ctx, tx, actor, "user.delete", "user", subject, func() error {
		if actor == subject {
			return ErrCannotDeleteSelf
		}
		current, err := userProfile(ctx, tx, subject)
		if err != nil {
			return err
		}
		if current.DeletedAt != nil {
			return ErrNotFound
		}
		var lastOwner bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM grants g JOIN resources w ON w.id=g.resource_id WHERE w.kind='workspace' AND w.personal_owner_id IS NULL AND w.deleted_at IS NULL AND g.principal_type='user' AND g.principal_id=? AND g.action='manage' AND NOT EXISTS(SELECT 1 FROM grants other JOIN identity_users u ON u.id=other.principal_id WHERE other.resource_id=w.id AND other.principal_type='user' AND other.action='manage' AND other.principal_id<>? AND u.active=1))", subject, subject).Scan(&lastOwner); err != nil {
			return err
		}
		if lastOwner {
			return ErrLastWorkspaceOwner
		}
		if err = purgeHome(); err != nil {
			return err
		}
		for _, query := range []string{
			"DELETE FROM identity_memberships WHERE principal_type='user' AND principal_id=?",
			"DELETE FROM identity_assignments WHERE principal_type='user' AND principal_id=?",
			"DELETE FROM identity_preferences WHERE owner_type='user' AND owner_id=?",
			"DELETE FROM smart_folders WHERE owner_id=?",
			"DELETE FROM grants WHERE principal_type='user' AND principal_id=?",
			"DELETE FROM webdav_credentials WHERE subject=?",
			"DELETE FROM identity_rest_tokens WHERE subject=?",
			"DELETE FROM sync_tokens WHERE view_id IN (SELECT id FROM sync_views WHERE subject=?)",
			"DELETE FROM sync_states WHERE view_id IN (SELECT id FROM sync_views WHERE subject=?)",
			"DELETE FROM sync_views WHERE subject=?",
		} {
			if _, err = tx.ExecContext(ctx, query, subject); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE identity_users SET active=0,display_name='Deleted user',email='',deleted_at=? WHERE id=?", time.Now().UTC(), subject)
		return err
	})
	if err != nil {
		return err
	}
	return checkAdministrator(ctx, tx)
}
