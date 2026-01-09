package scheduler

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerBasic(t *testing.T) {
	sched := New(&Config{
		Workers:   2,
		QueueSize: 100,
	})

	var pollCount atomic.Int32

	sched.SetPollFunc(func(key PollerKey) PollResult {
		pollCount.Add(1)
		return PollResult{
			Key:         key,
			TimestampMs: time.Now().UnixMilli(),
			Success:     true,
		}
	})

	sched.Start()
	defer sched.Stop()

	// Add poller with 50ms interval
	key := PollerKey{Namespace: "ns", Target: "target", Poller: "poller"}
	sched.Add(key, 50)

	// Wait for a few polls
	time.Sleep(200 * time.Millisecond)

	count := pollCount.Load()
	if count < 2 {
		t.Errorf("Expected at least 2 polls, got %d", count)
	}

	// Check stats
	heapSize, indexSize, _ := sched.Stats()
	if heapSize != 1 || indexSize != 1 {
		t.Errorf("Stats: heap=%d, index=%d, want 1,1", heapSize, indexSize)
	}

	// Remove
	sched.Remove(key)
	heapSize, indexSize, _ = sched.Stats()
	if heapSize != 0 || indexSize != 0 {
		t.Errorf("After remove: heap=%d, index=%d, want 0,0", heapSize, indexSize)
	}
}

func TestSchedulerPauseResume(t *testing.T) {
	sched := New(&Config{
		Workers:   2,
		QueueSize: 100,
	})

	var pollCount atomic.Int32

	sched.SetPollFunc(func(key PollerKey) PollResult {
		pollCount.Add(1)
		return PollResult{
			Key:         key,
			TimestampMs: time.Now().UnixMilli(),
			Success:     true,
		}
	})

	sched.Start()
	defer sched.Stop()

	key := PollerKey{Namespace: "ns", Target: "target", Poller: "poller"}
	sched.Add(key, 50)

	// Let it poll once
	time.Sleep(100 * time.Millisecond)
	countBefore := pollCount.Load()

	// Pause
	sched.Pause(key)
	time.Sleep(150 * time.Millisecond)
	countDuringPause := pollCount.Load()

	// Should not have polled during pause
	if countDuringPause > countBefore+1 {
		t.Errorf("Polled during pause: before=%d, during=%d", countBefore, countDuringPause)
	}

	// Resume
	sched.Resume(key)
	time.Sleep(150 * time.Millisecond)
	countAfterResume := pollCount.Load()

	if countAfterResume <= countDuringPause {
		t.Errorf("Did not poll after resume: during=%d, after=%d", countDuringPause, countAfterResume)
	}
}

func TestSchedulerUpdateInterval(t *testing.T) {
	sched := New(&Config{
		Workers:   2,
		QueueSize: 100,
	})

	var pollCount atomic.Int32

	sched.SetPollFunc(func(key PollerKey) PollResult {
		pollCount.Add(1)
		return PollResult{
			Key:         key,
			TimestampMs: time.Now().UnixMilli(),
			Success:     true,
		}
	})

	sched.Start()
	defer sched.Stop()

	key := PollerKey{Namespace: "ns", Target: "target", Poller: "poller"}

	// Start with 500ms interval
	sched.Add(key, 500)
	time.Sleep(100 * time.Millisecond)

	// Update to 50ms interval
	sched.UpdateInterval(key, 50)

	// Wait and check
	time.Sleep(200 * time.Millisecond)
	count := pollCount.Load()

	// Should have polled more than once with faster interval
	if count < 2 {
		t.Errorf("Expected more polls after interval update, got %d", count)
	}
}

func TestSchedulerMultiplePollers(t *testing.T) {
	sched := New(&Config{
		Workers:   4,
		QueueSize: 100,
	})

	pollCounts := make(map[string]*atomic.Int32)

	sched.SetPollFunc(func(key PollerKey) PollResult {
		keyStr := key.String()
		if _, ok := pollCounts[keyStr]; !ok {
			pollCounts[keyStr] = &atomic.Int32{}
		}
		pollCounts[keyStr].Add(1)
		return PollResult{
			Key:         key,
			TimestampMs: time.Now().UnixMilli(),
			Success:     true,
		}
	})

	sched.Start()
	defer sched.Stop()

	// Add multiple pollers
	keys := []PollerKey{
		{Namespace: "ns1", Target: "t1", Poller: "p1"},
		{Namespace: "ns1", Target: "t1", Poller: "p2"},
		{Namespace: "ns1", Target: "t2", Poller: "p1"},
		{Namespace: "ns2", Target: "t1", Poller: "p1"},
	}

	for i, k := range keys {
		pollCounts[k.String()] = &atomic.Int32{}
		sched.Add(k, uint32(50+i*10)) // Slightly different intervals
	}

	time.Sleep(300 * time.Millisecond)

	// All should have been polled
	for _, k := range keys {
		count := pollCounts[k.String()].Load()
		if count < 1 {
			t.Errorf("Poller %s not polled, count=%d", k.String(), count)
		}
	}

	// Check scheduled pollers
	scheduled := sched.GetScheduledPollers()
	if len(scheduled) != 4 {
		t.Errorf("Expected 4 scheduled pollers, got %d", len(scheduled))
	}
}

func TestPollerKey(t *testing.T) {
	key := PollerKey{Namespace: "prod", Target: "router", Poller: "cpu"}

	s := key.String()
	if s != "prod/router/cpu" {
		t.Errorf("String() = %s, want prod/router/cpu", s)
	}
}

func TestSNMPConfigParse(t *testing.T) {
	json := []byte(`{
		"host": "192.168.1.1",
		"port": 161,
		"oid": "1.3.6.1.2.1.1.3.0",
		"community": "public"
	}`)

	cfg, err := ParseSNMPConfig(json)
	if err != nil {
		t.Fatalf("ParseSNMPConfig: %v", err)
	}

	if cfg.Host != "192.168.1.1" {
		t.Errorf("Host = %s, want 192.168.1.1", cfg.Host)
	}
	if cfg.Port != 161 {
		t.Errorf("Port = %d, want 161", cfg.Port)
	}
	if cfg.OID != "1.3.6.1.2.1.1.3.0" {
		t.Errorf("OID = %s, want 1.3.6.1.2.1.1.3.0", cfg.OID)
	}
	if cfg.Community != "public" {
		t.Errorf("Community = %s, want public", cfg.Community)
	}
}

func TestSNMPConfigDefaults(t *testing.T) {
	json := []byte(`{"host": "192.168.1.1", "oid": "1.3.6.1.2.1.1.3.0"}`)

	cfg, err := ParseSNMPConfig(json)
	if err != nil {
		t.Fatalf("ParseSNMPConfig: %v", err)
	}

	// Check defaults
	if cfg.Port != 161 {
		t.Errorf("Port default = %d, want 161", cfg.Port)
	}
	if cfg.TimeoutMs != 5000 {
		t.Errorf("TimeoutMs default = %d, want 5000", cfg.TimeoutMs)
	}
	if cfg.Retries != 2 {
		t.Errorf("Retries default = %d, want 2", cfg.Retries)
	}
	if cfg.Version != 2 {
		t.Errorf("Version default = %d, want 2", cfg.Version)
	}
	if cfg.Community != "public" {
		t.Errorf("Community default = %s, want public", cfg.Community)
	}
}
