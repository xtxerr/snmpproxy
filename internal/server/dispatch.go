package server

import (
	"fmt"
	"log"

	"github.com/xtxerr/snmpproxy/internal/handler"
	"github.com/xtxerr/snmpproxy/internal/store"
	"github.com/xtxerr/snmpproxy/internal/wire"
)

// MessageDispatcher routes incoming messages to handlers.
type MessageDispatcher struct {
	srv *Server
}

// NewMessageDispatcher creates a new dispatcher.
func NewMessageDispatcher(srv *Server) *MessageDispatcher {
	return &MessageDispatcher{srv: srv}
}

// Dispatch routes a message to the appropriate handler.
// Returns the response to send back.
func (d *MessageDispatcher) Dispatch(session *handler.Session, requestID uint64, msgType string, payload interface{}) (interface{}, error) {
	ctx := d.srv.handler.NewContext(session, requestID)

	switch msgType {
	// Namespace operations
	case "bind_namespace":
		return d.handleBindNamespace(ctx, payload)
	case "list_namespaces":
		return d.handleListNamespaces(ctx, payload)
	case "get_namespace":
		return d.handleGetNamespace(ctx, payload)
	case "create_namespace":
		return d.handleCreateNamespace(ctx, payload)
	case "update_namespace":
		return d.handleUpdateNamespace(ctx, payload)
	case "delete_namespace":
		return d.handleDeleteNamespace(ctx, payload)

	// Target operations
	case "list_targets":
		return d.handleListTargets(ctx, payload)
	case "get_target":
		return d.handleGetTarget(ctx, payload)
	case "create_target":
		return d.handleCreateTarget(ctx, payload)
	case "update_target":
		return d.handleUpdateTarget(ctx, payload)
	case "delete_target":
		return d.handleDeleteTarget(ctx, payload)
	case "set_labels":
		return d.handleSetLabels(ctx, payload)

	// Poller operations
	case "list_pollers":
		return d.handleListPollers(ctx, payload)
	case "get_poller":
		return d.handleGetPoller(ctx, payload)
	case "create_poller":
		return d.handleCreatePoller(ctx, payload)
	case "update_poller":
		return d.handleUpdatePoller(ctx, payload)
	case "delete_poller":
		return d.handleDeletePoller(ctx, payload)
	case "enable_poller":
		return d.handleEnablePoller(ctx, payload)
	case "disable_poller":
		return d.handleDisablePoller(ctx, payload)
	case "get_history":
		return d.handleGetHistory(ctx, payload)
	case "get_resolved_config":
		return d.handleGetResolvedConfig(ctx, payload)

	// Browse operations
	case "browse":
		return d.handleBrowse(ctx, payload)
	case "create_directory":
		return d.handleCreateDirectory(ctx, payload)
	case "create_link":
		return d.handleCreateLink(ctx, payload)
	case "delete_tree_node":
		return d.handleDeleteTreeNode(ctx, payload)

	// Subscription operations
	case "subscribe":
		return d.handleSubscribe(ctx, payload)
	case "unsubscribe":
		return d.handleUnsubscribe(ctx, payload)
	case "list_subscriptions":
		return d.handleListSubscriptions(ctx, payload)
	case "subscribe_by_path":
		return d.handleSubscribeByPath(ctx, payload)

	// Status operations
	case "get_server_status":
		return d.handleGetServerStatus(ctx, payload)
	case "get_session_info":
		return d.handleGetSessionInfo(ctx, payload)
	case "get_namespace_status":
		return d.handleGetNamespaceStatus(ctx, payload)

	// Secret operations
	case "list_secrets":
		return d.handleListSecrets(ctx, payload)
	case "get_secret":
		return d.handleGetSecret(ctx, payload)
	case "create_secret":
		return d.handleCreateSecret(ctx, payload)
	case "update_secret":
		return d.handleUpdateSecret(ctx, payload)
	case "delete_secret":
		return d.handleDeleteSecret(ctx, payload)

	default:
		return nil, fmt.Errorf("unknown message type: %s", msgType)
	}
}

// ============================================================================
// Namespace handlers
// ============================================================================

func (d *MessageDispatcher) handleBindNamespace(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.BindNamespaceRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.nsHandler.BindNamespace(ctx, req)
}

func (d *MessageDispatcher) handleListNamespaces(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.ListNamespacesRequest)
	if !ok {
		req = &handler.ListNamespacesRequest{}
	}
	return d.srv.nsHandler.ListNamespaces(ctx, req)
}

func (d *MessageDispatcher) handleGetNamespace(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetNamespaceRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.nsHandler.GetNamespace(ctx, req)
}

func (d *MessageDispatcher) handleCreateNamespace(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.CreateNamespaceRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.nsHandler.CreateNamespace(ctx, req)
}

func (d *MessageDispatcher) handleUpdateNamespace(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.UpdateNamespaceRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.nsHandler.UpdateNamespace(ctx, req)
}

func (d *MessageDispatcher) handleDeleteNamespace(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.DeleteNamespaceRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.nsHandler.DeleteNamespace(ctx, req)
}

// ============================================================================
// Target handlers
// ============================================================================

func (d *MessageDispatcher) handleListTargets(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.ListTargetsRequest)
	if !ok {
		req = &handler.ListTargetsRequest{}
	}
	return d.srv.targetHandler.ListTargets(ctx, req)
}

func (d *MessageDispatcher) handleGetTarget(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetTargetRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.targetHandler.GetTarget(ctx, req)
}

func (d *MessageDispatcher) handleCreateTarget(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.CreateTargetRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.targetHandler.CreateTarget(ctx, req)
}

func (d *MessageDispatcher) handleUpdateTarget(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.UpdateTargetRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.targetHandler.UpdateTarget(ctx, req)
}

func (d *MessageDispatcher) handleDeleteTarget(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.DeleteTargetRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.targetHandler.DeleteTarget(ctx, req)
}

func (d *MessageDispatcher) handleSetLabels(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.SetLabelsRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.targetHandler.SetLabels(ctx, req)
}

// ============================================================================
// Poller handlers
// ============================================================================

func (d *MessageDispatcher) handleListPollers(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.ListPollersRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.pollerHandler.ListPollers(ctx, req)
}

func (d *MessageDispatcher) handleGetPoller(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetPollerRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.pollerHandler.GetPoller(ctx, req)
}

func (d *MessageDispatcher) handleCreatePoller(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.CreatePollerRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}

	resp, err := d.srv.pollerHandler.CreatePoller(ctx, req)
	if err != nil {
		return nil, err
	}

	// If created enabled, start polling
	if resp.Poller.AdminState == "enabled" {
		cfg, _ := d.srv.mgr.ConfigResolver.Resolve(
			resp.Poller.Namespace,
			resp.Poller.Target,
			resp.Poller.Name,
		)
		if cfg != nil {
			d.srv.onPollerCreated(
				resp.Poller.Namespace,
				resp.Poller.Target,
				resp.Poller.Name,
				cfg.IntervalMs,
			)
		}
	}

	return resp, nil
}

func (d *MessageDispatcher) handleUpdatePoller(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.UpdatePollerRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.pollerHandler.UpdatePoller(ctx, req)
}

func (d *MessageDispatcher) handleDeletePoller(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.DeletePollerRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.pollerHandler.DeletePoller(ctx, req)
}

func (d *MessageDispatcher) handleEnablePoller(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.EnablePollerRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}

	resp, err := d.srv.pollerHandler.EnablePoller(ctx, req)
	if err != nil {
		return nil, err
	}

	// Start polling
	ns, _ := ctx.Namespace()
	cfg, _ := d.srv.mgr.ConfigResolver.Resolve(ns, req.Target, req.Name)
	if cfg != nil {
		d.srv.onPollerCreated(ns, req.Target, req.Name, cfg.IntervalMs)

		state := d.srv.mgr.States.Get(ns, req.Target, req.Name)
		state.Start()
		state.MarkRunning()
	}

	return resp, nil
}

func (d *MessageDispatcher) handleDisablePoller(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.DisablePollerRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}

	resp, err := d.srv.pollerHandler.DisablePoller(ctx, req)
	if err != nil {
		return nil, err
	}

	// Stop polling
	ns, _ := ctx.Namespace()
	d.srv.onPollerDeleted(ns, req.Target, req.Name)

	state := d.srv.mgr.States.Get(ns, req.Target, req.Name)
	state.Stop()
	state.MarkStopped()

	return resp, nil
}

func (d *MessageDispatcher) handleGetHistory(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetHistoryRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.pollerHandler.GetHistory(ctx, req)
}

func (d *MessageDispatcher) handleGetResolvedConfig(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetResolvedConfigRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.pollerHandler.GetResolvedConfig(ctx, req)
}

// ============================================================================
// Browse handlers
// ============================================================================

func (d *MessageDispatcher) handleBrowse(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.BrowseRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.browseHandler.Browse(ctx, req)
}

func (d *MessageDispatcher) handleCreateDirectory(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.CreateDirectoryRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.browseHandler.CreateDirectory(ctx, req)
}

func (d *MessageDispatcher) handleCreateLink(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.CreateLinkRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.browseHandler.CreateLink(ctx, req)
}

func (d *MessageDispatcher) handleDeleteTreeNode(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.DeleteTreeNodeRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.browseHandler.DeleteTreeNode(ctx, req)
}

// ============================================================================
// Subscription handlers
// ============================================================================

func (d *MessageDispatcher) handleSubscribe(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.SubscribeRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.subHandler.Subscribe(ctx, req)
}

func (d *MessageDispatcher) handleUnsubscribe(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.UnsubscribeRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.subHandler.Unsubscribe(ctx, req)
}

func (d *MessageDispatcher) handleListSubscriptions(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.ListSubscriptionsRequest)
	if !ok {
		req = &handler.ListSubscriptionsRequest{}
	}
	return d.srv.subHandler.ListSubscriptions(ctx, req)
}

func (d *MessageDispatcher) handleSubscribeByPath(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.SubscribeByPathRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.subHandler.SubscribeByPath(ctx, req)
}

// ============================================================================
// Status handlers
// ============================================================================

func (d *MessageDispatcher) handleGetServerStatus(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetServerStatusRequest)
	if !ok {
		req = &handler.GetServerStatusRequest{}
	}
	return d.srv.statusHandler.GetServerStatus(ctx, req)
}

func (d *MessageDispatcher) handleGetSessionInfo(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetSessionInfoRequest)
	if !ok {
		req = &handler.GetSessionInfoRequest{}
	}
	return d.srv.statusHandler.GetSessionInfo(ctx, req)
}

func (d *MessageDispatcher) handleGetNamespaceStatus(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetNamespaceStatusRequest)
	if !ok {
		req = &handler.GetNamespaceStatusRequest{}
	}
	return d.srv.statusHandler.GetNamespaceStatus(ctx, req)
}

// ============================================================================
// Secret handlers
// ============================================================================

func (d *MessageDispatcher) handleListSecrets(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.ListSecretsRequest)
	if !ok {
		req = &handler.ListSecretsRequest{}
	}
	return d.srv.secretHandler.ListSecrets(ctx, req)
}

func (d *MessageDispatcher) handleGetSecret(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.GetSecretRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.secretHandler.GetSecret(ctx, req)
}

func (d *MessageDispatcher) handleCreateSecret(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.CreateSecretRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.secretHandler.CreateSecret(ctx, req)
}

func (d *MessageDispatcher) handleUpdateSecret(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.UpdateSecretRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.secretHandler.UpdateSecret(ctx, req)
}

func (d *MessageDispatcher) handleDeleteSecret(ctx *handler.RequestContext, payload interface{}) (interface{}, error) {
	req, ok := payload.(*handler.DeleteSecretRequest)
	if !ok {
		return nil, fmt.Errorf("invalid payload type")
	}
	return d.srv.secretHandler.DeleteSecret(ctx, req)
}

// ============================================================================
// Error helper
// ============================================================================

// MakeErrorResponse creates an error response.
func MakeErrorResponse(id uint64, err error) interface{} {
	herr, ok := err.(*handler.HandlerError)
	if ok {
		return wire.NewError(id, int32(herr.Code), herr.Message)
	}
	return wire.NewError(id, wire.ErrInternal, err.Error())
}

// Use log to avoid unused import
var _ = log.Printf
