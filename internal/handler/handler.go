package handler

import (
	"fmt"

	"github.com/xtxerr/snmpproxy/internal/manager"
)

// RequestContext holds context for handling a request.
type RequestContext struct {
	Session   *Session
	RequestID uint64
	Manager   *manager.Manager
}

// Namespace returns the session's namespace or error if not bound.
func (ctx *RequestContext) Namespace() (string, error) {
	ns := ctx.Session.GetNamespace()
	if ns == "" {
		return "", fmt.Errorf("session not bound to namespace")
	}
	return ns, nil
}

// MustNamespace returns the namespace, panics if not bound.
func (ctx *RequestContext) MustNamespace() string {
	ns, err := ctx.Namespace()
	if err != nil {
		panic(err)
	}
	return ns
}

// Handler is the main request handler.
type Handler struct {
	mgr            *manager.Manager
	sessionManager *SessionManager
}

// NewHandler creates a new handler.
func NewHandler(mgr *manager.Manager, sm *SessionManager) *Handler {
	return &Handler{
		mgr:            mgr,
		sessionManager: sm,
	}
}

// Manager returns the entity manager.
func (h *Handler) Manager() *manager.Manager {
	return h.mgr
}

// SessionManager returns the session manager.
func (h *Handler) SessionManager() *SessionManager {
	return h.sessionManager
}

// NewContext creates a request context.
func (h *Handler) NewContext(session *Session, requestID uint64) *RequestContext {
	return &RequestContext{
		Session:   session,
		RequestID: requestID,
		Manager:   h.mgr,
	}
}

// Error codes
const (
	ErrUnknown          = 1
	ErrAuthFailed       = 2
	ErrNotAuthenticated = 3
	ErrNotBound         = 4
	ErrInvalidRequest   = 5
	ErrNotFound         = 6
	ErrAlreadyExists    = 7
	ErrNotAuthorized    = 8
	ErrInternal         = 9
	ErrConcurrentMod    = 10
	ErrInUse            = 11
)

// HandlerError represents a handler error.
type HandlerError struct {
	Code    int
	Message string
}

func (e *HandlerError) Error() string {
	return e.Message
}

// NewError creates a handler error.
func NewError(code int, msg string) *HandlerError {
	return &HandlerError{Code: code, Message: msg}
}

// Errorf creates a formatted handler error.
func Errorf(code int, format string, args ...interface{}) *HandlerError {
	return &HandlerError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Common errors
var (
	ErrSessionNotBound = NewError(ErrNotBound, "session not bound to namespace")
)

// RequireNamespace returns error if session is not bound.
func RequireNamespace(ctx *RequestContext) error {
	if !ctx.Session.IsBound() {
		return ErrSessionNotBound
	}
	return nil
}

// RequireNamespaceAccess checks if session can access the namespace.
func RequireNamespaceAccess(ctx *RequestContext, sm *SessionManager, namespace string) error {
	if !sm.CanAccessNamespace(ctx.Session.TokenID, namespace) {
		return Errorf(ErrNotAuthorized, "not authorized to access namespace %s", namespace)
	}
	return nil
}
