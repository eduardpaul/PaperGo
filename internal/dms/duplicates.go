package dms

import (
	"context"
	"errors"
	"papergo/ent"
	"papergo/ent/resource"
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
	*ent.Blob
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
		rows, err := s.Client.QueryContext(ctx, `SELECT DISTINCT r.id FROM blobs b
JOIN item_revisions v ON v.blob_id=b.id JOIN resources r ON r.id=v.item_id
WHERE b.sha256=? AND r.deleted_at IS NULL AND r.id<>? AND r.id>?
AND (v.id=r.head_revision_id OR v.id=r.published_revision_id) ORDER BY r.id LIMIT 100`, hash, exceptItem, after)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr := rows.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
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
	item, err := s.Client.Resource.Get(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if item.ContainerID == nil {
		return nil, invalid("file requires a library")
	}
	collection, err := s.Client.Resource.Get(ctx, *item.ContainerID)
	if err != nil {
		return nil, err
	}
	if collection.DuplicatePolicy == resource.DuplicatePolicyAllow {
		return []DuplicateDocument{}, nil
	}
	matches, err := s.duplicateDocuments(ctx, subject, hash, itemID)
	if err != nil {
		return nil, err
	}
	if collection.DuplicatePolicy == resource.DuplicatePolicyBlock && len(matches) > 0 {
		return nil, &DuplicateError{matches}
	}
	return matches, nil
}
