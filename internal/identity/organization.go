package identity

import (
	"context"
	"database/sql"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Organization struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
}
type OrganizationUpdate struct {
	Identifier  *string `json:"identifier,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Description *string `json:"description,omitempty"`
}

var organizationIdentifier = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

func organization(ctx context.Context, store *dal.Store) (Organization, error) {
	row, err := store.Directory().Organization(ctx)
	if err != nil {
		return Organization{}, err
	}
	return Organization{ID: row.ID, Identifier: row.Identifier, DisplayName: row.DisplayName, Description: row.Description, Version: int(row.Version)}, nil
}

func (d Directory) Organization(ctx context.Context, subject string) (Organization, error) {
	if err := RequireActive(ctx, dal.NewStore(d.DB), subject); err != nil {
		return Organization{}, err
	}
	return organization(ctx, dal.NewStore(d.DB))
}
func (d Directory) UpdateOrganization(ctx context.Context, subject string, version int, in OrganizationUpdate) (Organization, error) {
	var out Organization
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), subject, directory.EntityOrganization, directory.ChangeUpdate, "", func() error {
			if err := RequireScope(ctx, d.store(tx), subject, "organization.manage"); err != nil {
				return err
			}
			current, err := organization(ctx, d.store(tx))
			if err != nil {
				return err
			}
			if current.Version != version {
				return ErrPreconditionFailed
			}
			if in.Identifier != nil {
				current.Identifier = strings.TrimSpace(*in.Identifier)
			}
			if in.DisplayName != nil {
				current.DisplayName = strings.TrimSpace(*in.DisplayName)
			}
			if in.Description != nil {
				current.Description = *in.Description
			}
			if !organizationIdentifier.MatchString(current.Identifier) || current.DisplayName == "" || !utf8.ValidString(current.Description) || len(current.Description) > 2000 {
				return invalid("organization requires an identifier of 1..128 lowercase letters, digits or hyphens, a display name, and a description of at most 2000 bytes")
			}
			if err = validateProfile(current.DisplayName, ""); err != nil {
				return err
			}
			if err = d.store(tx).Directory().SaveOrganization(ctx, directory.Organization{ID: current.ID, Identifier: current.Identifier, DisplayName: current.DisplayName, Description: current.Description}, int64(version)); err != nil {
				if errors.Is(err, dberr.ErrConcurrentUpdate) {
					return ErrPreconditionFailed
				}
				return err
			}

			out, err = organization(ctx, d.store(tx))
			return err
		})
	})
	return out, err
}
