package handler

import (
	"testing"
	"time"

	"github.com/xtxerr/snmpproxy/internal/manager"
	"github.com/xtxerr/snmpproxy/internal/store"
)

func setupTestHandler(t *testing.T) (*Handler, *SessionManager, func()) {
	mgr, err := manager.New(&manager.Config{
		InMemory: true,
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	sm := NewSessionManager(&SessionManagerConfig{
		ReconnectWindow: 10 * time.Minute,
		AuthTimeout:     30 * time.Second,
		Tokens: []TokenConfig{
			{ID: "admin", Token: "secret", Namespaces: nil},                 // All namespaces
			{ID: "limited", Token: "limited", Namespaces: []string{"prod"}}, // Only prod
		},
	})

	h := NewHandler(mgr, sm)

	cleanup := func() {
		mgr.Stop()
	}

	return h, sm, cleanup
}

func TestNamespaceHandler(t *testing.T) {
	h, sm, cleanup := setupTestHandler(t)
	defer cleanup()

	// Create mock session
	session := sm.CreateSession("admin", nil, nil)
	ctx := h.NewContext(session, 1)

	nsHandler := NewNamespaceHandler(h)

	// Create namespace
	createResp, err := nsHandler.CreateNamespace(ctx, &CreateNamespaceRequest{
		Name:        "prod",
		Description: "Production",
	})
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}
	if createResp.Namespace.Name != "prod" {
		t.Errorf("Name = %s, want prod", createResp.Namespace.Name)
	}

	// List namespaces
	listResp, err := nsHandler.ListNamespaces(ctx, &ListNamespacesRequest{})
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	if len(listResp.Namespaces) != 1 {
		t.Errorf("Count = %d, want 1", len(listResp.Namespaces))
	}

	// Bind to namespace
	bindResp, err := nsHandler.BindNamespace(ctx, &BindNamespaceRequest{Namespace: "prod"})
	if err != nil {
		t.Fatalf("BindNamespace: %v", err)
	}
	if bindResp.Namespace != "prod" {
		t.Errorf("Bound namespace = %s, want prod", bindResp.Namespace)
	}

	// Delete namespace
	_, err = nsHandler.DeleteNamespace(ctx, &DeleteNamespaceRequest{Name: "prod", Force: true})
	if err != nil {
		t.Fatalf("DeleteNamespace: %v", err)
	}
}

func TestTargetHandler(t *testing.T) {
	h, sm, cleanup := setupTestHandler(t)
	defer cleanup()

	// Setup
	session := sm.CreateSession("admin", nil, nil)
	ctx := h.NewContext(session, 1)
	nsHandler := NewNamespaceHandler(h)
	targetHandler := NewTargetHandler(h)

	// Create and bind namespace
	nsHandler.CreateNamespace(ctx, &CreateNamespaceRequest{Name: "prod"})
	nsHandler.BindNamespace(ctx, &BindNamespaceRequest{Namespace: "prod"})

	// Create target
	createResp, err := targetHandler.CreateTarget(ctx, &CreateTargetRequest{
		Name:        "router",
		Description: "Core router",
		Labels:      map[string]string{"env": "production", "site": "dc1"},
	})
	if err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	if createResp.Target.Name != "router" {
		t.Errorf("Name = %s, want router", createResp.Target.Name)
	}

	// List targets
	listResp, err := targetHandler.ListTargets(ctx, &ListTargetsRequest{})
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
	}
	if len(listResp.Targets) != 1 {
		t.Errorf("Count = %d, want 1", len(listResp.Targets))
	}

	// Get target
	getResp, err := targetHandler.GetTarget(ctx, &GetTargetRequest{Name: "router"})
	if err != nil {
		t.Fatalf("GetTarget: %v", err)
	}
	if getResp.Target.Labels["env"] != "production" {
		t.Errorf("Label env = %s, want production", getResp.Target.Labels["env"])
	}

	// Update target
	newDesc := "Updated description"
	updateResp, err := targetHandler.UpdateTarget(ctx, &UpdateTargetRequest{
		Name:        "router",
		Description: &newDesc,
	})
	if err != nil {
		t.Fatalf("UpdateTarget: %v", err)
	}
	if updateResp.Target.Description != "Updated description" {
		t.Errorf("Description = %s, want Updated description", updateResp.Target.Description)
	}

	// Delete target
	_, err = targetHandler.DeleteTarget(ctx, &DeleteTargetRequest{Name: "router", Force: true})
	if err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}
}

func TestPollerHandler(t *testing.T) {
	h, sm, cleanup := setupTestHandler(t)
	defer cleanup()

	// Setup
	session := sm.CreateSession("admin", nil, nil)
	ctx := h.NewContext(session, 1)
	nsHandler := NewNamespaceHandler(h)
	targetHandler := NewTargetHandler(h)
	pollerHandler := NewPollerHandler(h)

	// Create namespace, target
	nsHandler.CreateNamespace(ctx, &CreateNamespaceRequest{Name: "prod"})
	nsHandler.BindNamespace(ctx, &BindNamespaceRequest{Namespace: "prod"})
	targetHandler.CreateTarget(ctx, &CreateTargetRequest{Name: "router"})

	// Create poller
	createResp, err := pollerHandler.CreatePoller(ctx, &CreatePollerRequest{
		Target:         "router",
		Name:           "cpu",
		Description:    "CPU usage",
		Protocol:       "snmp",
		ProtocolConfig: []byte(`{"host":"192.168.1.1","oid":"1.3.6.1.4.1.9.2.1.58.0"}`),
	})
	if err != nil {
		t.Fatalf("CreatePoller: %v", err)
	}
	if createResp.Poller.Name != "cpu" {
		t.Errorf("Name = %s, want cpu", createResp.Poller.Name)
	}

	// List pollers
	listResp, err := pollerHandler.ListPollers(ctx, &ListPollersRequest{Target: "router"})
	if err != nil {
		t.Fatalf("ListPollers: %v", err)
	}
	if len(listResp.Pollers) != 1 {
		t.Errorf("Count = %d, want 1", len(listResp.Pollers))
	}

	// Get poller
	getResp, err := pollerHandler.GetPoller(ctx, &GetPollerRequest{Target: "router", Name: "cpu"})
	if err != nil {
		t.Fatalf("GetPoller: %v", err)
	}
	if getResp.AdminState != "disabled" {
		t.Errorf("AdminState = %s, want disabled", getResp.AdminState)
	}

	// Enable poller
	enableResp, err := pollerHandler.EnablePoller(ctx, &EnablePollerRequest{Target: "router", Name: "cpu"})
	if err != nil {
		t.Fatalf("EnablePoller: %v", err)
	}
	if enableResp.AdminState != "enabled" {
		t.Errorf("AdminState after enable = %s, want enabled", enableResp.AdminState)
	}

	// Disable poller
	disableResp, err := pollerHandler.DisablePoller(ctx, &DisablePollerRequest{Target: "router", Name: "cpu"})
	if err != nil {
		t.Fatalf("DisablePoller: %v", err)
	}
	if disableResp.AdminState != "disabled" {
		t.Errorf("AdminState after disable = %s, want disabled", disableResp.AdminState)
	}

	// Delete poller
	_, err = pollerHandler.DeletePoller(ctx, &DeletePollerRequest{Target: "router", Name: "cpu"})
	if err != nil {
		t.Fatalf("DeletePoller: %v", err)
	}
}

func TestBrowseHandler(t *testing.T) {
	h, sm, cleanup := setupTestHandler(t)
	defer cleanup()

	// Setup
	session := sm.CreateSession("admin", nil, nil)
	ctx := h.NewContext(session, 1)
	nsHandler := NewNamespaceHandler(h)
	targetHandler := NewTargetHandler(h)
	pollerHandler := NewPollerHandler(h)
	browseHandler := NewBrowseHandler(h)

	// Create namespace, target, poller
	nsHandler.CreateNamespace(ctx, &CreateNamespaceRequest{Name: "prod"})
	nsHandler.BindNamespace(ctx, &BindNamespaceRequest{Namespace: "prod"})
	targetHandler.CreateTarget(ctx, &CreateTargetRequest{Name: "router"})
	pollerHandler.CreatePoller(ctx, &CreatePollerRequest{
		Target:         "router",
		Name:           "cpu",
		Protocol:       "snmp",
		ProtocolConfig: []byte(`{}`),
	})

	// Browse root
	rootResp, err := browseHandler.Browse(ctx, &BrowseRequest{Path: "/"})
	if err != nil {
		t.Fatalf("Browse /: %v", err)
	}
	if len(rootResp.Entries) != 2 {
		t.Errorf("Root entries = %d, want 2", len(rootResp.Entries))
	}

	// Browse targets
	targetsResp, err := browseHandler.Browse(ctx, &BrowseRequest{Path: "/targets"})
	if err != nil {
		t.Fatalf("Browse /targets: %v", err)
	}
	if len(targetsResp.Entries) != 1 {
		t.Errorf("Targets entries = %d, want 1", len(targetsResp.Entries))
	}
	if targetsResp.Entries[0].Name != "router" {
		t.Errorf("Target name = %s, want router", targetsResp.Entries[0].Name)
	}

	// Browse target
	targetResp, err := browseHandler.Browse(ctx, &BrowseRequest{Path: "/targets/router"})
	if err != nil {
		t.Fatalf("Browse /targets/router: %v", err)
	}
	if len(targetResp.Entries) != 1 {
		t.Errorf("Poller entries = %d, want 1", len(targetResp.Entries))
	}
	if targetResp.Entries[0].Name != "cpu" {
		t.Errorf("Poller name = %s, want cpu", targetResp.Entries[0].Name)
	}

	// Create tree directory and link
	_, err = browseHandler.CreateDirectory(ctx, &CreateDirectoryRequest{
		Path:        "/dc1/network",
		Description: "Network devices",
	})
	if err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}

	_, err = browseHandler.CreateLink(ctx, &CreateLinkRequest{
		Path:     "/dc1/network",
		LinkName: "core-router",
		LinkType: "target",
		LinkRef:  "router",
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	// Browse tree
	treeResp, err := browseHandler.Browse(ctx, &BrowseRequest{Path: "/tree/dc1/network"})
	if err != nil {
		t.Fatalf("Browse /tree/dc1/network: %v", err)
	}
	if len(treeResp.Entries) != 1 {
		t.Errorf("Tree entries = %d, want 1", len(treeResp.Entries))
	}

	// Resolve path
	resolveResp, err := browseHandler.ResolvePath(ctx, &ResolvePathRequest{Path: "/tree/dc1/network/core-router"})
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	if resolveResp.Type != "target" {
		t.Errorf("Type = %s, want target", resolveResp.Type)
	}
	if resolveResp.Target != "router" {
		t.Errorf("Target = %s, want router", resolveResp.Target)
	}
}

func TestSubscriptionHandler(t *testing.T) {
	h, sm, cleanup := setupTestHandler(t)
	defer cleanup()

	// Setup
	session := sm.CreateSession("admin", nil, nil)
	ctx := h.NewContext(session, 1)
	nsHandler := NewNamespaceHandler(h)
	targetHandler := NewTargetHandler(h)
	pollerHandler := NewPollerHandler(h)
	subHandler := NewSubscriptionHandler(h)

	// Create namespace, target, pollers
	nsHandler.CreateNamespace(ctx, &CreateNamespaceRequest{Name: "prod"})
	nsHandler.BindNamespace(ctx, &BindNamespaceRequest{Namespace: "prod"})
	targetHandler.CreateTarget(ctx, &CreateTargetRequest{Name: "router"})
	pollerHandler.CreatePoller(ctx, &CreatePollerRequest{
		Target: "router", Name: "cpu", Protocol: "snmp", ProtocolConfig: []byte(`{}`),
	})
	pollerHandler.CreatePoller(ctx, &CreatePollerRequest{
		Target: "router", Name: "memory", Protocol: "snmp", ProtocolConfig: []byte(`{}`),
	})

	// Subscribe to specific poller
	subResp, err := subHandler.Subscribe(ctx, &SubscribeRequest{
		Subscriptions: []SubscriptionSpec{
			{Target: "router", Poller: "cpu"},
		},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if len(subResp.Subscribed) != 1 {
		t.Errorf("Subscribed = %d, want 1", len(subResp.Subscribed))
	}

	// Subscribe to all pollers for target
	subResp2, err := subHandler.Subscribe(ctx, &SubscribeRequest{
		Subscriptions: []SubscriptionSpec{
			{Target: "router"}, // No poller = all
		},
	})
	if err != nil {
		t.Fatalf("Subscribe all: %v", err)
	}
	if len(subResp2.Subscribed) != 2 {
		t.Errorf("Subscribed = %d, want 2", len(subResp2.Subscribed))
	}

	// List subscriptions
	listResp, err := subHandler.ListSubscriptions(ctx, &ListSubscriptionsRequest{})
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if listResp.Count != 2 {
		t.Errorf("Count = %d, want 2", listResp.Count)
	}

	// Unsubscribe specific
	unsubResp, err := subHandler.Unsubscribe(ctx, &UnsubscribeRequest{
		Keys: []string{"router/cpu"},
	})
	if err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if len(unsubResp.Unsubscribed) != 1 {
		t.Errorf("Unsubscribed = %d, want 1", len(unsubResp.Unsubscribed))
	}

	// Unsubscribe all
	unsubResp2, err := subHandler.Unsubscribe(ctx, &UnsubscribeRequest{})
	if err != nil {
		t.Fatalf("Unsubscribe all: %v", err)
	}
	if len(unsubResp2.Unsubscribed) != 1 {
		t.Errorf("Unsubscribed = %d, want 1", len(unsubResp2.Unsubscribed))
	}
}

func TestSessionManager(t *testing.T) {
	sm := NewSessionManager(&SessionManagerConfig{
		ReconnectWindow: 10 * time.Minute,
		AuthTimeout:     30 * time.Second,
		Tokens: []TokenConfig{
			{ID: "admin", Token: "admin-secret", Namespaces: nil},
			{ID: "user", Token: "user-secret", Namespaces: []string{"prod", "dev"}},
		},
	})

	// Validate tokens
	cfg, ok := sm.ValidateToken("admin-secret")
	if !ok {
		t.Error("Admin token should be valid")
	}
	if cfg.ID != "admin" {
		t.Errorf("Token ID = %s, want admin", cfg.ID)
	}

	_, ok = sm.ValidateToken("wrong")
	if ok {
		t.Error("Wrong token should be invalid")
	}

	// Namespace access
	if !sm.CanAccessNamespace("admin", "anything") {
		t.Error("Admin should access any namespace")
	}
	if !sm.CanAccessNamespace("user", "prod") {
		t.Error("User should access prod")
	}
	if sm.CanAccessNamespace("user", "staging") {
		t.Error("User should not access staging")
	}

	// Create session
	session := sm.CreateSession("admin", nil, nil)
	if session == nil {
		t.Fatal("Session should be created")
	}

	// Session count
	if sm.Count() != 1 {
		t.Errorf("Count = %d, want 1", sm.Count())
	}
	if sm.CountActive() != 1 {
		t.Errorf("CountActive = %d, want 1", sm.CountActive())
	}

	// Mark lost
	session.MarkLost()
	if sm.CountActive() != 0 {
		t.Errorf("CountActive after lost = %d, want 0", sm.CountActive())
	}
	if sm.CountLost() != 1 {
		t.Errorf("CountLost = %d, want 1", sm.CountLost())
	}
}

func TestNamespaceAccess(t *testing.T) {
	h, sm, cleanup := setupTestHandler(t)
	defer cleanup()

	// Create limited session
	session := sm.CreateSession("limited", nil, nil)
	ctx := h.NewContext(session, 1)

	nsHandler := NewNamespaceHandler(h)

	// Create prod namespace (allowed)
	_, err := nsHandler.CreateNamespace(ctx, &CreateNamespaceRequest{Name: "prod"})
	if err != nil {
		t.Fatalf("CreateNamespace prod: %v", err)
	}

	// Bind to prod (allowed)
	_, err = nsHandler.BindNamespace(ctx, &BindNamespaceRequest{Namespace: "prod"})
	if err != nil {
		t.Fatalf("BindNamespace prod: %v", err)
	}

	// Create staging namespace
	h.mgr.Namespaces.Create(&store.Namespace{Name: "staging"})

	// Try to bind to staging (not allowed)
	_, err = nsHandler.BindNamespace(ctx, &BindNamespaceRequest{Namespace: "staging"})
	if err == nil {
		t.Error("Should not be able to bind to staging")
	}

	// List should only show accessible namespaces
	listResp, err := nsHandler.ListNamespaces(ctx, &ListNamespacesRequest{})
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	if len(listResp.Namespaces) != 1 {
		t.Errorf("Should only see 1 namespace, got %d", len(listResp.Namespaces))
	}
}
