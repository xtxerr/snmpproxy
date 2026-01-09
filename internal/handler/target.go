package handler

import (
	"github.com/xtxerr/snmpproxy/internal/store"
)

// TargetHandler handles target operations.
type TargetHandler struct {
	*Handler
}

// NewTargetHandler creates a target handler.
func NewTargetHandler(h *Handler) *TargetHandler {
	return &TargetHandler{Handler: h}
}

// ============================================================================
// List Targets
// ============================================================================

// ListTargetsRequest holds list request data.
type ListTargetsRequest struct {
	Labels map[string]string
	Limit  int
	Cursor string
}

// ListTargetsResponse holds list response data.
type ListTargetsResponse struct {
	Targets    []*TargetInfo
	NextCursor string
}

// TargetInfo holds target information with stats.
type TargetInfo struct {
	Target *store.Target
	Stats  *store.TargetStats
}

// ListTargets lists targets in the bound namespace.
func (h *TargetHandler) ListTargets(ctx *RequestContext, req *ListTargetsRequest) (*ListTargetsResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	// Use filtered list if filters provided
	if len(req.Labels) > 0 || req.Limit > 0 || req.Cursor != "" {
		limit := req.Limit
		if limit == 0 {
			limit = 100
		}

		targets, nextCursor, err := h.mgr.Targets.ListFiltered(namespace, req.Labels, limit, req.Cursor)
		if err != nil {
			return nil, Errorf(ErrInternal, "list targets: %v", err)
		}

		var infos []*TargetInfo
		for _, t := range targets {
			info := &TargetInfo{Target: t}
			info.Stats, _ = h.mgr.Targets.GetStats(namespace, t.Name)
			infos = append(infos, info)
		}

		return &ListTargetsResponse{
			Targets:    infos,
			NextCursor: nextCursor,
		}, nil
	}

	// Simple list
	targets := h.mgr.Targets.List(namespace)
	var infos []*TargetInfo
	for _, t := range targets {
		info := &TargetInfo{Target: t}
		info.Stats, _ = h.mgr.Targets.GetStats(namespace, t.Name)
		infos = append(infos, info)
	}

	return &ListTargetsResponse{Targets: infos}, nil
}

// ============================================================================
// Get Target
// ============================================================================

// GetTargetRequest holds get request data.
type GetTargetRequest struct {
	Name string
}

// GetTargetResponse holds get response data.
type GetTargetResponse struct {
	Target  *store.Target
	Stats   *store.TargetStats
	Pollers []*PollerSummary
}

// PollerSummary holds summary poller information.
type PollerSummary struct {
	Name        string
	Protocol    string
	AdminState  string
	OperState   string
	HealthState string
}

// GetTarget gets target details.
func (h *TargetHandler) GetTarget(ctx *RequestContext, req *GetTargetRequest) (*GetTargetResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	target, err := h.mgr.Targets.Get(namespace, req.Name)
	if err != nil {
		return nil, Errorf(ErrInternal, "get target: %v", err)
	}
	if target == nil {
		return nil, Errorf(ErrNotFound, "target not found: %s", req.Name)
	}

	stats, _ := h.mgr.Targets.GetStats(namespace, req.Name)

	// Get poller summaries
	pollers := h.mgr.Pollers.List(namespace, req.Name)
	var summaries []*PollerSummary
	for _, p := range pollers {
		state := h.mgr.States.Get(namespace, req.Name, p.Name)
		admin, oper, health := state.GetState()

		summaries = append(summaries, &PollerSummary{
			Name:        p.Name,
			Protocol:    p.Protocol,
			AdminState:  admin,
			OperState:   oper,
			HealthState: health,
		})
	}

	return &GetTargetResponse{
		Target:  target,
		Stats:   stats,
		Pollers: summaries,
	}, nil
}

// ============================================================================
// Create Target
// ============================================================================

// CreateTargetRequest holds create request data.
type CreateTargetRequest struct {
	Name        string
	Description string
	Labels      map[string]string
	Config      *store.TargetConfig
}

// CreateTargetResponse holds create response data.
type CreateTargetResponse struct {
	Target *store.Target
}

// CreateTarget creates a new target.
func (h *TargetHandler) CreateTarget(ctx *RequestContext, req *CreateTargetRequest) (*CreateTargetResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	target := &store.Target{
		Namespace:   namespace,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		Config:      req.Config,
	}

	if err := h.mgr.Targets.Create(target); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &CreateTargetResponse{Target: target}, nil
}

// ============================================================================
// Update Target
// ============================================================================

// UpdateTargetRequest holds update request data.
type UpdateTargetRequest struct {
	Name        string
	Description *string
	Labels      map[string]string
	Config      *store.TargetConfig
}

// UpdateTargetResponse holds update response data.
type UpdateTargetResponse struct {
	Target *store.Target
}

// UpdateTarget updates a target.
func (h *TargetHandler) UpdateTarget(ctx *RequestContext, req *UpdateTargetRequest) (*UpdateTargetResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	target, err := h.mgr.Targets.Get(namespace, req.Name)
	if err != nil {
		return nil, Errorf(ErrInternal, "get target: %v", err)
	}
	if target == nil {
		return nil, Errorf(ErrNotFound, "target not found: %s", req.Name)
	}

	// Apply updates
	if req.Description != nil {
		target.Description = *req.Description
	}
	if req.Labels != nil {
		target.Labels = req.Labels
	}
	if req.Config != nil {
		target.Config = req.Config
	}

	if err := h.mgr.Targets.Update(target); err != nil {
		if err == store.ErrConcurrentModification {
			return nil, Errorf(ErrConcurrentMod, "target was modified by another client")
		}
		return nil, Errorf(ErrInternal, "update target: %v", err)
	}

	return &UpdateTargetResponse{Target: target}, nil
}

// ============================================================================
// Delete Target
// ============================================================================

// DeleteTargetRequest holds delete request data.
type DeleteTargetRequest struct {
	Name  string
	Force bool
}

// DeleteTargetResponse holds delete response data.
type DeleteTargetResponse struct {
	PollersDeleted int
	LinksDeleted   int
}

// DeleteTarget deletes a target.
func (h *TargetHandler) DeleteTarget(ctx *RequestContext, req *DeleteTargetRequest) (*DeleteTargetResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	pollers, links, err := h.mgr.Targets.Delete(namespace, req.Name, req.Force)
	if err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &DeleteTargetResponse{
		PollersDeleted: pollers,
		LinksDeleted:   links,
	}, nil
}

// ============================================================================
// Set Labels
// ============================================================================

// SetLabelsRequest holds set labels request data.
type SetLabelsRequest struct {
	Name   string
	Labels map[string]string
}

// SetLabelsResponse holds set labels response data.
type SetLabelsResponse struct {
	Target *store.Target
}

// SetLabels sets target labels.
func (h *TargetHandler) SetLabels(ctx *RequestContext, req *SetLabelsRequest) (*SetLabelsResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	if err := h.mgr.Targets.SetLabels(namespace, req.Name, req.Labels); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	target, _ := h.mgr.Targets.Get(namespace, req.Name)
	return &SetLabelsResponse{Target: target}, nil
}
