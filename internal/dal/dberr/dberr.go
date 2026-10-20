// Package dberr defines the storage errors shared by DAL domains and drivers.
package dberr

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Standard normalized database & domain errors across PostgreSQL and SQLite.
var (
	ErrConcurrentUpdate       = errors.New("record changed concurrently")
	ErrNotFound               = errors.New("record not found")
	ErrUniqueViolation        = errors.New("unique constraint violation")
	ErrForeignKeyViolation    = errors.New("foreign key constraint violation")
	ErrCheckViolation         = errors.New("check constraint violation")
	ErrCycleDetected          = errors.New("hierarchy cycle detected")
	ErrInvalidContainment     = errors.New("invalid resource containment")
	ErrIncompatibleCollection = errors.New("incompatible collection or content type")
	ErrImmutable              = errors.New("entity or identity field is immutable")
	ErrRetained               = errors.New("entity is retained and cannot be deleted")
	ErrInvalidPermissionScope = errors.New("invalid permission scope or grant")
	ErrLastWorkspaceOwner     = errors.New("a workspace needs at least one owner")
	ErrCardinalityExceeded    = errors.New("relationship cardinality exceeded")
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrInvalidReference       = errors.New("invalid cross-entity reference")
)

const (
	// PostgreSQL SQLSTATE error codes.
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
	pgNotNullViolation    = "23502"

	// SQLite result codes (modernc.org/sqlite): the primary constraint code
	// and its extended codes.
	sqliteConstraint           = 19
	sqliteConstraintCheck      = 275
	sqliteConstraintForeignKey = 787
	sqliteConstraintNotNull    = 1299
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

type sqliteCodedError interface {
	Code() int
}

// ConstraintError wraps an underlying driver error with a normalized sentinel error.
type ConstraintError struct {
	Kind       error
	Constraint string
	Cause      error
}

func (e *ConstraintError) Error() string {
	if e.Constraint != "" {
		return fmt.Sprintf("%s (%s): %v", e.Kind.Error(), e.Constraint, e.Cause)
	}
	return fmt.Sprintf("%s: %v", e.Kind.Error(), e.Cause)
}

func (e *ConstraintError) Unwrap() []error {
	return []error{e.Kind, e.Cause}
}

// Translate normalizes driver-specific database errors (pgx & modernc.org/sqlite)
// into portable sentinel errors that callers can inspect with errors.Is.
func Translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}

	// Already translated or domain error
	var ce *ConstraintError
	if errors.As(err, &ce) {
		return err
	}

	// 1. PostgreSQL (pgconn.PgError)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			return &ConstraintError{Kind: ErrUniqueViolation, Constraint: pgErr.ConstraintName, Cause: err}
		case pgForeignKeyViolation:
			return &ConstraintError{Kind: ErrForeignKeyViolation, Constraint: pgErr.ConstraintName, Cause: err}
		case pgCheckViolation, pgNotNullViolation:
			return &ConstraintError{Kind: ErrCheckViolation, Constraint: pgErr.ConstraintName, Cause: err}
		}
	}

	// 2. SQLite extended error codes (modernc.org/sqlite)
	var sqErr sqliteCodedError
	if errors.As(err, &sqErr) {
		switch sqErr.Code() {
		case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
			return &ConstraintError{Kind: ErrUniqueViolation, Cause: err}
		case sqliteConstraintForeignKey:
			return &ConstraintError{Kind: ErrForeignKeyViolation, Cause: err}
		case sqliteConstraintCheck, sqliteConstraintNotNull:
			return &ConstraintError{Kind: ErrCheckViolation, Cause: err}
		}
	}

	// 3. Fallback string matching for wrapped driver errors
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY constraint failed") ||
		strings.Contains(msg, "duplicate key value violates unique constraint"):
		return &ConstraintError{Kind: ErrUniqueViolation, Cause: err}
	case strings.Contains(msg, "FOREIGN KEY constraint failed") ||
		strings.Contains(msg, "violates foreign key constraint"):
		return &ConstraintError{Kind: ErrForeignKeyViolation, Cause: err}
	case strings.Contains(msg, "CHECK constraint failed") ||
		strings.Contains(msg, "NOT NULL constraint failed") ||
		strings.Contains(msg, "violates check constraint") ||
		strings.Contains(msg, "violates not-null constraint"):
		return &ConstraintError{Kind: ErrCheckViolation, Cause: err}
	}

	return err
}

// IsNotFound reports whether err represents a missing row (sql.ErrNoRows or ErrNotFound).
func IsNotFound(err error) bool {
	return err != nil && (errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound))
}

// IsConstraintError reports whether err is a unique, foreign-key, check, or not-null constraint violation.
func IsConstraintError(err error) bool {
	if err == nil {
		return false
	}
	err = Translate(err)
	return errors.Is(err, ErrUniqueViolation) ||
		errors.Is(err, ErrForeignKeyViolation) ||
		errors.Is(err, ErrCheckViolation)
}

// IsIntegrityError reports whether the database refused a write to keep
// its data consistent: a constraint violation or another integrity failure,
// such as a trigger rejecting the row. That is SQLite's primary
// SQLITE_CONSTRAINT code and PostgreSQL's SQLSTATE class 23.
func IsIntegrityError(err error) bool {
	if IsConstraintError(err) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return strings.HasPrefix(pgErr.Code, "23")
	}
	var sqErr sqliteCodedError
	return errors.As(err, &sqErr) && sqErr.Code()&0xff == sqliteConstraint
}

// IsRetryable reports driver failures for which replaying a transaction is safe.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "55P03"
	}
	var sqliteErr sqliteCodedError
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&255 == 5 || sqliteErr.Code()&255 == 6
	}
	return false
}
