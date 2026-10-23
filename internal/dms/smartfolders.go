package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/taxonomy"
	viewstore "papergo/internal/dal/views"
	"strings"
	"time"
)

const MaxSmartCollections = 100
const MaxSmartGroupLevels = 3
const MaxSharedSmartFolders = 100

type SmartFolderGroupBy struct {
	Field string `json:"field"`
	By    string `json:"by,omitempty"`
}

type SmartFolderDefinition struct {
	Archive        refs.ArchiveSelection `json:"archive,omitempty"`
	Mode           string                `json:"mode"`
	CollectionKind string                `json:"collection_kind,omitempty"`
	Collections    []string              `json:"collections,omitempty"`
	Templates      []string              `json:"templates,omitempty"`
	ContentTypes   []string              `json:"content_types,omitempty"`
	Terms          []string              `json:"terms,omitempty"`
	TermMatch      string                `json:"term_match,omitempty"`
	Filter         *FilterExpr           `json:"filter,omitempty"`
	GroupBy        []SmartFolderGroupBy  `json:"group_by,omitempty"`
	IncludeFolders bool                  `json:"include_folders,omitempty"`
}

type SmartFolderInput struct {
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	WorkspaceID *string               `json:"workspace_id,omitempty"`
	Personal    bool                  `json:"personal"`
	Definition  SmartFolderDefinition `json:"definition"`
}

type SmartFolderQueryRequest struct {
	Sort         *SortSpec `json:"sort,omitempty"`
	TotalLimit   int       `json:"-"`
	Path         []*string `json:"path,omitempty"`
	Surface      string    `json:"surface,omitempty"`
	After        string    `json:"-"`
	Limit        int       `json:"-"`
	IncludeTotal bool      `json:"include_total,omitempty"`
}

func smartDefinition(f *viewstore.SmartFolder) (SmartFolderDefinition, error) {
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
	if !d.Archive.Valid() {
		return invalid("archive must be active, archived or all")
	}
	if d.Mode != "query" && d.Mode != "classification" {
		return invalid("mode must be query or classification")
	}
	if d.CollectionKind != "" && d.CollectionKind != "list" && d.CollectionKind != "library" {
		return invalid("collection_kind must be list or library")
	}
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
		terms, err := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: d.Terms})
		if err != nil {
			return err
		}
		if len(terms) != len(d.Terms) {
			return invalid("unknown smart folder term")
		}
		if err := s.requireTaxonomy(ctx, subject, "read"); err != nil {
			return err
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
			if builtin := systemField(g.Field); builtin == nil {
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

func (s *Service) smartFolder(ctx context.Context, subject, id string, manage bool) (*viewstore.SmartFolder, error) {
	scope := "item.read"
	if manage {
		scope = "item.write"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return nil, err
	}
	row, err := s.DAL.Views().SmartFolder(ctx, id)
	if dberr.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f := &row
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

func (s *Service) auditSmartFolder(ctx context.Context, subject, action string, f *viewstore.SmartFolder) error {
	if f.WorkspaceID == nil || f.OwnerID != nil {
		return s.auditCatalog(ctx, subject, action, map[string]any{"smart_folder_id": f.ID})
	}
	if err := s.DAL.Resources().Touch(ctx, *f.WorkspaceID, 0, subject); err != nil {
		return err
	}
	return s.audit(ctx, subject, action, &resources.Resource{ID: *f.WorkspaceID, WorkspaceID: *f.WorkspaceID}, map[string]any{"smart_folder_id": f.ID, "version": f.Version, "personal": f.OwnerID != nil})
}

func (s *Service) createSmartFolder(ctx context.Context, subject string, in SmartFolderInput) (*viewstore.SmartFolder, error) {
	if err := s.RequireScope(ctx, subject, "item.write"); err != nil {
		return nil, err
	}
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
		count, err := s.DAL.Views().CountSmartFolders(ctx, viewstore.FolderFilter{WorkspaceID: *in.WorkspaceID, Shared: true})
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
	f := viewstore.SmartFolder{Name: in.Name, Description: in.Description, WorkspaceID: in.WorkspaceID, Definition: raw, CreatedBy: subject}
	if in.Personal {
		f.OwnerID = &subject
	}
	f, err = s.DAL.Views().CreateSmartFolder(ctx, f)
	if err != nil {
		return nil, err
	}
	return &f, s.auditSmartFolder(ctx, subject, "smart_folder.create", &f)
}

func (s *Service) CreateSmartFolder(ctx context.Context, subject string, in SmartFolderInput) (out *viewstore.SmartFolder, err error) {
	err = s.write(ctx, func(t *Service) error { var e error; out, e = t.createSmartFolder(ctx, subject, in); return e })
	return
}

func (s *Service) SmartFolder(ctx context.Context, subject, id string) (*viewstore.SmartFolder, error) {
	return read(ctx, s, func(t *Service) (*viewstore.SmartFolder, error) { return t.smartFolder(ctx, subject, id, false) })
}

func (s *Service) SmartFolders(ctx context.Context, subject, workspace, after string, limit int) (Page[viewstore.SmartFolder], error) {
	return read(ctx, s, func(t *Service) (Page[viewstore.SmartFolder], error) {
		if err := t.RequireScope(ctx, subject, "item.read"); err != nil {
			return Page[viewstore.SmartFolder]{}, err
		}
		reader := principal(ctx, subject)
		rows, err := t.DAL.Views().SmartFolders(ctx, viewstore.FolderFilter{WorkspaceID: workspace, Reader: &reader, AfterID: after, Limit: pageSize(limit) + 1})
		return entityPage(rows, limit, func(f viewstore.SmartFolder) string { return f.ID }), err
	})
}

func (s *Service) UpdateSmartFolder(ctx context.Context, subject, id string, version int, in SmartFolderInput) (out *viewstore.SmartFolder, err error) {
	err = s.write(ctx, func(t *Service) error {
		old, e := t.smartFolder(ctx, subject, id, true)
		if e != nil {
			return e
		}
		if old.Version != version {
			return ErrPreconditionFailed
		}
		out, e = t.updateSmartFolder(ctx, subject, old, in)
		return e
	})
	return
}

func (s *Service) updateSmartFolder(ctx context.Context, subject string, old *viewstore.SmartFolder, in SmartFolderInput) (*viewstore.SmartFolder, error) {
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
	before, err := json.Marshal(old)
	if err != nil {
		return nil, err
	}
	out, err := s.DAL.Views().UpdateSmartFolder(ctx, viewstore.SmartFolder{ID: old.ID, Name: in.Name, Description: in.Description, Definition: raw, UpdatedBy: subject}, old.Version)
	if err != nil {
		return nil, err
	}
	if err = s.trackAuditProperties(out.ID, before, out, "name", "description", "definition"); err != nil {
		return nil, err
	}
	return &out, s.auditSmartFolder(ctx, subject, "smart_folder.update", &out)
}

func (s *Service) DeleteSmartFolder(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		f, err := t.smartFolder(ctx, subject, id, true)
		if err != nil {
			return err
		}
		if f.Version != version {
			return ErrPreconditionFailed
		}
		if err = t.DAL.Views().DeleteSmartFolder(ctx, f.ID, version); err != nil {
			return err
		}
		return t.auditSmartFolder(ctx, subject, "smart_folder.delete", f)
	})
}
