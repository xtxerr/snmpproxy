package tests

import (
	"os"
	"testing"
	"time"

	"github.com/xtxerr/snmpproxy/internal/handler"
	"github.com/xtxerr/snmpproxy/internal/loader"
	"github.com/xtxerr/snmpproxy/internal/manager"
	"github.com/xtxerr/snmpproxy/internal/scheduler"
	"github.com/xtxerr/snmpproxy/internal/store"
)

// TestFullWorkflow tests the complete workflow from config to polling.
func TestFullWorkflow(t *testing.T) {
	// Create manager with in-memory storage
	mgr, err := manager.New(&manager.Config{
		InMemory: true,
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("Create manager: %v", err)
	}
	defer mgr.Stop()

	// Create session manager
	sm := handler.NewSessionManager(&handler.SessionManagerConfig{
		ReconnectWindow: 10 * time.Minute,
		AuthTimeout:     30 * time.Second,
		Tokens: []handler.TokenConfig{
			{ID: "admin", Token: "secret"},
		},
	})

	// Create handler
	h := handler.NewHandler(mgr, sm)

	// Create session
	session := sm.CreateSession("admin", nil, nil)
	ctx := h.NewContext(session, 1)

	// Create namespace handler
	nsHandler := handler.NewNamespaceHandler(h)

	// Step 1: Create namespace
	nsResp, err := nsHandler.CreateNamespace(ctx, &handler.CreateNamespaceRequest{
		Name:        "prod",
		Description: "Production",
	})
	if err != nil {
		t.Fatalf("Create namespace: %v", err)
	}
	if nsResp.Namespace.Name != "prod" {
		t.Errorf("Namespace name = %s, want prod", nsResp.Namespace.Name)
	}

	// Step 2: Bind to namespace
	_, err = nsHandler.BindNamespace(ctx, &handler.BindNamespaceRequest{Namespace: "prod"})
	if err != nil {
		t.Fatalf("Bind namespace: %v", err)
	}

	// Step 3: Create target
	targetHandler := handler.NewTargetHandler(h)
	targetResp, err := targetHandler.CreateTarget(ctx, &handler.CreateTargetRequest{
		Name:        "router",
		Description: "Core router",
		Labels:      map[string]string{"site": "dc1"},
	})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	if targetResp.Target.Name != "router" {
		t.Errorf("Target name = %s, want router", targetResp.Target.Name)
	}

	// Step 4: Create poller
	pollerHandler := handler.NewPollerHandler(h)
	pollerResp, err := pollerHandler.CreatePoller(ctx, &handler.CreatePollerRequest{
		Target:      "router",
		Name:        "cpu",
		Description: "CPU usage",
		Protocol:    "snmp",
		ProtocolConfig: []byte(`{
			"host": "127.0.0.1",
			"oid": "1.3.6.1.2.1.1.3.0"
		}`),
	})
	if err != nil {
		t.Fatalf("Create poller: %v", err)
	}
	if pollerResp.Poller.AdminState != "disabled" {
		t.Errorf("AdminState = %s, want disabled", pollerResp.Poller.AdminState)
	}

	// Step 5: Enable poller
	enableResp, err := pollerHandler.EnablePoller(ctx, &handler.EnablePollerRequest{
		Target: "router",
		Name:   "cpu",
	})
	if err != nil {
		t.Fatalf("Enable poller: %v", err)
	}
	if enableResp.AdminState != "enabled" {
		t.Errorf("AdminState after enable = %s, want enabled", enableResp.AdminState)
	}

	// Step 6: Create tree structure
	browseHandler := handler.NewBrowseHandler(h)
	_, err = browseHandler.CreateDirectory(ctx, &handler.CreateDirectoryRequest{
		Path:        "/dc1/network",
		Description: "Network devices",
	})
	if err != nil {
		t.Fatalf("Create directory: %v", err)
	}

	_, err = browseHandler.CreateLink(ctx, &handler.CreateLinkRequest{
		Path:     "/dc1/network",
		LinkName: "core-router",
		LinkType: "target",
		LinkRef:  "router",
	})
	if err != nil {
		t.Fatalf("Create link: %v", err)
	}

	// Step 7: Browse and verify
	browseResp, err := browseHandler.Browse(ctx, &handler.BrowseRequest{Path: "/tree/dc1/network"})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(browseResp.Entries) != 1 {
		t.Errorf("Entries = %d, want 1", len(browseResp.Entries))
	}
	if browseResp.Entries[0].Name != "core-router" {
		t.Errorf("Entry name = %s, want core-router", browseResp.Entries[0].Name)
	}

	// Step 8: Subscribe
	subHandler := handler.NewSubscriptionHandler(h)
	subResp, err := subHandler.Subscribe(ctx, &handler.SubscribeRequest{
		Subscriptions: []handler.SubscriptionSpec{
			{Target: "router", Poller: "cpu"},
		},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if len(subResp.Subscribed) != 1 {
		t.Errorf("Subscribed = %d, want 1", len(subResp.Subscribed))
	}

	// Step 9: Get status
	statusHandler := handler.NewStatusHandler(h)
	statusResp, err := statusHandler.GetNamespaceStatus(ctx, &handler.GetNamespaceStatusRequest{})
	if err != nil {
		t.Fatalf("Get status: %v", err)
	}
	if statusResp.TargetCount != 1 {
		t.Errorf("TargetCount = %d, want 1", statusResp.TargetCount)
	}
	if statusResp.PollerCount != 1 {
		t.Errorf("PollerCount = %d, want 1", statusResp.PollerCount)
	}
}

// TestSchedulerIntegration tests scheduler with manager.
func TestSchedulerIntegration(t *testing.T) {
	// Create manager
	mgr, err := manager.New(&manager.Config{
		InMemory: true,
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("Create manager: %v", err)
	}
	defer mgr.Stop()

	// Create scheduler
	sched := scheduler.New(&scheduler.Config{
		Workers:   2,
		QueueSize: 100,
	})

	// Track poll results
	results := make(chan scheduler.PollResult, 100)

	sched.SetPollFunc(func(key scheduler.PollerKey) scheduler.PollResult {
		// Simulate poll
		return scheduler.PollResult{
			Key:         key,
			TimestampMs: time.Now().UnixMilli(),
			Success:     true,
			PollMs:      10,
		}
	})

	sched.Start()
	defer sched.Stop()

	// Create namespace and target in store
	mgr.Namespaces.Create(&store.Namespace{Name: "test"})
	mgr.Targets.Create(&store.Target{Namespace: "test", Name: "device"})
	mgr.Pollers.Create(&store.Poller{
		Namespace:  "test",
		Target:     "device",
		Name:       "metric",
		Protocol:   "snmp",
		AdminState: "enabled",
	})

	// Add to scheduler
	key := scheduler.PollerKey{Namespace: "test", Target: "device", Poller: "metric"}
	sched.Add(key, 100) // 100ms interval

	// Wait for some polls
	time.Sleep(350 * time.Millisecond)

	// Check stats
	heapSize, indexSize, _ := sched.Stats()
	if heapSize != 1 {
		t.Errorf("Heap size = %d, want 1", heapSize)
	}
	if indexSize != 1 {
		t.Errorf("Index size = %d, want 1", indexSize)
	}

	// Verify poller is scheduled
	if !sched.Contains(key) {
		t.Error("Scheduler should contain the poller")
	}

	// Pause poller
	sched.Pause(key)
	heapSize, _, _ = sched.Stats()
	if heapSize != 0 {
		t.Errorf("After pause, heap size = %d, want 0", heapSize)
	}

	// Resume
	sched.Resume(key)
	heapSize, _, _ = sched.Stats()
	if heapSize != 1 {
		t.Errorf("After resume, heap size = %d, want 1", heapSize)
	}

	close(results)
}

// TestConfigLoader tests the config loader.
func TestConfigLoader(t *testing.T) {
	// Create test config
	configYAML := `
listen: "0.0.0.0:9161"

snmp:
  timeout_ms: 5000
  retries: 2
  interval_ms: 1000
  buffer_size: 3600

namespaces:
  test:
    description: "Test namespace"
    targets:
      device1:
        description: "Test device"
        labels:
          env: test
        pollers:
          uptime:
            description: "System uptime"
            protocol: snmp
            config:
              host: "127.0.0.1"
              oid: "1.3.6.1.2.1.1.3.0"
            admin_state: enabled
`

	// Write temp file
	tmpFile := t.TempDir() + "/config.yaml"
	if err := writeFile(tmpFile, configYAML); err != nil {
		t.Fatalf("Write config: %v", err)
	}

	// Load config
	cfg, err := loader.Load(tmpFile)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	// Verify
	if cfg.Listen != "0.0.0.0:9161" {
		t.Errorf("Listen = %s, want 0.0.0.0:9161", cfg.Listen)
	}
	if cfg.SNMP.TimeoutMs != 5000 {
		t.Errorf("TimeoutMs = %d, want 5000", cfg.SNMP.TimeoutMs)
	}
	if len(cfg.Namespaces) != 1 {
		t.Errorf("Namespaces count = %d, want 1", len(cfg.Namespaces))
	}

	ns := cfg.Namespaces["test"]
	if ns == nil {
		t.Fatal("Namespace 'test' not found")
	}
	if ns.Description != "Test namespace" {
		t.Errorf("Description = %s", ns.Description)
	}
	if len(ns.Targets) != 1 {
		t.Errorf("Targets count = %d, want 1", len(ns.Targets))
	}

	target := ns.Targets["device1"]
	if target == nil {
		t.Fatal("Target 'device1' not found")
	}
	if len(target.Pollers) != 1 {
		t.Errorf("Pollers count = %d, want 1", len(target.Pollers))
	}

	// Apply to manager
	mgr, err := manager.New(&manager.Config{
		InMemory: true,
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("Create manager: %v", err)
	}
	defer mgr.Stop()

	result, err := loader.Apply(cfg, mgr)
	if err != nil {
		t.Fatalf("Apply config: %v", err)
	}

	if result.NamespacesCreated != 1 {
		t.Errorf("NamespacesCreated = %d, want 1", result.NamespacesCreated)
	}
	if result.TargetsCreated != 1 {
		t.Errorf("TargetsCreated = %d, want 1", result.TargetsCreated)
	}
	if result.PollersCreated != 1 {
		t.Errorf("PollersCreated = %d, want 1", result.PollersCreated)
	}

	// Verify in store
	nsList := mgr.Namespaces.List()
	if len(nsList) != 1 {
		t.Errorf("Store namespace count = %d, want 1", len(nsList))
	}
}

// TestConfigInheritance tests config value inheritance.
func TestConfigInheritance(t *testing.T) {
	mgr, err := manager.New(&manager.Config{
		InMemory: true,
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("Create manager: %v", err)
	}
	defer mgr.Stop()

	// Set server defaults
	mgr.Store().UpdateServerConfig(&store.ServerConfig{
		DefaultTimeoutMs:  5000,
		DefaultRetries:    2,
		DefaultIntervalMs: 1000,
		DefaultBufferSize: 3600,
	})

	// Create namespace with overrides
	mgr.Namespaces.Create(&store.Namespace{
		Name:             "prod",
		DefaultTimeoutMs: 3000, // Override
		// Retries not set - should inherit from server
	})

	// Create target with overrides
	mgr.Targets.Create(&store.Target{
		Namespace:        "prod",
		Name:             "router",
		DefaultIntervalMs: 2000, // Override
	})

	// Create poller with explicit value
	mgr.Pollers.Create(&store.Poller{
		Namespace:  "prod",
		Target:     "router",
		Name:       "cpu",
		Protocol:   "snmp",
		TimeoutMs:  1000, // Override
		// IntervalMs not set - should inherit from target
	})

	// Resolve config
	resolved, err := mgr.ConfigResolver.Resolve("prod", "router", "cpu")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Verify inheritance
	// TimeoutMs: poller (1000) overrides all
	if resolved.TimeoutMs != 1000 {
		t.Errorf("TimeoutMs = %d, want 1000 (from poller)", resolved.TimeoutMs)
	}

	// Retries: not set anywhere below server, should be 2
	if resolved.Retries != 2 {
		t.Errorf("Retries = %d, want 2 (from server)", resolved.Retries)
	}

	// IntervalMs: target (2000) overrides namespace and server
	if resolved.IntervalMs != 2000 {
		t.Errorf("IntervalMs = %d, want 2000 (from target)", resolved.IntervalMs)
	}

	// BufferSize: only set at server level
	if resolved.BufferSize != 3600 {
		t.Errorf("BufferSize = %d, want 3600 (from server)", resolved.BufferSize)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
