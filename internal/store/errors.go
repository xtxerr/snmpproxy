package store

import "errors"

// Common errors
var (
	ErrNotFound               = errors.New("not found")
	ErrAlreadyExists          = errors.New("already exists")
	ErrConcurrentModification = errors.New("concurrent modification detected (version mismatch)")
	ErrNotEmpty               = errors.New("not empty")
	ErrInUse                  = errors.New("in use")
	ErrInvalidReference       = errors.New("invalid reference")
	ErrSecretKeyNotConfigured = errors.New("secret key not configured")
)
