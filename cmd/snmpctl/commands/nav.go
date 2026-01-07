package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// ============================================================================
// pwd
// ============================================================================

type PwdCmd struct{}

func (c *PwdCmd) Name() string       { return "pwd" }
func (c *PwdCmd) Aliases() []string  { return nil }
func (c *PwdCmd) Brief() string      { return "Print current path" }
func (c *PwdCmd) Usage() string      { return "Usage: pwd\n\nPrint the current working path." }

func (c *PwdCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	return nil
}

func (c *PwdCmd) Execute(ctx *shell.Context, args []string) error {
	fmt.Println(ctx.Path)
	return nil
}

// ============================================================================
// cd
// ============================================================================

type CdCmd struct{}

func (c *CdCmd) Name() string       { return "cd" }
func (c *CdCmd) Aliases() []string  { return nil }
func (c *CdCmd) Brief() string      { return "Change current path" }

func (c *CdCmd) Usage() string {
	return `Usage: cd [path]

Change the current working path.

Arguments:
  path    Path to navigate to (absolute or relative)
          If omitted, returns to root (/)

Examples:
  cd /targets           Go to targets
  cd /tags/location     Go to tag path
  cd ..                 Go up one level
  cd                    Go to root`
}

func (c *CdCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	return completePath(ctx, partial)
}

func (c *CdCmd) Execute(ctx *shell.Context, args []string) error {
	path := "/"
	if len(args) > 0 {
		path = args[0]
	}

	return ctx.Cd(path)
}

// ============================================================================
// ls
// ============================================================================

type LsCmd struct{}

func (c *LsCmd) Name() string       { return "ls" }
func (c *LsCmd) Aliases() []string  { return []string{"l", "list"} }
func (c *LsCmd) Brief() string      { return "List contents" }

func (c *LsCmd) Usage() string {
	return `Usage: ls [path] [options]

List contents of the current or specified path.

Arguments:
  path    Path to list (default: current path)

Options:
  -l, --long     Show extended details
  --state=X      Filter by state: polling, unreachable, error
  --tag=X        Filter by tag (repeatable)
  --host=X       Filter by host pattern (glob)

Paths:
  /               Root - overview of all sections
  /targets        All targets
  /targets/<id>   Target details
  /tags           Tag hierarchy
  /tags/<path>    Targets with specific tag
  /server         Server information
  /session        Current session

Examples:
  ls                    List current path
  ls /targets           List all targets
  ls -l                 List with details
  ls --state=polling    Filter by state
  ls /tags/location     List location tags`
}

func (c *LsCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	// Check for flag completion
	if strings.HasPrefix(partial, "-") {
		return FilterSuggestions(lsFlags, partial)
	}

	// Check if we already have a path argument
	hasPath := false
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			hasPath = true
			break
		}
	}

	if !hasPath {
		// Complete path or flags
		if partial == "" {
			// Suggest both paths and flags
			paths := completePath(ctx, "")
			return append(paths, lsFlags...)
		}
		if strings.HasPrefix(partial, "/") || strings.HasPrefix(partial, ".") {
			return completePath(ctx, partial)
		}
		// Could be either
		paths := completePath(ctx, partial)
		flags := FilterSuggestions(lsFlags, partial)
		return append(paths, flags...)
	}

	// Already have path, only suggest flags
	return FilterSuggestions(lsFlags, partial)
}

var lsFlags = []Suggestion{
	{Text: "-l", Description: "Long format with details"},
	{Text: "--long", Description: "Long format with details"},
	{Text: "--state=", Description: "Filter by state"},
	{Text: "--tag=", Description: "Filter by tag"},
	{Text: "--host=", Description: "Filter by host pattern"},
}

func (c *LsCmd) Execute(ctx *shell.Context, args []string) error {
	// Parse arguments
	path := ctx.Path
	long := false
	var tags []string
	state := ""
	host := ""

	for _, arg := range args {
		switch {
		case arg == "-l" || arg == "--long":
			long = true
		case strings.HasPrefix(arg, "--state="):
			state = strings.TrimPrefix(arg, "--state=")
		case strings.HasPrefix(arg, "--tag="):
			tags = append(tags, strings.TrimPrefix(arg, "--tag="))
		case strings.HasPrefix(arg, "--host="):
			host = strings.TrimPrefix(arg, "--host=")
		case !strings.HasPrefix(arg, "-"):
			path = ctx.ResolvePath(arg)
		}
	}

	// Build browse request
	req := &pb.BrowseRequest{
		Path:       path,
		LongFormat: long,
		FilterTags: tags,
		FilterState: state,
		FilterHost: host,
		Limit:      100,
	}

	resp, err := ctx.Client.Browse(req)
	if err != nil {
		return err
	}

	// Display results
	return displayBrowseResult(resp, long)
}

func displayBrowseResult(resp *pb.BrowseResponse, long bool) error {
	if len(resp.Nodes) == 0 {
		fmt.Println("(empty)")
		return nil
	}

	// Group by type for display
	var dirs, targets, infos []*pb.BrowseNode
	for _, node := range resp.Nodes {
		switch node.Type {
		case pb.NodeType_NODE_DIRECTORY:
			dirs = append(dirs, node)
		case pb.NodeType_NODE_TARGET:
			targets = append(targets, node)
		case pb.NodeType_NODE_INFO:
			infos = append(infos, node)
		}
	}

	// Display directories
	for _, node := range dirs {
		if long {
			fmt.Printf("%-20s  %s\n", node.Name+"/", node.Description)
		} else {
			fmt.Printf("%s/\n", node.Name)
		}
	}

	// Display targets
	if long && len(targets) > 0 {
		fmt.Printf("\n%-12s %-20s %-12s %-10s %s\n", "ID", "HOST", "STATE", "INTERVAL", "NAME")
		fmt.Println(strings.Repeat("-", 70))
	}
	for _, node := range targets {
		if long {
			t := node.Target
			if t != nil {
				host := ""
				if snmp := t.GetSnmp(); snmp != nil {
					host = fmt.Sprintf("%s:%d", snmp.Host, snmp.Port)
				}
				name := t.Name
				if name == "" {
					name = t.Description
				}
				fmt.Printf("%-12s %-20s %-12s %-10s %s\n",
					t.Id, truncate(host, 20), t.State,
					fmt.Sprintf("%dms", t.IntervalMs), truncate(name, 20))
			}
		} else {
			desc := node.Description
			if desc != "" {
				fmt.Printf("%-12s  %s\n", node.Name, desc)
			} else {
				fmt.Println(node.Name)
			}
		}
	}

	// Display info nodes
	for _, node := range infos {
		if long {
			fmt.Printf("\n%s:\n", node.Name)
			// Sort keys for consistent output
			var keys []string
			for k := range node.Info {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("  %-20s %s\n", k+":", node.Info[k])
			}
		} else {
			fmt.Printf("%s  %s\n", node.Name, node.Description)
		}
	}

	// Show pagination info
	if resp.HasMore {
		fmt.Printf("\n(more results available, use --cursor=%s)\n", resp.NextCursor)
	}

	return nil
}

// ============================================================================
// Path Completion Helper
// ============================================================================

func completePath(ctx *shell.Context, partial string) []Suggestion {
	// Determine base path and prefix to complete
	basePath := ctx.Path
	prefix := partial

	if strings.HasPrefix(partial, "/") {
		// Absolute path
		lastSlash := strings.LastIndex(partial, "/")
		if lastSlash > 0 {
			basePath = partial[:lastSlash]
			prefix = partial[lastSlash+1:]
		} else {
			basePath = "/"
			prefix = partial[1:]
		}
	} else if strings.Contains(partial, "/") {
		// Relative path with slashes
		lastSlash := strings.LastIndex(partial, "/")
		basePath = ctx.ResolvePath(partial[:lastSlash])
		prefix = partial[lastSlash+1:]
	}

	// Fetch children of basePath
	req := &pb.BrowseRequest{
		Path:  basePath,
		Limit: 50,
	}

	resp, err := ctx.Client.Browse(req)
	if err != nil {
		return nil
	}

	var suggestions []Suggestion
	for _, node := range resp.Nodes {
		name := node.Name
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}

		// Build full path for suggestion
		var fullPath string
		if strings.HasPrefix(partial, "/") {
			if basePath == "/" {
				fullPath = "/" + name
			} else {
				fullPath = basePath + "/" + name
			}
		} else if strings.Contains(partial, "/") {
			lastSlash := strings.LastIndex(partial, "/")
			fullPath = partial[:lastSlash+1] + name
		} else {
			fullPath = name
		}

		// Add trailing slash for directories
		if node.Type == pb.NodeType_NODE_DIRECTORY {
			fullPath += "/"
		}

		suggestions = append(suggestions, Suggestion{
			Text:        fullPath,
			Description: node.Description,
		})
	}

	return suggestions
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-2] + ".."
}
