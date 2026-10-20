package dms

import (
	"context"
	"errors"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/resources"
)

type DuplicateDocument struct {
	WorkspaceID  string `json:"workspace_id"`
	CollectionID string `json:"collection_id"`
	ItemID       string `json:"item_id"`
	Name         string `json:"name"`
}

type DuplicateError struct{ Documents []DuplicateDocument }

func (e *DuplicateError) Error() string { return "the library does not accept duplicate files" }
func (e *DuplicateError) Unwrap() error { return ErrConflict }

type BlobUpload struct {
	*blobs.Blob
	Duplicates []DuplicateDocument `json:"duplicates"`
}

func validateDuplicatePolicy(kind string, policy *string) error {
	if policy == nil {
		return nil
	}
	if kind != "library" {
		return invalid("duplicate_policy belongs to libraries")
	}
	if *policy != "allow" && *policy != "warn" && *policy != "block" {
		return invalid("duplicate_policy must be allow, warn or block")
	}
	return nil
}

// File identity is the SHA-256 of bytes. Only a caller's visible current file
// counts, across all libraries/workspaces; historical and deleted files do not.
func (s *Service) duplicateDocuments(ctx context.Context, subject, hash, exceptItem string) ([]DuplicateDocument, error) {
	out := []DuplicateDocument{}
	after := ""
	for len(out) < 20 {
		ids, err := s.DAL.Content().CurrentFiles(ctx, hash, exceptItem, after, 100)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			after = id
			file, err := s.GetBlob(ctx, subject, id, "")
			if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if file.Sha256 != hash {
				continue
			}
			item, err := s.Get(ctx, subject, id)
			if err != nil {
				return nil, err
			}
			out = append(out, DuplicateDocument{item.WorkspaceID, *item.ContainerID, item.ID, item.Name})
			if len(out) == 20 {
				break
			}
		}
		if len(ids) < 100 {
			break
		}
	}
	return out, nil
}

func (s *Service) checkDuplicatePolicy(ctx context.Context, subject, itemID, hash string) ([]DuplicateDocument, error) {
	item, err := s.DAL.Resources().Resource(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if item.ContainerID == nil {
		return nil, invalid("file requires a library")
	}
	collection, err := s.DAL.Resources().Resource(ctx, *item.ContainerID)
	if err != nil {
		return nil, err
	}
	if collection.DuplicatePolicy == resources.DuplicateAllow {
		return []DuplicateDocument{}, nil
	}
	matches, err := s.duplicateDocuments(ctx, subject, hash, itemID)
	if err != nil {
		return nil, err
	}
	if collection.DuplicatePolicy == resources.DuplicateBlock && len(matches) > 0 {
		return nil, &DuplicateError{matches}
	}
	return matches, nil
}
