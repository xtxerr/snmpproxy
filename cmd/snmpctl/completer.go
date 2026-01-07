package main

import (
	"fmt"
	"strings"

	"github.com/c-bata/go-prompt"
	"github.com/xtxerr/snmpproxy/internal/client"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// Completer provides tab completion for snmpctl.
type Completer struct {
	client *client.Client

	// Caches (refreshed on demand)
	cachedTargetIDs []string
	cachedTags      []string
}

// NewCompleter creates a new completer.
func NewCompleter(c *client.Client) *Completer {
	return &Completer{client: c}
}

// Complete returns suggestions for the current input.
func (c *Completer) Complete(d prompt.Document) []prompt.Suggest {
	text := d.TextBeforeCursor()
	if text == "" {
		return commandSuggestions
	}

	args := parseArgsForComplete(text)
	if len(args) == 0 {
		return prompt.FilterHasPrefix(commandSuggestions, text, true)
	}

	cmd := strings.ToLower(args[0])
	
	// If still typing the command
	if len(args) == 1 && !strings.HasSuffix(text, " ") {
		return prompt.FilterHasPrefix(commandSuggestions, cmd, true)
	}

	// Command-specific completion
	switch cmd {
	case "ls", "l", "list":
		return c.completeLs(args, text)
	case "add", "a":
		return c.completeAdd(args, text)
	case "set", "s":
		return c.completeSet(args, text)
	case "rm", "del", "delete":
		return c.completeRm(args, text)
	case "history", "hist", "h":
		return c.completeTargetID(args, text)
	case "sub", "subscribe":
		return c.completeSub(args, text)
	case "unsub", "unsubscribe":
		return c.completeTargetID(args, text)
	case "config", "cfg":
		return c.completeConfig(args, text)
	}

	return nil
}

var commandSuggestions = []prompt.Suggest{
	{Text: "ls", Description: "Browse paths and targets"},
	{Text: "add", Description: "Create a new target"},
	{Text: "set", Description: "Update target settings"},
	{Text: "rm", Description: "Delete a target"},
	{Text: "history", Description: "Show sample history"},
	{Text: "sub", Description: "Subscribe to targets"},
	{Text: "unsub", Description: "Unsubscribe from targets"},
	{Text: "config", Description: "View/change configuration"},
	{Text: "help", Description: "Show help"},
	{Text: "quit", Description: "Exit"},
}

// ============================================================================
// ls completion
// ============================================================================

func (c *Completer) completeLs(args []string, text string) []prompt.Suggest {
	lastArg := args[len(args)-1]
	
	// Flag completion
	if strings.HasPrefix(lastArg, "-") && !strings.HasSuffix(text, " ") {
		return prompt.FilterHasPrefix(lsFlags, lastArg, true)
	}
	
	// After a flag that expects a value
	if len(args) >= 2 {
		prevArg := args[len(args)-2]
		if strings.HasPrefix(prevArg, "--state=") || prevArg == "--state" {
			return prompt.FilterHasPrefix(stateSuggestions, lastArg, true)
		}
		if strings.HasPrefix(prevArg, "--protocol=") || prevArg == "--protocol" {
			return prompt.FilterHasPrefix(protocolSuggestions, lastArg, true)
		}
		if strings.HasPrefix(prevArg, "--tag=") || prevArg == "--tag" {
			return c.completeTagValue(lastArg)
		}
	}

	// Path completion
	if !strings.HasPrefix(lastArg, "-") {
		if strings.HasSuffix(text, " ") {
			// New argument - suggest paths and flags
			suggestions := c.completePath("")
			return append(suggestions, lsFlags...)
		}
		return c.completePath(lastArg)
	}
	
	return prompt.FilterHasPrefix(lsFlags, "", true)
}

var lsFlags = []prompt.Suggest{
	{Text: "-l", Description: "Long format with details"},
	{Text: "--long", Description: "Long format with details"},
	{Text: "--tag=", Description: "Filter by tag"},
	{Text: "--state=", Description: "Filter by state"},
	{Text: "--protocol=", Description: "Filter by protocol"},
	{Text: "--host=", Description: "Filter by host (glob)"},
	{Text: "--limit=", Description: "Max results"},
	{Text: "--json", Description: "JSON output"},
}

var stateSuggestions = []prompt.Suggest{
	{Text: "polling", Description: "Active targets"},
	{Text: "unreachable", Description: "Failed targets"},
	{Text: "error", Description: "Targets with errors"},
}

var protocolSuggestions = []prompt.Suggest{
	{Text: "snmp", Description: "SNMP targets"},
	{Text: "http", Description: "HTTP targets (future)"},
	{Text: "icmp", Description: "ICMP targets (future)"},
}

func (c *Completer) completePath(partial string) []prompt.Suggest {
	if !strings.HasPrefix(partial, "/") {
		partial = "/" + partial
	}

	// Find parent path
	parentPath := partial
	prefix := ""
	if idx := strings.LastIndex(partial, "/"); idx >= 0 {
		if idx == 0 {
			parentPath = "/"
			prefix = partial[1:]
		} else {
			parentPath = partial[:idx]
			prefix = partial[idx+1:]
		}
	}

	// Fetch children from server
	resp, err := c.client.BrowsePath(parentPath, false)
	if err != nil {
		return nil
	}

	var suggestions []prompt.Suggest
	for _, node := range resp.Nodes {
		name := node.Name
		if node.Type == pb.NodeType_NODE_DIRECTORY {
			name += "/"
		}
		
		fullPath := parentPath
		if fullPath == "/" {
			fullPath = "/" + name
		} else {
			fullPath = parentPath + "/" + name
		}

		if prefix == "" || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			suggestions = append(suggestions, prompt.Suggest{
				Text:        fullPath,
				Description: node.Description,
			})
		}
	}

	return suggestions
}

// ============================================================================
// add completion
// ============================================================================

func (c *Completer) completeAdd(args []string, text string) []prompt.Suggest {
	// "add " -> suggest protocol
	if len(args) == 1 && strings.HasSuffix(text, " ") {
		return []prompt.Suggest{
			{Text: "snmp", Description: "Add SNMP target"},
		}
	}
	
	// "add sn" -> filter protocol
	if len(args) == 2 && !strings.HasSuffix(text, " ") {
		return prompt.FilterHasPrefix([]prompt.Suggest{
			{Text: "snmp", Description: "Add SNMP target"},
		}, args[1], true)
	}

	// "add snmp " -> suggest host placeholder
	if len(args) == 2 && strings.HasSuffix(text, " ") {
		return []prompt.Suggest{
			{Text: "<host>", Description: "Target hostname or IP address"},
		}
	}

	// "add snmp host " -> suggest common OIDs
	if len(args) == 3 && strings.HasSuffix(text, " ") {
		return commonOIDs
	}

	// "add snmp host 1.3..." -> filter OIDs
	if len(args) == 4 && !strings.HasSuffix(text, " ") {
		lastArg := args[3]
		if strings.HasPrefix(lastArg, "1.") || strings.HasPrefix(lastArg, ".1.") {
			return prompt.FilterHasPrefix(commonOIDs, lastArg, true)
		}
	}

	// After host and OID, suggest flags
	if len(args) >= 4 {
		lastArg := args[len(args)-1]
		
		// Flag completion
		if strings.HasPrefix(lastArg, "-") && !strings.HasSuffix(text, " ") {
			return prompt.FilterHasPrefix(addFlags, lastArg, true)
		}
		
		// After flags or args
		if strings.HasSuffix(text, " ") {
			return addFlags
		}
	}

	return nil
}

var commonOIDs = []prompt.Suggest{
	{Text: "1.3.6.1.2.1.1.1.0", Description: "sysDescr - System description"},
	{Text: "1.3.6.1.2.1.1.3.0", Description: "sysUpTime - System uptime"},
	{Text: "1.3.6.1.2.1.1.5.0", Description: "sysName - System name"},
	{Text: "1.3.6.1.2.1.1.6.0", Description: "sysLocation - System location"},
	{Text: "1.3.6.1.2.1.2.2.1.10", Description: "ifInOctets - Interface input bytes"},
	{Text: "1.3.6.1.2.1.2.2.1.16", Description: "ifOutOctets - Interface output bytes"},
	{Text: "1.3.6.1.2.1.31.1.1.1.6", Description: "ifHCInOctets - 64-bit input counter"},
	{Text: "1.3.6.1.2.1.31.1.1.1.10", Description: "ifHCOutOctets - 64-bit output counter"},
	{Text: "1.3.6.1.2.1.2.2.1.8", Description: "ifOperStatus - Interface oper status"},
	{Text: "1.3.6.1.2.1.2.2.1.7", Description: "ifAdminStatus - Interface admin status"},
	{Text: "1.3.6.1.4.1.9.9.109.1.1.1.1.3", Description: "Cisco CPU 1min"},
	{Text: "1.3.6.1.4.1.9.9.109.1.1.1.1.4", Description: "Cisco CPU 5min"},
}

var addFlags = []prompt.Suggest{
	{Text: "--name=", Description: "User-friendly name"},
	{Text: "--id=", Description: "Custom target ID"},
	{Text: "--desc=", Description: "Description"},
	{Text: "--tag=", Description: "Tag (repeatable)"},
	{Text: "--interval=", Description: "Poll interval ms"},
	{Text: "--buffer=", Description: "Buffer size"},
	{Text: "--timeout=", Description: "SNMP timeout ms"},
	{Text: "--retries=", Description: "SNMP retries"},
	{Text: "--persistent", Description: "Mark as persistent"},
	{Text: "--community=", Description: "SNMPv2c community"},
	{Text: "--v3", Description: "Use SNMPv3"},
	{Text: "--user=", Description: "SNMPv3 security name"},
	{Text: "--level=", Description: "SNMPv3 security level"},
	{Text: "--auth-proto=", Description: "Auth protocol"},
	{Text: "--auth-pass=", Description: "Auth password"},
	{Text: "--priv-proto=", Description: "Privacy protocol"},
	{Text: "--priv-pass=", Description: "Privacy password"},
}

// ============================================================================
// set completion
// ============================================================================

func (c *Completer) completeSet(args []string, text string) []prompt.Suggest {
	// First arg after "set" should be target ID
	if len(args) == 1 && strings.HasSuffix(text, " ") {
		return c.getTargetSuggestions("")
	}
	
	if len(args) == 2 && !strings.HasSuffix(text, " ") {
		return c.getTargetSuggestions(args[1])
	}

	lastArg := args[len(args)-1]
	
	// Flag completion
	if strings.HasPrefix(lastArg, "-") && !strings.HasSuffix(text, " ") {
		return prompt.FilterHasPrefix(setFlags, lastArg, true)
	}
	
	// Check if previous arg was --tag
	if len(args) >= 2 && args[len(args)-2] == "--tag" {
		// Suggest +tag, -tag, or tag
		return c.completeTagOperation(lastArg)
	}

	if strings.HasSuffix(text, " ") {
		return setFlags
	}

	return nil
}

var setFlags = []prompt.Suggest{
	{Text: "--name=", Description: "Change name"},
	{Text: "--desc=", Description: "Set description"},
	{Text: "--interval=", Description: "Set poll interval ms"},
	{Text: "--buffer=", Description: "Set buffer size"},
	{Text: "--timeout=", Description: "Set SNMP timeout ms"},
	{Text: "--retries=", Description: "Set SNMP retries"},
	{Text: "--persistent", Description: "Mark as persistent"},
	{Text: "--no-persistent", Description: "Remove persistent flag"},
	{Text: "--tag", Description: "Modify tags (+add, -remove, set)"},
	{Text: "--community=", Description: "Change SNMPv2c community"},
	{Text: "--user=", Description: "Change SNMPv3 security name"},
	{Text: "--level=", Description: "Change SNMPv3 level"},
	{Text: "--auth-proto=", Description: "Change auth protocol"},
	{Text: "--auth-pass=", Description: "Change auth password"},
	{Text: "--priv-proto=", Description: "Change priv protocol"},
	{Text: "--priv-pass=", Description: "Change priv password"},
}

func (c *Completer) completeTagOperation(partial string) []prompt.Suggest {
	tags := c.getTagSuggestions("")
	
	var suggestions []prompt.Suggest
	for _, tag := range tags {
		// Add suggestions
		suggestions = append(suggestions, prompt.Suggest{
			Text:        "+" + tag.Text,
			Description: "Add tag",
		})
		// Remove suggestions
		suggestions = append(suggestions, prompt.Suggest{
			Text:        "-" + tag.Text,
			Description: "Remove tag",
		})
		// Set suggestions
		suggestions = append(suggestions, prompt.Suggest{
			Text:        tag.Text,
			Description: "Set tag",
		})
	}
	
	return prompt.FilterHasPrefix(suggestions, partial, true)
}

// ============================================================================
// rm completion
// ============================================================================

func (c *Completer) completeRm(args []string, text string) []prompt.Suggest {
	if len(args) == 1 && strings.HasSuffix(text, " ") {
		return c.getTargetSuggestions("")
	}
	
	lastArg := args[len(args)-1]
	
	if !strings.HasPrefix(lastArg, "-") {
		return c.getTargetSuggestions(lastArg)
	}
	
	return prompt.FilterHasPrefix([]prompt.Suggest{
		{Text: "-f", Description: "Force delete"},
		{Text: "--force", Description: "Force delete"},
	}, lastArg, true)
}

// ============================================================================
// sub completion
// ============================================================================

func (c *Completer) completeSub(args []string, text string) []prompt.Suggest {
	lastArg := ""
	if len(args) > 1 {
		lastArg = args[len(args)-1]
	}

	if strings.HasPrefix(lastArg, "--tag=") {
		tagPart := strings.TrimPrefix(lastArg, "--tag=")
		return c.completeTagValue(tagPart)
	}
	
	if strings.HasPrefix(lastArg, "-") && !strings.HasSuffix(text, " ") {
		return prompt.FilterHasPrefix([]prompt.Suggest{
			{Text: "--tag=", Description: "Subscribe by tag"},
		}, lastArg, true)
	}

	if strings.HasSuffix(text, " ") || len(args) == 1 {
		suggestions := c.getTargetSuggestions("")
		suggestions = append(suggestions, prompt.Suggest{
			Text:        "--tag=",
			Description: "Subscribe by tag",
		})
		return suggestions
	}

	return c.getTargetSuggestions(lastArg)
}

// ============================================================================
// config completion
// ============================================================================

func (c *Completer) completeConfig(args []string, text string) []prompt.Suggest {
	if len(args) == 1 && strings.HasSuffix(text, " ") {
		return []prompt.Suggest{
			{Text: "set", Description: "Set config value"},
		}
	}
	
	if len(args) >= 2 && args[1] == "set" {
		if len(args) == 2 && strings.HasSuffix(text, " ") {
			return configKeys
		}
		if len(args) == 3 && !strings.HasSuffix(text, " ") {
			return prompt.FilterHasPrefix(configKeys, args[2], true)
		}
	}
	
	return nil
}

var configKeys = []prompt.Suggest{
	{Text: "timeout", Description: "Default SNMP timeout ms"},
	{Text: "retries", Description: "Default SNMP retries"},
	{Text: "buffer", Description: "Default buffer size"},
	{Text: "min-interval", Description: "Minimum poll interval ms"},
}

// ============================================================================
// Target ID completion
// ============================================================================

func (c *Completer) completeTargetID(args []string, text string) []prompt.Suggest {
	if len(args) == 1 && strings.HasSuffix(text, " ") {
		return c.getTargetSuggestions("")
	}
	
	lastArg := args[len(args)-1]
	if !strings.HasPrefix(lastArg, "-") {
		return c.getTargetSuggestions(lastArg)
	}
	
	return nil
}

func (c *Completer) getTargetSuggestions(prefix string) []prompt.Suggest {
	// Refresh cache
	resp, err := c.client.BrowsePath("/targets", false)
	if err != nil {
		return nil
	}

	var suggestions []prompt.Suggest
	for _, node := range resp.Nodes {
		if node.Type != pb.NodeType_NODE_TARGET {
			continue
		}
		suggestions = append(suggestions, prompt.Suggest{
			Text:        node.Name,
			Description: node.Description,
		})
	}

	return prompt.FilterHasPrefix(suggestions, prefix, true)
}

// ============================================================================
// Tag completion
// ============================================================================

func (c *Completer) completeTagValue(partial string) []prompt.Suggest {
	suggestions := c.getTagSuggestions(partial)
	
	// Prepend --tag= for proper insertion
	var result []prompt.Suggest
	for _, s := range suggestions {
		result = append(result, prompt.Suggest{
			Text:        "--tag=" + s.Text,
			Description: s.Description,
		})
	}
	
	return result
}

func (c *Completer) getTagSuggestions(prefix string) []prompt.Suggest {
	// Get tag hierarchy from server
	path := "/tags"
	if prefix != "" {
		// Get parent path for partial completion
		if idx := strings.LastIndex(prefix, "/"); idx >= 0 {
			path = "/tags/" + prefix[:idx]
		}
	}

	resp, err := c.client.BrowsePath(path, false)
	if err != nil {
		return nil
	}

	var suggestions []prompt.Suggest
	basePath := strings.TrimPrefix(path, "/tags")
	if basePath != "" {
		basePath = basePath[1:] + "/" // Remove leading slash, add trailing
	}

	for _, node := range resp.Nodes {
		if node.Type != pb.NodeType_NODE_DIRECTORY {
			continue
		}
		fullTag := basePath + node.Name
		desc := ""
		if node.TargetCount > 0 {
			desc = fmt.Sprintf("%d targets", node.TargetCount)
		}
		suggestions = append(suggestions, prompt.Suggest{
			Text:        fullTag,
			Description: desc,
		})
	}

	return prompt.FilterHasPrefix(suggestions, prefix, true)
}

// ============================================================================
// Helpers
// ============================================================================

func parseArgsForComplete(text string) []string {
	var args []string
	var current strings.Builder
	inQuote := false

	for _, r := range text {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
