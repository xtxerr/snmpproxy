// Package manager provides business logic for entity management.
package manager

import (
	"github.com/xtxerr/snmpproxy/internal/store"
)

// ResolvedPollerConfig holds the fully resolved configuration for a poller.
// Each field tracks its source for display purposes.
type ResolvedPollerConfig struct {
	// SNMP v2c
	Community       string
	CommunitySource string

	// SNMPv3
	SecurityName        string
	SecurityNameSource  string
	SecurityLevel       string
	SecurityLevelSource string
	AuthProtocol        string
	AuthProtocolSource  string
	AuthPassword        string
	AuthPasswordSource  string
	PrivProtocol        string
	PrivProtocolSource  string
	PrivPassword        string
	PrivPasswordSource  string

	// Timing
	IntervalMs       uint32
	IntervalMsSource string
	TimeoutMs        uint32
	TimeoutMsSource  string
	Retries          uint32
	RetriesSource    string
	BufferSize       uint32
	BufferSizeSource string
}

// ConfigResolver resolves inherited configuration for pollers.
type ConfigResolver struct {
	store *store.Store
}

// NewConfigResolver creates a new config resolver.
func NewConfigResolver(s *store.Store) *ConfigResolver {
	return &ConfigResolver{store: s}
}

// Resolve builds the effective configuration for a poller by merging
// server defaults → namespace defaults → target defaults → poller explicit.
func (r *ConfigResolver) Resolve(namespace, target, poller string) (*ResolvedPollerConfig, error) {
	result := &ResolvedPollerConfig{}

	// 1. Server defaults
	serverCfg, err := r.store.GetServerConfig()
	if err != nil {
		return nil, err
	}
	r.applyServerDefaults(result, serverCfg)

	// 2. Namespace defaults
	ns, err := r.store.GetNamespace(namespace)
	if err != nil {
		return nil, err
	}
	if ns != nil && ns.Config != nil && ns.Config.Defaults != nil {
		r.applyDefaults(result, ns.Config.Defaults, "namespace:"+namespace)
	}

	// 3. Target defaults
	t, err := r.store.GetTarget(namespace, target)
	if err != nil {
		return nil, err
	}
	if t != nil && t.Config != nil && t.Config.Defaults != nil {
		r.applyDefaults(result, t.Config.Defaults, "target:"+target)
	}

	// 4. Poller explicit config
	p, err := r.store.GetPoller(namespace, target, poller)
	if err != nil {
		return nil, err
	}
	if p != nil && p.PollingConfig != nil {
		r.applyPollingConfig(result, p.PollingConfig, "explicit")
	}

	return result, nil
}

// ResolveWithPoller builds the effective configuration using provided entities.
// This avoids extra DB lookups when entities are already loaded.
func (r *ConfigResolver) ResolveWithPoller(
	serverCfg *store.ServerConfig,
	ns *store.Namespace,
	t *store.Target,
	p *store.Poller,
) *ResolvedPollerConfig {
	result := &ResolvedPollerConfig{}

	// 1. Server defaults
	if serverCfg != nil {
		r.applyServerDefaults(result, serverCfg)
	}

	// 2. Namespace defaults
	if ns != nil && ns.Config != nil && ns.Config.Defaults != nil {
		r.applyDefaults(result, ns.Config.Defaults, "namespace:"+ns.Name)
	}

	// 3. Target defaults
	if t != nil && t.Config != nil && t.Config.Defaults != nil {
		r.applyDefaults(result, t.Config.Defaults, "target:"+t.Name)
	}

	// 4. Poller explicit config
	if p != nil && p.PollingConfig != nil {
		r.applyPollingConfig(result, p.PollingConfig, "explicit")
	}

	return result
}

func (r *ConfigResolver) applyServerDefaults(result *ResolvedPollerConfig, cfg *store.ServerConfig) {
	result.IntervalMs = cfg.DefaultIntervalMs
	result.IntervalMsSource = "server"
	result.TimeoutMs = cfg.DefaultTimeoutMs
	result.TimeoutMsSource = "server"
	result.Retries = cfg.DefaultRetries
	result.RetriesSource = "server"
	result.BufferSize = cfg.DefaultBufferSize
	result.BufferSizeSource = "server"

	// Default community
	result.Community = "public"
	result.CommunitySource = "server"
}

func (r *ConfigResolver) applyDefaults(result *ResolvedPollerConfig, defaults *store.PollerDefaults, source string) {
	// SNMP v2c
	if defaults.SNMPCommunity != nil {
		result.Community = *defaults.SNMPCommunity
		result.CommunitySource = source
	}

	// SNMPv3
	if defaults.SNMPv3 != nil {
		v3 := defaults.SNMPv3
		if v3.SecurityName != "" {
			result.SecurityName = v3.SecurityName
			result.SecurityNameSource = source
		}
		if v3.SecurityLevel != "" {
			result.SecurityLevel = v3.SecurityLevel
			result.SecurityLevelSource = source
		}
		if v3.AuthProtocol != "" {
			result.AuthProtocol = v3.AuthProtocol
			result.AuthProtocolSource = source
		}
		if v3.AuthPassword != "" {
			result.AuthPassword = v3.AuthPassword
			result.AuthPasswordSource = source
		} else if v3.AuthSecretRef != "" {
			// Resolve secret reference
			result.AuthPassword = v3.AuthSecretRef // Will be resolved later
			result.AuthPasswordSource = source + " (secret)"
		}
		if v3.PrivProtocol != "" {
			result.PrivProtocol = v3.PrivProtocol
			result.PrivProtocolSource = source
		}
		if v3.PrivPassword != "" {
			result.PrivPassword = v3.PrivPassword
			result.PrivPasswordSource = source
		} else if v3.PrivSecretRef != "" {
			result.PrivPassword = v3.PrivSecretRef
			result.PrivPasswordSource = source + " (secret)"
		}
	}

	// Timing
	if defaults.IntervalMs != nil {
		result.IntervalMs = *defaults.IntervalMs
		result.IntervalMsSource = source
	}
	if defaults.TimeoutMs != nil {
		result.TimeoutMs = *defaults.TimeoutMs
		result.TimeoutMsSource = source
	}
	if defaults.Retries != nil {
		result.Retries = *defaults.Retries
		result.RetriesSource = source
	}
	if defaults.BufferSize != nil {
		result.BufferSize = *defaults.BufferSize
		result.BufferSizeSource = source
	}
}

func (r *ConfigResolver) applyPollingConfig(result *ResolvedPollerConfig, cfg *store.PollingConfig, source string) {
	if cfg.IntervalMs != nil {
		result.IntervalMs = *cfg.IntervalMs
		result.IntervalMsSource = source
	}
	if cfg.TimeoutMs != nil {
		result.TimeoutMs = *cfg.TimeoutMs
		result.TimeoutMsSource = source
	}
	if cfg.Retries != nil {
		result.Retries = *cfg.Retries
		result.RetriesSource = source
	}
	if cfg.BufferSize != nil {
		result.BufferSize = *cfg.BufferSize
		result.BufferSizeSource = source
	}
}

// ResolveSecrets resolves secret references in the config to actual values.
func (r *ConfigResolver) ResolveSecrets(namespace string, config *ResolvedPollerConfig) error {
	if !r.store.HasSecretKey() {
		return nil
	}

	// Check if auth password is a secret reference
	if isSecretRef(config.AuthPassword) {
		secretName := extractSecretName(config.AuthPassword)
		value, err := r.store.GetSecretValue(namespace, secretName)
		if err != nil {
			return err
		}
		config.AuthPassword = value
	}

	// Check if priv password is a secret reference
	if isSecretRef(config.PrivPassword) {
		secretName := extractSecretName(config.PrivPassword)
		value, err := r.store.GetSecretValue(namespace, secretName)
		if err != nil {
			return err
		}
		config.PrivPassword = value
	}

	return nil
}

// isSecretRef checks if a value is a secret reference.
func isSecretRef(value string) bool {
	return len(value) > 7 && value[:7] == "secret:"
}

// extractSecretName extracts the secret name from a reference.
func extractSecretName(ref string) string {
	if len(ref) > 7 {
		return ref[7:]
	}
	return ""
}
