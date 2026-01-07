// Package oids provides OID aliases for common SNMP objects.
package oids

import "strings"

// Alias maps friendly names to OIDs.
var Alias = map[string]string{
	// System MIB (1.3.6.1.2.1.1)
	"sysDescr":    "1.3.6.1.2.1.1.1.0",
	"sysObjectID": "1.3.6.1.2.1.1.2.0",
	"sysUpTime":   "1.3.6.1.2.1.1.3.0",
	"sysContact":  "1.3.6.1.2.1.1.4.0",
	"sysName":     "1.3.6.1.2.1.1.5.0",
	"sysLocation": "1.3.6.1.2.1.1.6.0",
	"sysServices": "1.3.6.1.2.1.1.7.0",

	// Interfaces MIB (1.3.6.1.2.1.2.2.1) - append .N for index
	"ifDescr":       "1.3.6.1.2.1.2.2.1.2",
	"ifType":        "1.3.6.1.2.1.2.2.1.3",
	"ifMtu":         "1.3.6.1.2.1.2.2.1.4",
	"ifSpeed":       "1.3.6.1.2.1.2.2.1.5",
	"ifAdminStatus": "1.3.6.1.2.1.2.2.1.7",
	"ifOperStatus":  "1.3.6.1.2.1.2.2.1.8",
	"ifInOctets":    "1.3.6.1.2.1.2.2.1.10",
	"ifInErrors":    "1.3.6.1.2.1.2.2.1.14",
	"ifOutOctets":   "1.3.6.1.2.1.2.2.1.16",
	"ifOutErrors":   "1.3.6.1.2.1.2.2.1.20",

	// IF-MIB 64-bit counters (1.3.6.1.2.1.31.1.1.1) - append .N for index
	"ifName":        "1.3.6.1.2.1.31.1.1.1.1",
	"ifHCInOctets":  "1.3.6.1.2.1.31.1.1.1.6",
	"ifHCOutOctets": "1.3.6.1.2.1.31.1.1.1.10",
	"ifAlias":       "1.3.6.1.2.1.31.1.1.1.18",

	// IP MIB
	"ipForwarding": "1.3.6.1.2.1.4.1.0",
	"ipInReceives": "1.3.6.1.2.1.4.3.0",
	"ipOutRequests": "1.3.6.1.2.1.4.10.0",

	// TCP MIB
	"tcpActiveOpens":  "1.3.6.1.2.1.6.5.0",
	"tcpPassiveOpens": "1.3.6.1.2.1.6.6.0",
	"tcpCurrEstab":    "1.3.6.1.2.1.6.9.0",
	"tcpInSegs":       "1.3.6.1.2.1.6.10.0",
	"tcpOutSegs":      "1.3.6.1.2.1.6.11.0",

	// UDP MIB
	"udpInDatagrams":  "1.3.6.1.2.1.7.1.0",
	"udpOutDatagrams": "1.3.6.1.2.1.7.4.0",

	// Host Resources MIB
	"hrSystemUptime":       "1.3.6.1.2.1.25.1.1.0",
	"hrSystemNumUsers":     "1.3.6.1.2.1.25.1.5.0",
	"hrSystemProcesses":    "1.3.6.1.2.1.25.1.6.0",
	"hrMemorySize":         "1.3.6.1.2.1.25.2.2.0",
	"hrStorageDescr":       "1.3.6.1.2.1.25.2.3.1.3",
	"hrStorageSize":        "1.3.6.1.2.1.25.2.3.1.5",
	"hrStorageUsed":        "1.3.6.1.2.1.25.2.3.1.6",
	"hrProcessorLoad":      "1.3.6.1.2.1.25.3.3.1.2",

	// Cisco (1.3.6.1.4.1.9)
	"cpmCPUTotal1min":  "1.3.6.1.4.1.9.9.109.1.1.1.1.4",
	"cpmCPUTotal5min":  "1.3.6.1.4.1.9.9.109.1.1.1.1.5",
	"cpmCPUTotal1minRev": "1.3.6.1.4.1.9.9.109.1.1.1.1.7",
	"cpmCPUTotal5minRev": "1.3.6.1.4.1.9.9.109.1.1.1.1.8",

	// Linux UCD-SNMP
	"memTotalReal":  "1.3.6.1.4.1.2021.4.5.0",
	"memAvailReal":  "1.3.6.1.4.1.2021.4.6.0",
	"memTotalFree":  "1.3.6.1.4.1.2021.4.11.0",
	"ssCpuUser":     "1.3.6.1.4.1.2021.11.9.0",
	"ssCpuSystem":   "1.3.6.1.4.1.2021.11.10.0",
	"ssCpuIdle":     "1.3.6.1.4.1.2021.11.11.0",
	"laLoad1":       "1.3.6.1.4.1.2021.10.1.3.1",
	"laLoad5":       "1.3.6.1.4.1.2021.10.1.3.2",
	"laLoad15":      "1.3.6.1.4.1.2021.10.1.3.3",
}

// descriptions for completion
var Description = map[string]string{
	"sysDescr":      "System description",
	"sysUpTime":     "System uptime (timeticks)",
	"sysName":       "System hostname",
	"sysLocation":   "System location",
	"sysContact":    "System contact",
	"ifDescr":       "Interface description (.N)",
	"ifOperStatus":  "Interface oper status (.N)",
	"ifAdminStatus": "Interface admin status (.N)",
	"ifSpeed":       "Interface speed (.N)",
	"ifInOctets":    "Input bytes 32-bit (.N)",
	"ifOutOctets":   "Output bytes 32-bit (.N)",
	"ifHCInOctets":  "Input bytes 64-bit (.N)",
	"ifHCOutOctets": "Output bytes 64-bit (.N)",
	"ifName":        "Interface name (.N)",
	"ifAlias":       "Interface alias (.N)",
	"hrMemorySize":  "Total memory (KB)",
	"hrProcessorLoad": "CPU load % (.N)",
	"cpmCPUTotal5min": "Cisco CPU 5min %",
	"laLoad1":       "Linux load avg 1min",
	"laLoad5":       "Linux load avg 5min",
	"ssCpuIdle":     "Linux CPU idle %",
	"memAvailReal":  "Linux available memory",
}

// Resolve converts an alias (with optional .N suffix) to full OID.
// Returns the original string if not an alias.
func Resolve(aliasOrOID string) string {
	// Already an OID?
	if strings.HasPrefix(aliasOrOID, ".") || strings.HasPrefix(aliasOrOID, "1.") {
		return strings.TrimPrefix(aliasOrOID, ".")
	}

	// Check for .N suffix (e.g., ifHCInOctets.1)
	parts := strings.SplitN(aliasOrOID, ".", 2)
	alias := parts[0]
	suffix := ""
	if len(parts) == 2 {
		suffix = "." + parts[1]
	}

	if oid, ok := Alias[alias]; ok {
		return oid + suffix
	}

	// Not found, return as-is
	return aliasOrOID
}

// List returns all aliases sorted by category for completion.
func List() []struct {
	Name        string
	OID         string
	Description string
} {
	// Prioritized list for completion
	priority := []string{
		"sysUpTime", "sysDescr", "sysName", "sysLocation",
		"ifHCInOctets", "ifHCOutOctets", "ifOperStatus", "ifDescr", "ifName",
		"hrProcessorLoad", "hrMemorySize",
		"laLoad1", "laLoad5", "ssCpuIdle", "memAvailReal",
		"cpmCPUTotal5min",
	}

	var result []struct {
		Name        string
		OID         string
		Description string
	}

	seen := make(map[string]bool)
	for _, name := range priority {
		if oid, ok := Alias[name]; ok {
			desc := Description[name]
			if desc == "" {
				desc = oid
			}
			result = append(result, struct {
				Name        string
				OID         string
				Description string
			}{name, oid, desc})
			seen[name] = true
		}
	}

	return result
}
