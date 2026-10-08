package dms

import "errors"

var (
	ErrForbidden = errors.New("access denied")
	ErrNotFound  = errors.New("resource not found")
	ErrConflict  = errors.New("version conflict or duplicate")
	// Library file paths distinguish a missing parent folder from a missing
	// entry, an occupied name, and a failed conditional request.
	ErrMissingParent      = errors.New("parent folder not found")
	ErrExists             = errors.New("an entry with this name already exists")
	ErrPreconditionFailed = errors.New("precondition failed")
)

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func invalid(message string) error       { return &ValidationError{Message: message} }
