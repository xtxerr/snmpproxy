// Package errors provides defined error types for snmpproxy.
package errors

import (
	"errors"
	"fmt"
)

// Sentinel errors for common conditions.
var (
	// Not found errors
	ErrNotFound          = errors.New("not found")
	ErrNamespaceNotFound = errors.New("namespace not found")
	ErrTargetNotFound    = errors.New("target not found")
	ErrPollerNotFound    = errors.New("poller not found")
	ErrSecretNotFound    = errors.New("secret not found")
	ErrPathNotFound      = errors.New("path not found")

	// Already exists errors
	ErrAlreadyExists          = errors.New("already exists")
	ErrNamespaceAlreadyExists = errors.New("namespace already exists")
	ErrTargetAlreadyExists    = errors.New("target already exists")
	ErrPollerAlreadyExists    = errors.New("poller already exists")

	// Validation errors
	ErrInvalidName       = errors.New("invalid name")
	ErrInvalidPath       = errors.New("invalid path")
	ErrInvalidOID        = errors.New("invalid OID")
	ErrInvalidInterval   = errors.New("invalid interval")
	ErrInvalidConfig     = errors.New("invalid configuration")
	ErrMissingField      = errors.New("missing required field")

	// State errors
	ErrInvalidState      = errors.New("invalid state")
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrPollerDisabled    = errors.New("poller is disabled")
	ErrPollerRunning     = errors.New("poller is already running")
	ErrPollerStopped     = errors.New("poller is already stopped")

	// Auth/Session errors
	ErrNotAuthenticated  = errors.New("not authenticated")
	ErrNotAuthorized     = errors.New("not authorized")
	ErrInvalidToken      = errors.New("invalid token")
	ErrSessionExpired    = errors.New("session expired")
	ErrNamespaceRequired = errors.New("namespace binding required")

	// Protocol errors
	ErrUnsupportedProtocol = errors.New("unsupported protocol")
	ErrSNMPError           = errors.New("SNMP error")
	ErrTimeout             = errors.New("timeout")

	// Internal errors
	ErrInternal   = errors.New("internal error")
	ErrDatabase   = errors.New("database error")
	ErrEncryption = errors.New("encryption error")
)

// Helper functions for error checking.

// IsNotFound returns true if err is a not-found error.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrNamespaceNotFound) ||
		errors.Is(err, ErrTargetNotFound) ||
		errors.Is(err, ErrPollerNotFound) ||
		errors.Is(err, ErrSecretNotFound) ||
		errors.Is(err, ErrPathNotFound)
}

// IsAlreadyExists returns true if err is an already-exists error.
func IsAlreadyExists(err error) bool {
	return errors.Is(err, ErrAlreadyExists) ||
		errors.Is(err, ErrNamespaceAlreadyExists) ||
		errors.Is(err, ErrTargetAlreadyExists) ||
		errors.Is(err, ErrPollerAlreadyExists)
}

// IsValidation returns true if err is a validation error.
func IsValidation(err error) bool {
	return errors.Is(err, ErrInvalidName) ||
		errors.Is(err, ErrInvalidPath) ||
		errors.Is(err, ErrInvalidOID) ||
		errors.Is(err, ErrInvalidInterval) ||
		errors.Is(err, ErrInvalidConfig) ||
		errors.Is(err, ErrMissingField)
}

// IsStateError returns true if err is a state-related error.
func IsStateError(err error) bool {
	return errors.Is(err, ErrInvalidState) ||
		errors.Is(err, ErrInvalidTransition) ||
		errors.Is(err, ErrPollerDisabled) ||
		errors.Is(err, ErrPollerRunning) ||
		errors.Is(err, ErrPollerStopped)
}

// IsAuthError returns true if err is an authentication/authorization error.
func IsAuthError(err error) bool {
	return errors.Is(err, ErrNotAuthenticated) ||
		errors.Is(err, ErrNotAuthorized) ||
		errors.Is(err, ErrInvalidToken) ||
		errors.Is(err, ErrSessionExpired) ||
		errors.Is(err, ErrNamespaceRequired)
}

// Wrap wraps an error with additional context.
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

// Wrapf wraps an error with formatted context.
func Wrapf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), err)
}

// NewNotFound creates a not-found error with context.
func NewNotFound(entityType, identifier string) error {
	return fmt.Errorf("%s '%s': %w", entityType, identifier, ErrNotFound)
}

// NewAlreadyExists creates an already-exists error with context.
func NewAlreadyExists(entityType, identifier string) error {
	return fmt.Errorf("%s '%s': %w", entityType, identifier, ErrAlreadyExists)
}

// NewValidation creates a validation error with context.
func NewValidation(field, reason string) error {
	return fmt.Errorf("invalid %s: %s: %w", field, reason, ErrInvalidConfig)
}
