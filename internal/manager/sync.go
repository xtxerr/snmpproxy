package manager

import (
	"log"
	"sync"
	"time"

	"github.com/xtxerr/snmpproxy/internal/store"
)

// SyncManager handles batched persistence of state, stats, and samples.
type SyncManager struct {
	store        *store.Store
	stateManager *StateManager
	statsManager *StatsManager

	// Sample buffer
	sampleMu     sync.Mutex
	sampleBuffer []*store.Sample

	// Configuration
	stateFlushInterval time.Duration
	statsFlushInterval time.Duration
	sampleBatchSize    int
	sampleFlushTimeout time.Duration

	// Control
	shutdown chan struct{}
	wg       sync.WaitGroup
}

// SyncConfig holds sync manager configuration.
type SyncConfig struct {
	StateFlushInterval time.Duration
	StatsFlushInterval time.Duration
	SampleBatchSize    int
	SampleFlushTimeout time.Duration
}

// DefaultSyncConfig returns default sync configuration.
func DefaultSyncConfig() *SyncConfig {
	return &SyncConfig{
		StateFlushInterval: 5 * time.Second,
		StatsFlushInterval: 10 * time.Second,
		SampleBatchSize:    1000,
		SampleFlushTimeout: 5 * time.Second,
	}
}

// NewSyncManager creates a new sync manager.
func NewSyncManager(s *store.Store, stateMgr *StateManager, statsMgr *StatsManager, cfg *SyncConfig) *SyncManager {
	if cfg == nil {
		cfg = DefaultSyncConfig()
	}

	return &SyncManager{
		store:              s,
		stateManager:       stateMgr,
		statsManager:       statsMgr,
		sampleBuffer:       make([]*store.Sample, 0, cfg.SampleBatchSize),
		stateFlushInterval: cfg.StateFlushInterval,
		statsFlushInterval: cfg.StatsFlushInterval,
		sampleBatchSize:    cfg.SampleBatchSize,
		sampleFlushTimeout: cfg.SampleFlushTimeout,
		shutdown:           make(chan struct{}),
	}
}

// Start starts the sync manager.
func (m *SyncManager) Start() {
	m.wg.Add(3)
	go m.stateFlushLoop()
	go m.statsFlushLoop()
	go m.sampleFlushLoop()
}

// Stop stops the sync manager and flushes all pending data.
func (m *SyncManager) Stop() {
	close(m.shutdown)
	m.wg.Wait()

	// Final flush
	m.FlushAll()
}

// FlushAll flushes all pending data.
func (m *SyncManager) FlushAll() {
	m.flushStates()
	m.flushStats()
	m.flushSamples()
}

// AddSample adds a sample to the buffer.
func (m *SyncManager) AddSample(sample *store.Sample) {
	m.sampleMu.Lock()
	m.sampleBuffer = append(m.sampleBuffer, sample)
	shouldFlush := len(m.sampleBuffer) >= m.sampleBatchSize
	m.sampleMu.Unlock()

	if shouldFlush {
		m.flushSamples()
	}
}

// stateFlushLoop periodically flushes dirty states.
func (m *SyncManager) stateFlushLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.stateFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.flushStates()
		case <-m.shutdown:
			return
		}
	}
}

// statsFlushLoop periodically flushes dirty stats.
func (m *SyncManager) statsFlushLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.statsFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.flushStats()
		case <-m.shutdown:
			return
		}
	}
}

// sampleFlushLoop periodically flushes samples.
func (m *SyncManager) sampleFlushLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.sampleFlushTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.flushSamples()
		case <-m.shutdown:
			return
		}
	}
}

// flushStates flushes all dirty states to the store.
func (m *SyncManager) flushStates() {
	dirtyStates := m.stateManager.GetDirty()
	if len(dirtyStates) == 0 {
		return
	}

	// Convert to store format
	storeStates := make([]*store.PollerState, 0, len(dirtyStates))
	for _, state := range dirtyStates {
		state.mu.RLock()
		storeState := &store.PollerState{
			Namespace:           state.Namespace,
			Target:              state.Target,
			Poller:              state.Poller,
			OperState:           state.OperState,
			HealthState:         state.HealthState,
			LastError:           state.LastError,
			ConsecutiveFailures: state.ConsecutiveFailures,
			LastPollAt:          state.LastPollAt,
			LastSuccessAt:       state.LastSuccessAt,
			LastFailureAt:       state.LastFailureAt,
		}
		state.mu.RUnlock()
		storeStates = append(storeStates, storeState)
	}

	// Batch update
	if err := m.store.BatchUpdatePollerStates(storeStates); err != nil {
		log.Printf("Error flushing states: %v", err)
		return
	}

	// Clear dirty flags
	for _, state := range dirtyStates {
		state.ClearDirty()
	}

	log.Printf("Flushed %d poller states", len(storeStates))
}

// flushStats flushes all dirty stats to the store.
func (m *SyncManager) flushStats() {
	dirtyStats := m.statsManager.GetDirty()
	if len(dirtyStats) == 0 {
		return
	}

	// Convert to store format
	storeStats := make([]*store.PollerStatsRecord, 0, len(dirtyStats))
	for _, stats := range dirtyStats {
		total, success, failed, timeout, _, minMs, maxMs := stats.GetStats()

		storeRecord := &store.PollerStatsRecord{
			Namespace:    stats.Namespace,
			Target:       stats.Target,
			Poller:       stats.Poller,
			PollsTotal:   total,
			PollsSuccess: success,
			PollsFailed:  failed,
			PollsTimeout: timeout,
		}

		if minMs >= 0 {
			storeRecord.PollMsMin = &minMs
		}
		if maxMs > 0 {
			storeRecord.PollMsMax = &maxMs
		}

		storeStats = append(storeStats, storeRecord)
	}

	// Batch update
	if err := m.store.BatchUpdatePollerStats(storeStats); err != nil {
		log.Printf("Error flushing stats: %v", err)
		return
	}

	// Clear dirty flags
	for _, stats := range dirtyStats {
		stats.ClearDirty()
	}

	log.Printf("Flushed %d poller stats", len(storeStats))
}

// flushSamples flushes buffered samples to the store.
func (m *SyncManager) flushSamples() {
	m.sampleMu.Lock()
	if len(m.sampleBuffer) == 0 {
		m.sampleMu.Unlock()
		return
	}

	// Take the buffer and replace with new one
	samples := m.sampleBuffer
	m.sampleBuffer = make([]*store.Sample, 0, m.sampleBatchSize)
	m.sampleMu.Unlock()

	// Batch insert
	if err := m.store.InsertSamplesBatch(samples); err != nil {
		log.Printf("Error flushing samples: %v", err)
		return
	}

	log.Printf("Flushed %d samples", len(samples))
}

// GetBufferedSampleCount returns the number of buffered samples.
func (m *SyncManager) GetBufferedSampleCount() int {
	m.sampleMu.Lock()
	defer m.sampleMu.Unlock()
	return len(m.sampleBuffer)
}
