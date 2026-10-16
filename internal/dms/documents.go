package dms

import (
	"context"
	"errors"
	"papergo/internal/identity"
	"regexp"
	"strings"

	"papergo/ent"
	"papergo/ent/resource"
)

// DocumentMetadata describes the new item and optional per-upload processing hint.
type DocumentMetadata struct {
	Name          string         `json:"name,omitempty"`
	ContentTypeID string         `json:"content_type_id,omitempty"`
	FolderID      string         `json:"folder_id,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	Values        map[string]any `json:"values,omitempty"`
	Languages     string         `json:"languages,omitempty"`
}

type DocumentUpload struct {
	Item       *ent.Resource
	File       *ent.Blob
	Duplicates []DuplicateDocument
}

var languageList = regexp.MustCompile(`^[a-z][a-z_]{1,30}(\+[a-z][a-z_]{1,30}){0,5}$`)

func ValidateDocumentLanguages(languages string) error {
	if languages != "" && !languageList.MatchString(languages) {
		return invalid("languages must be up to six lowercase OCR language codes joined with '+'")
	}
	return nil
}

func (s *Service) documentParent(ctx context.Context, subject, libraryID, folderID string) (*ent.Resource, error) {
	if err := s.RequireScope(ctx, subject, "document.write"); err != nil {
		return nil, err
	}
	library, err := s.authorize(ctx, subject, libraryID, "write")
	if err != nil {
		return nil, err
	}
	if library.Kind != resource.KindLibrary {
		return nil, invalid("documents belong to a library")
	}
	if folderID == "" {
		return library, nil
	}
	folder, err := s.authorize(ctx, subject, folderID, "write")
	if err != nil {
		return nil, err
	}
	if folder.Kind != resource.KindFolder || folder.ContainerID == nil || *folder.ContainerID != library.ID {
		return nil, invalid("folder_id must identify a folder in this library")
	}
	return folder, nil
}

// CanCreateDocument authorizes the destination before the HTTP layer streams bytes.
func (s *Service) CanCreateDocument(ctx context.Context, subject, libraryID string) error {
	if err := s.RequireScope(ctx, subject, "list.write"); err != nil {
		return err
	}
	_, err := read(ctx, s, func(t *Service) (*ent.Resource, error) { return t.documentParent(ctx, subject, libraryID, "") })
	return err
}

func (s *Service) CreateDocument(ctx context.Context, subject, libraryID string, metadata DocumentMetadata, content BlobInput) (out *DocumentUpload, err error) {
	err = s.write(ctx, func(t *Service) error {
		if err := identity.RequireCredential(ctx, t.tx, subject); err != nil {
			if errors.Is(err, identity.ErrInvalidRESTToken) {
				return ErrForbidden
			}
			return err
		}
		parent, err := t.documentParent(ctx, subject, libraryID, metadata.FolderID)
		if err != nil {
			return err
		}
		content.Languages = strings.TrimSpace(metadata.Languages)
		if err = ValidateDocumentLanguages(content.Languages); err != nil {
			return err
		}
		name := metadata.Name
		if name == "" {
			name = content.Filename
		}
		existing, err := t.Client.Resource.Query().Where(resource.ParentIDEQ(parent.ID), resource.NameKeyEQ(nameKey(strings.TrimSpace(name))), resource.DeletedAtIsNil()).Only(ctx)
		if err == nil {
			if existing.Kind == resource.KindItem {
				if _, access := t.authorize(ctx, subject, existing.ID, "write"); access == nil {
					return &DocumentNameConflict{ItemID: existing.ID, Version: existing.Version}
				}
			}
			return ErrConflict
		}
		if !ent.IsNotFound(err) {
			return err
		}
		item, err := t.create(ctx, subject, parent.ID, CreateResource{Kind: "item", Name: name, ContentTypeID: metadata.ContentTypeID, Tags: metadata.Tags, Values: metadata.Values}, &content)
		if err != nil {
			return err
		}
		revision, err := t.Client.ItemRevision.Get(ctx, *item.HeadRevisionID)
		if err != nil {
			return err
		}
		file, err := t.Client.Blob.Get(ctx, *revision.BlobID)
		if err != nil {
			return err
		}
		matches, err := t.checkDuplicatePolicy(ctx, subject, item.ID, content.SHA256)
		if err != nil {
			return err
		}
		out = &DocumentUpload{item, file, matches}
		return nil
	})
	return
}
