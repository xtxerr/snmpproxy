package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// SetCmd modifies targets or configuration.
type SetCmd struct{}

func (c *SetCmd) Name() string       { return "set" }
func (c *SetCmd) Aliases() []string  { return []string{"s"} }
func (c *SetCmd) Brief() string      { return "Modify target or config settings" }

func (c *SetCmd) Usage() string {
	return `Usage: set <type> <id> <key> <value>
       set <type> <id> [options]

Modify settings for a target or configuration.

Types:
  target <id|name> [options]    Modify a target
  config <key> <value>          Modify server config

Target Options:
  --name=STRING        Change name
  --desc=STRING        Change description
  --interval=INT       Poll interval in ms
  --timeout=INT        SNMP timeout in ms
  --retries=INT        SNMP retries
  --persistent         Mark as persistent
  --no-persistent      Remove persistent flag
  --tag +TAG           Add tag
  --tag -TAG           Remove tag
  --tag TAG            Set tags (replace all)
  --community=STRING   Change SNMPv2c community
  --auth-pass=STRING   Change SNMPv3 auth password
  --priv-pass=STRING   Change SNMPv3 priv password

Config Keys:
  timeout              Default SNMP timeout (ms)
  retries              Default SNMP retries
  buffer               Default buffer size
  min-interval         Minimum poll interval (ms)

Examples:
  set target router-wan --interval=5000
  set target abc123 --tag +location/dc1
  set target router --name=router-core --desc="Core router"
  set config timeout 3000`
}

func (c *SetCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	// set [TAB] -> types
	if len(args) == 0 {
		return FilterSuggestions(setTypes, partial)
	}

	setType := strings.ToLower(args[0])

	switch setType {
	case "target":
		if len(args) == 1 {
			return c.completeTargetID(ctx, partial)
		}
		return FilterSuggestions(setTargetFlags, partial)
	case "config":
		if len(args) == 1 {
			return FilterSuggestions(configKeys, partial)
		}
		return nil
	default:
		return FilterSuggestions(setTypes, partial)
	}
}

var setTypes = []Suggestion{
	{Text: "target", Description: "Modify a target"},
	{Text: "config", Description: "Modify server config"},
}

var setTargetFlags = []Suggestion{
	{Text: "--name=", Description: "Change name"},
	{Text: "--desc=", Description: "Change description"},
	{Text: "--interval=", Description: "Poll interval (ms)"},
	{Text: "--timeout=", Description: "SNMP timeout (ms)"},
	{Text: "--retries=", Description: "SNMP retries"},
	{Text: "--persistent", Description: "Mark as persistent"},
	{Text: "--no-persistent", Description: "Remove persistent flag"},
	{Text: "--tag", Description: "Modify tags (+add, -remove, set)"},
	{Text: "--community=", Description: "SNMPv2c community"},
	{Text: "--auth-pass=", Description: "SNMPv3 auth password"},
	{Text: "--priv-pass=", Description: "SNMPv3 priv password"},
}

var configKeys = []Suggestion{
	{Text: "timeout", Description: "Default SNMP timeout (ms)"},
	{Text: "retries", Description: "Default SNMP retries"},
	{Text: "buffer", Description: "Default buffer size"},
	{Text: "min-interval", Description: "Minimum poll interval (ms)"},
}

func (c *SetCmd) completeTargetID(ctx *shell.Context, partial string) []Suggestion {
	// Same as RmCmd
	req := &pb.BrowseRequest{Path: "/targets", LongFormat: true, Limit: 50}
	resp, err := ctx.Client.Browse(req)
	if err != nil {
		return nil
	}

	var suggestions []Suggestion
	for _, node := range resp.Nodes {
		if node.Type != pb.NodeType_NODE_TARGET {
			continue
		}
		t := node.Target
		if t == nil {
			continue
		}

		if partial == "" || strings.HasPrefix(t.Id, partial) {
			desc := t.Name
			if desc == "" {
				desc = t.Description
			}
			suggestions = append(suggestions, Suggestion{
				Text:        t.Id,
				Description: desc,
			})
		}

		if t.Name != "" && (partial == "" || strings.HasPrefix(t.Name, partial)) {
			suggestions = append(suggestions, Suggestion{
				Text:        t.Name,
				Description: fmt.Sprintf("→ %s", t.Id),
			})
		}
	}

	return suggestions
}

func (c *SetCmd) Execute(ctx *shell.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: set target <id> [options] OR set config <key> <value>")
	}

	setType := strings.ToLower(args[0])

	switch setType {
	case "target":
		return c.executeTarget(ctx, args[1:])
	case "config":
		return c.executeConfig(ctx, args[1:])
	default:
		return fmt.Errorf("unknown type: %s (use: target, config)", setType)
	}
}

func (c *SetCmd) executeTarget(ctx *shell.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: set target <id|name> [options]")
	}

	targetID := args[0]
	req := &pb.UpdateTargetRequest{TargetId: targetID}
	hasChanges := false

	for i := 1; i < len(args); i++ {
		arg := args[i]

		switch {
		case strings.HasPrefix(arg, "--name="):
			v := strings.TrimPrefix(arg, "--name=")
			req.Name = &v
			hasChanges = true
		case strings.HasPrefix(arg, "--desc="):
			v := strings.TrimPrefix(arg, "--desc=")
			req.Description = &v
			hasChanges = true
		case strings.HasPrefix(arg, "--interval="):
			if v, err := strconv.ParseUint(strings.TrimPrefix(arg, "--interval="), 10, 32); err == nil {
				val := uint32(v)
				req.IntervalMs = &val
				hasChanges = true
			}
		case strings.HasPrefix(arg, "--timeout="):
			if v, err := strconv.ParseUint(strings.TrimPrefix(arg, "--timeout="), 10, 32); err == nil {
				val := uint32(v)
				req.TimeoutMs = &val
				hasChanges = true
			}
		case strings.HasPrefix(arg, "--retries="):
			if v, err := strconv.ParseUint(strings.TrimPrefix(arg, "--retries="), 10, 32); err == nil {
				val := uint32(v)
				req.Retries = &val
				hasChanges = true
			}
		case arg == "--persistent":
			val := true
			req.Persistent = &val
			hasChanges = true
		case arg == "--no-persistent":
			val := false
			req.Persistent = &val
			hasChanges = true
		case strings.HasPrefix(arg, "--tag"):
			// Handle --tag=X, --tag +X, --tag -X
			var tagVal string
			if strings.HasPrefix(arg, "--tag=") {
				tagVal = strings.TrimPrefix(arg, "--tag=")
			} else if i+1 < len(args) {
				i++
				tagVal = args[i]
			} else {
				continue
			}

			if strings.HasPrefix(tagVal, "+") {
				req.AddTags = append(req.AddTags, strings.TrimPrefix(tagVal, "+"))
			} else if strings.HasPrefix(tagVal, "-") {
				req.RemoveTags = append(req.RemoveTags, strings.TrimPrefix(tagVal, "-"))
			} else {
				req.SetTags = append(req.SetTags, tagVal)
			}
			hasChanges = true
		case strings.HasPrefix(arg, "--community="):
			v := strings.TrimPrefix(arg, "--community=")
			req.Community = &v
			hasChanges = true
		case strings.HasPrefix(arg, "--auth-pass="):
			v := strings.TrimPrefix(arg, "--auth-pass=")
			req.AuthPassword = &v
			hasChanges = true
		case strings.HasPrefix(arg, "--priv-pass="):
			v := strings.TrimPrefix(arg, "--priv-pass=")
			req.PrivPassword = &v
			hasChanges = true
		}
	}

	if !hasChanges {
		return fmt.Errorf("no changes specified")
	}

	resp, err := ctx.Client.UpdateTarget(req)
	if err != nil {
		return err
	}

	fmt.Printf("Updated: %s\n", resp.Message)
	return nil
}

func (c *SetCmd) executeConfig(ctx *shell.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: set config <key> <value>")
	}

	key := args[0]
	value := args[1]

	val, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid value: %s (must be a number)", value)
	}
	v := uint32(val)

	req := &pb.SetConfigRequest{}
	switch key {
	case "timeout", "default_timeout_ms":
		req.DefaultTimeoutMs = &v
	case "retries", "default_retries":
		req.DefaultRetries = &v
	case "buffer", "default_buffer_size":
		req.DefaultBufferSize = &v
	case "min-interval", "min_interval_ms":
		req.MinIntervalMs = &v
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	resp, err := ctx.Client.SetConfig(req)
	if err != nil {
		return err
	}

	if resp.Ok {
		fmt.Printf("Config updated: %s = %d\n", key, v)
	} else {
		fmt.Printf("Failed: %s\n", resp.Message)
	}
	return nil
}
