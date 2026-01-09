// Package loader handles loading configuration from YAML files.
package loader

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xtxerr/snmpproxy/internal/manager"
	"github.com/xtxerr/snmpproxy/internal/store"
)

// Config represents the complete configuration file.
type Config struct {
	// Server config
	Listen string `yaml:"listen"`

	TLS struct {
		CertFile string `yaml:"cert_file"`
		KeyFile  string `yaml:"key_file"`
	} `yaml:"tls"`

	Auth struct {
		Tokens []TokenConfig `yaml:"tokens"`
	} `yaml:"auth"`

	Session struct {
		AuthTimeoutSec     int `yaml:"auth_timeout_sec"`
		ReconnectWindowSec int `yaml:"reconnect_window_sec"`
	} `yaml:"session"`

	Poller struct {
		Workers   int `yaml:"workers"`
		QueueSize int `yaml:"queue_size"`
	} `yaml:"poller"`

	Storage struct {
		DBPath        string `yaml:"db_path"`
		SecretKeyPath string `yaml:"secret_key_path"`
	} `yaml:"storage"`

	SNMP struct {
		TimeoutMs  uint32 `yaml:"timeout_ms"`
		Retries    uint32 `yaml:"retries"`
		IntervalMs uint32 `yaml:"interval_ms"`
		BufferSize uint32 `yaml:"buffer_size"`
	} `yaml:"snmp"`

	// Inline namespaces
	Namespaces map[string]*NamespaceConfig `yaml:"namespaces"`

	// Include paths for separate namespace files
	Include []string `yaml:"include"`
}

// TokenConfig represents an auth token.
type TokenConfig struct {
	ID         string   `yaml:"id"`
	Token      string   `yaml:"token"`
	Namespaces []string `yaml:"namespaces"` // nil = all
}

// NamespaceConfig represents a namespace configuration.
type NamespaceConfig struct {
	Description string                   `yaml:"description"`
	Defaults    *DefaultsConfig          `yaml:"defaults"`
	Targets     map[string]*TargetConfig `yaml:"targets"`
	Tree        map[string]*TreeNode     `yaml:"tree"`
	Secrets     map[string]*SecretConfig `yaml:"secrets"`
}

// DefaultsConfig represents polling defaults.
type DefaultsConfig struct {
	IntervalMs uint32        `yaml:"interval_ms"`
	TimeoutMs  uint32        `yaml:"timeout_ms"`
	Retries    uint32        `yaml:"retries"`
	BufferSize uint32        `yaml:"buffer_size"`
	SNMP       *SNMPDefaults `yaml:"snmp"`
}

// SNMPDefaults represents SNMP-specific defaults.
type SNMPDefaults struct {
	Community string          `yaml:"community"`
	V3        *SNMPV3Defaults `yaml:"v3"`
}

// SNMPV3Defaults represents SNMPv3 defaults.
type SNMPV3Defaults struct {
	SecurityName  string `yaml:"security_name"`
	SecurityLevel string `yaml:"security_level"`
	AuthProtocol  string `yaml:"auth_protocol"`
	AuthPassword  string `yaml:"auth_password"`
	PrivProtocol  string `yaml:"priv_protocol"`
	PrivPassword  string `yaml:"priv_password"`
}

// TargetConfig represents a target configuration.
type TargetConfig struct {
	Description string                   `yaml:"description"`
	Labels      map[string]string        `yaml:"labels"`
	Defaults    *DefaultsConfig          `yaml:"defaults"`
	Pollers     map[string]*PollerConfig `yaml:"pollers"`
}

// PollerConfig represents a poller configuration.
type PollerConfig struct {
	Description string                 `yaml:"description"`
	Protocol    string                 `yaml:"protocol"`
	Config      map[string]interface{} `yaml:"config"`
	IntervalMs  uint32                 `yaml:"interval_ms"`
	TimeoutMs   uint32                 `yaml:"timeout_ms"`
	Retries     uint32                 `yaml:"retries"`
	BufferSize  uint32                 `yaml:"buffer_size"`
	AdminState  string                 `yaml:"admin_state"` // enabled/disabled
}

// TreeNode represents a tree node in config.
type TreeNode struct {
	Description string               `yaml:"description"`
	Children    map[string]*TreeNode `yaml:"children"`
	Links       []LinkConfig         `yaml:"links"`
}

// LinkConfig represents a symlink in the tree.
type LinkConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // target/poller
	Ref  string `yaml:"ref"`  // target name or target/poller
}

// SecretConfig represents a secret.
type SecretConfig struct {
	Type  string `yaml:"type"`
	Value string `yaml:"value"`
}

// Load loads configuration from a file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand environment variables
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Process includes
	baseDir := filepath.Dir(path)
	for _, inc := range cfg.Include {
		incPath := inc
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(baseDir, inc)
		}

		// Support glob patterns
		matches, err := filepath.Glob(incPath)
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", inc, err)
		}

		for _, match := range matches {
			nsCfg, err := loadNamespaceFile(match)
			if err != nil {
				return nil, fmt.Errorf("load %s: %w", match, err)
			}

			// Merge into config
			if cfg.Namespaces == nil {
				cfg.Namespaces = make(map[string]*NamespaceConfig)
			}
			for name, ns := range nsCfg {
				cfg.Namespaces[name] = ns
			}
		}
	}

	return &cfg, nil
}

// loadNamespaceFile loads namespaces from a file.
func loadNamespaceFile(path string) (map[string]*NamespaceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	expanded := os.ExpandEnv(string(data))

	var result map[string]*NamespaceConfig
	if err := yaml.Unmarshal([]byte(expanded), &result); err != nil {
		return nil, err
	}

	return result, nil
}

// Apply applies the configuration to a manager.
func Apply(cfg *Config, mgr *manager.Manager) (*ApplyResult, error) {
	result := &ApplyResult{}

	for nsName, nsCfg := range cfg.Namespaces {
		if err := applyNamespace(mgr, nsName, nsCfg, result); err != nil {
			return result, fmt.Errorf("namespace %s: %w", nsName, err)
		}
	}

	return result, nil
}

// ApplyResult holds statistics from applying configuration.
type ApplyResult struct {
	NamespacesCreated int
	TargetsCreated    int
	PollersCreated    int
	TreeNodesCreated  int
	SecretsCreated    int
	Errors            []string
}

func applyNamespace(mgr *manager.Manager, name string, cfg *NamespaceConfig, result *ApplyResult) error {
	// Create namespace
	ns := &store.Namespace{
		Name:        name,
		Description: cfg.Description,
	}

	if cfg.Defaults != nil {
		ns.DefaultIntervalMs = cfg.Defaults.IntervalMs
		ns.DefaultTimeoutMs = cfg.Defaults.TimeoutMs
		ns.DefaultRetries = cfg.Defaults.Retries
		ns.DefaultBufferSize = cfg.Defaults.BufferSize
		if cfg.Defaults.SNMP != nil {
			ns.DefaultCommunity = cfg.Defaults.SNMP.Community
		}
	}

	if err := mgr.Namespaces.Create(ns); err != nil {
		// Namespace might already exist - try update
		existing, _ := mgr.Namespaces.Get(name)
		if existing != nil {
			// Update existing
			ns.Version = existing.Version
			if err := mgr.Namespaces.Update(ns); err != nil {
				return fmt.Errorf("update namespace: %w", err)
			}
		} else {
			return fmt.Errorf("create namespace: %w", err)
		}
	} else {
		result.NamespacesCreated++
	}

	// Create secrets first (they might be referenced)
	for secretName, secretCfg := range cfg.Secrets {
		secret := &store.Secret{
			Namespace: name,
			Name:      secretName,
			Type:      secretCfg.Type,
		}
		if err := mgr.Secrets.Create(secret, secretCfg.Value); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("secret %s/%s: %v", name, secretName, err))
		} else {
			result.SecretsCreated++
		}
	}

	// Create targets and pollers
	for targetName, targetCfg := range cfg.Targets {
		if err := applyTarget(mgr, name, targetName, targetCfg, result); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("target %s/%s: %v", name, targetName, err))
		}
	}

	// Create tree structure
	for path, node := range cfg.Tree {
		if err := applyTreeNode(mgr, name, "/"+path, node, result); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("tree %s/%s: %v", name, path, err))
		}
	}

	return nil
}

func applyTarget(mgr *manager.Manager, namespace, name string, cfg *TargetConfig, result *ApplyResult) error {
	target := &store.Target{
		Namespace:   namespace,
		Name:        name,
		Description: cfg.Description,
		Labels:      cfg.Labels,
	}

	if cfg.Defaults != nil {
		target.DefaultIntervalMs = cfg.Defaults.IntervalMs
		target.DefaultTimeoutMs = cfg.Defaults.TimeoutMs
		target.DefaultRetries = cfg.Defaults.Retries
		target.DefaultBufferSize = cfg.Defaults.BufferSize
		if cfg.Defaults.SNMP != nil {
			target.DefaultCommunity = cfg.Defaults.SNMP.Community
		}
	}

	if err := mgr.Targets.Create(target); err != nil {
		// Target might already exist
		existing, _ := mgr.Targets.Get(namespace, name)
		if existing != nil {
			target.Version = existing.Version
			if err := mgr.Targets.Update(target); err != nil {
				return fmt.Errorf("update: %w", err)
			}
		} else {
			return fmt.Errorf("create: %w", err)
		}
	} else {
		result.TargetsCreated++
	}

	// Create pollers
	for pollerName, pollerCfg := range cfg.Pollers {
		if err := applyPoller(mgr, namespace, name, pollerName, pollerCfg, result); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("poller %s/%s/%s: %v", namespace, name, pollerName, err))
		}
	}

	return nil
}

func applyPoller(mgr *manager.Manager, namespace, target, name string, cfg *PollerConfig, result *ApplyResult) error {
	// Serialize config to JSON
	jsonConfig, err := json.Marshal(cfg.Config)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}

	adminState := "disabled"
	if cfg.AdminState == "enabled" {
		adminState = "enabled"
	}

	protocol := cfg.Protocol
	if protocol == "" {
		protocol = "snmp"
	}

	poller := &store.Poller{
		Namespace:      namespace,
		Target:         target,
		Name:           name,
		Description:    cfg.Description,
		Protocol:       protocol,
		ProtocolConfig: jsonConfig,
		IntervalMs:     cfg.IntervalMs,
		TimeoutMs:      cfg.TimeoutMs,
		Retries:        cfg.Retries,
		BufferSize:     cfg.BufferSize,
		AdminState:     adminState,
	}

	if err := mgr.Pollers.Create(poller); err != nil {
		// Poller might already exist
		existing, _ := mgr.Pollers.Get(namespace, target, name)
		if existing != nil {
			poller.Version = existing.Version
			if err := mgr.Pollers.Update(poller); err != nil {
				return fmt.Errorf("update: %w", err)
			}
		} else {
			return fmt.Errorf("create: %w", err)
		}
	} else {
		result.PollersCreated++
	}

	return nil
}

func applyTreeNode(mgr *manager.Manager, namespace, path string, node *TreeNode, result *ApplyResult) error {
	// Create directory
	if err := mgr.Tree.CreateDirectory(namespace, path, node.Description); err != nil {
		if !strings.Contains(err.Error(), "exists") {
			return fmt.Errorf("create directory: %w", err)
		}
	} else {
		result.TreeNodesCreated++
	}

	// Create links
	for _, link := range node.Links {
		linkType := link.Type
		if linkType == "" {
			linkType = "target"
		}

		targetRef := link.Ref
		pollerRef := ""
		if linkType == "poller" && strings.Contains(link.Ref, "/") {
			parts := strings.SplitN(link.Ref, "/", 2)
			targetRef = parts[0]
			pollerRef = parts[1]
		}

		linkNode := &store.TreeNode{
			Namespace:  namespace,
			Path:       path + "/" + link.Name,
			Name:       link.Name,
			Type:       "link",
			LinkType:   linkType,
			LinkTarget: targetRef,
			LinkPoller: pollerRef,
		}

		if err := mgr.Tree.Create(linkNode); err != nil {
			if !strings.Contains(err.Error(), "exists") {
				result.Errors = append(result.Errors,
					fmt.Sprintf("link %s/%s: %v", path, link.Name, err))
			}
		} else {
			result.TreeNodesCreated++
		}
	}

	// Process children recursively
	for childName, childNode := range node.Children {
		childPath := path + "/" + childName
		if err := applyTreeNode(mgr, namespace, childPath, childNode, result); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("child %s: %v", childPath, err))
		}
	}

	return nil
}

// Watcher watches a config file for changes.
type Watcher struct {
	path     string
	mgr      *manager.Manager
	onChange func(*ApplyResult)
	stop     chan struct{}
}

// NewWatcher creates a config file watcher.
func NewWatcher(path string, mgr *manager.Manager, onChange func(*ApplyResult)) *Watcher {
	return &Watcher{
		path:     path,
		mgr:      mgr,
		onChange: onChange,
		stop:     make(chan struct{}),
	}
}

// Start starts watching the config file.
func (w *Watcher) Start() {
	go w.watchLoop()
}

// Stop stops watching.
func (w *Watcher) Stop() {
	close(w.stop)
}

func (w *Watcher) watchLoop() {
	var lastMod time.Time
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			info, err := os.Stat(w.path)
			if err != nil {
				continue
			}

			if info.ModTime().After(lastMod) {
				lastMod = info.ModTime()

				cfg, err := Load(w.path)
				if err != nil {
					continue
				}

				result, err := Apply(cfg, w.mgr)
				if err != nil {
					result.Errors = append(result.Errors, err.Error())
				}

				if w.onChange != nil {
					w.onChange(result)
				}
			}
		}
	}
}
