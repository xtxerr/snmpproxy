// snmpctl is the SNMP proxy CLI client.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c-bata/go-prompt"
	"github.com/c-bata/go-prompt/completer"
	"github.com/xtxerr/snmpproxy/internal/client"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

var (
	serverAddr    = flag.String("server", "localhost:9161", "server address")
	token         = flag.String("token", "", "auth token (or SNMPPROXY_TOKEN env)")
	noTLS         = flag.Bool("no-tls", false, "disable TLS")
	tlsSkipVerify = flag.Bool("tls-skip-verify", false, "skip TLS certificate verification")
	noPrompt      = flag.Bool("no-prompt", false, "disable interactive prompt (use basic readline)")
)

var cli *client.Client
var comp *Completer
var historyFile string

func main() {
	flag.Parse()

	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("SNMPPROXY_TOKEN")
	}
	if authToken == "" {
		log.Fatal("Token required: use -token or set SNMPPROXY_TOKEN")
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// Setup history file
	homeDir, _ := os.UserHomeDir()
	historyFile = filepath.Join(homeDir, ".snmpctl_history")

	cli = client.New(&client.Config{
		Addr:          *serverAddr,
		Token:         authToken,
		TLS:           !*noTLS,
		TLSSkipVerify: *tlsSkipVerify,
	})

	fmt.Printf("Connecting to %s...\n", *serverAddr)
	if err := cli.Connect(); err != nil {
		log.Fatalf("Connect: %v", err)
	}
	fmt.Printf("Connected (session: %s)\n\n", cli.SessionID())

	cli.OnSample(handleSample)

	comp = NewCompleter(cli)

	printHelp()
	
	if *noPrompt {
		interactiveBasic()
	} else {
		interactivePrompt()
	}
}

func handleSample(s *pb.Sample) {
	ts := time.UnixMilli(s.TimestampMs).Format("15:04:05.000")
	if s.Valid {
		if s.Text != "" {
			fmt.Printf("[%s] %s: %s (%dms)\n", ts, s.TargetId, s.Text, s.PollMs)
		} else {
			fmt.Printf("[%s] %s: %d (%dms)\n", ts, s.TargetId, s.Counter, s.PollMs)
		}
	} else {
		fmt.Printf("[%s] %s: ERROR %s\n", ts, s.TargetId, s.Error)
	}
}

func printHelp() {
	fmt.Println(`Commands:

  ls [path] [flags]           Browse paths
    Paths:
      /                       Root
      /targets                All targets
      /targets/<id>           Target details
      /targets/<id>/config    Target config
      /targets/<id>/stats     Target statistics
      /targets/<id>/history   Sample history
      /tags                   Tag hierarchy
      /tags/<path>            Sub-tags and targets
      /server                 Server info
      /server/status          Server status
      /server/config          Runtime config
      /server/sessions        All sessions
      /session                Current session
      /session/owned          Owned targets
      /session/subscribed     Subscribed targets
    Flags:
      -l, --long              Extended details
      --tag=X                 Filter by tag (repeatable)
      --state=X               Filter: polling, unreachable, error
      --protocol=X            Filter: snmp, http, icmp
      --host=X                Filter by host (glob: 192.168.*)
      --limit=N               Max results (default 100)
      --cursor=X              Pagination cursor
      --json                  JSON output

  add snmp <host> <oid> [flags]
    --name=X                  User-friendly name
    --id=X                    Custom target ID (auto-generated if empty)
    --desc=X                  Description
    --tag=X                   Tag (repeatable)
    --interval=N              Poll interval ms (default 1000)
    --buffer=N                Buffer size (default 3600)
    --persistent              Persistent target
    SNMPv2c:
      --community=X           Community (default: public)
    SNMPv3:
      --v3                    Use SNMPv3
      --user=X                Security name
      --level=X               noAuthNoPriv, authNoPriv, authPriv
      --auth-proto=X          MD5, SHA, SHA256, etc.
      --auth-pass=X           Auth password
      --priv-proto=X          DES, AES, AES256, etc.
      --priv-pass=X           Privacy password

  set <target-id|name> [flags]
    --name=X                  Change name
    --desc=X                  Set description
    --interval=N              Set poll interval
    --buffer=N                Set buffer size
    --timeout=N               Set SNMP timeout
    --retries=N               Set SNMP retries
    --persistent              Mark as persistent
    --no-persistent           Remove persistent flag
    --tag +X                  Add tag
    --tag -X                  Remove tag
    --tag X                   Set tags (replace all)
    --community=X             Change SNMPv2c community
    --auth-pass=X             Change SNMPv3 auth password
    --priv-pass=X             Change SNMPv3 priv password

  rm <target-id|name> [-f]    Delete target (-f for force)

  history <target-id|name> [n]  Show last n samples (default 10)

  sub <target-id|name>... [--tag=X]  Subscribe to targets
  unsub [target-id|name]...          Unsubscribe (all if no args)

  config                      Show runtime config
  config set <key> <value>    Set config value
    Keys: timeout, retries, buffer, min-interval

  help                        Show this help
  quit                        Exit
`)
}

// interactivePrompt uses go-prompt for tab completion and history.
func interactivePrompt() {
	// Load history
	history := loadHistory()

	p := prompt.New(
		executor,
		comp.Complete,
		prompt.OptionPrefix("> "),
		prompt.OptionTitle("snmpctl"),
		prompt.OptionPrefixTextColor(prompt.Cyan),
		prompt.OptionPreviewSuggestionTextColor(prompt.DarkGray),
		prompt.OptionSelectedSuggestionBGColor(prompt.DarkBlue),
		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionMaxSuggestion(10),
		prompt.OptionHistory(history),
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlC,
			Fn: func(b *prompt.Buffer) {
				fmt.Println("^C")
			},
		}),
		prompt.OptionCompletionWordSeparator(completer.FilePathCompletionSeparator),
	)
	p.Run()
}

func loadHistory() []string {
	data, err := os.ReadFile(historyFile)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var history []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			history = append(history, line)
		}
	}
	// Keep last 1000 entries
	if len(history) > 1000 {
		history = history[len(history)-1000:]
	}
	return history
}

func saveHistory(line string) {
	f, err := os.OpenFile(historyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line + "\n")
}

func executor(input string) {
	line := strings.TrimSpace(input)
	if line == "" {
		return
	}

	// Save to history
	saveHistory(line)

	// Check connection status
	if !cli.IsConnected() {
		fmt.Println("Error: Connection lost. Reconnecting...")
		if err := cli.Reconnect(); err != nil {
			fmt.Printf("Reconnect failed: %v\n", err)
			return
		}
		fmt.Println("Reconnected.")
	}

	args := parseArgs(line)
	if len(args) == 0 {
		return
	}

	cmd := strings.ToLower(args[0])
	cmdArgs := args[1:]

	switch cmd {
	case "ls", "l", "list":
		cmdLs(cmdArgs)
	case "add", "a":
		cmdAdd(cmdArgs)
	case "set", "s":
		cmdSet(cmdArgs)
	case "rm", "del", "delete":
		cmdRm(cmdArgs)
	case "history", "hist", "h":
		cmdHistory(cmdArgs)
	case "sub", "subscribe":
		cmdSub(cmdArgs)
	case "unsub", "unsubscribe":
		cmdUnsub(cmdArgs)
	case "config", "cfg":
		cmdConfig(cmdArgs)
	case "help", "?":
		printHelp()
	case "quit", "exit", "q":
		cli.Close()
		fmt.Println("Goodbye!")
		os.Exit(0)
	default:
		fmt.Printf("Unknown command: %s (type 'help' for commands)\n", cmd)
	}
}

// interactiveBasic uses basic readline without completion.
func interactiveBasic() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}

		args := parseArgs(line)
		if len(args) == 0 {
			fmt.Print("> ")
			continue
		}

		cmd := strings.ToLower(args[0])
		cmdArgs := args[1:]

		switch cmd {
		case "ls", "l", "list":
			cmdLs(cmdArgs)
		case "add", "a":
			cmdAdd(cmdArgs)
		case "set", "s":
			cmdSet(cmdArgs)
		case "rm", "del", "delete":
			cmdRm(cmdArgs)
		case "history", "hist", "h":
			cmdHistory(cmdArgs)
		case "sub", "subscribe":
			cmdSub(cmdArgs)
		case "unsub", "unsubscribe":
			cmdUnsub(cmdArgs)
		case "config", "cfg":
			cmdConfig(cmdArgs)
		case "help", "?":
			printHelp()
		case "quit", "exit", "q":
			cli.Close()
			return
		default:
			fmt.Printf("Unknown command: %s (type 'help' for commands)\n", cmd)
		}

		fmt.Print("> ")
	}
}

func parseArgs(line string) []string {
	var args []string
	var current strings.Builder
	inQuote := false

	for _, r := range line {
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

// ============================================================================
// ls command
// ============================================================================

func cmdLs(args []string) {
	opts := &client.BrowseOptions{
		Path:  "/",
		Limit: 100,
	}

	jsonOutput := false

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "-l" || arg == "--long":
			opts.LongFormat = true
		case strings.HasPrefix(arg, "--tag="):
			opts.Tags = append(opts.Tags, strings.TrimPrefix(arg, "--tag="))
		case strings.HasPrefix(arg, "--state="):
			opts.State = strings.TrimPrefix(arg, "--state=")
		case strings.HasPrefix(arg, "--protocol="):
			opts.Protocol = strings.TrimPrefix(arg, "--protocol=")
		case strings.HasPrefix(arg, "--host="):
			opts.Host = strings.TrimPrefix(arg, "--host=")
		case strings.HasPrefix(arg, "--limit="):
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit=")); err == nil {
				opts.Limit = int32(v)
			}
		case strings.HasPrefix(arg, "--cursor="):
			opts.Cursor = strings.TrimPrefix(arg, "--cursor=")
		case arg == "--json":
			jsonOutput = true
		case !strings.HasPrefix(arg, "-"):
			opts.Path = arg
			if !strings.HasPrefix(opts.Path, "/") {
				opts.Path = "/" + opts.Path
			}
		}
	}

	resp, err := cli.Browse(opts)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if jsonOutput {
		printBrowseJSON(resp)
		return
	}

	printBrowseResult(resp, opts.LongFormat)
}

func printBrowseResult(resp *pb.BrowseResponse, longFormat bool) {
	if len(resp.Nodes) == 0 {
		fmt.Println("(empty)")
		return
	}

	// Separate directories and targets
	var dirs, targets, infos []*pb.BrowseNode
	for _, n := range resp.Nodes {
		switch n.Type {
		case pb.NodeType_NODE_DIRECTORY:
			dirs = append(dirs, n)
		case pb.NodeType_NODE_TARGET:
			targets = append(targets, n)
		case pb.NodeType_NODE_INFO:
			infos = append(infos, n)
		}
	}

	// Print directories
	for _, n := range dirs {
		if n.TargetCount > 0 {
			fmt.Printf("%-20s %d targets\n", n.Name+"/", n.TargetCount)
		} else if n.Description != "" {
			fmt.Printf("%-20s %s\n", n.Name+"/", n.Description)
		} else {
			fmt.Printf("%s/\n", n.Name)
		}
	}

	// Print targets
	if len(targets) > 0 {
		if longFormat {
			printTargetsLong(targets)
		} else {
			printTargetsShort(targets)
		}
	}

	// Print info nodes
	for _, n := range infos {
		if len(n.Info) > 0 {
			printInfo(n.Name, n.Info)
		} else if n.Description != "" {
			fmt.Printf("%-20s %s\n", n.Name, n.Description)
		}
	}

	// Pagination info
	if resp.HasMore {
		fmt.Printf("\n--more-- (cursor: %s)\n", resp.NextCursor)
	}
	if resp.TotalCount > 0 && int(resp.TotalCount) > len(resp.Nodes) {
		fmt.Printf("Showing %d of %d\n", len(resp.Nodes), resp.TotalCount)
	}
}

func printTargetsShort(nodes []*pb.BrowseNode) {
	for _, n := range nodes {
		desc := n.Description
		if desc == "" && n.Target != nil {
			desc = n.Target.State
		}
		fmt.Printf("%-12s %s\n", n.Name, desc)
	}
}

func printTargetsLong(nodes []*pb.BrowseNode) {
	fmt.Printf("%-10s %-18s %-12s %-10s %-8s %s\n",
		"ID", "Host", "State", "Interval", "Buffer", "Description")
	fmt.Println(strings.Repeat("-", 80))

	for _, n := range nodes {
		t := n.Target
		if t == nil {
			fmt.Printf("%-10s (no details)\n", n.Name)
			continue
		}

		host := ""
		if snmpCfg := t.GetSnmp(); snmpCfg != nil {
			host = fmt.Sprintf("%s:%d", snmpCfg.Host, snmpCfg.Port)
		}

		buffer := fmt.Sprintf("%d/%d", t.SamplesBuffered, t.BufferSize)
		interval := fmt.Sprintf("%dms", t.IntervalMs)

		persistent := ""
		if t.Persistent {
			persistent = " [P]"
		}

		fmt.Printf("%-10s %-18s %-12s %-10s %-8s %s%s\n",
			truncate(t.Id, 10),
			truncate(host, 18),
			t.State,
			interval,
			buffer,
			truncate(t.Description, 20),
			persistent)
	}
}

func printInfo(name string, info map[string]string) {
	if name != "" {
		fmt.Printf("=== %s ===\n", name)
	}

	// Sort keys for consistent output
	keys := make([]string, 0, len(info))
	for k := range info {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	maxKeyLen := 0
	for _, k := range keys {
		if len(k) > maxKeyLen {
			maxKeyLen = len(k)
		}
	}

	for _, k := range keys {
		fmt.Printf("  %-*s  %s\n", maxKeyLen, k+":", info[k])
	}
}

func printBrowseJSON(resp *pb.BrowseResponse) {
	fmt.Printf("{\n")
	fmt.Printf("  \"path\": %q,\n", resp.Path)
	fmt.Printf("  \"total_count\": %d,\n", resp.TotalCount)
	fmt.Printf("  \"has_more\": %v,\n", resp.HasMore)
	if resp.NextCursor != "" {
		fmt.Printf("  \"next_cursor\": %q,\n", resp.NextCursor)
	}
	fmt.Printf("  \"nodes\": [\n")
	for i, n := range resp.Nodes {
		comma := ","
		if i == len(resp.Nodes)-1 {
			comma = ""
		}
		fmt.Printf("    {\"name\": %q, \"type\": %q, \"description\": %q}%s\n",
			n.Name, n.Type.String(), n.Description, comma)
	}
	fmt.Printf("  ]\n")
	fmt.Printf("}\n")
}

// ============================================================================
// add command
// ============================================================================

func cmdAdd(args []string) {
	if len(args) < 3 || args[0] != "snmp" {
		fmt.Println("Usage: add snmp <host> <oid> [flags]")
		return
	}

	host := args[1]
	oid := args[2]

	// Build SNMP config
	snmpCfg := &pb.SNMPTargetConfig{
		Host: host,
		Port: 161,
		Oid:  oid,
	}

	req := &pb.CreateTargetRequest{
		IntervalMs: 1000,
	}

	// Defaults
	community := "public"
	useV3 := false
	var v3 pb.SNMPv3Config

	var tags []string

	for i := 3; i < len(args); i++ {
		arg := args[i]

		switch {
		case strings.HasPrefix(arg, "--id="):
			req.Id = strings.TrimPrefix(arg, "--id=")
		case strings.HasPrefix(arg, "--name="):
			req.Name = strings.TrimPrefix(arg, "--name=")
		case strings.HasPrefix(arg, "--desc="):
			req.Description = strings.TrimPrefix(arg, "--desc=")
		case strings.HasPrefix(arg, "--tag="):
			tags = append(tags, strings.TrimPrefix(arg, "--tag="))
		case strings.HasPrefix(arg, "--interval="):
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--interval=")); err == nil {
				req.IntervalMs = uint32(v)
			}
		case strings.HasPrefix(arg, "--buffer="):
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--buffer=")); err == nil {
				req.BufferSize = uint32(v)
			}
		case arg == "--persistent":
			req.Persistent = true
		case strings.HasPrefix(arg, "--community="):
			community = strings.TrimPrefix(arg, "--community=")
		case arg == "--v3":
			useV3 = true
		case strings.HasPrefix(arg, "--user="):
			v3.SecurityName = strings.TrimPrefix(arg, "--user=")
		case strings.HasPrefix(arg, "--level="):
			v3.SecurityLevel = strings.TrimPrefix(arg, "--level=")
		case strings.HasPrefix(arg, "--auth-proto="):
			v3.AuthProtocol = strings.TrimPrefix(arg, "--auth-proto=")
		case strings.HasPrefix(arg, "--auth-pass="):
			v3.AuthPassword = strings.TrimPrefix(arg, "--auth-pass=")
		case strings.HasPrefix(arg, "--priv-proto="):
			v3.PrivProtocol = strings.TrimPrefix(arg, "--priv-proto=")
		case strings.HasPrefix(arg, "--priv-pass="):
			v3.PrivPassword = strings.TrimPrefix(arg, "--priv-pass=")
		case strings.HasPrefix(arg, "--timeout="):
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--timeout=")); err == nil {
				snmpCfg.TimeoutMs = uint32(v)
			}
		case strings.HasPrefix(arg, "--retries="):
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--retries=")); err == nil {
				snmpCfg.Retries = uint32(v)
			}
		}
	}

	req.Tags = tags

	if useV3 {
		if v3.SecurityName == "" {
			fmt.Println("Error: --user required for SNMPv3")
			return
		}
		if v3.SecurityLevel == "" {
			v3.SecurityLevel = "authPriv"
		}
		snmpCfg.Version = &pb.SNMPTargetConfig_V3{V3: &v3}
	} else {
		snmpCfg.Version = &pb.SNMPTargetConfig_V2C{
			V2C: &pb.SNMPv2CConfig{Community: community},
		}
	}

	// Set the oneof protocol field
	req.Protocol = &pb.CreateTargetRequest_Snmp{Snmp: snmpCfg}

	resp, err := cli.CreateTarget(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Show name if set, otherwise ID
	displayName := resp.TargetId
	if resp.TargetName != "" {
		displayName = fmt.Sprintf("%s (%s)", resp.TargetName, resp.TargetId)
	}

	if resp.Created {
		fmt.Printf("Created: %s\n", displayName)
	} else {
		fmt.Printf("Joined: %s (%s)\n", displayName, resp.Message)
	}
}

// ============================================================================
// set command
// ============================================================================

func cmdSet(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: set <target-id> [flags]")
		return
	}

	targetID := args[0]
	req := &pb.UpdateTargetRequest{TargetId: targetID}

	for i := 1; i < len(args); i++ {
		arg := args[i]

		switch {
		case strings.HasPrefix(arg, "--name="):
			name := strings.TrimPrefix(arg, "--name=")
			req.Name = &name
		case strings.HasPrefix(arg, "--desc="):
			desc := strings.TrimPrefix(arg, "--desc=")
			req.Description = &desc
		case strings.HasPrefix(arg, "--interval="):
			if v, err := strconv.ParseUint(strings.TrimPrefix(arg, "--interval="), 10, 32); err == nil {
				val := uint32(v)
				req.IntervalMs = &val
			}
		case strings.HasPrefix(arg, "--buffer="):
			if v, err := strconv.ParseUint(strings.TrimPrefix(arg, "--buffer="), 10, 32); err == nil {
				val := uint32(v)
				req.BufferSize = &val
			}
		case strings.HasPrefix(arg, "--timeout="):
			if v, err := strconv.ParseUint(strings.TrimPrefix(arg, "--timeout="), 10, 32); err == nil {
				val := uint32(v)
				req.TimeoutMs = &val
			}
		case strings.HasPrefix(arg, "--retries="):
			if v, err := strconv.ParseUint(strings.TrimPrefix(arg, "--retries="), 10, 32); err == nil {
				val := uint32(v)
				req.Retries = &val
			}
		case arg == "--persistent":
			val := true
			req.Persistent = &val
		case arg == "--no-persistent":
			val := false
			req.Persistent = &val
		case strings.HasPrefix(arg, "--community="):
			community := strings.TrimPrefix(arg, "--community=")
			req.Community = &community
		case strings.HasPrefix(arg, "--user="):
			user := strings.TrimPrefix(arg, "--user=")
			req.SecurityName = &user
		case strings.HasPrefix(arg, "--level="):
			level := strings.TrimPrefix(arg, "--level=")
			req.SecurityLevel = &level
		case strings.HasPrefix(arg, "--auth-proto="):
			proto := strings.TrimPrefix(arg, "--auth-proto=")
			req.AuthProtocol = &proto
		case strings.HasPrefix(arg, "--auth-pass="):
			pass := strings.TrimPrefix(arg, "--auth-pass=")
			req.AuthPassword = &pass
		case strings.HasPrefix(arg, "--priv-proto="):
			proto := strings.TrimPrefix(arg, "--priv-proto=")
			req.PrivProtocol = &proto
		case strings.HasPrefix(arg, "--priv-pass="):
			pass := strings.TrimPrefix(arg, "--priv-pass=")
			req.PrivPassword = &pass
		case strings.HasPrefix(arg, "--tag"):
			// --tag +X (add), --tag -X (remove), --tag X (set)
			if i+1 < len(args) {
				i++
				tagVal := args[i]
				if strings.HasPrefix(tagVal, "+") {
					req.AddTags = append(req.AddTags, strings.TrimPrefix(tagVal, "+"))
				} else if strings.HasPrefix(tagVal, "-") {
					req.RemoveTags = append(req.RemoveTags, strings.TrimPrefix(tagVal, "-"))
				} else {
					req.SetTags = append(req.SetTags, tagVal)
				}
			}
		}
	}

	resp, err := cli.UpdateTarget(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Updated: %s\n", resp.Message)
}

// ============================================================================
// rm command
// ============================================================================

func cmdRm(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: rm <target-id> [-f]")
		return
	}

	targetID := args[0]
	force := false

	for _, arg := range args[1:] {
		if arg == "-f" || arg == "--force" {
			force = true
		}
	}

	resp, err := cli.DeleteTarget(targetID, force)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("OK: %s\n", resp.Message)
}

// ============================================================================
// history command
// ============================================================================

func cmdHistory(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: history <target-id> [count]")
		return
	}

	targetID := args[0]
	count := uint32(10)
	if len(args) > 1 {
		if v, err := strconv.Atoi(args[1]); err == nil {
			count = uint32(v)
		}
	}

	resp, err := cli.GetHistory(targetID, count)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(resp.Samples) == 0 {
		fmt.Println("No samples")
		return
	}

	fmt.Printf("Showing %d of %d buffered samples:\n\n", len(resp.Samples), resp.TotalBuffered)
	fmt.Printf("%-24s %-20s %-6s %-8s\n", "Timestamp", "Value", "Valid", "PollMs")
	fmt.Println(strings.Repeat("-", 60))

	for _, s := range resp.Samples {
		ts := time.UnixMilli(s.TimestampMs).Format("2006-01-02 15:04:05.000")
		valid := "✓"
		if !s.Valid {
			valid = "✗"
		}
		if s.Text != "" {
			fmt.Printf("%-24s %-20s %-6s %-8d\n", ts, truncate(s.Text, 20), valid, s.PollMs)
		} else {
			fmt.Printf("%-24s %-20d %-6s %-8d\n", ts, s.Counter, valid, s.PollMs)
		}
	}
}

// ============================================================================
// sub/unsub commands
// ============================================================================

func cmdSub(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: sub <target-id>... [--tag=X]")
		return
	}

	var ids, tags []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "--tag=") {
			tags = append(tags, strings.TrimPrefix(arg, "--tag="))
		} else {
			ids = append(ids, arg)
		}
	}

	resp, err := cli.Subscribe(ids, tags)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Subscribed to %d targets (total: %d)\n", len(resp.Subscribed), resp.TotalSubscribed)
	if len(resp.Subscribed) > 0 && len(resp.Subscribed) <= 10 {
		fmt.Printf("  %s\n", strings.Join(resp.Subscribed, ", "))
	}
}

func cmdUnsub(args []string) {
	resp, err := cli.Unsubscribe(args)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(args) == 0 {
		fmt.Printf("Unsubscribed from all (remaining: %d)\n", resp.TotalSubscribed)
	} else {
		fmt.Printf("Unsubscribed from %d targets (remaining: %d)\n", len(resp.Unsubscribed), resp.TotalSubscribed)
	}
}

// ============================================================================
// config command
// ============================================================================

func cmdConfig(args []string) {
	if len(args) >= 3 && args[0] == "set" {
		cmdConfigSet(args[1], args[2])
		return
	}

	cfg, err := cli.GetConfig()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("=== Runtime Configuration ===")
	fmt.Println()
	fmt.Println("Changeable:")
	fmt.Printf("  default_timeout_ms:  %d\n", cfg.DefaultTimeoutMs)
	fmt.Printf("  default_retries:     %d\n", cfg.DefaultRetries)
	fmt.Printf("  default_buffer_size: %d\n", cfg.DefaultBufferSize)
	fmt.Printf("  min_interval_ms:     %d\n", cfg.MinIntervalMs)
	fmt.Println()
	fmt.Println("Read-only:")
	fmt.Printf("  poller_workers:      %d\n", cfg.PollerWorkers)
	fmt.Printf("  poller_queue_size:   %d\n", cfg.PollerQueueSize)
	fmt.Printf("  reconnect_window:    %d sec\n", cfg.ReconnectWindowSec)
	fmt.Println()
	fmt.Println("Server:")
	fmt.Printf("  version:             %s\n", cfg.Version)
	fmt.Printf("  uptime:              %s\n", formatDuration(time.Duration(cfg.UptimeMs)*time.Millisecond))
}

func cmdConfigSet(key, value string) {
	v, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		fmt.Printf("Invalid value: %s\n", value)
		return
	}
	val := uint32(v)

	req := &pb.SetConfigRequest{}
	switch key {
	case "timeout", "default_timeout_ms":
		req.DefaultTimeoutMs = &val
	case "retries", "default_retries":
		req.DefaultRetries = &val
	case "buffer", "default_buffer_size":
		req.DefaultBufferSize = &val
	case "min-interval", "min_interval_ms":
		req.MinIntervalMs = &val
	default:
		fmt.Printf("Unknown config key: %s\n", key)
		fmt.Println("Valid keys: timeout, retries, buffer, min-interval")
		return
	}

	resp, err := cli.SetConfig(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if resp.Ok {
		fmt.Printf("Updated: %s = %d\n", key, val)
	} else {
		fmt.Printf("Failed: %s\n", resp.Message)
	}
}

// ============================================================================
// Helpers
// ============================================================================

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
