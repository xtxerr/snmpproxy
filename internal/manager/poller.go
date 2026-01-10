package manager

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/xtxerr/snmpproxy/internal/store"
)

// PollerManager handles poller operations.
type PollerManager struct {
	store          *store.Store
	stateManager   *StateManager
	statsManager   *StatsManager
	configResolver *ConfigResolver
	mu             sync.RWMutex

	// Primary cache: namespace/target/name -> poller
	pollers map[string]*store.Poller

	// Secondary indexes for efficient lookups
	byNamespace map[string]map[string]struct{} // namespace -> set of poller keys
	byTarget    map[string]map[string]struct{} // namespace/target -> set of poller keys

	// Callbacks for scheduler integration
	onPollerCreated func(namespace, target, poller string, intervalMs uint32)
	onPollerDeleted func(namespace, target, poller string)
	onPollerUpdated func(namespace, target, poller string, intervalMs uint32)
}

// NewPollerManager creates a new poller manager.
func NewPollerManager(s *store.Store, stateMgr *StateManager, statsMgr *StatsManager, cfgResolver *ConfigResolver) *PollerManager {
	return &PollerManager{
		store:          s,
		stateManager:   stateMgr,
		statsManager:   statsMgr,
		configResolver: cfgResolver,
		pollers:        make(map[string]*store.Poller),
		byNamespace:    make(map[string]map[string]struct{}),
		byTarget:       make(map[string]map[string]struct{}),
	}
}

// pollerKey returns the cache key for a poller.
func pollerKey(namespace, target, name string) string {
	return namespace + "/" + target + "/" + name
}

// SetCallbacks sets the callbacks for scheduler integration.
func (m *PollerManager) SetCallbacks(
	onCreate func(namespace, target, poller string, intervalMs uint32),
	onDelete func(namespace, target, poller string),
	onUpdate func(namespace, target, poller string, intervalMs uint32),
) {
	m.onPollerCreated = onCreate
	m.onPollerDeleted = onDelete
	m.onPollerUpdated = onUpdate
}

// Load loads all pollers from store.
func (m *PollerManager) Load() error {
	pollers, err := m.store.ListAllPollers()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.pollers = make(map[string]*store.Poller)
	m.byNamespace = make(map[string]map[string]struct{})
	m.byTarget = make(map[string]map[string]struct{})

	for _, p := range pollers {
		key := pollerKey(p.Namespace, p.Target, p.Name)
		m.pollers[key] = p

		// Update secondary indexes
		m.addToIndexes(p.Namespace, p.Target, key)

		// Load state from store
		state, err := m.store.GetPollerState(p.Namespace, p.Target, p.Name)
		if err == nil && state != nil {
			ps := m.stateManager.Get(p.Namespace, p.Target, p.Name)
			ps.AdminState = p.AdminState
			ps.OperState = state.OperState
			ps.HealthState = state.HealthState
			ps.LastError = state.LastError
			ps.ConsecutiveFailures = state.ConsecutiveFailures
			ps.LastPollAt = state.LastPollAt
			ps.LastSuccessAt = state.LastSuccessAt
			ps.LastFailureAt = state.LastFailureAt
		}

		// Load stats from store
		stats, err := m.store.GetPollerStats(p.Namespace, p.Target, p.Name)
		if err == nil && stats != nil {
			ps := m.statsManager.Get(p.Namespace, p.Target, p.Name)
			ps.PollsTotal.Store(stats.PollsTotal)
			ps.PollsSuccess.Store(stats.PollsSuccess)
			ps.PollsFailed.Store(stats.PollsFailed)
			ps.PollsTimeout.Store(stats.PollsTimeout)
		}
	}

	return nil
}

// addToIndexes adds a poller key to secondary indexes (caller must hold lock).
func (m *PollerManager) addToIndexes(namespace, target, key string) {
	// Namespace index
	if m.byNamespace[namespace] == nil {
		m.byNamespace[namespace] = make(map[string]struct{})
	}
	m.byNamespace[namespace][key] = struct{}{}

	// Target index
	targetKey := namespace + "/" + target
	if m.byTarget[targetKey] == nil {
		m.byTarget[targetKey] = make(map[string]struct{})
	}
	m.byTarget[targetKey][key] = struct{}{}
}

// removeFromIndexes removes a poller key from secondary indexes (caller must hold lock).
func (m *PollerManager) removeFromIndexes(namespace, target, key string) {
	// Namespace index
	if ns, ok := m.byNamespace[namespace]; ok {
		delete(ns, key)
		if len(ns) == 0 {
			delete(m.byNamespace, namespace)
		}
	}

	// Target index
	targetKey := namespace + "/" + target
	if t, ok := m.byTarget[targetKey]; ok {
		delete(t, key)
		if len(t) == 0 {
			delete(m.byTarget, targetKey)
		}
	}
}

// Create creates a new poller.
func (m *PollerManager) Create(p *store.Poller) error {
	key := pollerKey(p.Namespace, p.Target, p.Name)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	if _, exists := m.pollers[key]; exists {
		return fmt.Errorf("poller already exists: %s", key)
	}

	// Validate name
	if !isValidName(p.Name) {
		return fmt.Errorf("invalid poller name: %s", p.Name)
	}

	// Set default admin state
	if p.AdminState == "" {
		p.AdminState = AdminStateDisabled
	}

	// Create in store
	if err := m.store.CreatePoller(p); err != nil {
		return err
	}

	// Update cache and indexes
	m.pollers[key] = p
	m.addToIndexes(p.Namespace, p.Target, key)

	// Initialize state
	state := m.stateManager.Get(p.Namespace, p.Target, p.Name)
	state.AdminState = p.AdminState

	// Initialize stats
	m.statsManager.Get(p.Namespace, p.Target, p.Name)

	return nil
}

// Get returns a poller by namespace, target, and name.
func (m *PollerManager) Get(namespace, target, name string) (*store.Poller, error) {
	key := pollerKey(namespace, target, name)

	m.mu.RLock()
	p, ok := m.pollers[key]
	m.mu.RUnlock()

	if ok {
		return p, nil
	}

	// Try loading from store
	p, err := m.store.GetPoller(namespace, target, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}

	// Update cache
	m.mu.Lock()
	m.pollers[key] = p
	m.mu.Unlock()

	return p, nil
}

// List returns all pollers for a target.
// Uses secondary index for O(m) lookup where m = pollers in target.
func (m *PollerManager) List(namespace, target string) []*store.Poller {
	targetKey := namespace + "/" + target

	m.mu.RLock()
	defer m.mu.RUnlock()

	keys, ok := m.byTarget[targetKey]
	if !ok {
		return nil
	}

	result := make([]*store.Poller, 0, len(keys))
	for key := range keys {
		if p, ok := m.pollers[key]; ok {
			result = append(result, p)
		}
	}
	return result
}

// ListInNamespace returns all pollers in a namespace.
// Uses secondary index for O(m) lookup where m = pollers in namespace.
func (m *PollerManager) ListInNamespace(namespace string) []*store.Poller {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys, ok := m.byNamespace[namespace]
	if !ok {
		return nil
	}

	result := make([]*store.Poller, 0, len(keys))
	for key := range keys {
		if p, ok := m.pollers[key]; ok {
			result = append(result, p)
		}
	}
	return result
}

// Update updates a poller configuration.
func (m *PollerManager) Update(p *store.Poller) error {
	key := pollerKey(p.Namespace, p.Target, p.Name)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	existing, ok := m.pollers[key]
	if !ok {
		return fmt.Errorf("poller not found: %s", key)
	}

	// Preserve version
	p.Version = existing.Version

	// Check if interval changed
	oldInterval := m.getEffectiveInterval(existing)

	// Update in store
	if err := m.store.UpdatePoller(p); err != nil {
		return err
	}

	// Update cache
	m.pollers[key] = p

	// Notify scheduler if interval changed
	newInterval := m.getEffectiveInterval(p)
	if oldInterval != newInterval && m.onPollerUpdated != nil {
		m.onPollerUpdated(p.Namespace, p.Target, p.Name, newInterval)
	}

	return nil
}

func (m *PollerManager) getEffectiveInterval(p *store.Poller) uint32 {
	if p.PollingConfig != nil && p.PollingConfig.IntervalMs != nil {
		return *p.PollingConfig.IntervalMs
	}
	// Get from resolved config
	cfg, err := m.configResolver.Resolve(p.Namespace, p.Target, p.Name)
	if err == nil {
		return cfg.IntervalMs
	}
	return 1000 // Default
}

// Delete deletes a poller.
func (m *PollerManager) Delete(namespace, target, name string) (linksDeleted int, err error) {
	key := pollerKey(namespace, target, name)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	if _, ok := m.pollers[key]; !ok {
		return 0, fmt.Errorf("poller not found: %s", key)
	}

	// Notify scheduler before deletion
	if m.onPollerDeleted != nil {
		m.onPollerDeleted(namespace, target, name)
	}

	// Delete from store
	linksDeleted, err = m.store.DeletePoller(namespace, target, name)
	if err != nil {
		return 0, err
	}

	// Remove from cache and indexes
	delete(m.pollers, key)
	m.removeFromIndexes(namespace, target, key)

	// Remove state and stats
	m.stateManager.Remove(namespace, target, name)
	m.statsManager.Remove(namespace, target, name)

	// Invalidate config cache
	m.configResolver.Invalidate(namespace, target, name)

	return linksDeleted, nil
}

// Enable enables a poller.
func (m *PollerManager) Enable(namespace, target, name string) error {
	key := pollerKey(namespace, target, name)

	m.mu.Lock()
	p, ok := m.pollers[key]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("poller not found: %s", key)
	}

	p.AdminState = AdminStateEnabled
	if err := m.store.UpdatePollerAdminState(namespace, target, name, AdminStateEnabled); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	// Update state
	state := m.stateManager.Get(namespace, target, name)
	return state.Enable()
}

// Disable disables a poller.
func (m *PollerManager) Disable(namespace, target, name string) error {
	key := pollerKey(namespace, target, name)

	m.mu.Lock()
	p, ok := m.pollers[key]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("poller not found: %s", key)
	}

	p.AdminState = AdminStateDisabled
	if err := m.store.UpdatePollerAdminState(namespace, target, name, AdminStateDisabled); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	// Update state
	state := m.stateManager.Get(namespace, target, name)
	return state.Disable()
}

// Start starts a poller.
func (m *PollerManager) Start(namespace, target, name string) error {
	state := m.stateManager.Get(namespace, target, name)
	if err := state.Start(); err != nil {
		return err
	}

	// Get effective interval and notify scheduler
	if m.onPollerCreated != nil {
		m.mu.RLock()
		key := pollerKey(namespace, target, name)
		p, ok := m.pollers[key]
		m.mu.RUnlock()

		if ok {
			interval := m.getEffectiveInterval(p)
			m.onPollerCreated(namespace, target, name, interval)
		}
	}

	return nil
}

// Stop stops a poller.
func (m *PollerManager) Stop(namespace, target, name string) error {
	state := m.stateManager.Get(namespace, target, name)
	return state.Stop()
}

// GetState returns the state for a poller.
func (m *PollerManager) GetState(namespace, target, name string) *PollerState {
	return m.stateManager.Get(namespace, target, name)
}

// GetStats returns the stats for a poller.
func (m *PollerManager) GetStats(namespace, target, name string) *PollerStats {
	return m.statsManager.Get(namespace, target, name)
}

// GetResolvedConfig returns the resolved configuration for a poller.
func (m *PollerManager) GetResolvedConfig(namespace, target, name string) (*ResolvedPollerConfig, error) {
	return m.configResolver.Resolve(namespace, target, name)
}

// Exists checks if a poller exists.
func (m *PollerManager) Exists(namespace, target, name string) bool {
	key := pollerKey(namespace, target, name)

	m.mu.RLock()
	_, ok := m.pollers[key]
	m.mu.RUnlock()
	return ok
}

// Count returns the total number of pollers.
func (m *PollerManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pollers)
}

// CountInTarget returns the number of pollers in a target.
// Uses secondary index for O(1) lookup.
func (m *PollerManager) CountInTarget(namespace, target string) int {
	targetKey := namespace + "/" + target

	m.mu.RLock()
	defer m.mu.RUnlock()

	if keys, ok := m.byTarget[targetKey]; ok {
		return len(keys)
	}
	return 0
}

// GetEnabledPollers returns all enabled pollers.
func (m *PollerManager) GetEnabledPollers() []*store.Poller {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*store.Poller
	for _, p := range m.pollers {
		if p.AdminState == AdminStateEnabled {
			result = append(result, p)
		}
	}
	return result
}

// GetPollerConfig returns the protocol-specific configuration.
func (m *PollerManager) GetPollerConfig(namespace, target, name string) (protocol string, config map[string]interface{}, err error) {
	m.mu.RLock()
	key := pollerKey(namespace, target, name)
	p, ok := m.pollers[key]
	m.mu.RUnlock()

	if !ok {
		return "", nil, fmt.Errorf("poller not found: %s", key)
	}

	config = make(map[string]interface{})
	if err := json.Unmarshal(p.ProtocolConfig, &config); err != nil {
		return p.Protocol, nil, err
	}

	return p.Protocol, config, nil
}

// InvalidateCache removes a poller from the cache.
func (m *PollerManager) InvalidateCache(namespace, target, name string) {
	key := pollerKey(namespace, target, name)

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pollers, key)
}
