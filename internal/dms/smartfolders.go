package dms

import (
	"context"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"papergo/ent"
	"papergo/ent/smartfolder"
	"papergo/ent/term"
	"strings"
	"time"
)

const MaxSmartCollections = 100
const MaxSmartGroupLevels = 3
const MaxSharedSmartFolders = 100

type SmartFolderGroupBy struct {
	Field string `json:"field" doc:"Indexed scalar field key or system field."`
	By    string `json:"by,omitempty" enum:"year,month" doc:"Date bucket for date/datetime fields."`
}

type SmartFolderDefinition struct {
	Collections    []string             `json:"collections,omitempty" maxItems:"100" uniqueItems:"true" doc:"Collection names, ignoring case."`
	Templates      []string             `json:"templates,omitempty" maxItems:"100" uniqueItems:"true" doc:"Adopted template keys, ignoring case."`
	ContentTypes   []string             `json:"content_types,omitempty" maxItems:"100" uniqueItems:"true" doc:"Content type keys or names, ignoring case."`
	Terms          []string             `json:"terms,omitempty" maxItems:"20" uniqueItems:"true" doc:"Term IDs; descendants match too."`
	TermMatch      string               `json:"term_match,omitempty" enum:"all,any" doc:"Defaults to all."`
	Filter         *FilterExpr          `json:"filter,omitempty"`
	GroupBy        []SmartFolderGroupBy `json:"group_by,omitempty" maxItems:"3" doc:"Navigation levels."`
	IncludeFolders bool                 `json:"include_folders,omitempty"`
}

type SmartFolderInput struct {
	Name        string                `json:"name" minLength:"1" maxLength:"255"`
	Description string                `json:"description,omitempty" maxLength:"4096"`
	WorkspaceID *string               `json:"workspace_id,omitempty" format:"uuid" doc:"Required for shared folders."`
	Personal    bool                  `json:"personal"`
	Definition  SmartFolderDefinition `json:"definition"`
}

type SmartFolderQueryRequest struct {
	Path    []*string `json:"path,omitempty" maxItems:"3" doc:"One value per navigation level; null means missing. Numeric and boolean values use canonical strings, dates use YYYY or YYYY-MM when grouped."`
	Surface string    `json:"surface,omitempty" enum:"auto,head,published" doc:"Defaults to auto."`
	After   string    `json:"after,omitempty" doc:"Opaque cursor from next_cursor."`
	Limit   int       `json:"limit,omitempty" minimum:"1" maximum:"100" doc:"Maximum results; defaults to 50."`
}

func smartDefinition(f *ent.SmartFolder) (SmartFolderDefinition, error) {
	var def SmartFolderDefinition
	err := json.Unmarshal(f.Definition, &def)
	return def, err
}

func (s *Service) validateSmartFolder(ctx context.Context, subject string, in SmartFolderInput) error {
	if err := validateName(in.Name); err != nil {
		return err
	}
	if err := validateDescription(in.Description); err != nil {
		return err
	}
	if !in.Personal && in.WorkspaceID == nil {
		return invalid("shared smart folders require workspace_id")
	}
	if in.WorkspaceID != nil {
		if _, err := s.workspace(ctx, subject, *in.WorkspaceID, "read"); err != nil {
			return err
		}
	}
	d := in.Definition
	for _, values := range [][]string{d.Collections, d.Templates, d.ContentTypes} {
		if len(values) > MaxSmartCollections {
			return invalid("at most 100 collection, template or content type selectors")
		}
		seen := map[string]bool{}
		for _, v := range values {
			if err := validateName(v); err != nil {
				return err
			}
			key := strings.ToLower(v)
			if seen[key] {
				return invalid("selectors must be unique ignoring case")
			}
			seen[key] = true
		}
	}
	if d.TermMatch != "" && d.TermMatch != "all" && d.TermMatch != "any" {
		return invalid("term_match must be all or any")
	}
	if len(d.Terms) > 20 {
		return invalid("at most 20 terms per smart folder")
	}
	seen := map[string]bool{}
	if len(d.Terms) > 0 {
		for _, id := range d.Terms {
			if seen[id] {
				return invalid("terms must be unique")
			}
			seen[id] = true
		}
		terms, err := s.Client.Term.Query().Where(term.IDIn(d.Terms...)).WithTermSet().All(ctx)
		if err != nil {
			return err
		}
		if len(terms) != len(d.Terms) {
			return invalid("unknown smart folder term")
		}
		checked := map[string]bool{}
		if in.WorkspaceID != nil {
			checked[*in.WorkspaceID] = true
		}
		for _, t := range terms {
			set, err := t.Edges.TermSetOrErr()
			if err != nil {
				return err
			}
			if in.WorkspaceID != nil && set.WorkspaceID != *in.WorkspaceID {
				return invalid("smart folder terms must belong to its workspace")
			}
			if !checked[set.WorkspaceID] {
				if _, err = s.workspace(ctx, subject, set.WorkspaceID, "read"); err != nil {
					return err
				}
				checked[set.WorkspaceID] = true
			}
		}
	}
	if _, err := resolveFilter(d.Filter, subject, time.Now().UTC(), nil); err != nil {
		return err
	}
	if len(d.GroupBy) > MaxSmartGroupLevels {
		return invalid("at most 3 metadata navigation levels")
	}
	seen = map[string]bool{}
	for _, g := range d.GroupBy {
		if !fieldKey.MatchString(g.Field) {
			if _, builtin := systemField(g.Field); builtin == nil {
				return invalid("invalid grouping field")
			}
		}
		if seen[g.Field] {
			return invalid("grouping fields must be unique")
		}
		seen[g.Field] = true
		if g.By != "" && g.By != "year" && g.By != "month" {
			return invalid("grouping by must be year or month")
		}
	}
	return nil
}

func (s *Service) smartFolder(ctx context.Context, subject, id string, manage bool) (*ent.SmartFolder, error) {
	f, err := s.Client.SmartFolder.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if f.OwnerID != nil {
		if *f.OwnerID != subject {
			return nil, ErrNotFound
		}
		return f, nil
	}
	action := "read"
	if manage {
		action = "manage"
	}
	if _, err = s.workspace(ctx, subject, *f.WorkspaceID, action); err != nil {
		if !manage && errors.Is(err, ErrForbidden) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

func (s *Service) auditSmartFolder(ctx context.Context, subject, action string, f *ent.SmartFolder) error {
	if f.WorkspaceID == nil || f.OwnerID != nil {
		return nil
	}
	if err := s.Client.Resource.UpdateOneID(*f.WorkspaceID).SetUpdatedBy(subject).AddVersion(1).Exec(ctx); err != nil {
		return err
	}
	return s.audit(ctx, subject, action, &ent.Resource{ID: *f.WorkspaceID, WorkspaceID: *f.WorkspaceID}, map[string]any{"smart_folder_id": f.ID, "version": f.Version, "personal": f.OwnerID != nil})
}

func (s *Service) createSmartFolder(ctx context.Context, subject string, in SmartFolderInput) (*ent.SmartFolder, error) {
	if subject == "" {
		return nil, ErrForbidden
	}
	if err := s.validateSmartFolder(ctx, subject, in); err != nil {
		return nil, err
	}
	if !in.Personal {
		if _, err := s.workspace(ctx, subject, *in.WorkspaceID, "manage"); err != nil {
			return nil, err
		}
		count, err := s.Client.SmartFolder.Query().Where(smartfolder.WorkspaceIDEQ(*in.WorkspaceID), smartfolder.OwnerIDIsNil()).Count(ctx)
		if err != nil {
			return nil, err
		}
		if count >= MaxSharedSmartFolders {
			return nil, invalid("at most 100 shared smart folders per workspace")
		}
	}
	raw, err := json.Marshal(in.Definition)
	if err != nil {
		return nil, err
	}
	b := s.Client.SmartFolder.Create().SetName(in.Name).SetDescription(in.Description).SetNillableWorkspaceID(in.WorkspaceID).SetDefinition(raw).SetCreatedBy(subject).SetUpdatedBy(subject)
	if in.Personal {
		b.SetOwnerID(subject)
	}
	f, err := b.Save(ctx)
	if err != nil {
		return nil, err
	}
	return f, s.auditSmartFolder(ctx, subject, "smart_folder.create", f)
}

func (s *Service) CreateSmartFolder(ctx context.Context, subject string, in SmartFolderInput) (out *ent.SmartFolder, err error) {
	err = s.write(ctx, func(t *Service) error { var e error; out, e = t.createSmartFolder(ctx, subject, in); return e })
	return
}

func (s *Service) SmartFolder(ctx context.Context, subject, id string) (*ent.SmartFolder, error) {
	return read(ctx, s, func(t *Service) (*ent.SmartFolder, error) { return t.smartFolder(ctx, subject, id, false) })
}

func (s *Service) SmartFolders(ctx context.Context, subject, workspace, after string, limit int) (Page[*ent.SmartFolder], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.SmartFolder], error) {
		acl, args := permissionSQL("smart_folders.workspace_id", subject, "read")
		q := t.Client.SmartFolder.Query().Where(smartfolder.IDGT(after), func(sel *entsql.Selector) {
			sel.Where(entsql.ExprP("(owner_id=? OR (owner_id IS NULL AND "+acl+"))", append([]any{subject}, args...)...))
		})
		if workspace != "" {
			q.Where(smartfolder.WorkspaceIDEQ(workspace))
		}
		rows, err := q.Order(ent.Asc(smartfolder.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(f *ent.SmartFolder) string { return f.ID }), err
	})
}

func (s *Service) UpdateSmartFolder(ctx context.Context, subject, id string, version int, in SmartFolderInput) (out *ent.SmartFolder, err error) {
	err = s.write(ctx, func(t *Service) error {
		old, e := t.smartFolder(ctx, subject, id, true)
		if e != nil {
			return e
		}
		if old.Version != version {
			return ErrConflict
		}
		out, e = t.updateSmartFolder(ctx, subject, old, in)
		return e
	})
	return
}

func (s *Service) updateSmartFolder(ctx context.Context, subject string, old *ent.SmartFolder, in SmartFolderInput) (*ent.SmartFolder, error) {
	if (old.OwnerID != nil) != in.Personal || (old.WorkspaceID == nil) != (in.WorkspaceID == nil) || (old.WorkspaceID != nil && *old.WorkspaceID != *in.WorkspaceID) {
		return nil, invalid("smart folder ownership and workspace are immutable")
	}
	if err := s.validateSmartFolder(ctx, subject, in); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(in.Definition)
	if err != nil {
		return nil, err
	}
	out, err := s.Client.SmartFolder.UpdateOne(old).SetName(in.Name).SetDescription(in.Description).SetDefinition(raw).SetUpdatedBy(subject).AddVersion(1).Save(ctx)
	if err != nil {
		return nil, err
	}
	return out, s.auditSmartFolder(ctx, subject, "smart_folder.update", out)
}

func (s *Service) DeleteSmartFolder(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		f, err := t.smartFolder(ctx, subject, id, true)
		if err != nil {
			return err
		}
		if f.Version != version {
			return ErrConflict
		}
		if err = t.Client.SmartFolder.DeleteOne(f).Exec(ctx); err != nil {
			return err
		}
		return t.auditSmartFolder(ctx, subject, "smart_folder.delete", f)
	})
}
