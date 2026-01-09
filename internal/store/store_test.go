package store

import (
	"testing"
)

func TestStoreInMemory(t *testing.T) {
	store, err := New(&Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Test namespace CRUD
	t.Run("Namespace", func(t *testing.T) {
		ns := &Namespace{
			Name:        "test-ns",
			Description: "Test namespace",
			Config: &NamespaceConfig{
				Defaults: &PollerDefaults{
					IntervalMs: ptr(uint32(5000)),
				},
			},
		}

		// Create
		if err := store.CreateNamespace(ns); err != nil {
			t.Fatalf("CreateNamespace: %v", err)
		}

		// Get
		got, err := store.GetNamespace("test-ns")
		if err != nil {
			t.Fatalf("GetNamespace: %v", err)
		}
		if got == nil {
			t.Fatal("Namespace not found")
		}
		if got.Description != "Test namespace" {
			t.Errorf("Description = %q, want %q", got.Description, "Test namespace")
		}
		if got.Config == nil || got.Config.Defaults == nil || got.Config.Defaults.IntervalMs == nil {
			t.Error("Config not preserved")
		} else if *got.Config.Defaults.IntervalMs != 5000 {
			t.Errorf("IntervalMs = %d, want 5000", *got.Config.Defaults.IntervalMs)
		}

		// List
		list, err := store.ListNamespaces()
		if err != nil {
			t.Fatalf("ListNamespaces: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("ListNamespaces len = %d, want 1", len(list))
		}

		// Update
		got.Description = "Updated"
		if err := store.UpdateNamespace(got); err != nil {
			t.Fatalf("UpdateNamespace: %v", err)
		}

		got2, _ := store.GetNamespace("test-ns")
		if got2.Description != "Updated" {
			t.Errorf("Update not persisted")
		}

		// Delete
		_, _, err = store.DeleteNamespace("test-ns", false)
		if err != nil {
			t.Fatalf("DeleteNamespace: %v", err)
		}

		got3, _ := store.GetNamespace("test-ns")
		if got3 != nil {
			t.Error("Namespace not deleted")
		}
	})

	// Test target CRUD
	t.Run("Target", func(t *testing.T) {
		// Create namespace first
		store.CreateNamespace(&Namespace{Name: "ns1"})

		target := &Target{
			Namespace:   "ns1",
			Name:        "target1",
			Description: "Test target",
			Labels:      map[string]string{"env": "test"},
		}

		// Create
		if err := store.CreateTarget(target); err != nil {
			t.Fatalf("CreateTarget: %v", err)
		}

		// Get
		got, err := store.GetTarget("ns1", "target1")
		if err != nil {
			t.Fatalf("GetTarget: %v", err)
		}
		if got == nil {
			t.Fatal("Target not found")
		}
		if got.Labels["env"] != "test" {
			t.Errorf("Labels not preserved")
		}

		// List
		list, err := store.ListTargets("ns1")
		if err != nil {
			t.Fatalf("ListTargets: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("ListTargets len = %d, want 1", len(list))
		}

		// Delete
		_, _, err = store.DeleteTarget("ns1", "target1", false)
		if err != nil {
			t.Fatalf("DeleteTarget: %v", err)
		}

		// Cleanup
		store.DeleteNamespace("ns1", true)
	})

	// Test poller CRUD
	t.Run("Poller", func(t *testing.T) {
		store.CreateNamespace(&Namespace{Name: "ns2"})
		store.CreateTarget(&Target{Namespace: "ns2", Name: "target2"})

		poller := &Poller{
			Namespace:      "ns2",
			Target:         "target2",
			Name:           "poller1",
			Description:    "Test poller",
			Protocol:       "snmp",
			ProtocolConfig: []byte(`{"host":"192.168.1.1","port":161,"oid":"1.3.6.1.2.1.1.3.0","community":"public"}`),
			AdminState:     "disabled",
		}

		// Create
		if err := store.CreatePoller(poller); err != nil {
			t.Fatalf("CreatePoller: %v", err)
		}

		// Get
		got, err := store.GetPoller("ns2", "target2", "poller1")
		if err != nil {
			t.Fatalf("GetPoller: %v", err)
		}
		if got == nil {
			t.Fatal("Poller not found")
		}
		if got.Protocol != "snmp" {
			t.Errorf("Protocol = %q, want snmp", got.Protocol)
		}

		// State should be initialized
		state, err := store.GetPollerState("ns2", "target2", "poller1")
		if err != nil {
			t.Fatalf("GetPollerState: %v", err)
		}
		if state == nil {
			t.Fatal("Poller state not initialized")
		}
		if state.OperState != "stopped" {
			t.Errorf("OperState = %q, want stopped", state.OperState)
		}

		// Stats should be initialized
		stats, err := store.GetPollerStats("ns2", "target2", "poller1")
		if err != nil {
			t.Fatalf("GetPollerStats: %v", err)
		}
		if stats == nil {
			t.Fatal("Poller stats not initialized")
		}

		// Update admin state
		if err := store.UpdatePollerAdminState("ns2", "target2", "poller1", "enabled"); err != nil {
			t.Fatalf("UpdatePollerAdminState: %v", err)
		}

		got2, _ := store.GetPoller("ns2", "target2", "poller1")
		if got2.AdminState != "enabled" {
			t.Errorf("AdminState not updated")
		}

		// Delete
		_, err = store.DeletePoller("ns2", "target2", "poller1")
		if err != nil {
			t.Fatalf("DeletePoller: %v", err)
		}

		// Cleanup
		store.DeleteNamespace("ns2", true)
	})

	// Test samples
	t.Run("Samples", func(t *testing.T) {
		store.CreateNamespace(&Namespace{Name: "ns3"})
		store.CreateTarget(&Target{Namespace: "ns3", Name: "target3"})
		store.CreatePoller(&Poller{
			Namespace:      "ns3",
			Target:         "target3",
			Name:           "poller3",
			Protocol:       "snmp",
			ProtocolConfig: []byte(`{}`),
			AdminState:     "disabled",
		})

		// Insert samples
		for i := 0; i < 10; i++ {
			counter := uint64(i * 100)
			store.InsertSample(&Sample{
				Namespace:    "ns3",
				Target:       "target3",
				Poller:       "poller3",
				TimestampMs:  int64(1000 + i*100),
				ValueCounter: &counter,
				Valid:        true,
				PollMs:       5,
			})
		}

		// Get samples
		samples, err := store.GetSamples("ns3", "target3", "poller3", 5, 0, 0)
		if err != nil {
			t.Fatalf("GetSamples: %v", err)
		}
		if len(samples) != 5 {
			t.Errorf("GetSamples len = %d, want 5", len(samples))
		}

		// Count
		count, _ := store.CountSamples("ns3", "target3", "poller3")
		if count != 10 {
			t.Errorf("CountSamples = %d, want 10", count)
		}

		// Trim
		deleted, _ := store.TrimSamples("ns3", "target3", "poller3", 5)
		if deleted != 5 {
			t.Errorf("TrimSamples deleted = %d, want 5", deleted)
		}

		count2, _ := store.CountSamples("ns3", "target3", "poller3")
		if count2 != 5 {
			t.Errorf("After trim count = %d, want 5", count2)
		}

		// Cleanup
		store.DeleteNamespace("ns3", true)
	})

	// Test tree
	t.Run("Tree", func(t *testing.T) {
		store.CreateNamespace(&Namespace{Name: "ns4"})
		store.CreateTarget(&Target{Namespace: "ns4", Name: "router"})
		store.CreatePoller(&Poller{
			Namespace:      "ns4",
			Target:         "router",
			Name:           "cpu",
			Protocol:       "snmp",
			ProtocolConfig: []byte(`{}`),
			AdminState:     "disabled",
		})

		// Create directory
		if err := store.CreateTreeDirectory("ns4", "/dc1/network", "Network devices"); err != nil {
			t.Fatalf("CreateTreeDirectory: %v", err)
		}

		// Check parent was created
		node, err := store.GetTreeNode("ns4", "/dc1")
		if err != nil {
			t.Fatalf("GetTreeNode: %v", err)
		}
		if node == nil {
			t.Error("Parent directory not created")
		}

		// Create link to target
		if err := store.CreateTreeLink("ns4", "/dc1/network", "router", "target", "router"); err != nil {
			t.Fatalf("CreateTreeLink target: %v", err)
		}

		// Create link to poller
		if err := store.CreateTreeLink("ns4", "/dc1/network", "router-cpu", "poller", "router/cpu"); err != nil {
			t.Fatalf("CreateTreeLink poller: %v", err)
		}

		// List children
		children, err := store.ListTreeChildren("ns4", "/dc1/network")
		if err != nil {
			t.Fatalf("ListTreeChildren: %v", err)
		}
		if len(children) != 2 {
			t.Errorf("ListTreeChildren len = %d, want 2", len(children))
		}

		// Delete target should delete links
		linksDeleted, _ := store.DeleteLinksToTargetAndPollers("ns4", "router")
		if linksDeleted != 2 {
			t.Errorf("DeleteLinksToTargetAndPollers = %d, want 2", linksDeleted)
		}

		// Cleanup
		store.DeleteNamespace("ns4", true)
	})

	// Test optimistic locking
	t.Run("OptimisticLocking", func(t *testing.T) {
		store.CreateNamespace(&Namespace{Name: "ns5"})

		ns1, _ := store.GetNamespace("ns5")
		ns2, _ := store.GetNamespace("ns5")

		// First update succeeds
		ns1.Description = "Updated by 1"
		if err := store.UpdateNamespace(ns1); err != nil {
			t.Fatalf("First update failed: %v", err)
		}

		// Second update with stale version fails
		ns2.Description = "Updated by 2"
		err := store.UpdateNamespace(ns2)
		if err != ErrConcurrentModification {
			t.Errorf("Expected ErrConcurrentModification, got %v", err)
		}

		// Cleanup
		store.DeleteNamespace("ns5", true)
	})
}

func ptr[T any](v T) *T {
	return &v
}
