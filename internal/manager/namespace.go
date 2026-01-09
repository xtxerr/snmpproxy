package manager

import (
	"fmt"
	"sync"

	"github.com/xtxerr/snmpproxy/internal/store"
)

// NamespaceManager handles namespace operations.
type NamespaceManager struct {
	store *store.Store
	mu    sync.RWMutex

	// Cache
	namespaces map[string]*store.Namespace
}

// NewNamespaceManager creates a new namespace manager.
func NewNamespaceManager(s *store.Store) *NamespaceManager {
	return &NamespaceManager{
		store:      s,
		namespaces: make(map[string]*store.Namespace),
	}
}

// Load loads all namespaces from store.
func (m *NamespaceManager) Load() error {
	namespaces, err := m.store.ListNamespaces()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.namespaces = make(map[string]*store.Namespace)
	for _, ns := range namespaces {
		m.namespaces[ns.Name] = ns
	}

	return nil
}

// Create creates a new namespace.
func (m *NamespaceManager) Create(ns *store.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	if _, exists := m.namespaces[ns.Name]; exists {
		return fmt.Errorf("namespace already exists: %s", ns.Name)
	}

	// Validate name
	if !isValidName(ns.Name) {
		return fmt.Errorf("invalid namespace name: %s", ns.Name)
	}

	// Create in store
	if err := m.store.CreateNamespace(ns); err != nil {
		return err
	}

	// Update cache
	m.namespaces[ns.Name] = ns
	return nil
}

// Get returns a namespace by name.
func (m *NamespaceManager) Get(name string) (*store.Namespace, error) {
	m.mu.RLock()
	ns, ok := m.namespaces[name]
	m.mu.RUnlock()

	if ok {
		return ns, nil
	}

	// Try loading from store
	ns, err := m.store.GetNamespace(name)
	if err != nil {
		return nil, err
	}
	if ns == nil {
		return nil, nil
	}

	// Update cache
	m.mu.Lock()
	m.namespaces[name] = ns
	m.mu.Unlock()

	return ns, nil
}

// List returns all namespaces.
func (m *NamespaceManager) List() []*store.Namespace {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*store.Namespace, 0, len(m.namespaces))
	for _, ns := range m.namespaces {
		result = append(result, ns)
	}
	return result
}

// Update updates a namespace.
func (m *NamespaceManager) Update(ns *store.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	existing, ok := m.namespaces[ns.Name]
	if !ok {
		return fmt.Errorf("namespace not found: %s", ns.Name)
	}

	// Ensure version matches for optimistic locking
	ns.Version = existing.Version

	// Update in store
	if err := m.store.UpdateNamespace(ns); err != nil {
		return err
	}

	// Update cache
	m.namespaces[ns.Name] = ns
	return nil
}

// Delete deletes a namespace.
func (m *NamespaceManager) Delete(name string, force bool) (targetsDeleted, pollersDeleted int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	if _, ok := m.namespaces[name]; !ok {
		return 0, 0, fmt.Errorf("namespace not found: %s", name)
	}

	// Delete from store
	targetsDeleted, pollersDeleted, err = m.store.DeleteNamespace(name, force)
	if err != nil {
		return 0, 0, err
	}

	// Remove from cache
	delete(m.namespaces, name)
	return targetsDeleted, pollersDeleted, nil
}

// Exists checks if a namespace exists.
func (m *NamespaceManager) Exists(name string) bool {
	m.mu.RLock()
	_, ok := m.namespaces[name]
	m.mu.RUnlock()
	return ok
}

// GetStats returns statistics for a namespace.
func (m *NamespaceManager) GetStats(name string) (*store.NamespaceStats, error) {
	return m.store.GetNamespaceStats(name)
}

// GetDefaults returns the defaults for a namespace.
func (m *NamespaceManager) GetDefaults(name string) *store.PollerDefaults {
	m.mu.RLock()
	ns, ok := m.namespaces[name]
	m.mu.RUnlock()

	if !ok || ns.Config == nil {
		return nil
	}
	return ns.Config.Defaults
}

// SetDefaults updates the defaults for a namespace.
func (m *NamespaceManager) SetDefaults(name string, defaults *store.PollerDefaults) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return fmt.Errorf("namespace not found: %s", name)
	}

	if ns.Config == nil {
		ns.Config = &store.NamespaceConfig{}
	}
	ns.Config.Defaults = defaults

	// Update in store
	if err := m.store.UpdateNamespace(ns); err != nil {
		return err
	}

	return nil
}

// Count returns the number of namespaces.
func (m *NamespaceManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.namespaces)
}

// Validate name format
func isValidName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i, c := range name {
		if i == 0 {
			// First char must be letter
			if !isLetter(c) {
				return false
			}
		} else {
			// Rest can be letter, digit, hyphen, underscore
			if !isLetter(c) && !isDigit(c) && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

func isLetter(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c rune) bool {
	return c >= '0' && c <= '9'
}
