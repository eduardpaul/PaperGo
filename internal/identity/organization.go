package identity

import (
	"context"
	"database/sql"
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

func organization(ctx context.Context, q Querier) (Organization, error) {
	var out Organization
	err := q.QueryRowContext(ctx, "SELECT id,identifier,display_name,description,version FROM identity_organizations WHERE singleton=1").Scan(&out.ID, &out.Identifier, &out.DisplayName, &out.Description, &out.Version)
	return out, err
}
func (d Directory) Organization(ctx context.Context, subject string) (Organization, error) {
	if err := RequireActive(ctx, d.DB, subject); err != nil {
		return Organization{}, err
	}
	return organization(ctx, d.DB)
}
func (d Directory) UpdateOrganization(ctx context.Context, subject string, version int, in OrganizationUpdate) (Organization, error) {
	var out Organization
	err := d.transaction(ctx, func(tx *sql.Tx) error {
		if err := RequireScope(ctx, tx, subject, "organization.manage"); err != nil {
			return err
		}
		current, err := organization(ctx, tx)
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
		if _, err = tx.ExecContext(ctx, "UPDATE identity_organizations SET identifier=?,display_name=?,description=?,version=version+1 WHERE id=?", current.Identifier, current.DisplayName, current.Description, current.ID); err != nil {
			return err
		}
		out, err = organization(ctx, tx)
		return err
	})
	return out, err
}
