package handler

import (
	"fmt"
)

// SubscriptionHandler handles subscription operations.
type SubscriptionHandler struct {
	*Handler
}

// NewSubscriptionHandler creates a subscription handler.
func NewSubscriptionHandler(h *Handler) *SubscriptionHandler {
	return &SubscriptionHandler{Handler: h}
}

// subscriptionKey returns the key for a subscription.
func subscriptionKey(target, poller string) string {
	return target + "/" + poller
}

// ============================================================================
// Subscribe
// ============================================================================

// SubscribeRequest holds subscribe request data.
type SubscribeRequest struct {
	Subscriptions []SubscriptionSpec
}

// SubscriptionSpec specifies what to subscribe to.
type SubscriptionSpec struct {
	Target string
	Poller string // Empty = all pollers for target
}

// SubscribeResponse holds subscribe response data.
type SubscribeResponse struct {
	Subscribed []string // Keys that were subscribed
	Invalid    []string // Keys that were invalid
}

// Subscribe subscribes to poller updates.
func (h *SubscriptionHandler) Subscribe(ctx *RequestContext, req *SubscribeRequest) (*SubscribeResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()
	resp := &SubscribeResponse{}

	for _, spec := range req.Subscriptions {
		if spec.Poller != "" {
			// Subscribe to specific poller
			if h.mgr.Pollers.Exists(namespace, spec.Target, spec.Poller) {
				key := subscriptionKey(spec.Target, spec.Poller)
				ctx.Session.Subscribe(key)
				resp.Subscribed = append(resp.Subscribed, key)
			} else {
				resp.Invalid = append(resp.Invalid, subscriptionKey(spec.Target, spec.Poller))
			}
		} else {
			// Subscribe to all pollers for target
			pollers := h.mgr.Pollers.List(namespace, spec.Target)
			for _, p := range pollers {
				key := subscriptionKey(spec.Target, p.Name)
				ctx.Session.Subscribe(key)
				resp.Subscribed = append(resp.Subscribed, key)
			}
			if len(pollers) == 0 {
				resp.Invalid = append(resp.Invalid, spec.Target+"/*")
			}
		}
	}

	return resp, nil
}

// ============================================================================
// Unsubscribe
// ============================================================================

// UnsubscribeRequest holds unsubscribe request data.
type UnsubscribeRequest struct {
	Keys []string // Empty = unsubscribe all
}

// UnsubscribeResponse holds unsubscribe response data.
type UnsubscribeResponse struct {
	Unsubscribed []string
}

// Unsubscribe unsubscribes from poller updates.
func (h *SubscriptionHandler) Unsubscribe(ctx *RequestContext, req *UnsubscribeRequest) (*UnsubscribeResponse, error) {
	var unsubscribed []string

	if len(req.Keys) == 0 {
		// Unsubscribe all
		unsubscribed = ctx.Session.UnsubscribeAll()
	} else {
		// Unsubscribe specific
		for _, key := range req.Keys {
			if ctx.Session.IsSubscribed(key) {
				ctx.Session.Unsubscribe(key)
				unsubscribed = append(unsubscribed, key)
			}
		}
	}

	return &UnsubscribeResponse{Unsubscribed: unsubscribed}, nil
}

// ============================================================================
// List Subscriptions
// ============================================================================

// ListSubscriptionsRequest holds list request data.
type ListSubscriptionsRequest struct{}

// ListSubscriptionsResponse holds list response data.
type ListSubscriptionsResponse struct {
	Subscriptions []string
	Count         int
}

// ListSubscriptions lists current subscriptions.
func (h *SubscriptionHandler) ListSubscriptions(ctx *RequestContext, req *ListSubscriptionsRequest) (*ListSubscriptionsResponse, error) {
	subs := ctx.Session.GetSubscriptions()
	return &ListSubscriptionsResponse{
		Subscriptions: subs,
		Count:         len(subs),
	}, nil
}

// ============================================================================
// Broadcast Sample
// ============================================================================

// BroadcastSample sends a sample to all subscribed sessions.
func (h *SubscriptionHandler) BroadcastSample(namespace, target, poller string, sample []byte) int {
	key := subscriptionKey(target, poller)
	subscribers := h.sessionManager.GetSubscribersFor(namespace, key)

	sent := 0
	for _, s := range subscribers {
		if s.Send(sample) {
			sent++
		}
	}

	return sent
}

// ============================================================================
// Subscribe by Path
// ============================================================================

// SubscribeByPathRequest holds subscribe by path request data.
type SubscribeByPathRequest struct {
	Paths []string // e.g., "/targets/router/cpu", "/tree/dc1/router-cpu"
}

// SubscribeByPathResponse holds subscribe by path response data.
type SubscribeByPathResponse struct {
	Subscribed []string
	Invalid    []string
}

// SubscribeByPath subscribes to pollers by path.
func (h *SubscriptionHandler) SubscribeByPath(ctx *RequestContext, req *SubscribeByPathRequest) (*SubscribeByPathResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()
	resp := &SubscribeByPathResponse{}

	browse := NewBrowseHandler(h.Handler)

	for _, path := range req.Paths {
		resolved, err := browse.ResolvePath(ctx, &ResolvePathRequest{Path: path})
		if err != nil {
			resp.Invalid = append(resp.Invalid, path)
			continue
		}

		switch resolved.Type {
		case "poller":
			key := subscriptionKey(resolved.Target, resolved.Poller)
			if h.mgr.Pollers.Exists(namespace, resolved.Target, resolved.Poller) {
				ctx.Session.Subscribe(key)
				resp.Subscribed = append(resp.Subscribed, key)
			} else {
				resp.Invalid = append(resp.Invalid, path)
			}

		case "target":
			// Subscribe to all pollers
			pollers := h.mgr.Pollers.List(namespace, resolved.Target)
			for _, p := range pollers {
				key := subscriptionKey(resolved.Target, p.Name)
				ctx.Session.Subscribe(key)
				resp.Subscribed = append(resp.Subscribed, key)
			}
			if len(pollers) == 0 {
				resp.Invalid = append(resp.Invalid, path)
			}

		default:
			resp.Invalid = append(resp.Invalid, fmt.Sprintf("%s (not a poller/target)", path))
		}
	}

	return resp, nil
}
