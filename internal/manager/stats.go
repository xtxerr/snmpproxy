package manager

import (
	"sync"
	"sync/atomic"
)

// PollerStats holds runtime statistics for a poller.
type PollerStats struct {
	mu sync.RWMutex

	Namespace string
	Target    string
	Poller    string

	// Counters (atomic for lock-free updates)
	PollsTotal   atomic.Int64
	PollsSuccess atomic.Int64
	PollsFailed  atomic.Int64
	PollsTimeout atomic.Int64

	// Timing (protected by mutex)
	pollMsSum   int64
	pollMsMin   int
	pollMsMax   int
	pollMsCount int

	// Dirty flag
	dirty atomic.Bool
}

// NewPollerStats creates a new stats tracker.
func NewPollerStats(namespace, target, poller string) *PollerStats {
	return &PollerStats{
		Namespace: namespace,
		Target:    target,
		Poller:    poller,
		pollMsMin: -1, // -1 = not set
	}
}

// Key returns the unique key for this poller.
func (s *PollerStats) Key() string {
	return s.Namespace + "/" + s.Target + "/" + s.Poller
}

// RecordPoll records a poll result.
func (s *PollerStats) RecordPoll(success bool, timeout bool, pollMs int) {
	s.PollsTotal.Add(1)

	if success {
		s.PollsSuccess.Add(1)
	} else {
		s.PollsFailed.Add(1)
		if timeout {
			s.PollsTimeout.Add(1)
		}
	}

	s.mu.Lock()
	s.pollMsSum += int64(pollMs)
	s.pollMsCount++
	if s.pollMsMin < 0 || pollMs < s.pollMsMin {
		s.pollMsMin = pollMs
	}
	if pollMs > s.pollMsMax {
		s.pollMsMax = pollMs
	}
	s.mu.Unlock()

	s.dirty.Store(true)
}

// GetStats returns current statistics.
func (s *PollerStats) GetStats() (total, success, failed, timeout int64, avgMs, minMs, maxMs int) {
	total = s.PollsTotal.Load()
	success = s.PollsSuccess.Load()
	failed = s.PollsFailed.Load()
	timeout = s.PollsTimeout.Load()

	s.mu.RLock()
	if s.pollMsCount > 0 {
		avgMs = int(s.pollMsSum / int64(s.pollMsCount))
	}
	minMs = s.pollMsMin
	if minMs < 0 {
		minMs = 0
	}
	maxMs = s.pollMsMax
	s.mu.RUnlock()

	return
}

// IsDirty returns true if stats have changed.
func (s *PollerStats) IsDirty() bool {
	return s.dirty.Load()
}

// ClearDirty clears the dirty flag.
func (s *PollerStats) ClearDirty() {
	s.dirty.Store(false)
}

// Reset resets all statistics.
func (s *PollerStats) Reset() {
	s.PollsTotal.Store(0)
	s.PollsSuccess.Store(0)
	s.PollsFailed.Store(0)
	s.PollsTimeout.Store(0)

	s.mu.Lock()
	s.pollMsSum = 0
	s.pollMsMin = -1
	s.pollMsMax = 0
	s.pollMsCount = 0
	s.mu.Unlock()

	s.dirty.Store(true)
}

// ============================================================================
// Stats Manager
// ============================================================================

// StatsManager manages poller statistics.
type StatsManager struct {
	mu    sync.RWMutex
	stats map[string]*PollerStats // key: namespace/target/poller
}

// NewStatsManager creates a new stats manager.
func NewStatsManager() *StatsManager {
	return &StatsManager{
		stats: make(map[string]*PollerStats),
	}
}

// Get returns stats for a poller.
func (m *StatsManager) Get(namespace, target, poller string) *PollerStats {
	key := namespace + "/" + target + "/" + poller

	m.mu.RLock()
	stats, ok := m.stats[key]
	m.mu.RUnlock()

	if ok {
		return stats
	}

	// Create new stats
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check
	if stats, ok = m.stats[key]; ok {
		return stats
	}

	stats = NewPollerStats(namespace, target, poller)
	m.stats[key] = stats
	return stats
}

// Remove removes stats for a poller.
func (m *StatsManager) Remove(namespace, target, poller string) {
	key := namespace + "/" + target + "/" + poller

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.stats, key)
}

// GetDirty returns all dirty stats.
func (m *StatsManager) GetDirty() []*PollerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dirty []*PollerStats
	for _, stats := range m.stats {
		if stats.IsDirty() {
			dirty = append(dirty, stats)
		}
	}
	return dirty
}

// GetAll returns all stats.
func (m *StatsManager) GetAll() []*PollerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*PollerStats, 0, len(m.stats))
	for _, stats := range m.stats {
		result = append(result, stats)
	}
	return result
}

// Aggregate returns aggregated stats for all pollers.
func (m *StatsManager) Aggregate() (total, success, failed, timeout int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, stats := range m.stats {
		total += stats.PollsTotal.Load()
		success += stats.PollsSuccess.Load()
		failed += stats.PollsFailed.Load()
		timeout += stats.PollsTimeout.Load()
	}
	return
}

// AggregateByNamespace returns stats aggregated by namespace.
func (m *StatsManager) AggregateByNamespace(namespace string) (total, success, failed int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, stats := range m.stats {
		if stats.Namespace == namespace {
			total += stats.PollsTotal.Load()
			success += stats.PollsSuccess.Load()
			failed += stats.PollsFailed.Load()
		}
	}
	return
}

// AggregateByTarget returns stats aggregated by target.
func (m *StatsManager) AggregateByTarget(namespace, target string) (total, success, failed int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, stats := range m.stats {
		if stats.Namespace == namespace && stats.Target == target {
			total += stats.PollsTotal.Load()
			success += stats.PollsSuccess.Load()
			failed += stats.PollsFailed.Load()
		}
	}
	return
}
