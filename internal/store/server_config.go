package store

import "fmt"

// ServerConfig holds server-wide default configuration.
type ServerConfig struct {
	DefaultTimeoutMs  uint32
	DefaultRetries    uint32
	DefaultIntervalMs uint32
	DefaultBufferSize uint32
}

// GetServerConfig retrieves the server configuration.
func (s *Store) GetServerConfig() (*ServerConfig, error) {
	var cfg ServerConfig
	err := s.db.QueryRow(`
		SELECT default_timeout_ms, default_retries, default_interval_ms, default_buffer_size
		FROM server_config WHERE id = 1
	`).Scan(&cfg.DefaultTimeoutMs, &cfg.DefaultRetries, &cfg.DefaultIntervalMs, &cfg.DefaultBufferSize)
	if err != nil {
		return nil, fmt.Errorf("query server config: %w", err)
	}
	return &cfg, nil
}

// UpdateServerConfig updates the server configuration.
func (s *Store) UpdateServerConfig(cfg *ServerConfig) error {
	_, err := s.db.Exec(`
		UPDATE server_config 
		SET default_timeout_ms = ?, default_retries = ?, default_interval_ms = ?, default_buffer_size = ?
		WHERE id = 1
	`, cfg.DefaultTimeoutMs, cfg.DefaultRetries, cfg.DefaultIntervalMs, cfg.DefaultBufferSize)
	return err
}
