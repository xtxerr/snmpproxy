package handler

import (
	"github.com/xtxerr/snmpproxy/internal/manager"
	"github.com/xtxerr/snmpproxy/internal/store"
)

// PollerHandler handles poller operations.
type PollerHandler struct {
	*Handler
}

// NewPollerHandler creates a poller handler.
func NewPollerHandler(h *Handler) *PollerHandler {
	return &PollerHandler{Handler: h}
}

// ============================================================================
// List Pollers
// ============================================================================

// ListPollersRequest holds list request data.
type ListPollersRequest struct {
	Target string
}

// ListPollersResponse holds list response data.
type ListPollersResponse struct {
	Pollers []*PollerInfo
}

// PollerInfo holds poller information with state and stats.
type PollerInfo struct {
	Poller      *store.Poller
	AdminState  string
	OperState   string
	HealthState string
	LastError   string
	PollsTotal  int64
	PollsOK     int64
	PollsFailed int64
}

// ListPollers lists pollers for a target.
func (h *PollerHandler) ListPollers(ctx *RequestContext, req *ListPollersRequest) (*ListPollersResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	pollers := h.mgr.Pollers.List(namespace, req.Target)
	var infos []*PollerInfo

	for _, p := range pollers {
		state := h.mgr.States.Get(namespace, req.Target, p.Name)
		stats := h.mgr.Stats.Get(namespace, req.Target, p.Name)

		admin, oper, health := state.GetState()
		total, success, failed, _, _, _, _ := stats.GetStats()

		infos = append(infos, &PollerInfo{
			Poller:      p,
			AdminState:  admin,
			OperState:   oper,
			HealthState: health,
			LastError:   state.GetLastError(),
			PollsTotal:  total,
			PollsOK:     success,
			PollsFailed: failed,
		})
	}

	return &ListPollersResponse{Pollers: infos}, nil
}

// ============================================================================
// Get Poller
// ============================================================================

// GetPollerRequest holds get request data.
type GetPollerRequest struct {
	Target string
	Name   string
}

// GetPollerResponse holds get response data.
type GetPollerResponse struct {
	*manager.PollerFullInfo
}

// GetPoller gets poller details.
func (h *PollerHandler) GetPoller(ctx *RequestContext, req *GetPollerRequest) (*GetPollerResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	info, err := h.mgr.GetPollerFullInfo(namespace, req.Target, req.Name)
	if err != nil {
		return nil, Errorf(ErrNotFound, err.Error())
	}

	return &GetPollerResponse{PollerFullInfo: info}, nil
}

// ============================================================================
// Create Poller
// ============================================================================

// CreatePollerRequest holds create request data.
type CreatePollerRequest struct {
	Target         string
	Name           string
	Description    string
	Protocol       string
	ProtocolConfig []byte
	PollingConfig  *store.PollingConfig
	AdminState     string
}

// CreatePollerResponse holds create response data.
type CreatePollerResponse struct {
	Poller *store.Poller
}

// CreatePoller creates a new poller.
func (h *PollerHandler) CreatePoller(ctx *RequestContext, req *CreatePollerRequest) (*CreatePollerResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	// Verify target exists
	target, err := h.mgr.Targets.Get(namespace, req.Target)
	if err != nil {
		return nil, Errorf(ErrInternal, "get target: %v", err)
	}
	if target == nil {
		return nil, Errorf(ErrNotFound, "target not found: %s", req.Target)
	}

	adminState := req.AdminState
	if adminState == "" {
		adminState = manager.AdminStateDisabled
	}

	poller := &store.Poller{
		Namespace:      namespace,
		Target:         req.Target,
		Name:           req.Name,
		Description:    req.Description,
		Protocol:       req.Protocol,
		ProtocolConfig: req.ProtocolConfig,
		PollingConfig:  req.PollingConfig,
		AdminState:     adminState,
	}

	if err := h.mgr.Pollers.Create(poller); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &CreatePollerResponse{Poller: poller}, nil
}

// ============================================================================
// Update Poller
// ============================================================================

// UpdatePollerRequest holds update request data.
type UpdatePollerRequest struct {
	Target         string
	Name           string
	Description    *string
	ProtocolConfig []byte
	PollingConfig  *store.PollingConfig
}

// UpdatePollerResponse holds update response data.
type UpdatePollerResponse struct {
	Poller *store.Poller
}

// UpdatePoller updates a poller.
func (h *PollerHandler) UpdatePoller(ctx *RequestContext, req *UpdatePollerRequest) (*UpdatePollerResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	poller, err := h.mgr.Pollers.Get(namespace, req.Target, req.Name)
	if err != nil {
		return nil, Errorf(ErrInternal, "get poller: %v", err)
	}
	if poller == nil {
		return nil, Errorf(ErrNotFound, "poller not found: %s/%s", req.Target, req.Name)
	}

	// Apply updates
	if req.Description != nil {
		poller.Description = *req.Description
	}
	if req.ProtocolConfig != nil {
		poller.ProtocolConfig = req.ProtocolConfig
	}
	if req.PollingConfig != nil {
		poller.PollingConfig = req.PollingConfig
	}

	if err := h.mgr.Pollers.Update(poller); err != nil {
		if err == store.ErrConcurrentModification {
			return nil, Errorf(ErrConcurrentMod, "poller was modified by another client")
		}
		return nil, Errorf(ErrInternal, "update poller: %v", err)
	}

	return &UpdatePollerResponse{Poller: poller}, nil
}

// ============================================================================
// Delete Poller
// ============================================================================

// DeletePollerRequest holds delete request data.
type DeletePollerRequest struct {
	Target string
	Name   string
}

// DeletePollerResponse holds delete response data.
type DeletePollerResponse struct {
	LinksDeleted int
}

// DeletePoller deletes a poller.
func (h *PollerHandler) DeletePoller(ctx *RequestContext, req *DeletePollerRequest) (*DeletePollerResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	// Check if poller is running
	state := h.mgr.States.Get(namespace, req.Target, req.Name)
	if state.IsRunning() {
		return nil, Errorf(ErrInvalidRequest, "cannot delete running poller, disable it first")
	}

	links, err := h.mgr.Pollers.Delete(namespace, req.Target, req.Name)
	if err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &DeletePollerResponse{LinksDeleted: links}, nil
}

// ============================================================================
// Enable / Disable
// ============================================================================

// EnablePollerRequest holds enable request data.
type EnablePollerRequest struct {
	Target string
	Name   string
}

// EnablePollerResponse holds enable response data.
type EnablePollerResponse struct {
	AdminState  string
	OperState   string
	HealthState string
}

// EnablePoller enables a poller.
func (h *PollerHandler) EnablePoller(ctx *RequestContext, req *EnablePollerRequest) (*EnablePollerResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	if err := h.mgr.Pollers.Enable(namespace, req.Target, req.Name); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	state := h.mgr.States.Get(namespace, req.Target, req.Name)
	admin, oper, health := state.GetState()

	return &EnablePollerResponse{
		AdminState:  admin,
		OperState:   oper,
		HealthState: health,
	}, nil
}

// DisablePollerRequest holds disable request data.
type DisablePollerRequest struct {
	Target string
	Name   string
}

// DisablePollerResponse holds disable response data.
type DisablePollerResponse struct {
	AdminState  string
	OperState   string
	HealthState string
}

// DisablePoller disables a poller.
func (h *PollerHandler) DisablePoller(ctx *RequestContext, req *DisablePollerRequest) (*DisablePollerResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	if err := h.mgr.Pollers.Disable(namespace, req.Target, req.Name); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	state := h.mgr.States.Get(namespace, req.Target, req.Name)
	admin, oper, health := state.GetState()

	return &DisablePollerResponse{
		AdminState:  admin,
		OperState:   oper,
		HealthState: health,
	}, nil
}

// ============================================================================
// Get History
// ============================================================================

// GetHistoryRequest holds history request data.
type GetHistoryRequest struct {
	Target  string
	Poller  string
	Limit   int
	SinceMs int64
	UntilMs int64
}

// GetHistoryResponse holds history response data.
type GetHistoryResponse struct {
	Samples []*store.Sample
}

// GetHistory gets poller sample history.
func (h *PollerHandler) GetHistory(ctx *RequestContext, req *GetHistoryRequest) (*GetHistoryResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	limit := req.Limit
	if limit == 0 {
		limit = 100
	}

	samples, err := h.mgr.Store().GetSamples(namespace, req.Target, req.Poller, limit, req.SinceMs, req.UntilMs)
	if err != nil {
		return nil, Errorf(ErrInternal, "get samples: %v", err)
	}

	return &GetHistoryResponse{Samples: samples}, nil
}

// ============================================================================
// Get Resolved Config
// ============================================================================

// GetResolvedConfigRequest holds request data.
type GetResolvedConfigRequest struct {
	Target string
	Poller string
}

// GetResolvedConfigResponse holds response data.
type GetResolvedConfigResponse struct {
	Config *manager.ResolvedPollerConfig
}

// GetResolvedConfig gets the resolved configuration for a poller.
func (h *PollerHandler) GetResolvedConfig(ctx *RequestContext, req *GetResolvedConfigRequest) (*GetResolvedConfigResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	config, err := h.mgr.ConfigResolver.Resolve(namespace, req.Target, req.Poller)
	if err != nil {
		return nil, Errorf(ErrInternal, "resolve config: %v", err)
	}

	return &GetResolvedConfigResponse{Config: config}, nil
}
