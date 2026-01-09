package handler

import (
	"time"
)

// StatusHandler handles server status operations.
type StatusHandler struct {
	*Handler
}

// NewStatusHandler creates a status handler.
func NewStatusHandler(h *Handler) *StatusHandler {
	return &StatusHandler{Handler: h}
}

// ============================================================================
// Get Server Status
// ============================================================================

// GetServerStatusRequest holds request data.
type GetServerStatusRequest struct{}

// GetServerStatusResponse holds response data.
type GetServerStatusResponse struct {
	Version   string
	StartedAt time.Time
	UptimeMs  int64

	// Sessions
	SessionsActive int
	SessionsLost   int

	// Entities
	NamespaceCount int
	TargetCount    int
	PollerCount    int

	// Pollers by state
	PollersRunning int
	PollersStopped int
	PollersError   int

	// Pollers by health
	PollersUp      int
	PollersDown    int
	PollersDegraded int

	// Statistics
	PollsTotal   int64
	PollsSuccess int64
	PollsFailed  int64

	// Sync status
	BufferedSamples int
}

// GetServerStatus returns server status.
func (h *StatusHandler) GetServerStatus(ctx *RequestContext, req *GetServerStatusRequest) (*GetServerStatusResponse, error) {
	info := h.mgr.GetServerInfo()

	operCounts := h.mgr.States.CountByOperState()
	healthCounts := h.mgr.States.CountByHealthState()

	return &GetServerStatusResponse{
		Version:         info.Version,
		StartedAt:       info.StartedAt,
		UptimeMs:        info.Uptime.Milliseconds(),
		SessionsActive:  h.sessionManager.CountActive(),
		SessionsLost:    h.sessionManager.CountLost(),
		NamespaceCount:  info.NamespaceCount,
		TargetCount:     info.TargetCount,
		PollerCount:     info.PollerCount,
		PollersRunning:  operCounts["running"],
		PollersStopped:  operCounts["stopped"],
		PollersError:    operCounts["error"],
		PollersUp:       healthCounts["up"],
		PollersDown:     healthCounts["down"],
		PollersDegraded: healthCounts["degraded"],
		PollsTotal:      info.PollsTotal,
		PollsSuccess:    info.PollsSuccess,
		PollsFailed:     info.PollsFailed,
		BufferedSamples: h.mgr.Sync.GetBufferedSampleCount(),
	}, nil
}

// ============================================================================
// Get Session Info
// ============================================================================

// GetSessionInfoRequest holds request data.
type GetSessionInfoRequest struct{}

// GetSessionInfoResponse holds response data.
type GetSessionInfoResponse struct {
	SessionID     string
	TokenID       string
	Namespace     string
	CreatedAt     time.Time
	Subscriptions []string
}

// GetSessionInfo returns current session info.
func (h *StatusHandler) GetSessionInfo(ctx *RequestContext, req *GetSessionInfoRequest) (*GetSessionInfoResponse, error) {
	return &GetSessionInfoResponse{
		SessionID:     ctx.Session.ID,
		TokenID:       ctx.Session.TokenID,
		Namespace:     ctx.Session.GetNamespace(),
		CreatedAt:     ctx.Session.CreatedAt,
		Subscriptions: ctx.Session.GetSubscriptions(),
	}, nil
}

// ============================================================================
// Get Namespace Status
// ============================================================================

// GetNamespaceStatusRequest holds request data.
type GetNamespaceStatusRequest struct{}

// GetNamespaceStatusResponse holds response data.
type GetNamespaceStatusResponse struct {
	Namespace string

	// Counts
	TargetCount int
	PollerCount int

	// Pollers by state
	PollersEnabled  int
	PollersDisabled int
	PollersRunning  int

	// Pollers by health
	PollersUp   int
	PollersDown int

	// Statistics
	PollsTotal   int64
	PollsSuccess int64
	PollsFailed  int64
}

// GetNamespaceStatus returns namespace-specific status.
func (h *StatusHandler) GetNamespaceStatus(ctx *RequestContext, req *GetNamespaceStatusRequest) (*GetNamespaceStatusResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()

	resp := &GetNamespaceStatusResponse{
		Namespace: namespace,
	}

	// Count targets
	resp.TargetCount = h.mgr.Targets.CountInNamespace(namespace)

	// Count pollers and states
	pollers := h.mgr.Pollers.ListInNamespace(namespace)
	resp.PollerCount = len(pollers)

	for _, p := range pollers {
		state := h.mgr.States.Get(namespace, p.Target, p.Name)
		admin, oper, health := state.GetState()

		// Admin state
		if admin == "enabled" {
			resp.PollersEnabled++
		} else {
			resp.PollersDisabled++
		}

		// Oper state
		if oper == "running" {
			resp.PollersRunning++
		}

		// Health state
		switch health {
		case "up":
			resp.PollersUp++
		case "down":
			resp.PollersDown++
		}
	}

	// Aggregate stats
	resp.PollsTotal, resp.PollsSuccess, resp.PollsFailed = h.mgr.Stats.AggregateByNamespace(namespace)

	return resp, nil
}
