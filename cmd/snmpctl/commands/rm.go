package commands

import (
	"fmt"
	"strings"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// RmCmd removes targets, tags, or aliases.
type RmCmd struct{}

func (c *RmCmd) Name() string       { return "rm" }
func (c *RmCmd) Aliases() []string  { return []string{"del", "delete"} }
func (c *RmCmd) Brief() string      { return "Remove targets or tags" }

func (c *RmCmd) Usage() string {
	return `Usage: rm <type> <id> [options]

Remove a target or tag.

Types:
  target <id|name>    Remove a target
  tag <path>          Remove a tag (future)

Options:
  -f, --force         Force removal (even if persistent)

Examples:
  rm target router-wan
  rm target abc123
  rm target router-wan -f`
}

func (c *RmCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	// rm [TAB] -> types
	if len(args) == 0 {
		return FilterSuggestions(rmTypes, partial)
	}

	rmType := strings.ToLower(args[0])

	switch rmType {
	case "target":
		if len(args) == 1 {
			return c.completeTargetID(ctx, partial)
		}
		return FilterSuggestions(rmFlags, partial)
	case "tag":
		// Future: complete tag paths
		return nil
	default:
		return FilterSuggestions(rmTypes, partial)
	}
}

var rmTypes = []Suggestion{
	{Text: "target", Description: "Remove a target"},
	{Text: "tag", Description: "Remove a tag"},
}

var rmFlags = []Suggestion{
	{Text: "-f", Description: "Force removal"},
	{Text: "--force", Description: "Force removal"},
}

func (c *RmCmd) completeTargetID(ctx *shell.Context, partial string) []Suggestion {
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

		// Suggest by ID
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

		// Also suggest by name if set
		if t.Name != "" && (partial == "" || strings.HasPrefix(t.Name, partial)) {
			suggestions = append(suggestions, Suggestion{
				Text:        t.Name,
				Description: fmt.Sprintf("→ %s", t.Id),
			})
		}
	}

	return suggestions
}

func (c *RmCmd) Execute(ctx *shell.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: rm target <id|name> [-f]")
	}

	rmType := strings.ToLower(args[0])

	switch rmType {
	case "target":
		return c.executeTarget(ctx, args[1:])
	case "tag":
		return fmt.Errorf("tag removal not yet implemented")
	default:
		return fmt.Errorf("unknown type: %s (use: target, tag)", rmType)
	}
}

func (c *RmCmd) executeTarget(ctx *shell.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: rm target <id|name> [-f]")
	}

	targetID := args[0]
	force := false

	for _, arg := range args[1:] {
		if arg == "-f" || arg == "--force" {
			force = true
		}
	}

	resp, err := ctx.Client.DeleteTarget(targetID, force)
	if err != nil {
		return err
	}

	fmt.Printf("Removed: %s\n", resp.Message)
	return nil
}
