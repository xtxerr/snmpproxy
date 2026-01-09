// Package store provides persistent storage using DuckDB.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/marcboeker/go-duckdb"
)

// Store provides access to the DuckDB database.
type Store struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex

	// Encryption key for secrets
	secretKey []byte
}

// Config holds store configuration.
type Config struct {
	DBPath        string
	SecretKeyPath string
	InMemory      bool // For testing
}

// New creates a new store.
func New(cfg *Config) (*Store, error) {
	var dsn string
	if cfg.InMemory {
		dsn = ":memory:"
	} else {
		// Ensure directory exists
		dir := filepath.Dir(cfg.DBPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
		dsn = cfg.DBPath
	}

	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	s := &Store{
		db:     db,
		dbPath: cfg.DBPath,
	}

	// Load secret key if provided
	if cfg.SecretKeyPath != "" {
		key, err := os.ReadFile(cfg.SecretKeyPath)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("read secret key: %w", err)
		}
		if len(key) != 32 {
			db.Close()
			return nil, fmt.Errorf("secret key must be 32 bytes, got %d", len(key))
		}
		s.secretKey = key
	}

	// Initialize schema
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// initSchema creates all required tables.
func (s *Store) initSchema() error {
	schema := `
	-- Namespaces
	CREATE TABLE IF NOT EXISTS namespaces (
		name VARCHAR PRIMARY KEY,
		description VARCHAR,
		config JSON,
		created_at TIMESTAMP DEFAULT now(),
		updated_at TIMESTAMP DEFAULT now(),
		version INTEGER DEFAULT 1
	);

	-- Targets
	CREATE TABLE IF NOT EXISTS targets (
		namespace VARCHAR NOT NULL,
		name VARCHAR NOT NULL,
		description VARCHAR,
		labels JSON,
		config JSON,
		created_at TIMESTAMP DEFAULT now(),
		updated_at TIMESTAMP DEFAULT now(),
		version INTEGER DEFAULT 1,
		PRIMARY KEY (namespace, name)
	);

	-- Pollers
	CREATE TABLE IF NOT EXISTS pollers (
		namespace VARCHAR NOT NULL,
		target VARCHAR NOT NULL,
		name VARCHAR NOT NULL,
		description VARCHAR,
		protocol VARCHAR NOT NULL,
		protocol_config JSON NOT NULL,
		polling_config JSON,
		admin_state VARCHAR DEFAULT 'disabled',
		created_at TIMESTAMP DEFAULT now(),
		updated_at TIMESTAMP DEFAULT now(),
		version INTEGER DEFAULT 1,
		PRIMARY KEY (namespace, target, name)
	);

	-- Poller State (runtime, but persisted for recovery)
	CREATE TABLE IF NOT EXISTS poller_state (
		namespace VARCHAR NOT NULL,
		target VARCHAR NOT NULL,
		poller VARCHAR NOT NULL,
		oper_state VARCHAR DEFAULT 'stopped',
		health_state VARCHAR DEFAULT 'unknown',
		last_error VARCHAR,
		consecutive_failures INTEGER DEFAULT 0,
		last_poll_at TIMESTAMP,
		last_success_at TIMESTAMP,
		last_failure_at TIMESTAMP,
		PRIMARY KEY (namespace, target, poller)
	);

	-- Poller Stats
	CREATE TABLE IF NOT EXISTS poller_stats (
		namespace VARCHAR NOT NULL,
		target VARCHAR NOT NULL,
		poller VARCHAR NOT NULL,
		polls_total BIGINT DEFAULT 0,
		polls_success BIGINT DEFAULT 0,
		polls_failed BIGINT DEFAULT 0,
		polls_timeout BIGINT DEFAULT 0,
		poll_ms_sum BIGINT DEFAULT 0,
		poll_ms_min INTEGER,
		poll_ms_max INTEGER,
		poll_ms_count INTEGER DEFAULT 0,
		PRIMARY KEY (namespace, target, poller)
	);

	-- Samples (Time-Series)
	CREATE TABLE IF NOT EXISTS samples (
		namespace VARCHAR NOT NULL,
		target VARCHAR NOT NULL,
		poller VARCHAR NOT NULL,
		timestamp_ms BIGINT NOT NULL,
		value_counter UBIGINT,
		value_text VARCHAR,
		value_gauge DOUBLE,
		valid BOOLEAN,
		error VARCHAR,
		poll_ms INTEGER
	);

	-- Index for sample queries
	CREATE INDEX IF NOT EXISTS idx_samples_lookup 
		ON samples(namespace, target, poller, timestamp_ms DESC);

	-- Tree Nodes (Symlink System)
	CREATE TABLE IF NOT EXISTS tree_nodes (
		namespace VARCHAR NOT NULL,
		path VARCHAR NOT NULL,
		node_type VARCHAR NOT NULL,
		link_ref VARCHAR,
		description VARCHAR,
		created_at TIMESTAMP DEFAULT now(),
		PRIMARY KEY (namespace, path)
	);

	-- Index for tree parent lookup
	CREATE INDEX IF NOT EXISTS idx_tree_parent 
		ON tree_nodes(namespace, path);

	-- Secrets (encrypted)
	CREATE TABLE IF NOT EXISTS secrets (
		namespace VARCHAR NOT NULL,
		name VARCHAR NOT NULL,
		secret_type VARCHAR NOT NULL,
		encrypted_value BLOB NOT NULL,
		nonce BLOB NOT NULL,
		created_at TIMESTAMP DEFAULT now(),
		updated_at TIMESTAMP DEFAULT now(),
		PRIMARY KEY (namespace, name)
	);

	-- Users
	CREATE TABLE IF NOT EXISTS users (
		id VARCHAR PRIMARY KEY,
		token_hash VARCHAR NOT NULL,
		roles JSON,
		created_at TIMESTAMP DEFAULT now(),
		last_login_at TIMESTAMP
	);

	-- Roles
	CREATE TABLE IF NOT EXISTS roles (
		name VARCHAR PRIMARY KEY,
		namespaces JSON,
		permissions JSON
	);

	-- Server defaults (singleton)
	CREATE TABLE IF NOT EXISTS server_config (
		id INTEGER PRIMARY KEY DEFAULT 1,
		default_timeout_ms INTEGER DEFAULT 5000,
		default_retries INTEGER DEFAULT 2,
		default_interval_ms INTEGER DEFAULT 1000,
		default_buffer_size INTEGER DEFAULT 3600,
		CHECK (id = 1)
	);

	-- Insert default server config if not exists
	INSERT INTO server_config (id) 
		SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM server_config);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Transaction executes a function within a transaction.
func (s *Store) Transaction(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// DB returns the underlying database connection.
// Use with caution - prefer using the typed methods.
func (s *Store) DB() *sql.DB {
	return s.db
}
