package manager

import (
	"fmt"
	"sync"
	"time"
)

// Poller states
const (
	// AdminState - User controlled
	AdminStateEnabled  = "enabled"
	AdminStateDisabled = "disabled"

	// OperState - System controlled
	OperStateStopped  = "stopped"
	OperStateStarting = "starting"
	OperStateRunning  = "running"
	OperStateStopping = "stopping"
	OperStateError    = "error"

	// HealthState - Derived from polling results
	HealthStateUnknown  = "unknown"
	HealthStateUp       = "up"
	HealthStateDegraded = "degraded"
	HealthStateDown     = "down"
)

// Health thresholds
const (
	HealthDegradedThreshold = 3 // Failures before DEGRADED
	HealthDownThreshold     = 5 // Consecutive failures before DOWN
	HealthRecoveryCount     = 2 // Successes to go from DOWN → UP
)

// PollerState holds the complete state of a poller.
type PollerState struct {
	mu sync.RWMutex

	Namespace string
	Target    string
	Poller    string

	// States
	AdminState  string
	OperState   string
	HealthState string

	// Error tracking
	LastError           string
	ConsecutiveFailures int
	RecentSuccesses     int

	// Timestamps
	LastPollAt    *time.Time
	LastSuccessAt *time.Time
	LastFailureAt *time.Time

	// Dirty flag for batched persistence
	dirty bool
}

// NewPollerState creates a new poller state.
func NewPollerState(namespace, target, poller string) *PollerState {
	return &PollerState{
		Namespace:   namespace,
		Target:      target,
		Poller:      poller,
		AdminState:  AdminStateDisabled,
		OperState:   OperStateStopped,
		HealthState: HealthStateUnknown,
	}
}

// Key returns the unique key for this poller.
func (s *PollerState) Key() string {
	return s.Namespace + "/" + s.Target + "/" + s.Poller
}

// ============================================================================
// Admin State Transitions
// ============================================================================

// Enable enables the poller (admin action).
func (s *PollerState) Enable() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.AdminState = AdminStateEnabled
	s.dirty = true
	return nil
}

// Disable disables the poller (admin action).
func (s *PollerState) Disable() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.AdminState = AdminStateDisabled
	s.dirty = true
	return nil
}

// IsEnabled returns true if admin state is enabled.
func (s *PollerState) IsEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AdminState == AdminStateEnabled
}

// ============================================================================
// Oper State Transitions
// ============================================================================

// Start transitions from stopped → starting.
func (s *PollerState) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.AdminState != AdminStateEnabled {
		return fmt.Errorf("cannot start: admin state is %s", s.AdminState)
	}

	switch s.OperState {
	case OperStateStopped, OperStateError:
		s.OperState = OperStateStarting
		s.dirty = true
		return nil
	case OperStateStarting, OperStateRunning:
		return nil // Already starting/running
	case OperStateStopping:
		return fmt.Errorf("cannot start: currently stopping")
	default:
		return fmt.Errorf("invalid oper state: %s", s.OperState)
	}
}

// MarkRunning transitions from starting → running.
func (s *PollerState) MarkRunning() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.OperState == OperStateStarting {
		s.OperState = OperStateRunning
		s.dirty = true
	}
}

// Stop transitions to stopping → stopped.
func (s *PollerState) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.OperState {
	case OperStateRunning, OperStateStarting:
		s.OperState = OperStateStopping
		s.dirty = true
		return nil
	case OperStateStopped, OperStateStopping:
		return nil // Already stopped/stopping
	case OperStateError:
		s.OperState = OperStateStopped
		s.dirty = true
		return nil
	default:
		return fmt.Errorf("invalid oper state: %s", s.OperState)
	}
}

// MarkStopped transitions to stopped.
func (s *PollerState) MarkStopped() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.OperState = OperStateStopped
	s.dirty = true
}

// MarkError transitions to error state.
func (s *PollerState) MarkError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.OperState = OperStateError
	s.LastError = err.Error()
	s.dirty = true
}

// IsRunning returns true if oper state is running.
func (s *PollerState) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.OperState == OperStateRunning
}

// CanRun returns true if the poller can be started.
func (s *PollerState) CanRun() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AdminState == AdminStateEnabled &&
		(s.OperState == OperStateStopped || s.OperState == OperStateError)
}

// ============================================================================
// Health State Transitions
// ============================================================================

// RecordPollResult updates health state based on poll result.
func (s *PollerState) RecordPollResult(success bool, pollErr string, pollMs int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.LastPollAt = &now

	if success {
		s.ConsecutiveFailures = 0
		s.RecentSuccesses++
		s.LastSuccessAt = &now
		s.LastError = ""

		// Transition health state
		switch s.HealthState {
		case HealthStateDown:
			if s.RecentSuccesses >= HealthRecoveryCount {
				s.HealthState = HealthStateUp
			}
		case HealthStateDegraded:
			s.HealthState = HealthStateUp
		default:
			s.HealthState = HealthStateUp
		}
	} else {
		s.ConsecutiveFailures++
		s.RecentSuccesses = 0
		s.LastFailureAt = &now
		s.LastError = pollErr

		// Transition health state
		if s.ConsecutiveFailures >= HealthDownThreshold {
			s.HealthState = HealthStateDown
		} else if s.ConsecutiveFailures >= HealthDegradedThreshold {
			s.HealthState = HealthStateDegraded
		}
	}

	s.dirty = true
}

// GetHealthState returns the current health state.
func (s *PollerState) GetHealthState() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.HealthState
}

// ============================================================================
// State Queries
// ============================================================================

// GetState returns a snapshot of the current state.
func (s *PollerState) GetState() (admin, oper, health string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AdminState, s.OperState, s.HealthState
}

// GetLastError returns the last error.
func (s *PollerState) GetLastError() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastError
}

// IsDirty returns true if state has changed since last persist.
func (s *PollerState) IsDirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dirty
}

// ClearDirty clears the dirty flag.
func (s *PollerState) ClearDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false
}

// ============================================================================
// State Manager - Manages all poller states
// ============================================================================

// StateManager manages poller states.
type StateManager struct {
	mu     sync.RWMutex
	states map[string]*PollerState // key: namespace/target/poller
}

// NewStateManager creates a new state manager.
func NewStateManager() *StateManager {
	return &StateManager{
		states: make(map[string]*PollerState),
	}
}

// Get returns the state for a poller.
func (m *StateManager) Get(namespace, target, poller string) *PollerState {
	key := fmt.Sprintf("%s/%s/%s", namespace, target, poller)

	m.mu.RLock()
	state, ok := m.states[key]
	m.mu.RUnlock()

	if ok {
		return state
	}

	// Create new state
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if state, ok = m.states[key]; ok {
		return state
	}

	state = NewPollerState(namespace, target, poller)
	m.states[key] = state
	return state
}

// Remove removes a poller state.
func (m *StateManager) Remove(namespace, target, poller string) {
	key := fmt.Sprintf("%s/%s/%s", namespace, target, poller)

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, key)
}

// GetDirty returns all dirty states.
func (m *StateManager) GetDirty() []*PollerState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dirty []*PollerState
	for _, state := range m.states {
		if state.IsDirty() {
			dirty = append(dirty, state)
		}
	}
	return dirty
}

// GetAll returns all states.
func (m *StateManager) GetAll() []*PollerState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	states := make([]*PollerState, 0, len(m.states))
	for _, state := range m.states {
		states = append(states, state)
	}
	return states
}

// Count returns the number of states.
func (m *StateManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.states)
}

// CountByOperState counts states by oper state.
func (m *StateManager) CountByOperState() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := make(map[string]int)
	for _, state := range m.states {
		state.mu.RLock()
		counts[state.OperState]++
		state.mu.RUnlock()
	}
	return counts
}

// CountByHealthState counts states by health state.
func (m *StateManager) CountByHealthState() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := make(map[string]int)
	for _, state := range m.states {
		state.mu.RLock()
		counts[state.HealthState]++
		state.mu.RUnlock()
	}
	return counts
}
