package dms

import (
	"context"
	"database/sql"

	"papergo/ent"
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

// SQLTx is the transaction a write-bound service runs on, or nil outside one.
func (s *Service) SQLTx() *sql.Tx { return s.tx }
