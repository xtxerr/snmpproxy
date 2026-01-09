package handler

import (
	"github.com/xtxerr/snmpproxy/internal/store"
)

// SecretHandler handles secret operations.
type SecretHandler struct {
	*Handler
}

// NewSecretHandler creates a secret handler.
func NewSecretHandler(h *Handler) *SecretHandler {
	return &SecretHandler{Handler: h}
}

// ============================================================================
// List Secrets
// ============================================================================

// ListSecretsRequest holds list request data.
type ListSecretsRequest struct{}

// ListSecretsResponse holds list response data.
type ListSecretsResponse struct {
	Secrets []*store.Secret
}

// ListSecrets lists secrets in the namespace.
func (h *SecretHandler) ListSecrets(ctx *RequestContext, req *ListSecretsRequest) (*ListSecretsResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	secrets, err := h.mgr.Store().ListSecrets(namespace)
	if err != nil {
		return nil, Errorf(ErrInternal, "list secrets: %v", err)
	}

	return &ListSecretsResponse{Secrets: secrets}, nil
}

// ============================================================================
// Get Secret (metadata only, not value)
// ============================================================================

// GetSecretRequest holds get request data.
type GetSecretRequest struct {
	Name string
}

// GetSecretResponse holds get response data.
type GetSecretResponse struct {
	Secret *store.Secret
}

// GetSecret gets secret metadata.
func (h *SecretHandler) GetSecret(ctx *RequestContext, req *GetSecretRequest) (*GetSecretResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	secret, err := h.mgr.Store().GetSecret(namespace, req.Name)
	if err != nil {
		return nil, Errorf(ErrInternal, "get secret: %v", err)
	}
	if secret == nil {
		return nil, Errorf(ErrNotFound, "secret not found: %s", req.Name)
	}

	return &GetSecretResponse{Secret: secret}, nil
}

// ============================================================================
// Create Secret
// ============================================================================

// CreateSecretRequest holds create request data.
type CreateSecretRequest struct {
	Name       string
	SecretType string // "community", "auth_password", "priv_password", "generic"
	Value      string
}

// CreateSecretResponse holds create response data.
type CreateSecretResponse struct {
	Secret *store.Secret
}

// CreateSecret creates a new secret.
func (h *SecretHandler) CreateSecret(ctx *RequestContext, req *CreateSecretRequest) (*CreateSecretResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	// Check if secret key is configured
	if !h.mgr.Store().HasSecretKey() {
		return nil, Errorf(ErrInvalidRequest, "secret encryption not configured on server")
	}

	// Check if exists
	exists, err := h.mgr.Store().SecretExists(namespace, req.Name)
	if err != nil {
		return nil, Errorf(ErrInternal, "check secret: %v", err)
	}
	if exists {
		return nil, Errorf(ErrAlreadyExists, "secret already exists: %s", req.Name)
	}

	// Create
	if err := h.mgr.Store().CreateSecret(namespace, req.Name, req.SecretType, req.Value); err != nil {
		return nil, Errorf(ErrInternal, "create secret: %v", err)
	}

	secret, _ := h.mgr.Store().GetSecret(namespace, req.Name)
	return &CreateSecretResponse{Secret: secret}, nil
}

// ============================================================================
// Update Secret
// ============================================================================

// UpdateSecretRequest holds update request data.
type UpdateSecretRequest struct {
	Name  string
	Value string
}

// UpdateSecretResponse holds update response data.
type UpdateSecretResponse struct {
	Secret *store.Secret
}

// UpdateSecret updates a secret value.
func (h *SecretHandler) UpdateSecret(ctx *RequestContext, req *UpdateSecretRequest) (*UpdateSecretResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	if err := h.mgr.Store().UpdateSecret(namespace, req.Name, req.Value); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	secret, _ := h.mgr.Store().GetSecret(namespace, req.Name)
	return &UpdateSecretResponse{Secret: secret}, nil
}

// ============================================================================
// Delete Secret
// ============================================================================

// DeleteSecretRequest holds delete request data.
type DeleteSecretRequest struct {
	Name  string
	Force bool // Delete even if in use
}

// DeleteSecretResponse holds delete response data.
type DeleteSecretResponse struct {
	UsersAffected int
}

// DeleteSecret deletes a secret.
func (h *SecretHandler) DeleteSecret(ctx *RequestContext, req *DeleteSecretRequest) (*DeleteSecretResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	affected, err := h.mgr.Store().DeleteSecret(namespace, req.Name, req.Force)
	if err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &DeleteSecretResponse{UsersAffected: affected}, nil
}
