package commands

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// CatCmd shows detailed information.
type CatCmd struct{}

func (c *CatCmd) Name() string       { return "cat" }
func (c *CatCmd) Aliases() []string  { return []string{"show", "info"} }
func (c *CatCmd) Brief() string      { return "Show detailed information" }

func (c *CatCmd) Usage() string {
	return `Usage: cat [path]

Show detailed information for the current or specified path.

Examples:
  cat                     Show current path details
  cat /targets/abc123     Show target details
  cat /server/config      Show server config
  cat /session            Show session info`
}

func (c *CatCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	return completePath(ctx, partial)
}

func (c *CatCmd) Execute(ctx *shell.Context, args []string) error {
	path := ctx.Path
	if len(args) > 0 {
		path = ctx.ResolvePath(args[0])
	}

	req := &pb.BrowseRequest{
		Path:       path,
		LongFormat: true,
	}

	resp, err := ctx.Client.Browse(req)
	if err != nil {
		return err
	}

	return c.display(resp)
}

func (c *CatCmd) display(resp *pb.BrowseResponse) error {
	fmt.Printf("Path: %s\n", resp.Path)
	fmt.Println()

	for _, node := range resp.Nodes {
		switch node.Type {
		case pb.NodeType_NODE_TARGET:
			c.displayTarget(node)
		case pb.NodeType_NODE_INFO:
			c.displayInfo(node)
		case pb.NodeType_NODE_DIRECTORY:
			c.displayDirectory(node)
		}
	}

	return nil
}

func (c *CatCmd) displayTarget(node *pb.BrowseNode) {
	t := node.Target
	if t == nil {
		fmt.Printf("Target: %s\n", node.Name)
		return
	}

	fmt.Printf("Target: %s\n", t.Id)
	fmt.Println(strings.Repeat("-", 40))

	if t.Name != "" {
		fmt.Printf("  Name:        %s\n", t.Name)
	}
	if t.Description != "" {
		fmt.Printf("  Description: %s\n", t.Description)
	}

	// SNMP Config
	if snmp := t.GetSnmp(); snmp != nil {
		fmt.Printf("  Protocol:    SNMP\n")
		fmt.Printf("  Host:        %s:%d\n", snmp.Host, snmp.Port)
		fmt.Printf("  OID:         %s\n", snmp.Oid)

		if v2c := snmp.GetV2C(); v2c != nil {
			fmt.Printf("  Version:     v2c\n")
			fmt.Printf("  Community:   %s\n", v2c.Community)
		}
		if v3 := snmp.GetV3(); v3 != nil {
			fmt.Printf("  Version:     v3\n")
			fmt.Printf("  Security:    %s (%s)\n", v3.SecurityName, v3.SecurityLevel)
		}

		if snmp.TimeoutMs > 0 {
			fmt.Printf("  Timeout:     %dms\n", snmp.TimeoutMs)
		}
		if snmp.Retries > 0 {
			fmt.Printf("  Retries:     %d\n", snmp.Retries)
		}
	}

	fmt.Println()

	// Polling config
	fmt.Printf("  Interval:    %dms\n", t.IntervalMs)
	fmt.Printf("  Buffer:      %d/%d samples\n", t.SamplesBuffered, t.BufferSize)
	if t.Persistent {
		fmt.Printf("  Persistent:  yes\n")
	}

	// Tags
	if len(t.Tags) > 0 {
		fmt.Printf("  Tags:        %s\n", strings.Join(t.Tags, ", "))
	}

	fmt.Println()

	// State
	stateIcon := "●"
	switch t.State {
	case "polling":
		stateIcon = "✓"
	case "unreachable":
		stateIcon = "✗"
	case "error":
		stateIcon = "!"
	}
	fmt.Printf("  State:       %s %s\n", stateIcon, t.State)

	if t.LastPollMs > 0 {
		lastPoll := time.UnixMilli(t.LastPollMs)
		fmt.Printf("  Last Poll:   %s (%s ago)\n",
			lastPoll.Format("2006-01-02 15:04:05"),
			time.Since(lastPoll).Truncate(time.Second))
	}

	if t.LastError != "" {
		fmt.Printf("  Last Error:  %s\n", t.LastError)
	}

	fmt.Println()

	// Statistics
	if t.PollsTotal > 0 {
		successRate := float64(t.PollsSuccess) / float64(t.PollsTotal) * 100
		fmt.Printf("  Polls:       %d total, %d ok, %d failed (%.1f%% success)\n",
			t.PollsTotal, t.PollsSuccess, t.PollsFailed, successRate)
		fmt.Printf("  Poll Time:   avg %dms, min %dms, max %dms\n",
			t.AvgPollMs, t.MinPollMs, t.MaxPollMs)
	}

	if t.CreatedAtMs > 0 {
		created := time.UnixMilli(t.CreatedAtMs)
		fmt.Printf("  Created:     %s\n", created.Format("2006-01-02 15:04:05"))
	}

	fmt.Printf("  Owners:      %d\n", t.OwnerCount)
	fmt.Printf("  Subscribers: %d\n", t.SubscriberCount)

	fmt.Println()
}

func (c *CatCmd) displayInfo(node *pb.BrowseNode) {
	fmt.Printf("%s\n", node.Name)
	fmt.Println(strings.Repeat("-", 40))

	if node.Description != "" {
		fmt.Printf("  %s\n\n", node.Description)
	}

	// Sort keys
	var keys []string
	for k := range node.Info {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fmt.Printf("  %-20s %s\n", k+":", node.Info[k])
	}

	fmt.Println()
}

func (c *CatCmd) displayDirectory(node *pb.BrowseNode) {
	fmt.Printf("%s/\n", node.Name)
	if node.Description != "" {
		fmt.Printf("  %s\n", node.Description)
	}
	if node.TargetCount > 0 {
		fmt.Printf("  Targets: %d\n", node.TargetCount)
	}
	if node.ChildCount > 0 {
		fmt.Printf("  Children: %d\n", node.ChildCount)
	}
	fmt.Println()
}
