package scheduler

import (
	"encoding/json"
	"time"

	"github.com/gosnmp/gosnmp"
)

// SNMPConfig holds SNMP-specific configuration.
type SNMPConfig struct {
	Host      string `json:"host"`
	Port      uint16 `json:"port"`
	OID       string `json:"oid"`
	Community string `json:"community,omitempty"`

	// SNMPv3
	Version       int    `json:"version,omitempty"` // 2 or 3
	SecurityName  string `json:"security_name,omitempty"`
	SecurityLevel string `json:"security_level,omitempty"` // noAuthNoPriv, authNoPriv, authPriv
	AuthProtocol  string `json:"auth_protocol,omitempty"`
	AuthPassword  string `json:"auth_password,omitempty"`
	PrivProtocol  string `json:"priv_protocol,omitempty"`
	PrivPassword  string `json:"priv_password,omitempty"`
	ContextName   string `json:"context_name,omitempty"`

	// Timing
	TimeoutMs uint32 `json:"timeout_ms,omitempty"`
	Retries   uint32 `json:"retries,omitempty"`
}

// ParseSNMPConfig parses SNMP config from JSON.
func ParseSNMPConfig(data []byte) (*SNMPConfig, error) {
	var cfg SNMPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Defaults
	if cfg.Port == 0 {
		cfg.Port = 161
	}
	if cfg.TimeoutMs == 0 {
		cfg.TimeoutMs = 5000
	}
	if cfg.Retries == 0 {
		cfg.Retries = 2
	}
	if cfg.Version == 0 {
		cfg.Version = 2
	}
	if cfg.Community == "" && cfg.Version == 2 {
		cfg.Community = "public"
	}

	return &cfg, nil
}

// SNMPPoller executes SNMP polls.
type SNMPPoller struct {
	defaultTimeoutMs uint32
	defaultRetries   uint32
}

// NewSNMPPoller creates a new SNMP poller.
func NewSNMPPoller(defaultTimeoutMs, defaultRetries uint32) *SNMPPoller {
	return &SNMPPoller{
		defaultTimeoutMs: defaultTimeoutMs,
		defaultRetries:   defaultRetries,
	}
}

// Poll executes an SNMP poll.
func (p *SNMPPoller) Poll(key PollerKey, cfg *SNMPConfig) PollResult {
	start := time.Now()

	result := PollResult{
		Key:         key,
		TimestampMs: start.UnixMilli(),
	}

	// Apply defaults
	timeoutMs := cfg.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = p.defaultTimeoutMs
	}
	retries := cfg.Retries
	if retries == 0 {
		retries = p.defaultRetries
	}

	// Configure SNMP client
	snmp := &gosnmp.GoSNMP{
		Target:  cfg.Host,
		Port:    cfg.Port,
		Timeout: time.Duration(timeoutMs) * time.Millisecond,
		Retries: int(retries),
	}

	// Configure version
	if cfg.Version == 3 {
		snmp.Version = gosnmp.Version3
		snmp.SecurityModel = gosnmp.UserSecurityModel
		snmp.ContextName = cfg.ContextName

		// Security level
		switch cfg.SecurityLevel {
		case "noAuthNoPriv":
			snmp.MsgFlags = gosnmp.NoAuthNoPriv
		case "authNoPriv":
			snmp.MsgFlags = gosnmp.AuthNoPriv
		case "authPriv":
			snmp.MsgFlags = gosnmp.AuthPriv
		default:
			snmp.MsgFlags = gosnmp.AuthPriv
		}

		snmp.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 cfg.SecurityName,
			AuthenticationProtocol:   mapAuthProtocol(cfg.AuthProtocol),
			AuthenticationPassphrase: cfg.AuthPassword,
			PrivacyProtocol:          mapPrivProtocol(cfg.PrivProtocol),
			PrivacyPassphrase:        cfg.PrivPassword,
		}
	} else {
		snmp.Version = gosnmp.Version2c
		snmp.Community = cfg.Community
	}

	// Connect
	if err := snmp.Connect(); err != nil {
		result.Success = false
		result.Error = err.Error()
		result.Timeout = isTimeout(err)
		result.PollMs = int(time.Since(start).Milliseconds())
		return result
	}
	defer snmp.Conn.Close()

	// Execute GET
	pdu, err := snmp.Get([]string{cfg.OID})
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		result.Timeout = isTimeout(err)
		result.PollMs = int(time.Since(start).Milliseconds())
		return result
	}

	if len(pdu.Variables) == 0 {
		result.Success = false
		result.Error = "no variables returned"
		result.PollMs = int(time.Since(start).Milliseconds())
		return result
	}

	// Parse value
	v := pdu.Variables[0]
	switch v.Type {
	case gosnmp.Counter64, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.Integer, gosnmp.Uinteger32, gosnmp.TimeTicks:
		val := gosnmp.ToBigInt(v.Value).Uint64()
		result.Counter = &val
		result.Success = true

	case gosnmp.OctetString:
		if b, ok := v.Value.([]byte); ok {
			s := string(b)
			result.Text = &s
		}
		result.Success = true

	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance:
		result.Success = false
		result.Error = "no such object"

	default:
		result.Success = false
		result.Error = "unsupported type"
	}

	result.PollMs = int(time.Since(start).Milliseconds())
	return result
}

func mapAuthProtocol(s string) gosnmp.SnmpV3AuthProtocol {
	switch s {
	case "MD5":
		return gosnmp.MD5
	case "SHA", "SHA1":
		return gosnmp.SHA
	case "SHA224":
		return gosnmp.SHA224
	case "SHA256":
		return gosnmp.SHA256
	case "SHA384":
		return gosnmp.SHA384
	case "SHA512":
		return gosnmp.SHA512
	default:
		return gosnmp.NoAuth
	}
}

func mapPrivProtocol(s string) gosnmp.SnmpV3PrivProtocol {
	switch s {
	case "DES":
		return gosnmp.DES
	case "AES", "AES128":
		return gosnmp.AES
	case "AES192":
		return gosnmp.AES192
	case "AES256":
		return gosnmp.AES256
	default:
		return gosnmp.NoPriv
	}
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "timeout") || contains(s, "deadline")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
