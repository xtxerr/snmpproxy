package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// ============================================================================
// watch
// ============================================================================

type WatchCmd struct{}

func (c *WatchCmd) Name() string       { return "watch" }
func (c *WatchCmd) Aliases() []string  { return []string{"w"} }
func (c *WatchCmd) Brief() string      { return "Watch live target updates" }

func (c *WatchCmd) Usage() string {
	return `Usage: watch <target-id|name>...

Watch live updates from one or more targets.
Press Ctrl+C to stop watching.

Examples:
  watch router-wan
  watch abc123 def456
  watch router-wan switch-core`
}

func (c *WatchCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	return completeTargetIDs(ctx, partial)
}

func (c *WatchCmd) Execute(ctx *shell.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: watch <target-id|name>...")
	}

	// Subscribe to targets
	resp, err := ctx.Client.Subscribe(args, nil)
	if err != nil {
		return err
	}

	if len(resp.Subscribed) == 0 {
		return fmt.Errorf("no targets found matching: %s", strings.Join(args, ", "))
	}

	fmt.Printf("Watching %d targets. Press Ctrl+C to stop.\n", len(resp.Subscribed))
	fmt.Println()

	// The sample handler is already set up in main
	// Just wait indefinitely (user will Ctrl+C)
	select {}
}

// ============================================================================
// sub
// ============================================================================

type SubCmd struct{}

func (c *SubCmd) Name() string       { return "sub" }
func (c *SubCmd) Aliases() []string  { return []string{"subscribe"} }
func (c *SubCmd) Brief() string      { return "Subscribe to target updates" }

func (c *SubCmd) Usage() string {
	return `Usage: sub <target-id|name>... [--tag=TAG]

Subscribe to live updates from targets.

Arguments:
  target-id    One or more target IDs or names

Options:
  --tag=TAG    Subscribe to all targets with this tag

Examples:
  sub router-wan
  sub abc123 def456
  sub --tag=location/dc1`
}

func (c *SubCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	if strings.HasPrefix(partial, "--tag=") {
		// Complete tag
		return nil
	}
	if strings.HasPrefix(partial, "-") {
		return []Suggestion{{Text: "--tag=", Description: "Subscribe by tag"}}
	}
	return completeTargetIDs(ctx, partial)
}

func (c *SubCmd) Execute(ctx *shell.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sub <target-id|name>... [--tag=TAG]")
	}

	var targetIDs []string
	var tags []string

	for _, arg := range args {
		if strings.HasPrefix(arg, "--tag=") {
			tags = append(tags, strings.TrimPrefix(arg, "--tag="))
		} else if !strings.HasPrefix(arg, "-") {
			targetIDs = append(targetIDs, arg)
		}
	}

	resp, err := ctx.Client.Subscribe(targetIDs, tags)
	if err != nil {
		return err
	}

	if len(resp.Subscribed) == 0 {
		fmt.Println("No targets matched")
	} else {
		fmt.Printf("Subscribed to %d targets:\n", len(resp.Subscribed))
		for _, id := range resp.Subscribed {
			fmt.Printf("  %s\n", id)
		}
	}
	fmt.Printf("Total subscriptions: %d\n", resp.TotalSubscribed)

	return nil
}

// ============================================================================
// unsub
// ============================================================================

type UnsubCmd struct{}

func (c *UnsubCmd) Name() string       { return "unsub" }
func (c *UnsubCmd) Aliases() []string  { return []string{"unsubscribe"} }
func (c *UnsubCmd) Brief() string      { return "Unsubscribe from targets" }

func (c *UnsubCmd) Usage() string {
	return `Usage: unsub [target-id|name]...

Unsubscribe from target updates.
If no targets specified, unsubscribes from all.

Examples:
  unsub router-wan
  unsub abc123 def456
  unsub                   # Unsubscribe from all`
}

func (c *UnsubCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	return completeTargetIDs(ctx, partial)
}

func (c *UnsubCmd) Execute(ctx *shell.Context, args []string) error {
	resp, err := ctx.Client.Unsubscribe(args)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		fmt.Println("Unsubscribed from all targets")
	} else {
		fmt.Printf("Unsubscribed from %d targets\n", len(resp.Unsubscribed))
	}
	fmt.Printf("Remaining subscriptions: %d\n", resp.TotalSubscribed)

	return nil
}

// ============================================================================
// history
// ============================================================================

type HistoryCmd struct{}

func (c *HistoryCmd) Name() string       { return "history" }
func (c *HistoryCmd) Aliases() []string  { return []string{"hist"} }
func (c *HistoryCmd) Brief() string      { return "Show target history" }

func (c *HistoryCmd) Usage() string {
	return `Usage: history <target-id|name> [count]

Show historical samples for a target.

Arguments:
  target-id    Target ID or name
  count        Number of samples (default: 20)

Examples:
  history router-wan
  history abc123 50`
}

func (c *HistoryCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	if len(args) == 0 {
		return completeTargetIDs(ctx, partial)
	}
	return nil
}

func (c *HistoryCmd) Execute(ctx *shell.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: history <target-id|name> [count]")
	}

	targetID := args[0]
	count := uint32(20)

	if len(args) > 1 {
		if n, err := fmt.Sscanf(args[1], "%d", &count); n == 1 && err == nil {
			// ok
		}
	}

	resp, err := ctx.Client.GetHistory(targetID, count)
	if err != nil {
		return err
	}

	if len(resp.Samples) == 0 {
		fmt.Println("No samples")
		return nil
	}

	// Header
	fmt.Printf("%-24s %-20s %-6s %-8s\n", "TIMESTAMP", "VALUE", "OK", "POLL MS")
	fmt.Println(strings.Repeat("-", 65))

	for _, s := range resp.Samples {
		ts := time.UnixMilli(s.TimestampMs).Format("2006-01-02 15:04:05.000")
		ok := "✓"
		if !s.Valid {
			ok = "✗"
		}

		value := fmt.Sprintf("%d", s.Counter)
		if s.Text != "" {
			value = truncate(s.Text, 20)
		}
		if !s.Valid && s.Error != "" {
			value = s.Error
		}

		fmt.Printf("%-24s %-20s %-6s %-8d\n", ts, value, ok, s.PollMs)
	}

	fmt.Printf("\nShowing %d of %d buffered samples\n", len(resp.Samples), resp.TotalBuffered)
	return nil
}

// ============================================================================
// Helper
// ============================================================================

func completeTargetIDs(ctx *shell.Context, partial string) []Suggestion {
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

		// Also by name
		if t.Name != "" && (partial == "" || strings.HasPrefix(t.Name, partial)) {
			suggestions = append(suggestions, Suggestion{
				Text:        t.Name,
				Description: fmt.Sprintf("→ %s", t.Id),
			})
		}
	}

	return suggestions
}
