package manager

import (
	"fmt"
	"log"
	"time"

	"github.com/xtxerr/snmpproxy/internal/store"
)

// Manager orchestrates all entity managers.
type Manager struct {
	store *store.Store

	// Entity managers
	Namespaces *NamespaceManager
	Targets    *TargetManager
	Pollers    *PollerManager
	Tree       *TreeManager

	// Runtime managers
	States *StateManager
	Stats  *StatsManager
	Sync   *SyncManager

	// Config
	ConfigResolver *ConfigResolver

	// Server info
	startedAt time.Time
	version   string
}

// Config holds manager configuration.
type Config struct {
	DBPath        string
	SecretKeyPath string
	InMemory      bool
	Version       string
	SyncConfig    *SyncConfig
}

// New creates and initializes the manager.
func New(cfg *Config) (*Manager, error) {
	// Create store
	storeCfg := &store.Config{
		DBPath:        cfg.DBPath,
		SecretKeyPath: cfg.SecretKeyPath,
		InMemory:      cfg.InMemory,
	}

	s, err := store.New(storeCfg)
	if err != nil {
		return nil, fmt.Errorf("create store: %w", err)
	}

	// Create runtime managers
	stateManager := NewStateManager()
	statsManager := NewStatsManager()
	configResolver := NewConfigResolver(s)

	// Create entity managers
	nsManager := NewNamespaceManager(s)
	targetManager := NewTargetManager(s)
	pollerManager := NewPollerManager(s, stateManager, statsManager, configResolver)
	treeManager := NewTreeManager(s)

	// Create sync manager
	syncManager := NewSyncManager(s, stateManager, statsManager, cfg.SyncConfig)

	m := &Manager{
		store:          s,
		Namespaces:     nsManager,
		Targets:        targetManager,
		Pollers:        pollerManager,
		Tree:           treeManager,
		States:         stateManager,
		Stats:          statsManager,
		Sync:           syncManager,
		ConfigResolver: configResolver,
		startedAt:      time.Now(),
		version:        cfg.Version,
	}

	return m, nil
}

// Load loads all data from the store.
func (m *Manager) Load() error {
	log.Println("Loading namespaces...")
	if err := m.Namespaces.Load(); err != nil {
		return fmt.Errorf("load namespaces: %w", err)
	}
	log.Printf("Loaded %d namespaces", m.Namespaces.Count())

	log.Println("Loading targets...")
	if err := m.Targets.Load(); err != nil {
		return fmt.Errorf("load targets: %w", err)
	}
	log.Printf("Loaded %d targets", m.Targets.Count())

	log.Println("Loading pollers...")
	if err := m.Pollers.Load(); err != nil {
		return fmt.Errorf("load pollers: %w", err)
	}
	log.Printf("Loaded %d pollers", m.Pollers.Count())

	return nil
}

// Start starts the sync manager.
func (m *Manager) Start() {
	m.Sync.Start()
}

// Stop stops the manager and flushes all data.
func (m *Manager) Stop() {
	log.Println("Stopping manager...")
	m.Sync.Stop()
	m.store.Close()
	log.Println("Manager stopped")
}

// Store returns the underlying store.
func (m *Manager) Store() *store.Store {
	return m.store
}

// StartedAt returns when the manager was started.
func (m *Manager) StartedAt() time.Time {
	return m.startedAt
}

// Version returns the server version.
func (m *Manager) Version() string {
	return m.version
}

// GetServerInfo returns server information.
func (m *Manager) GetServerInfo() *ServerInfo {
	operCounts := m.States.CountByOperState()
	healthCounts := m.States.CountByHealthState()
	pollsTotal, pollsSuccess, pollsFailed, _ := m.Stats.Aggregate()

	return &ServerInfo{
		Version:        m.version,
		StartedAt:      m.startedAt,
		Uptime:         time.Since(m.startedAt),
		NamespaceCount: m.Namespaces.Count(),
		TargetCount:    m.Targets.Count(),
		PollerCount:    m.Pollers.Count(),
		PollersRunning: operCounts[OperStateRunning],
		PollersUp:      healthCounts[HealthStateUp],
		PollersDown:    healthCounts[HealthStateDown],
		PollsTotal:     pollsTotal,
		PollsSuccess:   pollsSuccess,
		PollsFailed:    pollsFailed,
	}
}

// ServerInfo holds server information.
type ServerInfo struct {
	Version        string
	StartedAt      time.Time
	Uptime         time.Duration
	NamespaceCount int
	TargetCount    int
	PollerCount    int
	PollersRunning int
	PollersUp      int
	PollersDown    int
	PollsTotal     int64
	PollsSuccess   int64
	PollsFailed    int64
}

// RecordPollResult records a poll result for a poller.
func (m *Manager) RecordPollResult(namespace, target, poller string, success bool, timeout bool, pollErr string, pollMs int, sample *store.Sample) {
	// Update state
	state := m.States.Get(namespace, target, poller)
	state.RecordPollResult(success, pollErr, pollMs)

	// Update stats
	stats := m.Stats.Get(namespace, target, poller)
	stats.RecordPoll(success, timeout, pollMs)

	// Buffer sample
	if sample != nil {
		m.Sync.AddSample(sample)
	}
}

// StartEnabledPollers starts all enabled pollers.
// Returns the number of pollers started.
func (m *Manager) StartEnabledPollers() int {
	enabled := m.Pollers.GetEnabledPollers()
	started := 0

	for _, p := range enabled {
		state := m.States.Get(p.Namespace, p.Target, p.Name)
		if state.CanRun() {
			if err := state.Start(); err == nil {
				started++
			}
		}
	}

	return started
}

// GetPollerFullInfo returns complete information about a poller.
func (m *Manager) GetPollerFullInfo(namespace, target, poller string) (*PollerFullInfo, error) {
	p, err := m.Pollers.Get(namespace, target, poller)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("poller not found: %s/%s/%s", namespace, target, poller)
	}

	state := m.States.Get(namespace, target, poller)
	stats := m.Stats.Get(namespace, target, poller)
	resolvedCfg, _ := m.ConfigResolver.Resolve(namespace, target, poller)

	admin, oper, health := state.GetState()
	total, success, failed, timeout, avgMs, minMs, maxMs := stats.GetStats()

	return &PollerFullInfo{
		Poller:         p,
		AdminState:     admin,
		OperState:      oper,
		HealthState:    health,
		LastError:      state.GetLastError(),
		ResolvedConfig: resolvedCfg,
		PollsTotal:     total,
		PollsSuccess:   success,
		PollsFailed:    failed,
		PollsTimeout:   timeout,
		AvgPollMs:      avgMs,
		MinPollMs:      minMs,
		MaxPollMs:      maxMs,
	}, nil
}

// PollerFullInfo holds complete information about a poller.
type PollerFullInfo struct {
	Poller         *store.Poller
	AdminState     string
	OperState      string
	HealthState    string
	LastError      string
	ResolvedConfig *ResolvedPollerConfig
	PollsTotal     int64
	PollsSuccess   int64
	PollsFailed    int64
	PollsTimeout   int64
	AvgPollMs      int
	MinPollMs      int
	MaxPollMs      int
}
