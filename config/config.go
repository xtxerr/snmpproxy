// Package config provides configuration for snmpproxyd.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Server holds server configuration.
type Server struct {
	Listen     string        `yaml:"listen"`
	TLS        TLS           `yaml:"tls"`
	Auth       Auth          `yaml:"auth"`
	SNMP       SNMPDefaults  `yaml:"snmp"`
	Poller     Poller        `yaml:"poller"`
	Session    Session       `yaml:"session"`
}

type TLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type Auth struct {
	Tokens []Token `yaml:"tokens"`
}

type Token struct {
	ID    string `yaml:"id"`
	Token string `yaml:"token"`
}

type SNMPDefaults struct {
	TimeoutMs  uint32 `yaml:"timeout_ms"`
	Retries    uint32 `yaml:"retries"`
	BufferSize uint32 `yaml:"buffer_size"`
}

type Poller struct {
	Workers   int `yaml:"workers"`
	QueueSize int `yaml:"queue_size"`
}

type Session struct {
	AuthTimeoutSec     int `yaml:"auth_timeout_sec"`
	ReconnectWindowSec int `yaml:"reconnect_window_sec"`
}

// Default returns a Server config with sensible defaults.
func Default() *Server {
	return &Server{
		Listen: "0.0.0.0:9161",
		TLS: TLS{
			CertFile: "certs/server.crt",
			KeyFile:  "certs/server.key",
		},
		SNMP: SNMPDefaults{
			TimeoutMs:  5000,
			Retries:    2,
			BufferSize: 3600,
		},
		Poller: Poller{
			Workers:   100,
			QueueSize: 10000,
		},
		Session: Session{
			AuthTimeoutSec:     30,
			ReconnectWindowSec: 600,
		},
	}
}

// Load reads configuration from a YAML file.
func Load(path string) (*Server, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
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
func (c *Server) Validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen address required")
	}
	if len(c.Auth.Tokens) == 0 {
		return fmt.Errorf("at least one auth token required")
	}
	for i, t := range c.Auth.Tokens {
		if t.Token == "" {
			return fmt.Errorf("token %d has empty value", i)
		}
	}
	return nil
}

// TLSEnabled returns true if TLS cert and key are configured.
func (c *Server) TLSEnabled() bool {
	return c.TLS.CertFile != "" && c.TLS.KeyFile != ""
}

// AuthTimeout returns the auth timeout as a duration.
func (c *Server) AuthTimeout() time.Duration {
	return time.Duration(c.Session.AuthTimeoutSec) * time.Second
}

// ReconnectWindow returns the reconnect window as a duration.
func (c *Server) ReconnectWindow() time.Duration {
	return time.Duration(c.Session.ReconnectWindowSec) * time.Second
}
