package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Namespace represents a namespace in the store.
type Namespace struct {
	Name        string
	Description string
	Config      *NamespaceConfig
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int
}

// NamespaceConfig holds namespace configuration.
type NamespaceConfig struct {
	Defaults *PollerDefaults `json:"defaults,omitempty"`
}

// PollerDefaults holds default values for pollers.
type PollerDefaults struct {
	// SNMP v2c
	SNMPCommunity *string `json:"snmp_community,omitempty"`

	// SNMPv3
	SNMPv3 *SNMPv3Defaults `json:"snmpv3,omitempty"`

	// Timing
	IntervalMs *uint32 `json:"interval_ms,omitempty"`
	TimeoutMs  *uint32 `json:"timeout_ms,omitempty"`
	Retries    *uint32 `json:"retries,omitempty"`
	BufferSize *uint32 `json:"buffer_size,omitempty"`
}

// SNMPv3Defaults holds SNMPv3 default credentials.
type SNMPv3Defaults struct {
	SecurityName  string `json:"security_name,omitempty"`
	SecurityLevel string `json:"security_level,omitempty"`
	AuthProtocol  string `json:"auth_protocol,omitempty"`
	AuthPassword  string `json:"auth_password,omitempty"`
	AuthSecretRef string `json:"auth_secret_ref,omitempty"`
	PrivProtocol  string `json:"priv_protocol,omitempty"`
	PrivPassword  string `json:"priv_password,omitempty"`
	PrivSecretRef string `json:"priv_secret_ref,omitempty"`
}

// CreateNamespace creates a new namespace.
func (s *Store) CreateNamespace(ns *Namespace) error {
	configJSON, err := json.Marshal(ns.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO namespaces (name, description, config, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, 1)
	`, ns.Name, ns.Description, string(configJSON), time.Now(), time.Now())

	if err != nil {
		return fmt.Errorf("insert namespace: %w", err)
	}

	ns.Version = 1
	ns.CreatedAt = time.Now()
	ns.UpdatedAt = time.Now()
	return nil
}

// GetNamespace retrieves a namespace by name.
func (s *Store) GetNamespace(name string) (*Namespace, error) {
	var ns Namespace
	var configJSON sql.NullString

	err := s.db.QueryRow(`
		SELECT name, description, config, created_at, updated_at, version
		FROM namespaces WHERE name = ?
	`, name).Scan(&ns.Name, &ns.Description, &configJSON, &ns.CreatedAt, &ns.UpdatedAt, &ns.Version)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query namespace: %w", err)
	}

	if configJSON.Valid && configJSON.String != "" {
		ns.Config = &NamespaceConfig{}
		if err := json.Unmarshal([]byte(configJSON.String), ns.Config); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	}

	return &ns, nil
}

// ListNamespaces returns all namespaces.
func (s *Store) ListNamespaces() ([]*Namespace, error) {
	rows, err := s.db.Query(`
		SELECT name, description, config, created_at, updated_at, version
		FROM namespaces ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("query namespaces: %w", err)
	}
	defer rows.Close()

	var namespaces []*Namespace
	for rows.Next() {
		var ns Namespace
		var configJSON sql.NullString

		if err := rows.Scan(&ns.Name, &ns.Description, &configJSON, &ns.CreatedAt, &ns.UpdatedAt, &ns.Version); err != nil {
			return nil, fmt.Errorf("scan namespace: %w", err)
		}

		if configJSON.Valid && configJSON.String != "" {
			ns.Config = &NamespaceConfig{}
			if err := json.Unmarshal([]byte(configJSON.String), ns.Config); err != nil {
				return nil, fmt.Errorf("unmarshal config: %w", err)
			}
		}

		namespaces = append(namespaces, &ns)
	}

	return namespaces, rows.Err()
}

// UpdateNamespace updates a namespace with optimistic locking.
func (s *Store) UpdateNamespace(ns *Namespace) error {
	configJSON, err := json.Marshal(ns.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	result, err := s.db.Exec(`
		UPDATE namespaces 
		SET description = ?, config = ?, updated_at = ?, version = version + 1
		WHERE name = ? AND version = ?
	`, ns.Description, string(configJSON), time.Now(), ns.Name, ns.Version)

	if err != nil {
		return fmt.Errorf("update namespace: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrConcurrentModification
	}

	ns.Version++
	ns.UpdatedAt = time.Now()
	return nil
}

// DeleteNamespace deletes a namespace.
// Returns counts of deleted targets and pollers.
func (s *Store) DeleteNamespace(name string, force bool) (targetsDeleted, pollersDeleted int, err error) {
	// Check if empty
	var targetCount int
	err = s.db.QueryRow(`SELECT COUNT(*) FROM targets WHERE namespace = ?`, name).Scan(&targetCount)
	if err != nil {
		return 0, 0, fmt.Errorf("count targets: %w", err)
	}

	if targetCount > 0 && !force {
		return 0, 0, fmt.Errorf("namespace not empty: %d targets (use force to delete)", targetCount)
	}

	err = s.Transaction(func(tx *sql.Tx) error {
		// Count pollers before deletion
		err := tx.QueryRow(`SELECT COUNT(*) FROM pollers WHERE namespace = ?`, name).Scan(&pollersDeleted)
		if err != nil {
			return err
		}

		// Delete in order: samples, poller_state, poller_stats, pollers, tree_nodes, secrets, targets, namespace
		tables := []string{
			"DELETE FROM samples WHERE namespace = ?",
			"DELETE FROM poller_state WHERE namespace = ?",
			"DELETE FROM poller_stats WHERE namespace = ?",
			"DELETE FROM pollers WHERE namespace = ?",
			"DELETE FROM tree_nodes WHERE namespace = ?",
			"DELETE FROM secrets WHERE namespace = ?",
			"DELETE FROM targets WHERE namespace = ?",
			"DELETE FROM namespaces WHERE name = ?",
		}

		for _, query := range tables {
			if _, err := tx.Exec(query, name); err != nil {
				return err
			}
		}

		targetsDeleted = targetCount
		return nil
	})

	return targetsDeleted, pollersDeleted, err
}

// NamespaceExists checks if a namespace exists.
func (s *Store) NamespaceExists(name string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM namespaces WHERE name = ?`, name).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetNamespaceStats returns statistics for a namespace.
func (s *Store) GetNamespaceStats(name string) (*NamespaceStats, error) {
	stats := &NamespaceStats{}

	// Target count
	err := s.db.QueryRow(`SELECT COUNT(*) FROM targets WHERE namespace = ?`, name).Scan(&stats.TargetCount)
	if err != nil {
		return nil, err
	}

	// Poller counts
	err = s.db.QueryRow(`SELECT COUNT(*) FROM pollers WHERE namespace = ?`, name).Scan(&stats.PollerCount)
	if err != nil {
		return nil, err
	}

	// Running pollers
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM poller_state 
		WHERE namespace = ? AND oper_state = 'running'
	`, name).Scan(&stats.PollersRunning)
	if err != nil {
		return nil, err
	}

	// Health stats
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM poller_state 
		WHERE namespace = ? AND health_state = 'up'
	`, name).Scan(&stats.PollersUp)
	if err != nil {
		return nil, err
	}

	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM poller_state 
		WHERE namespace = ? AND health_state = 'down'
	`, name).Scan(&stats.PollersDown)
	if err != nil {
		return nil, err
	}

	// Tree nodes
	err = s.db.QueryRow(`SELECT COUNT(*) FROM tree_nodes WHERE namespace = ?`, name).Scan(&stats.TreeNodeCount)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// NamespaceStats holds statistics for a namespace.
type NamespaceStats struct {
	TargetCount    int
	PollerCount    int
	PollersRunning int
	PollersUp      int
	PollersDown    int
	TreeNodeCount  int
}
