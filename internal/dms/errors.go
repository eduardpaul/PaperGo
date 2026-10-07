package dms

import "errors"

var (
	ErrForbidden = errors.New("access denied")
	ErrNotFound  = errors.New("resource not found")
	ErrConflict  = errors.New("version conflict or duplicate")
)

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func invalid(message string) error       { return &ValidationError{Message: message} }
