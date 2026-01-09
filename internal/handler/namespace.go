package handler

import (
	"github.com/xtxerr/snmpproxy/internal/store"
)

// NamespaceHandler handles namespace operations.
type NamespaceHandler struct {
	*Handler
}

// NewNamespaceHandler creates a namespace handler.
func NewNamespaceHandler(h *Handler) *NamespaceHandler {
	return &NamespaceHandler{Handler: h}
}

// ============================================================================
// Bind Namespace
// ============================================================================

// BindNamespaceRequest holds bind request data.
type BindNamespaceRequest struct {
	Namespace string
}

// BindNamespaceResponse holds bind response data.
type BindNamespaceResponse struct {
	Namespace string
}

// BindNamespace binds a session to a namespace.
func (h *NamespaceHandler) BindNamespace(ctx *RequestContext, req *BindNamespaceRequest) (*BindNamespaceResponse, error) {
	// Check if namespace exists
	ns, err := h.mgr.Namespaces.Get(req.Namespace)
	if err != nil {
		return nil, Errorf(ErrInternal, "get namespace: %v", err)
	}
	if ns == nil {
		return nil, Errorf(ErrNotFound, "namespace not found: %s", req.Namespace)
	}

	// Check access
	if err := RequireNamespaceAccess(ctx, h.sessionManager, req.Namespace); err != nil {
		return nil, err
	}

	// Bind session
	if err := ctx.Session.BindNamespace(req.Namespace); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &BindNamespaceResponse{Namespace: req.Namespace}, nil
}

// ============================================================================
// List Namespaces
// ============================================================================

// ListNamespacesRequest holds list request data.
type ListNamespacesRequest struct{}

// ListNamespacesResponse holds list response data.
type ListNamespacesResponse struct {
	Namespaces []*NamespaceInfo
}

// NamespaceInfo holds namespace information.
type NamespaceInfo struct {
	Name        string
	Description string
	Stats       *store.NamespaceStats
}

// ListNamespaces lists accessible namespaces.
func (h *NamespaceHandler) ListNamespaces(ctx *RequestContext, req *ListNamespacesRequest) (*ListNamespacesResponse, error) {
	all := h.mgr.Namespaces.List()

	var namespaces []*NamespaceInfo
	for _, ns := range all {
		// Filter by access
		if !h.sessionManager.CanAccessNamespace(ctx.Session.TokenID, ns.Name) {
			continue
		}

		info := &NamespaceInfo{
			Name:        ns.Name,
			Description: ns.Description,
		}

		// Get stats
		stats, _ := h.mgr.Namespaces.GetStats(ns.Name)
		info.Stats = stats

		namespaces = append(namespaces, info)
	}

	return &ListNamespacesResponse{Namespaces: namespaces}, nil
}

// ============================================================================
// Get Namespace
// ============================================================================

// GetNamespaceRequest holds get request data.
type GetNamespaceRequest struct {
	Name string
}

// GetNamespaceResponse holds get response data.
type GetNamespaceResponse struct {
	Namespace *store.Namespace
	Stats     *store.NamespaceStats
}

// GetNamespace gets namespace details.
func (h *NamespaceHandler) GetNamespace(ctx *RequestContext, req *GetNamespaceRequest) (*GetNamespaceResponse, error) {
	// Check access
	if err := RequireNamespaceAccess(ctx, h.sessionManager, req.Name); err != nil {
		return nil, err
	}

	ns, err := h.mgr.Namespaces.Get(req.Name)
	if err != nil {
		return nil, Errorf(ErrInternal, "get namespace: %v", err)
	}
	if ns == nil {
		return nil, Errorf(ErrNotFound, "namespace not found: %s", req.Name)
	}

	stats, _ := h.mgr.Namespaces.GetStats(req.Name)

	return &GetNamespaceResponse{
		Namespace: ns,
		Stats:     stats,
	}, nil
}

// ============================================================================
// Create Namespace
// ============================================================================

// CreateNamespaceRequest holds create request data.
type CreateNamespaceRequest struct {
	Name        string
	Description string
	Config      *store.NamespaceConfig
}

// CreateNamespaceResponse holds create response data.
type CreateNamespaceResponse struct {
	Namespace *store.Namespace
}

// CreateNamespace creates a new namespace.
func (h *NamespaceHandler) CreateNamespace(ctx *RequestContext, req *CreateNamespaceRequest) (*CreateNamespaceResponse, error) {
	ns := &store.Namespace{
		Name:        req.Name,
		Description: req.Description,
		Config:      req.Config,
	}

	if err := h.mgr.Namespaces.Create(ns); err != nil {
		if err.Error() == "namespace already exists: "+req.Name {
			return nil, Errorf(ErrAlreadyExists, err.Error())
		}
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &CreateNamespaceResponse{Namespace: ns}, nil
}

// ============================================================================
// Update Namespace
// ============================================================================

// UpdateNamespaceRequest holds update request data.
type UpdateNamespaceRequest struct {
	Name        string
	Description *string
	Config      *store.NamespaceConfig
}

// UpdateNamespaceResponse holds update response data.
type UpdateNamespaceResponse struct {
	Namespace *store.Namespace
}

// UpdateNamespace updates a namespace.
func (h *NamespaceHandler) UpdateNamespace(ctx *RequestContext, req *UpdateNamespaceRequest) (*UpdateNamespaceResponse, error) {
	// Check access
	if err := RequireNamespaceAccess(ctx, h.sessionManager, req.Name); err != nil {
		return nil, err
	}

	ns, err := h.mgr.Namespaces.Get(req.Name)
	if err != nil {
		return nil, Errorf(ErrInternal, "get namespace: %v", err)
	}
	if ns == nil {
		return nil, Errorf(ErrNotFound, "namespace not found: %s", req.Name)
	}

	// Apply updates
	if req.Description != nil {
		ns.Description = *req.Description
	}
	if req.Config != nil {
		ns.Config = req.Config
	}

	if err := h.mgr.Namespaces.Update(ns); err != nil {
		if err == store.ErrConcurrentModification {
			return nil, Errorf(ErrConcurrentMod, "namespace was modified by another client")
		}
		return nil, Errorf(ErrInternal, "update namespace: %v", err)
	}

	return &UpdateNamespaceResponse{Namespace: ns}, nil
}

// ============================================================================
// Delete Namespace
// ============================================================================

// DeleteNamespaceRequest holds delete request data.
type DeleteNamespaceRequest struct {
	Name  string
	Force bool
}

// DeleteNamespaceResponse holds delete response data.
type DeleteNamespaceResponse struct {
	TargetsDeleted int
	PollersDeleted int
}

// DeleteNamespace deletes a namespace.
func (h *NamespaceHandler) DeleteNamespace(ctx *RequestContext, req *DeleteNamespaceRequest) (*DeleteNamespaceResponse, error) {
	// Check access
	if err := RequireNamespaceAccess(ctx, h.sessionManager, req.Name); err != nil {
		return nil, err
	}

	targets, pollers, err := h.mgr.Namespaces.Delete(req.Name, req.Force)
	if err != nil {
		if err.Error() == "namespace not found: "+req.Name {
			return nil, Errorf(ErrNotFound, err.Error())
		}
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &DeleteNamespaceResponse{
		TargetsDeleted: targets,
		PollersDeleted: pollers,
	}, nil
}
