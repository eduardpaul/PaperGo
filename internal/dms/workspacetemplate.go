package dms

import (
	"context"
	"papergo/internal/dal/portable"
)

// A configuration template can bind its single source account to the importing
// administrator. Ordinary copies retain directory identities. Bind only identity
// positions; text, prompts and other literal values are never substituted.
func (s *Service) bindWorkspaceTemplate(ctx context.Context, p *corePackage, subject string) error {
	if p.TemplateSubject == "" {
		return nil
	}
	source := p.TemplateSubject
	users := packageMaps(p.Tables["identity_users"])
	if len(users) != 1 || users[0]["id"] != source {
		return invalid("workspace templates require exactly their template_subject account")
	}
	for _, table := range []string{"identity_groups", "identity_memberships", "identity_roles", "identity_assignments", "identity_preferences", "group_inboxes"} {
		if len(p.Tables[table].Rows) != 0 {
			return invalid("workspace templates cannot include " + table)
		}
	}
	user, err := s.DAL.Portable().Find(ctx, "identity_users", portable.Row{"id": subject})
	if err != nil {
		return err
	}
	columns, err := s.packageColumns(ctx, "identity_users")
	if err != nil {
		return err
	}
	account := make([]any, len(columns))
	for i, column := range columns {
		account[i] = user[column]
	}
	for table, data := range p.Tables {
		if table == "identity_users" {
			data.Rows = [][]any{account}
		} else {
			for _, row := range data.Rows {
				for i, column := range data.Columns {
					identity := column == "created_by" || column == "updated_by" || column == "modified_by" || column == "archived_by" || column == "owner_id"
					if column == "principal_id" {
						for j, key := range data.Columns {
							if key == "principal_type" && row[j] == "user" {
								identity = true
							}
						}
					}
					if identity && row[i] != nil && row[i] != "" {
						if row[i] != source {
							return invalid("workspace template contains another subject in " + table + "." + column)
						}
						row[i] = subject
					}
				}
			}
		}
		p.Tables[table] = data
	}
	p.TemplateSubject = ""
	return validateWorkspacePackage(*p)
}
