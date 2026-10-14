package dms

import (
	"context"
	"database/sql"

	"papergo/ent"
	"papergo/ent/resource"
	"papergo/internal/identity"
)

// Packages that build on the DMS (workflows) use these to keep its rules: one
// authorization model, one writer, and audit records in the same transaction.

// Authorize returns the live resource id when subject may perform action on it.
func (s *Service) Authorize(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ent.Resource, error) { return t.authorize(ctx, subject, id, action) })
	}
	return s.authorize(ctx, subject, id, action)
}

// AuthorizeMany reports which of the resources subject may act on, as
// Authorize allows them one by one, in one query; missing and deleted
// resources are not allowed.
func (s *Service) AuthorizeMany(ctx context.Context, subject, action string, ids []string) (map[string]bool, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (map[string]bool, error) { return t.AuthorizeMany(ctx, subject, action, ids) })
	}
	if len(ids) == 0 {
		return map[string]bool{}, nil
	}
	rows, err := s.Client.Resource.Query().Where(resource.IDIn(ids...), resource.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return nil, err
	}
	if s.homeProvisioning {
		out := map[string]bool{}
		for _, r := range rows {
			if _, err := s.authorize(ctx, subject, r.ID, action); err == nil {
				out[r.ID] = true
			}
		}
		return out, nil
	}
	return s.allowedMany(ctx, subject, action, rows)
}

// Write runs fn as one write transaction, like every DMS mutation; inside a
// transaction it joins it.
func (s *Service) Write(ctx context.Context, fn func(*Service) error) error {
	return s.write(ctx, fn)
}

// Read runs fn on one consistent snapshot; inside a transaction it joins it.
func Read[T any](ctx context.Context, s *Service, fn func(*Service) (T, error)) (T, error) {
	return read(ctx, s, fn)
}

// Record adds action on r to the workspace's audit trail.
func (s *Service) Record(ctx context.Context, subject, action string, r *ent.Resource, details map[string]any) error {
	return s.audit(ctx, subject, action, r, details)
}

// Invalid is a validation failure that the API reports as a 422 problem.
func Invalid(message string) error { return invalid(message) }

// Emit raises an event from the current write; it is delivered with the
// write's other events when the transaction commits.
func (s *Service) Emit(ctx context.Context, e Event) {
	if s.pending != nil {
		*s.pending = append(*s.pending, e)
	}
}

// Raise raises an event of type typ from the current write, like the DMS's
// own events: one level deeper than the run that causes it, if any, and
// delivered when the transaction commits.
func (s *Service) Raise(ctx context.Context, typ, actor, workspaceID string, collectionID *string, resourceID string, data map[string]any) {
	s.emit(ctx, typ, actor, workspaceID, collectionID, resourceID, data)
}

// GroupIDs returns the groups subject belongs to, nested groups included.
func (s *Service) GroupIDs(ctx context.Context, subject string) ([]string, error) {
	return read(ctx, s, func(t *Service) ([]string, error) {
		rows, err := t.tx.QueryContext(ctx, identity.GroupIDsSQL, subject)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	})
}

// SQLTx is the transaction a write-bound service runs on, or nil outside one.
func (s *Service) SQLTx() *sql.Tx { return s.tx }
