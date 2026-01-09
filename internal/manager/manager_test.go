package manager

import (
	"testing"

	"github.com/xtxerr/snmpproxy/internal/store"
)

func TestManagerBasic(t *testing.T) {
	cfg := &Config{
		InMemory: true,
		Version:  "test",
	}

	mgr, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer mgr.Stop()

	// Load (should be empty)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Create namespace
	ns := &store.Namespace{
		Name:        "test-ns",
		Description: "Test namespace",
	}
	if err := mgr.Namespaces.Create(ns); err != nil {
		t.Fatalf("Create namespace: %v", err)
	}

	// Create target
	target := &store.Target{
		Namespace:   "test-ns",
		Name:        "router",
		Description: "Test router",
	}
	if err := mgr.Targets.Create(target); err != nil {
		t.Fatalf("Create target: %v", err)
	}

	// Create poller
	poller := &store.Poller{
		Namespace:      "test-ns",
		Target:         "router",
		Name:           "cpu",
		Description:    "CPU usage",
		Protocol:       "snmp",
		ProtocolConfig: []byte(`{"host":"192.168.1.1","oid":"1.3.6.1.4.1.9.9.109.1.1.1.1.8.1"}`),
		AdminState:     AdminStateDisabled,
	}
	if err := mgr.Pollers.Create(poller); err != nil {
		t.Fatalf("Create poller: %v", err)
	}

	// Check counts
	if mgr.Namespaces.Count() != 1 {
		t.Errorf("Namespace count = %d, want 1", mgr.Namespaces.Count())
	}
	if mgr.Targets.Count() != 1 {
		t.Errorf("Target count = %d, want 1", mgr.Targets.Count())
	}
	if mgr.Pollers.Count() != 1 {
		t.Errorf("Poller count = %d, want 1", mgr.Pollers.Count())
	}

	// Test state management
	state := mgr.States.Get("test-ns", "router", "cpu")
	if state.AdminState != AdminStateDisabled {
		t.Errorf("AdminState = %s, want disabled", state.AdminState)
	}

	// Enable poller
	if err := mgr.Pollers.Enable("test-ns", "router", "cpu"); err != nil {
		t.Fatalf("Enable poller: %v", err)
	}

	if state.AdminState != AdminStateEnabled {
		t.Errorf("AdminState after enable = %s, want enabled", state.AdminState)
	}

	// Test server info
	info := mgr.GetServerInfo()
	if info.PollerCount != 1 {
		t.Errorf("ServerInfo.PollerCount = %d, want 1", info.PollerCount)
	}
}

func TestPollerStateMachine(t *testing.T) {
	state := NewPollerState("ns", "target", "poller")

	// Initial state
	admin, oper, health := state.GetState()
	if admin != AdminStateDisabled {
		t.Errorf("Initial AdminState = %s, want disabled", admin)
	}
	if oper != OperStateStopped {
		t.Errorf("Initial OperState = %s, want stopped", oper)
	}
	if health != HealthStateUnknown {
		t.Errorf("Initial HealthState = %s, want unknown", health)
	}

	// Cannot start while disabled
	if err := state.Start(); err == nil {
		t.Error("Expected error when starting disabled poller")
	}

	// Enable and start
	state.Enable()
	if err := state.Start(); err != nil {
		t.Errorf("Start failed: %v", err)
	}

	_, oper, _ = state.GetState()
	if oper != OperStateStarting {
		t.Errorf("OperState after start = %s, want starting", oper)
	}

	// Mark running
	state.MarkRunning()
	_, oper, _ = state.GetState()
	if oper != OperStateRunning {
		t.Errorf("OperState after running = %s, want running", oper)
	}

	// Record successful polls
	state.RecordPollResult(true, "", 50)
	state.RecordPollResult(true, "", 45)

	_, _, health = state.GetState()
	if health != HealthStateUp {
		t.Errorf("HealthState after success = %s, want up", health)
	}

	// Record failures
	for i := 0; i < HealthDownThreshold; i++ {
		state.RecordPollResult(false, "timeout", 5000)
	}

	_, _, health = state.GetState()
	if health != HealthStateDown {
		t.Errorf("HealthState after failures = %s, want down", health)
	}

	// Recovery
	state.RecordPollResult(true, "", 50)
	state.RecordPollResult(true, "", 50)

	_, _, health = state.GetState()
	if health != HealthStateUp {
		t.Errorf("HealthState after recovery = %s, want up", health)
	}

	// Stop
	state.Stop()
	_, oper, _ = state.GetState()
	if oper != OperStateStopping {
		t.Errorf("OperState after stop = %s, want stopping", oper)
	}

	state.MarkStopped()
	_, oper, _ = state.GetState()
	if oper != OperStateStopped {
		t.Errorf("OperState after stopped = %s, want stopped", oper)
	}
}

func TestConfigResolver(t *testing.T) {
	cfg := &Config{
		InMemory: true,
		Version:  "test",
	}

	mgr, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer mgr.Stop()

	// Set server defaults
	serverCfg := &store.ServerConfig{
		DefaultIntervalMs: 1000,
		DefaultTimeoutMs:  5000,
		DefaultRetries:    2,
		DefaultBufferSize: 3600,
	}
	mgr.Store().UpdateServerConfig(serverCfg)

	// Create namespace with defaults
	intervalMs := uint32(5000)
	ns := &store.Namespace{
		Name: "test-ns",
		Config: &store.NamespaceConfig{
			Defaults: &store.PollerDefaults{
				IntervalMs: &intervalMs,
			},
		},
	}
	mgr.Namespaces.Create(ns)

	// Create target with defaults
	timeoutMs := uint32(3000)
	target := &store.Target{
		Namespace: "test-ns",
		Name:      "router",
		Config: &store.TargetConfig{
			Defaults: &store.PollerDefaults{
				TimeoutMs: &timeoutMs,
			},
		},
	}
	mgr.Targets.Create(target)

	// Create poller with explicit config
	pollerInterval := uint32(2000)
	poller := &store.Poller{
		Namespace:      "test-ns",
		Target:         "router",
		Name:           "cpu",
		Protocol:       "snmp",
		ProtocolConfig: []byte(`{}`),
		PollingConfig: &store.PollingConfig{
			IntervalMs: &pollerInterval,
		},
	}
	mgr.Pollers.Create(poller)

	// Resolve config
	resolved, err := mgr.ConfigResolver.Resolve("test-ns", "router", "cpu")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	// Check inheritance
	if resolved.IntervalMs != 2000 {
		t.Errorf("IntervalMs = %d, want 2000 (explicit)", resolved.IntervalMs)
	}
	if resolved.IntervalMsSource != "explicit" {
		t.Errorf("IntervalMsSource = %s, want explicit", resolved.IntervalMsSource)
	}

	if resolved.TimeoutMs != 3000 {
		t.Errorf("TimeoutMs = %d, want 3000 (from target)", resolved.TimeoutMs)
	}
	if resolved.TimeoutMsSource != "target:router" {
		t.Errorf("TimeoutMsSource = %s, want target:router", resolved.TimeoutMsSource)
	}

	if resolved.Retries != 2 {
		t.Errorf("Retries = %d, want 2 (from server)", resolved.Retries)
	}
	if resolved.RetriesSource != "server" {
		t.Errorf("RetriesSource = %s, want server", resolved.RetriesSource)
	}
}

func TestTreeManager(t *testing.T) {
	cfg := &Config{
		InMemory: true,
		Version:  "test",
	}

	mgr, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer mgr.Stop()

	// Create namespace, target, poller
	mgr.Namespaces.Create(&store.Namespace{Name: "prod"})
	mgr.Targets.Create(&store.Target{Namespace: "prod", Name: "router"})
	mgr.Pollers.Create(&store.Poller{
		Namespace:      "prod",
		Target:         "router",
		Name:           "cpu",
		Protocol:       "snmp",
		ProtocolConfig: []byte(`{}`),
	})

	// Create tree structure
	if err := mgr.Tree.CreateDirectory("prod", "/dc1/network", "Network devices"); err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}

	// Create link to target
	if err := mgr.Tree.CreateLink("prod", "/dc1/network", "core-router", "target", "router"); err != nil {
		t.Fatalf("CreateLink target: %v", err)
	}

	// Create link to poller
	if err := mgr.Tree.CreateLink("prod", "/dc1/network", "router-cpu", "poller", "router/cpu"); err != nil {
		t.Fatalf("CreateLink poller: %v", err)
	}

	// List children
	children, err := mgr.Tree.ListChildren("prod", "/dc1/network")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("ListChildren = %d, want 2", len(children))
	}

	// Test ParseLinkRef
	linkType, targetName, pollerName, err := ParseLinkRef("poller:router/cpu")
	if err != nil {
		t.Fatalf("ParseLinkRef: %v", err)
	}
	if linkType != "poller" || targetName != "router" || pollerName != "cpu" {
		t.Errorf("ParseLinkRef = %s, %s, %s", linkType, targetName, pollerName)
	}
}

func TestStatsManager(t *testing.T) {
	mgr := NewStatsManager()

	stats := mgr.Get("ns", "target", "poller")

	// Record polls
	stats.RecordPoll(true, false, 50)
	stats.RecordPoll(true, false, 60)
	stats.RecordPoll(false, true, 5000)

	total, success, failed, timeout, avgMs, minMs, maxMs := stats.GetStats()

	if total != 3 {
		t.Errorf("Total = %d, want 3", total)
	}
	if success != 2 {
		t.Errorf("Success = %d, want 2", success)
	}
	if failed != 1 {
		t.Errorf("Failed = %d, want 1", failed)
	}
	if timeout != 1 {
		t.Errorf("Timeout = %d, want 1", timeout)
	}
	if avgMs < 1000 {
		t.Errorf("AvgMs = %d, expected > 1000", avgMs)
	}
	if minMs != 50 {
		t.Errorf("MinMs = %d, want 50", minMs)
	}
	if maxMs != 5000 {
		t.Errorf("MaxMs = %d, want 5000", maxMs)
	}

	// Check dirty
	if !stats.IsDirty() {
		t.Error("Stats should be dirty")
	}

	stats.ClearDirty()
	if stats.IsDirty() {
		t.Error("Stats should not be dirty after clear")
	}
}
