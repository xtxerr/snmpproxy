// Package config provides configuration for snmpproxyd.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure.
type Config struct {
	Server  ServerConfig   `yaml:"server"`
	TLS     TLSConfig      `yaml:"tls"`
	Auth    AuthConfig     `yaml:"auth"`
	Session SessionConfig  `yaml:"session"`
	Poller  PollerConfig   `yaml:"poller"`
	Storage StorageConfig  `yaml:"storage"`
	SNMP    SNMPDefaults   `yaml:"snmp"`
	Targets []TargetConfig `yaml:"targets"`
}

// ServerConfig holds server settings.
type ServerConfig struct {
	Listen string `yaml:"listen"`
}

// TLSConfig holds TLS settings.
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	Tokens []TokenConfig `yaml:"tokens"`
}

// TokenConfig defines an auth token.
type TokenConfig struct {
	ID    string `yaml:"id"`
	Token string `yaml:"token"`
}

// SessionConfig holds session settings.
type SessionConfig struct {
	AuthTimeoutSec     int `yaml:"auth_timeout_sec"`
	ReconnectWindowSec int `yaml:"reconnect_window_sec"`
}

// PollerConfig holds poller settings.
type PollerConfig struct {
	Workers   int `yaml:"workers"`
	QueueSize int `yaml:"queue_size"`
}

// StorageConfig holds storage and persistence settings.
//
// The storage subsystem uses batched writes to optimize database performance.
// Samples are accumulated in memory and flushed to the database either when
// the batch size is reached OR the flush timeout expires, whichever comes first.
//
// Tuning Guidelines:
//   - High-throughput (>10k samples/s): Increase batch_size to 5000-10000
//   - Low-latency requirements: Decrease flush_timeout_sec to 1-2
//   - Memory-constrained: Decrease batch_size to 500-1000
//   - High-reliability: Decrease both for more frequent persistence
type StorageConfig struct {
	// DBPath is the path to the DuckDB database file.
	// Default: "data/snmpproxy.db"
	DBPath string `yaml:"db_path"`

	// SecretKeyPath is the path to the 32-byte encryption key for secrets.
	// If not set, secret encryption is disabled.
	// Generate with: openssl rand -out secret.key 32
	SecretKeyPath string `yaml:"secret_key_path"`

	// SampleBatchSize is the number of samples to accumulate before writing to DB.
	// Larger values improve write throughput but increase memory usage and
	// potential data loss on crash.
	// Range: 100-10000, Default: 1000
	SampleBatchSize int `yaml:"sample_batch_size"`

	// SampleFlushTimeoutSec is the maximum time to hold samples before flushing.
	// Samples are flushed when batch_size is reached OR this timeout expires.
	// Lower values reduce data loss risk but increase DB write frequency.
	// Range: 1-60, Default: 5
	SampleFlushTimeoutSec int `yaml:"sample_flush_timeout_sec"`

	// StateFlushIntervalSec is how often to persist poller state to DB.
	// Poller state includes: oper_state, health_state, last_error, etc.
	// Range: 1-60, Default: 5
	StateFlushIntervalSec int `yaml:"state_flush_interval_sec"`

	// StatsFlushIntervalSec is how often to persist poller statistics to DB.
	// Statistics include: polls_total, polls_success, poll timing, etc.
	// Range: 1-120, Default: 10
	StatsFlushIntervalSec int `yaml:"stats_flush_interval_sec"`
}

// SNMPDefaults holds default SNMP settings for new targets.
type SNMPDefaults struct {
	TimeoutMs  uint32 `yaml:"timeout_ms"`
	Retries    uint32 `yaml:"retries"`
	BufferSize uint32 `yaml:"buffer_size"`
}

// TargetConfig defines a target in the config file.
type TargetConfig struct {
	ID          string            `yaml:"id"`
	Description string            `yaml:"description"`
	Tags        []string          `yaml:"tags"`
	IntervalMs  uint32            `yaml:"interval_ms"`
	BufferSize  uint32            `yaml:"buffer_size"`
	SNMP        *SNMPTargetConfig `yaml:"snmp,omitempty"`
	// Future: HTTP *HTTPTargetConfig `yaml:"http,omitempty"`
}

// SNMPTargetConfig defines SNMP-specific target configuration.
type SNMPTargetConfig struct {
	Host      string `yaml:"host"`
	Port      uint16 `yaml:"port"`
	OID       string `yaml:"oid"`
	TimeoutMs uint32 `yaml:"timeout_ms"`
	Retries   uint32 `yaml:"retries"`

	// SNMPv2c
	Community string `yaml:"community,omitempty"`

	// SNMPv3
	Version       int    `yaml:"version,omitempty"`
	SecurityName  string `yaml:"security_name,omitempty"`
	SecurityLevel string `yaml:"security_level,omitempty"`
	AuthProtocol  string `yaml:"auth_protocol,omitempty"`
	AuthPassword  string `yaml:"auth_password,omitempty"`
	PrivProtocol  string `yaml:"priv_protocol,omitempty"`
	PrivPassword  string `yaml:"priv_password,omitempty"`
	ContextName   string `yaml:"context_name,omitempty"`
}

// IsV3 returns true if this is an SNMPv3 configuration.
func (c *SNMPTargetConfig) IsV3() bool {
	return c.Version == 3 || c.SecurityName != ""
}

// Protocol returns the protocol name for the target config.
func (tc *TargetConfig) Protocol() string {
	if tc.SNMP != nil {
		return "snmp"
	}
	return ""
}

// Validate checks the target configuration.
func (tc *TargetConfig) Validate() error {
	if tc.ID == "" {
		return fmt.Errorf("target id required")
	}

	protocols := 0
	if tc.SNMP != nil {
		protocols++
	}

	if protocols == 0 {
		return fmt.Errorf("target %s: no protocol specified", tc.ID)
	}
	if protocols > 1 {
		return fmt.Errorf("target %s: multiple protocols specified", tc.ID)
	}

	if tc.SNMP != nil {
		if err := tc.SNMP.Validate(); err != nil {
			return fmt.Errorf("target %s: %w", tc.ID, err)
		}
	}

	for _, tag := range tc.Tags {
		if tag == "" {
			return fmt.Errorf("target %s: empty tag not allowed", tc.ID)
		}
	}

	return nil
}

// Validate checks the SNMP target configuration.
func (c *SNMPTargetConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("snmp.host required")
	}
	if c.OID == "" {
		return fmt.Errorf("snmp.oid required")
	}

	if c.IsV3() {
		if c.SecurityName == "" {
			return fmt.Errorf("snmp.security_name required for v3")
		}

		switch c.SecurityLevel {
		case "", "noAuthNoPriv":
			// OK, no credentials needed
		case "authNoPriv":
			if c.AuthProtocol == "" {
				return fmt.Errorf("snmp.auth_protocol required for authNoPriv")
			}
			if c.AuthPassword == "" {
				return fmt.Errorf("snmp.auth_password required for authNoPriv")
			}
		case "authPriv":
			if c.AuthProtocol == "" {
				return fmt.Errorf("snmp.auth_protocol required for authPriv")
			}
			if c.AuthPassword == "" {
				return fmt.Errorf("snmp.auth_password required for authPriv")
			}
			if c.PrivProtocol == "" {
				return fmt.Errorf("snmp.priv_protocol required for authPriv")
			}
			if c.PrivPassword == "" {
				return fmt.Errorf("snmp.priv_password required for authPriv")
			}
		default:
			return fmt.Errorf("invalid snmp.security_level: %s (valid: noAuthNoPriv, authNoPriv, authPriv)", c.SecurityLevel)
		}
	}

	return nil
}

// Default returns a Config with sensible defaults.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Listen: "0.0.0.0:9161",
		},
		TLS: TLSConfig{
			CertFile: "certs/server.crt",
			KeyFile:  "certs/server.key",
		},
		Session: SessionConfig{
			AuthTimeoutSec:     30,
			ReconnectWindowSec: 600,
		},
		Poller: PollerConfig{
			Workers:   100,
			QueueSize: 10000,
		},
		Storage: StorageConfig{
			DBPath:                "data/snmpproxy.db",
			SampleBatchSize:       1000,
			SampleFlushTimeoutSec: 5,
			StateFlushIntervalSec: 5,
			StatsFlushIntervalSec: 10,
		},
		SNMP: SNMPDefaults{
			TimeoutMs:  5000,
			Retries:    2,
			BufferSize: 3600,
		},
	}
}

// Load reads configuration from a YAML file.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	expanded := os.ExpandEnv(string(data))
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks configuration for errors.
func (c *Config) Validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen required")
	}

	if len(c.Auth.Tokens) == 0 {
		return fmt.Errorf("at least one auth token required")
	}

	for i, t := range c.Auth.Tokens {
		if t.Token == "" {
			return fmt.Errorf("auth.tokens[%d] has empty token", i)
		}
	}

	// Validate storage settings
	if err := c.Storage.Validate(); err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	// Validate targets
	targetIDs := make(map[string]bool)
	for i, tc := range c.Targets {
		if err := tc.Validate(); err != nil {
			return fmt.Errorf("targets[%d]: %w", i, err)
		}
		if targetIDs[tc.ID] {
			return fmt.Errorf("targets[%d]: duplicate id %q", i, tc.ID)
		}
		targetIDs[tc.ID] = true
	}

	return nil
}

// Validate checks storage configuration for errors.
func (sc *StorageConfig) Validate() error {
	if sc.SampleBatchSize < 100 {
		return fmt.Errorf("sample_batch_size must be >= 100 (got %d)", sc.SampleBatchSize)
	}
	if sc.SampleBatchSize > 10000 {
		return fmt.Errorf("sample_batch_size must be <= 10000 (got %d)", sc.SampleBatchSize)
	}

	if sc.SampleFlushTimeoutSec < 1 {
		return fmt.Errorf("sample_flush_timeout_sec must be >= 1 (got %d)", sc.SampleFlushTimeoutSec)
	}
	if sc.SampleFlushTimeoutSec > 60 {
		return fmt.Errorf("sample_flush_timeout_sec must be <= 60 (got %d)", sc.SampleFlushTimeoutSec)
	}

	if sc.StateFlushIntervalSec < 1 {
		return fmt.Errorf("state_flush_interval_sec must be >= 1 (got %d)", sc.StateFlushIntervalSec)
	}
	if sc.StateFlushIntervalSec > 60 {
		return fmt.Errorf("state_flush_interval_sec must be <= 60 (got %d)", sc.StateFlushIntervalSec)
	}

	if sc.StatsFlushIntervalSec < 1 {
		return fmt.Errorf("stats_flush_interval_sec must be >= 1 (got %d)", sc.StatsFlushIntervalSec)
	}
	if sc.StatsFlushIntervalSec > 120 {
		return fmt.Errorf("stats_flush_interval_sec must be <= 120 (got %d)", sc.StatsFlushIntervalSec)
	}

	return nil
}

// SampleFlushTimeout returns the sample flush timeout as a duration.
func (sc *StorageConfig) SampleFlushTimeout() time.Duration {
	return time.Duration(sc.SampleFlushTimeoutSec) * time.Second
}

// StateFlushInterval returns the state flush interval as a duration.
func (sc *StorageConfig) StateFlushInterval() time.Duration {
	return time.Duration(sc.StateFlushIntervalSec) * time.Second
}

// StatsFlushInterval returns the stats flush interval as a duration.
func (sc *StorageConfig) StatsFlushInterval() time.Duration {
	return time.Duration(sc.StatsFlushIntervalSec) * time.Second
}

// TLSEnabled returns true if TLS cert and key are configured.
func (c *Config) TLSEnabled() bool {
	return c.TLS.CertFile != "" && c.TLS.KeyFile != ""
}

// AuthTimeout returns the auth timeout as a duration.
func (c *Config) AuthTimeout() time.Duration {
	return time.Duration(c.Session.AuthTimeoutSec) * time.Second
}

// ReconnectWindow returns the reconnect window as a duration.
func (c *Config) ReconnectWindow() time.Duration {
	return time.Duration(c.Session.ReconnectWindowSec) * time.Second
}
