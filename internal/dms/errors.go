package dms

import "errors"

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
)

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
