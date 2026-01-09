package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Target represents a target in the store.
type Target struct {
	Namespace   string
	Name        string
	Description string
	Labels      map[string]string
	Config      *TargetConfig
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int
}

// TargetConfig holds target configuration.
type TargetConfig struct {
	Defaults *PollerDefaults `json:"defaults,omitempty"`
}

// CreateTarget creates a new target.
func (s *Store) CreateTarget(t *Target) error {
	labelsJSON, err := json.Marshal(t.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}

	configJSON, err := json.Marshal(t.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO targets (namespace, name, description, labels, config, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)
	`, t.Namespace, t.Name, t.Description, string(labelsJSON), string(configJSON), time.Now(), time.Now())

	if err != nil {
		return fmt.Errorf("insert target: %w", err)
	}

	t.Version = 1
	t.CreatedAt = time.Now()
	t.UpdatedAt = time.Now()
	return nil
}

// GetTarget retrieves a target by namespace and name.
func (s *Store) GetTarget(namespace, name string) (*Target, error) {
	var t Target
	var labelsJSON, configJSON sql.NullString

	err := s.db.QueryRow(`
		SELECT namespace, name, description, labels, config, created_at, updated_at, version
		FROM targets WHERE namespace = ? AND name = ?
	`, namespace, name).Scan(&t.Namespace, &t.Name, &t.Description, &labelsJSON, &configJSON,
		&t.CreatedAt, &t.UpdatedAt, &t.Version)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query target: %w", err)
	}

	if labelsJSON.Valid && labelsJSON.String != "" {
		if err := json.Unmarshal([]byte(labelsJSON.String), &t.Labels); err != nil {
			return nil, fmt.Errorf("unmarshal labels: %w", err)
		}
	}

	if configJSON.Valid && configJSON.String != "" {
		t.Config = &TargetConfig{}
		if err := json.Unmarshal([]byte(configJSON.String), t.Config); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	}

	return &t, nil
}

// ListTargets returns all targets in a namespace.
func (s *Store) ListTargets(namespace string) ([]*Target, error) {
	rows, err := s.db.Query(`
		SELECT namespace, name, description, labels, config, created_at, updated_at, version
		FROM targets WHERE namespace = ? ORDER BY name
	`, namespace)
	if err != nil {
		return nil, fmt.Errorf("query targets: %w", err)
	}
	defer rows.Close()

	var targets []*Target
	for rows.Next() {
		var t Target
		var labelsJSON, configJSON sql.NullString

		if err := rows.Scan(&t.Namespace, &t.Name, &t.Description, &labelsJSON, &configJSON,
			&t.CreatedAt, &t.UpdatedAt, &t.Version); err != nil {
			return nil, fmt.Errorf("scan target: %w", err)
		}

		if labelsJSON.Valid && labelsJSON.String != "" {
			if err := json.Unmarshal([]byte(labelsJSON.String), &t.Labels); err != nil {
				return nil, fmt.Errorf("unmarshal labels: %w", err)
			}
		}

		if configJSON.Valid && configJSON.String != "" {
			t.Config = &TargetConfig{}
			if err := json.Unmarshal([]byte(configJSON.String), t.Config); err != nil {
				return nil, fmt.Errorf("unmarshal config: %w", err)
			}
		}

		targets = append(targets, &t)
	}

	return targets, rows.Err()
}

// ListTargetsFiltered returns targets matching filters.
func (s *Store) ListTargetsFiltered(namespace string, labels map[string]string, limit int, cursor string) ([]*Target, string, error) {
	// Build query with label filters
	query := `
		SELECT namespace, name, description, labels, config, created_at, updated_at, version
		FROM targets WHERE namespace = ?
	`
	args := []interface{}{namespace}

	// Add label filters (using JSON extraction)
	for k, v := range labels {
		query += fmt.Sprintf(` AND json_extract_string(labels, '$.%s') = ?`, k)
		args = append(args, v)
	}

	// Pagination
	if cursor != "" {
		query += ` AND name > ?`
		args = append(args, cursor)
	}

	query += ` ORDER BY name`
	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit+1) // +1 to check for more
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query targets: %w", err)
	}
	defer rows.Close()

	var targets []*Target
	for rows.Next() {
		var t Target
		var labelsJSON, configJSON sql.NullString

		if err := rows.Scan(&t.Namespace, &t.Name, &t.Description, &labelsJSON, &configJSON,
			&t.CreatedAt, &t.UpdatedAt, &t.Version); err != nil {
			return nil, "", fmt.Errorf("scan target: %w", err)
		}

		if labelsJSON.Valid && labelsJSON.String != "" {
			if err := json.Unmarshal([]byte(labelsJSON.String), &t.Labels); err != nil {
				return nil, "", fmt.Errorf("unmarshal labels: %w", err)
			}
		}

		if configJSON.Valid && configJSON.String != "" {
			t.Config = &TargetConfig{}
			if err := json.Unmarshal([]byte(configJSON.String), t.Config); err != nil {
				return nil, "", fmt.Errorf("unmarshal config: %w", err)
			}
		}

		targets = append(targets, &t)
	}

	// Check for more and build next cursor
	var nextCursor string
	if limit > 0 && len(targets) > limit {
		targets = targets[:limit]
		nextCursor = targets[len(targets)-1].Name
	}

	return targets, nextCursor, rows.Err()
}

// UpdateTarget updates a target with optimistic locking.
func (s *Store) UpdateTarget(t *Target) error {
	labelsJSON, err := json.Marshal(t.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}

	configJSON, err := json.Marshal(t.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	result, err := s.db.Exec(`
		UPDATE targets 
		SET description = ?, labels = ?, config = ?, updated_at = ?, version = version + 1
		WHERE namespace = ? AND name = ? AND version = ?
	`, t.Description, string(labelsJSON), string(configJSON), time.Now(),
		t.Namespace, t.Name, t.Version)

	if err != nil {
		return fmt.Errorf("update target: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrConcurrentModification
	}

	t.Version++
	t.UpdatedAt = time.Now()
	return nil
}

// DeleteTarget deletes a target and all its pollers.
func (s *Store) DeleteTarget(namespace, name string, force bool) (pollersDeleted, linksDeleted int, err error) {
	// Check if has pollers
	var pollerCount int
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM pollers WHERE namespace = ? AND target = ?
	`, namespace, name).Scan(&pollerCount)
	if err != nil {
		return 0, 0, fmt.Errorf("count pollers: %w", err)
	}

	if pollerCount > 0 && !force {
		return 0, 0, fmt.Errorf("target has %d pollers (use force to delete)", pollerCount)
	}

	err = s.Transaction(func(tx *sql.Tx) error {
		// Count links
		err := tx.QueryRow(`
			SELECT COUNT(*) FROM tree_nodes 
			WHERE namespace = ? AND (link_ref = ? OR link_ref LIKE ?)
		`, namespace, "target:"+name, "poller:"+name+"/%").Scan(&linksDeleted)
		if err != nil {
			return err
		}

		// Delete samples
		_, err = tx.Exec(`
			DELETE FROM samples WHERE namespace = ? AND target = ?
		`, namespace, name)
		if err != nil {
			return err
		}

		// Delete poller state
		_, err = tx.Exec(`
			DELETE FROM poller_state WHERE namespace = ? AND target = ?
		`, namespace, name)
		if err != nil {
			return err
		}

		// Delete poller stats
		_, err = tx.Exec(`
			DELETE FROM poller_stats WHERE namespace = ? AND target = ?
		`, namespace, name)
		if err != nil {
			return err
		}

		// Delete pollers
		_, err = tx.Exec(`
			DELETE FROM pollers WHERE namespace = ? AND target = ?
		`, namespace, name)
		if err != nil {
			return err
		}

		// Delete tree links pointing to this target or its pollers
		_, err = tx.Exec(`
			DELETE FROM tree_nodes 
			WHERE namespace = ? AND (link_ref = ? OR link_ref LIKE ?)
		`, namespace, "target:"+name, "poller:"+name+"/%")
		if err != nil {
			return err
		}

		// Delete target
		_, err = tx.Exec(`
			DELETE FROM targets WHERE namespace = ? AND name = ?
		`, namespace, name)
		if err != nil {
			return err
		}

		pollersDeleted = pollerCount
		return nil
	})

	return pollersDeleted, linksDeleted, err
}

// TargetExists checks if a target exists.
func (s *Store) TargetExists(namespace, name string) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM targets WHERE namespace = ? AND name = ?
	`, namespace, name).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetTargetStats returns statistics for a target.
func (s *Store) GetTargetStats(namespace, name string) (*TargetStats, error) {
	stats := &TargetStats{}

	// Poller count
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM pollers WHERE namespace = ? AND target = ?
	`, namespace, name).Scan(&stats.PollerCount)
	if err != nil {
		return nil, err
	}

	// Running pollers
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM poller_state 
		WHERE namespace = ? AND target = ? AND oper_state = 'running'
	`, namespace, name).Scan(&stats.PollersRunning)
	if err != nil {
		return nil, err
	}

	// Health stats
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM poller_state 
		WHERE namespace = ? AND target = ? AND health_state = 'up'
	`, namespace, name).Scan(&stats.PollersUp)
	if err != nil {
		return nil, err
	}

	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM poller_state 
		WHERE namespace = ? AND target = ? AND health_state = 'down'
	`, namespace, name).Scan(&stats.PollersDown)
	if err != nil {
		return nil, err
	}

	// Aggregate poll stats
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(polls_total), 0), COALESCE(SUM(polls_success), 0), COALESCE(SUM(polls_failed), 0)
		FROM poller_stats WHERE namespace = ? AND target = ?
	`, namespace, name).Scan(&stats.PollsTotal, &stats.PollsSuccess, &stats.PollsFailed)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// TargetStats holds statistics for a target.
type TargetStats struct {
	PollerCount    int
	PollersRunning int
	PollersUp      int
	PollersDown    int
	PollsTotal     int64
	PollsSuccess   int64
	PollsFailed    int64
}
