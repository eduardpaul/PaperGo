package dms

import (
	"errors"
	"fmt"
)

var (
	ErrForbidden      = errors.New("access denied")
	ErrNotFound       = errors.New("resource not found")
	ErrConflict       = errors.New("resource conflicts with existing state")
	ErrLastOwner      = errors.New("a workspace needs at least one owner")
	ErrFolderNotEmpty = errors.New("the folder is not empty")
	// Library file paths distinguish a missing parent folder from a missing
	// entry, an occupied name, and a failed conditional request.
	ErrMissingParent      = errors.New("parent folder not found")
	ErrExists             = errors.New("an entry with this name already exists")
	ErrPreconditionFailed = errors.New("precondition failed")
	// ErrContentUnavailable is a file version whose bytes were reclaimed.
	// Its identity and metadata remain.
	ErrContentUnavailable = errors.New("content unavailable")
	// ErrFiguresChanged is a page of figures whose items changed since the
	// first page: its groups would repeat or skip some of those read.
	ErrFiguresChanged = errors.New("the figures changed while their pages were read; read them again from the first page")
)

// PreconditionRequired is a write to a resource that exists, made without
// If-Match. Current is the version its ETag names, which a retry sends.
type PreconditionRequired struct{ Current int }

func (e *PreconditionRequired) Error() string {
	return fmt.Sprintf("If-Match is required; the current ETag is \"%d\"", e.Current)
}

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func invalid(message string) error       { return &ValidationError{Message: message} }

// DocumentNameConflict exposes a replace target only when the uploader may write it.
type DocumentNameConflict struct {
	ItemID  string `json:"id"`
	Version int    `json:"version"`
}

func (e *DocumentNameConflict) Error() string { return "a file with this name is already here" }
func (e *DocumentNameConflict) Unwrap() error { return ErrConflict }
