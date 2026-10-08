package dms

import (
	"context"
	"fmt"
	"papergo/ent"
	"papergo/ent/resource"
)

const MaxBulkOperations = 100

type BulkCreate struct {
	ContentTypeID string         `json:"content_type_id,omitempty"`
	Name          string         `json:"name"`
	Tags          []string       `json:"tags,omitempty"`
	Values        map[string]any `json:"values,omitempty"`
}

type BulkUpdate struct {
	Name     *string         `json:"name,omitempty"`
	Tags     *[]string       `json:"tags,omitempty"`
	Values   *map[string]any `json:"values,omitempty"`
	ParentID *string         `json:"parent_id,omitempty"`
}

type BulkOperation struct {
	Action   string      `json:"action"`
	ID       string      `json:"id,omitempty"`
	Version  int         `json:"version,omitempty"`
	ParentID string      `json:"parent_id,omitempty"`
	Create   *BulkCreate `json:"create,omitempty"`
	Update   *BulkUpdate `json:"update,omitempty"`
}

type BulkRequest struct {
	Operations []BulkOperation `json:"operations"`
}

// Results are ordered like the request and deliberately omit full content.
type BulkResult struct {
	Action  string `json:"action"`
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type BulkResponse struct {
	Data []BulkResult `json:"data"`
}

// BulkError retains the underlying error classification without returning
// intermediate results from a transaction that will be rolled back.
type BulkError struct {
	Index int
	Err   error
}

func (e *BulkError) Error() string { return fmt.Sprintf("operation %d: %v", e.Index, e.Err) }
func (e *BulkError) Unwrap() error { return e.Err }

func validateBulk(in BulkRequest) error {
	if len(in.Operations) == 0 || len(in.Operations) > MaxBulkOperations {
		return invalid("bulk requests require 1..100 operations")
	}
	seen := make(map[string]bool, len(in.Operations))
	for i, op := range in.Operations {
		var err error
		switch op.Action {
		case "create":
			if op.Create == nil || op.Update != nil || op.ID != "" || op.Version != 0 {
				err = invalid("create requires create and permits only an optional parent_id")
			}
		case "update", "publish", "unpublish", "delete":
			if op.ID == "" || op.Version < 1 {
				err = invalid("existing items require id and a positive current version")
			} else if op.Create != nil || op.ParentID != "" || (op.Action == "update") != (op.Update != nil) {
				err = invalid("only update operations accept update; create and parent_id are not allowed")
			} else if seen[op.ID] {
				err = invalid("each existing item may occur only once in a bulk request")
			}
			seen[op.ID] = true
		default:
			err = invalid("bulk action must be create, update, publish, unpublish or delete")
		}
		if err != nil {
			return &BulkError{Index: i, Err: err}
		}
	}
	return nil
}

// Bulk acquires the writer once and commits all item mutations, projections,
// relationships and audit events together. Folder mutations are excluded:
// an operation count must also bound the number of affected resources.
func (s *Service) Bulk(ctx context.Context, subject, collectionID string, in BulkRequest) (BulkResponse, error) {
	if err := validateBulk(in); err != nil {
		return BulkResponse{}, err
	}
	results := make([]BulkResult, 0, len(in.Operations))
	err := s.write(ctx, func(t *Service) error {
		c, err := t.authorize(ctx, subject, collectionID, "read")
		if err != nil {
			return err
		}
		if c.Kind != resource.KindList && c.Kind != resource.KindLibrary {
			return invalid("bulk operations require a list or library")
		}
		for i, op := range in.Operations {
			if err := ctx.Err(); err != nil {
				return &BulkError{Index: i, Err: err}
			}
			result, err := t.bulkOperation(ctx, subject, c.ID, op)
			if err != nil {
				if ent.IsConstraintError(err) {
					err = ErrConflict
				}
				return &BulkError{Index: i, Err: err}
			}
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		return BulkResponse{}, err
	}
	return BulkResponse{Data: results}, nil
}

func (s *Service) bulkOperation(ctx context.Context, subject, collectionID string, op BulkOperation) (BulkResult, error) {
	result := BulkResult{Action: op.Action, ID: op.ID, Version: op.Version + 1}
	if op.Action == "create" {
		parentID := op.ParentID
		if parentID == "" {
			parentID = collectionID
		}
		p, err := s.authorize(ctx, subject, parentID, "write")
		if err != nil {
			return result, err
		}
		if p.ID != collectionID && (p.Kind != resource.KindFolder || p.ContainerID == nil || *p.ContainerID != collectionID) {
			return result, invalid("create parent must belong to this collection")
		}
		r, err := s.create(ctx, subject, parentID, CreateResource{Kind: "item", Name: op.Create.Name, Tags: op.Create.Tags, Values: op.Create.Values, ContentTypeID: op.Create.ContentTypeID}, nil)
		if err != nil {
			return result, err
		}
		result.ID, result.Version = r.ID, r.Version
		return result, nil
	}
	action := "write"
	if op.Action == "publish" || op.Action == "unpublish" {
		action = "publish"
	}
	r, err := s.authorize(ctx, subject, op.ID, action)
	if err != nil {
		return result, err
	}
	if r.Kind != resource.KindItem || r.ContainerID == nil || *r.ContainerID != collectionID {
		return result, invalid("bulk targets must be items in this collection")
	}
	switch op.Action {
	case "update":
		_, err = s.update(ctx, subject, op.ID, op.Version, UpdateResource{Name: op.Update.Name, Tags: op.Update.Tags, Values: op.Update.Values, ParentID: op.Update.ParentID})
	case "publish":
		_, err = s.publish(ctx, subject, op.ID, op.Version)
	case "unpublish":
		_, err = s.unpublish(ctx, subject, op.ID, op.Version)
	case "delete":
		err = s.delete(ctx, subject, op.ID, op.Version)
	}
	return result, err
}
