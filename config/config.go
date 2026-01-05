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
