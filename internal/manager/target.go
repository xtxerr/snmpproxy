package manager

import (
	"fmt"
	"sync"

	"github.com/xtxerr/snmpproxy/internal/store"
)

// TargetManager handles target operations.
type TargetManager struct {
	store *store.Store
	mu    sync.RWMutex

	// Cache: namespace/name -> target
	targets map[string]*store.Target
}

// NewTargetManager creates a new target manager.
func NewTargetManager(s *store.Store) *TargetManager {
	return &TargetManager{
		store:   s,
		targets: make(map[string]*store.Target),
	}
}

// targetKey returns the cache key for a target.
func targetKey(namespace, name string) string {
	return namespace + "/" + name
}

// Load loads all targets from store.
func (m *TargetManager) Load() error {
	namespaces, err := m.store.ListNamespaces()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.targets = make(map[string]*store.Target)
	for _, ns := range namespaces {
		targets, err := m.store.ListTargets(ns.Name)
		if err != nil {
			return err
		}
		for _, t := range targets {
			key := targetKey(t.Namespace, t.Name)
			m.targets[key] = t
		}
	}

	return nil
}

// Create creates a new target.
func (m *TargetManager) Create(t *store.Target) error {
	key := targetKey(t.Namespace, t.Name)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	if _, exists := m.targets[key]; exists {
		return fmt.Errorf("target already exists: %s/%s", t.Namespace, t.Name)
	}

	// Validate name
	if !isValidName(t.Name) {
		return fmt.Errorf("invalid target name: %s", t.Name)
	}

	// Create in store
	if err := m.store.CreateTarget(t); err != nil {
		return err
	}

	// Update cache
	m.targets[key] = t
	return nil
}

// Get returns a target by namespace and name.
func (m *TargetManager) Get(namespace, name string) (*store.Target, error) {
	key := targetKey(namespace, name)

	m.mu.RLock()
	t, ok := m.targets[key]
	m.mu.RUnlock()

	if ok {
		return t, nil
	}

	// Try loading from store
	t, err := m.store.GetTarget(namespace, name)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}

	// Update cache
	m.mu.Lock()
	m.targets[key] = t
	m.mu.Unlock()

	return t, nil
}

// List returns all targets in a namespace.
func (m *TargetManager) List(namespace string) []*store.Target {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*store.Target
	for _, t := range m.targets {
		if t.Namespace == namespace {
			result = append(result, t)
		}
	}
	return result
}

// ListFiltered returns targets matching filters.
func (m *TargetManager) ListFiltered(namespace string, labels map[string]string, limit int, cursor string) ([]*store.Target, string, error) {
	return m.store.ListTargetsFiltered(namespace, labels, limit, cursor)
}

// Update updates a target.
func (m *TargetManager) Update(t *store.Target) error {
	key := targetKey(t.Namespace, t.Name)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	existing, ok := m.targets[key]
	if !ok {
		return fmt.Errorf("target not found: %s/%s", t.Namespace, t.Name)
	}

	// Ensure version matches
	t.Version = existing.Version

	// Update in store
	if err := m.store.UpdateTarget(t); err != nil {
		return err
	}

	// Update cache
	m.targets[key] = t
	return nil
}

// Delete deletes a target.
func (m *TargetManager) Delete(namespace, name string, force bool) (pollersDeleted, linksDeleted int, err error) {
	key := targetKey(namespace, name)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	if _, ok := m.targets[key]; !ok {
		return 0, 0, fmt.Errorf("target not found: %s/%s", namespace, name)
	}

	// Delete from store
	pollersDeleted, linksDeleted, err = m.store.DeleteTarget(namespace, name, force)
	if err != nil {
		return 0, 0, err
	}

	// Remove from cache
	delete(m.targets, key)
	return pollersDeleted, linksDeleted, nil
}

// Exists checks if a target exists.
func (m *TargetManager) Exists(namespace, name string) bool {
	key := targetKey(namespace, name)

	m.mu.RLock()
	_, ok := m.targets[key]
	m.mu.RUnlock()
	return ok
}

// GetStats returns statistics for a target.
func (m *TargetManager) GetStats(namespace, name string) (*store.TargetStats, error) {
	return m.store.GetTargetStats(namespace, name)
}

// GetDefaults returns the defaults for a target.
func (m *TargetManager) GetDefaults(namespace, name string) *store.PollerDefaults {
	key := targetKey(namespace, name)

	m.mu.RLock()
	t, ok := m.targets[key]
	m.mu.RUnlock()

	if !ok || t.Config == nil {
		return nil
	}
	return t.Config.Defaults
}

// SetDefaults updates the defaults for a target.
func (m *TargetManager) SetDefaults(namespace, name string, defaults *store.PollerDefaults) error {
	key := targetKey(namespace, name)

	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.targets[key]
	if !ok {
		return fmt.Errorf("target not found: %s/%s", namespace, name)
	}

	if t.Config == nil {
		t.Config = &store.TargetConfig{}
	}
	t.Config.Defaults = defaults

	// Update in store
	if err := m.store.UpdateTarget(t); err != nil {
		return err
	}

	return nil
}

// SetLabels updates labels for a target.
func (m *TargetManager) SetLabels(namespace, name string, labels map[string]string) error {
	key := targetKey(namespace, name)

	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.targets[key]
	if !ok {
		return fmt.Errorf("target not found: %s/%s", namespace, name)
	}

	t.Labels = labels

	// Update in store
	if err := m.store.UpdateTarget(t); err != nil {
		return err
	}

	return nil
}

// Count returns the total number of targets.
func (m *TargetManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.targets)
}

// CountInNamespace returns the number of targets in a namespace.
func (m *TargetManager) CountInNamespace(namespace string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, t := range m.targets {
		if t.Namespace == namespace {
			count++
		}
	}
	return count
}

// InvalidateCache removes a target from the cache.
func (m *TargetManager) InvalidateCache(namespace, name string) {
	key := targetKey(namespace, name)

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.targets, key)
}
