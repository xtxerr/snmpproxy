package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Poller represents a poller in the store.
type Poller struct {
	Namespace      string
	Target         string
	Name           string
	Description    string
	Protocol       string
	ProtocolConfig json.RawMessage
	PollingConfig  *PollingConfig
	AdminState     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Version        int
}

// PollingConfig holds polling configuration.
type PollingConfig struct {
	IntervalMs *uint32 `json:"interval_ms,omitempty"`
	TimeoutMs  *uint32 `json:"timeout_ms,omitempty"`
	Retries    *uint32 `json:"retries,omitempty"`
	BufferSize *uint32 `json:"buffer_size,omitempty"`
}

// PollerState represents runtime state of a poller.
type PollerState struct {
	Namespace           string
	Target              string
	Poller              string
	OperState           string
	HealthState         string
	LastError           string
	ConsecutiveFailures int
	LastPollAt          *time.Time
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
}

// PollerStats holds statistics for a poller.
type PollerStatsRecord struct {
	Namespace    string
	Target       string
	Poller       string
	PollsTotal   int64
	PollsSuccess int64
	PollsFailed  int64
	PollsTimeout int64
	PollMsSum    int64
	PollMsMin    *int
	PollMsMax    *int
	PollMsCount  int
}

// CreatePoller creates a new poller.
func (s *Store) CreatePoller(p *Poller) error {
	pollingJSON, err := json.Marshal(p.PollingConfig)
	if err != nil {
		return fmt.Errorf("marshal polling config: %w", err)
	}

	now := time.Now()

	err = s.Transaction(func(tx *sql.Tx) error {
		// Insert poller
		_, err := tx.Exec(`
			INSERT INTO pollers (namespace, target, name, description, protocol, protocol_config, 
			                     polling_config, admin_state, created_at, updated_at, version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		`, p.Namespace, p.Target, p.Name, p.Description, p.Protocol,
			string(p.ProtocolConfig), string(pollingJSON), p.AdminState, now, now)
		if err != nil {
			return fmt.Errorf("insert poller: %w", err)
		}

		// Initialize poller state
		_, err = tx.Exec(`
			INSERT INTO poller_state (namespace, target, poller, oper_state, health_state)
			VALUES (?, ?, ?, 'stopped', 'unknown')
		`, p.Namespace, p.Target, p.Name)
		if err != nil {
			return fmt.Errorf("insert poller state: %w", err)
		}

		// Initialize poller stats
		_, err = tx.Exec(`
			INSERT INTO poller_stats (namespace, target, poller)
			VALUES (?, ?, ?)
		`, p.Namespace, p.Target, p.Name)
		if err != nil {
			return fmt.Errorf("insert poller stats: %w", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	p.Version = 1
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

// GetPoller retrieves a poller by namespace, target, and name.
func (s *Store) GetPoller(namespace, target, name string) (*Poller, error) {
	var p Poller
	var protocolConfig, pollingConfig sql.NullString

	err := s.db.QueryRow(`
		SELECT namespace, target, name, description, protocol, protocol_config, 
		       polling_config, admin_state, created_at, updated_at, version
		FROM pollers WHERE namespace = ? AND target = ? AND name = ?
	`, namespace, target, name).Scan(
		&p.Namespace, &p.Target, &p.Name, &p.Description, &p.Protocol,
		&protocolConfig, &pollingConfig, &p.AdminState, &p.CreatedAt, &p.UpdatedAt, &p.Version)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query poller: %w", err)
	}

	if protocolConfig.Valid {
		p.ProtocolConfig = json.RawMessage(protocolConfig.String)
	}

	if pollingConfig.Valid && pollingConfig.String != "" {
		p.PollingConfig = &PollingConfig{}
		if err := json.Unmarshal([]byte(pollingConfig.String), p.PollingConfig); err != nil {
			return nil, fmt.Errorf("unmarshal polling config: %w", err)
		}
	}

	return &p, nil
}

// ListPollers returns all pollers for a target.
func (s *Store) ListPollers(namespace, target string) ([]*Poller, error) {
	rows, err := s.db.Query(`
		SELECT namespace, target, name, description, protocol, protocol_config, 
		       polling_config, admin_state, created_at, updated_at, version
		FROM pollers WHERE namespace = ? AND target = ? ORDER BY name
	`, namespace, target)
	if err != nil {
		return nil, fmt.Errorf("query pollers: %w", err)
	}
	defer rows.Close()

	return scanPollers(rows)
}

// ListPollersInNamespace returns all pollers in a namespace.
func (s *Store) ListPollersInNamespace(namespace string) ([]*Poller, error) {
	rows, err := s.db.Query(`
		SELECT namespace, target, name, description, protocol, protocol_config, 
		       polling_config, admin_state, created_at, updated_at, version
		FROM pollers WHERE namespace = ? ORDER BY target, name
	`, namespace)
	if err != nil {
		return nil, fmt.Errorf("query pollers: %w", err)
	}
	defer rows.Close()

	return scanPollers(rows)
}

// ListAllPollers returns all pollers.
func (s *Store) ListAllPollers() ([]*Poller, error) {
	rows, err := s.db.Query(`
		SELECT namespace, target, name, description, protocol, protocol_config, 
		       polling_config, admin_state, created_at, updated_at, version
		FROM pollers ORDER BY namespace, target, name
	`)
	if err != nil {
		return nil, fmt.Errorf("query pollers: %w", err)
	}
	defer rows.Close()

	return scanPollers(rows)
}

func scanPollers(rows *sql.Rows) ([]*Poller, error) {
	var pollers []*Poller
	for rows.Next() {
		var p Poller
		var protocolConfig, pollingConfig sql.NullString

		if err := rows.Scan(
			&p.Namespace, &p.Target, &p.Name, &p.Description, &p.Protocol,
			&protocolConfig, &pollingConfig, &p.AdminState, &p.CreatedAt, &p.UpdatedAt, &p.Version,
		); err != nil {
			return nil, fmt.Errorf("scan poller: %w", err)
		}

		if protocolConfig.Valid {
			p.ProtocolConfig = json.RawMessage(protocolConfig.String)
		}

		if pollingConfig.Valid && pollingConfig.String != "" {
			p.PollingConfig = &PollingConfig{}
			if err := json.Unmarshal([]byte(pollingConfig.String), p.PollingConfig); err != nil {
				return nil, fmt.Errorf("unmarshal polling config: %w", err)
			}
		}

		pollers = append(pollers, &p)
	}

	return pollers, rows.Err()
}

// UpdatePoller updates a poller with optimistic locking.
func (s *Store) UpdatePoller(p *Poller) error {
	pollingJSON, err := json.Marshal(p.PollingConfig)
	if err != nil {
		return fmt.Errorf("marshal polling config: %w", err)
	}

	result, err := s.db.Exec(`
		UPDATE pollers 
		SET description = ?, protocol_config = ?, polling_config = ?, 
		    admin_state = ?, updated_at = ?, version = version + 1
		WHERE namespace = ? AND target = ? AND name = ? AND version = ?
	`, p.Description, string(p.ProtocolConfig), string(pollingJSON),
		p.AdminState, time.Now(), p.Namespace, p.Target, p.Name, p.Version)

	if err != nil {
		return fmt.Errorf("update poller: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrConcurrentModification
	}

	p.Version++
	p.UpdatedAt = time.Now()
	return nil
}

// UpdatePollerAdminState updates only the admin state.
func (s *Store) UpdatePollerAdminState(namespace, target, name, adminState string) error {
	_, err := s.db.Exec(`
		UPDATE pollers SET admin_state = ?, updated_at = ?
		WHERE namespace = ? AND target = ? AND name = ?
	`, adminState, time.Now(), namespace, target, name)
	return err
}

// DeletePoller deletes a poller.
func (s *Store) DeletePoller(namespace, target, name string) (linksDeleted int, err error) {
	err = s.Transaction(func(tx *sql.Tx) error {
		// Count links
		err := tx.QueryRow(`
			SELECT COUNT(*) FROM tree_nodes 
			WHERE namespace = ? AND link_ref = ?
		`, namespace, fmt.Sprintf("poller:%s/%s", target, name)).Scan(&linksDeleted)
		if err != nil {
			return err
		}

		// Delete samples
		_, err = tx.Exec(`
			DELETE FROM samples WHERE namespace = ? AND target = ? AND poller = ?
		`, namespace, target, name)
		if err != nil {
			return err
		}

		// Delete state
		_, err = tx.Exec(`
			DELETE FROM poller_state WHERE namespace = ? AND target = ? AND poller = ?
		`, namespace, target, name)
		if err != nil {
			return err
		}

		// Delete stats
		_, err = tx.Exec(`
			DELETE FROM poller_stats WHERE namespace = ? AND target = ? AND poller = ?
		`, namespace, target, name)
		if err != nil {
			return err
		}

		// Delete tree links
		_, err = tx.Exec(`
			DELETE FROM tree_nodes WHERE namespace = ? AND link_ref = ?
		`, namespace, fmt.Sprintf("poller:%s/%s", target, name))
		if err != nil {
			return err
		}

		// Delete poller
		_, err = tx.Exec(`
			DELETE FROM pollers WHERE namespace = ? AND target = ? AND name = ?
		`, namespace, target, name)
		return err
	})

	return linksDeleted, err
}

// PollerExists checks if a poller exists.
func (s *Store) PollerExists(namespace, target, name string) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM pollers WHERE namespace = ? AND target = ? AND name = ?
	`, namespace, target, name).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ============================================================================
// Poller State
// ============================================================================

// GetPollerState retrieves the runtime state of a poller.
func (s *Store) GetPollerState(namespace, target, poller string) (*PollerState, error) {
	var state PollerState
	var lastError sql.NullString
	var lastPollAt, lastSuccessAt, lastFailureAt sql.NullTime

	err := s.db.QueryRow(`
		SELECT namespace, target, poller, oper_state, health_state, last_error,
		       consecutive_failures, last_poll_at, last_success_at, last_failure_at
		FROM poller_state WHERE namespace = ? AND target = ? AND poller = ?
	`, namespace, target, poller).Scan(
		&state.Namespace, &state.Target, &state.Poller, &state.OperState, &state.HealthState,
		&lastError, &state.ConsecutiveFailures, &lastPollAt, &lastSuccessAt, &lastFailureAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query poller state: %w", err)
	}

	if lastError.Valid {
		state.LastError = lastError.String
	}
	if lastPollAt.Valid {
		state.LastPollAt = &lastPollAt.Time
	}
	if lastSuccessAt.Valid {
		state.LastSuccessAt = &lastSuccessAt.Time
	}
	if lastFailureAt.Valid {
		state.LastFailureAt = &lastFailureAt.Time
	}

	return &state, nil
}

// UpdatePollerState updates the runtime state of a poller.
func (s *Store) UpdatePollerState(state *PollerState) error {
	_, err := s.db.Exec(`
		UPDATE poller_state 
		SET oper_state = ?, health_state = ?, last_error = ?, consecutive_failures = ?,
		    last_poll_at = ?, last_success_at = ?, last_failure_at = ?
		WHERE namespace = ? AND target = ? AND poller = ?
	`, state.OperState, state.HealthState, state.LastError, state.ConsecutiveFailures,
		state.LastPollAt, state.LastSuccessAt, state.LastFailureAt,
		state.Namespace, state.Target, state.Poller)
	return err
}

// BatchUpdatePollerStates updates multiple poller states in one transaction.
func (s *Store) BatchUpdatePollerStates(states []*PollerState) error {
	if len(states) == 0 {
		return nil
	}

	return s.Transaction(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			UPDATE poller_state 
			SET oper_state = ?, health_state = ?, last_error = ?, consecutive_failures = ?,
			    last_poll_at = ?, last_success_at = ?, last_failure_at = ?
			WHERE namespace = ? AND target = ? AND poller = ?
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, state := range states {
			_, err = stmt.Exec(
				state.OperState, state.HealthState, state.LastError, state.ConsecutiveFailures,
				state.LastPollAt, state.LastSuccessAt, state.LastFailureAt,
				state.Namespace, state.Target, state.Poller)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ============================================================================
// Poller Stats
// ============================================================================

// GetPollerStats retrieves statistics for a poller.
func (s *Store) GetPollerStats(namespace, target, poller string) (*PollerStatsRecord, error) {
	var stats PollerStatsRecord
	var pollMsMin, pollMsMax sql.NullInt32

	err := s.db.QueryRow(`
		SELECT namespace, target, poller, polls_total, polls_success, polls_failed, polls_timeout,
		       poll_ms_sum, poll_ms_min, poll_ms_max, poll_ms_count
		FROM poller_stats WHERE namespace = ? AND target = ? AND poller = ?
	`, namespace, target, poller).Scan(
		&stats.Namespace, &stats.Target, &stats.Poller,
		&stats.PollsTotal, &stats.PollsSuccess, &stats.PollsFailed, &stats.PollsTimeout,
		&stats.PollMsSum, &pollMsMin, &pollMsMax, &stats.PollMsCount)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query poller stats: %w", err)
	}

	if pollMsMin.Valid {
		v := int(pollMsMin.Int32)
		stats.PollMsMin = &v
	}
	if pollMsMax.Valid {
		v := int(pollMsMax.Int32)
		stats.PollMsMax = &v
	}

	return &stats, nil
}

// UpdatePollerStats updates statistics for a poller.
func (s *Store) UpdatePollerStats(stats *PollerStatsRecord) error {
	_, err := s.db.Exec(`
		UPDATE poller_stats 
		SET polls_total = ?, polls_success = ?, polls_failed = ?, polls_timeout = ?,
		    poll_ms_sum = ?, poll_ms_min = ?, poll_ms_max = ?, poll_ms_count = ?
		WHERE namespace = ? AND target = ? AND poller = ?
	`, stats.PollsTotal, stats.PollsSuccess, stats.PollsFailed, stats.PollsTimeout,
		stats.PollMsSum, stats.PollMsMin, stats.PollMsMax, stats.PollMsCount,
		stats.Namespace, stats.Target, stats.Poller)
	return err
}

// BatchUpdatePollerStats updates multiple poller stats in one transaction.
func (s *Store) BatchUpdatePollerStats(statsList []*PollerStatsRecord) error {
	if len(statsList) == 0 {
		return nil
	}

	return s.Transaction(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			UPDATE poller_stats 
			SET polls_total = ?, polls_success = ?, polls_failed = ?, polls_timeout = ?,
			    poll_ms_sum = ?, poll_ms_min = ?, poll_ms_max = ?, poll_ms_count = ?
			WHERE namespace = ? AND target = ? AND poller = ?
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, stats := range statsList {
			_, err = stmt.Exec(
				stats.PollsTotal, stats.PollsSuccess, stats.PollsFailed, stats.PollsTimeout,
				stats.PollMsSum, stats.PollMsMin, stats.PollMsMax, stats.PollMsCount,
				stats.Namespace, stats.Target, stats.Poller)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
