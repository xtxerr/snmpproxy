// snmpctl is the SNMP proxy CLI client.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xtxerr/snmpproxy/internal/client"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

var (
	serverAddr    = flag.String("server", "localhost:9161", "server address")
	token         = flag.String("token", "", "auth token (or SNMPPROXY_TOKEN env)")
	noTLS         = flag.Bool("no-tls", false, "disable TLS")
	tlsSkipVerify = flag.Bool("tls-skip-verify", false, "skip TLS certificate verification")
)

var cli *client.Client

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

	printHelp()
	interactive()
}

func handleSample(s *pb.Sample) {
	ts := time.UnixMilli(s.TimestampMs).Format("15:04:05.000")
	if s.Valid {
		if s.Text != "" {
			fmt.Printf("[%s] %s: %s\n", ts, s.TargetId, s.Text)
		} else {
			fmt.Printf("[%s] %s: %d (%dms)\n", ts, s.TargetId, s.Counter, s.PollMs)
		}
	} else {
		fmt.Printf("[%s] %s: ERROR %s\n", ts, s.TargetId, s.Error)
	}
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println()
	fmt.Println("  ls [path] [-l]         List/browse (targets, server, session)")
	fmt.Println()
	fmt.Println("  add snmp <host> <oid> [options]")
	fmt.Println("      --id <name>        Target ID (auto-generated if omitted)")
	fmt.Println("      --desc <text>      Description")
	fmt.Println("      --tag <path>       Tag path (repeatable)")
	fmt.Println("      --interval <ms>    Poll interval (default: 1000)")
	fmt.Println("      --persistent       Make target persistent")
	fmt.Println("      --community <str>  SNMPv2c community (default: public)")
	fmt.Println("      --v3               Use SNMPv3")
	fmt.Println("      --user <name>      v3 security name")
	fmt.Println("      --level <level>    v3: noAuthNoPriv, authNoPriv, authPriv")
	fmt.Println("      --auth-proto <p>   v3: MD5, SHA, SHA256, etc.")
	fmt.Println("      --auth-pass <p>    v3 auth password")
	fmt.Println("      --priv-proto <p>   v3: DES, AES, AES256, etc.")
	fmt.Println("      --priv-pass <p>    v3 priv password")
	fmt.Println()
	fmt.Println("  rm <target-id>         Remove target (or leave ownership)")
	fmt.Println("  rm -f <target-id>      Force delete (even if persistent)")
	fmt.Println()
	fmt.Println("  set <target-id> [options]")
	fmt.Println("      --desc <text>      Set description")
	fmt.Println("      --interval <ms>    Set poll interval")
	fmt.Println("      --buffer <size>    Set buffer size")
	fmt.Println("      --tag +<path>      Add tag")
	fmt.Println("      --tag -<path>      Remove tag")
	fmt.Println("      --tag <path>       Set tags (replaces all)")
	fmt.Println("      --persistent       Make persistent")
	fmt.Println("      --no-persistent    Remove persistent flag")
	fmt.Println()
	fmt.Println("  history <target> [n]   Show last n samples (default: 10)")
	fmt.Println()
	fmt.Println("  sub <targets...>       Subscribe to live updates")
	fmt.Println("  sub --tag <path>       Subscribe by tag")
	fmt.Println("  unsub [targets...]     Unsubscribe (all if none specified)")
	fmt.Println()
	fmt.Println("  config                 Show runtime config")
	fmt.Println("  config set <key> <val> Set config value")
	fmt.Println()
	fmt.Println("  help                   Show this help")
	fmt.Println("  quit                   Exit")
	fmt.Println()
}

func interactive() {
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
		case "add", "create":
			cmdAdd(cmdArgs)
		case "rm", "del", "delete", "remove":
			cmdRm(cmdArgs)
		case "set", "update":
			cmdSet(cmdArgs)
		case "history", "hist", "h":
			cmdHistory(cmdArgs)
		case "sub", "subscribe":
			cmdSubscribe(cmdArgs)
		case "unsub", "unsubscribe":
			cmdUnsubscribe(cmdArgs)
		case "config", "cfg":
			cmdConfig(cmdArgs)
		case "help", "?":
			printHelp()
		case "quit", "exit", "q":
			cli.Close()
			return
		default:
			fmt.Printf("Unknown command: %s (try 'help')\n", cmd)
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
	path := ""
	longFormat := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-l", "--long":
			longFormat = true
		default:
			if !strings.HasPrefix(args[i], "-") {
				path = args[i]
			}
		}
	}

	resp, err := cli.Browse(path, longFormat)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	printBrowseResponse(resp, longFormat)
}

func printBrowseResponse(resp *pb.BrowseResponse, longFormat bool) {
	if len(resp.Nodes) == 0 {
		fmt.Println("(empty)")
		return
	}

	for _, node := range resp.Nodes {
		switch node.Type {
		case pb.NodeType_NODE_DIRECTORY:
			if node.TargetCount > 0 {
				fmt.Printf("%s/\t\t(%d targets)\n", node.Name, node.TargetCount)
			} else {
				fmt.Printf("%s/\n", node.Name)
			}

		case pb.NodeType_NODE_TARGET:
			if longFormat && node.Target != nil {
				t := node.Target
				host := ""
				if snmp := t.GetSnmp(); snmp != nil {
					host = snmp.Host
				}
				persistent := ""
				if t.Persistent {
					persistent = " [P]"
				}
				desc := t.Description
				if len(desc) > 30 {
					desc = desc[:27] + "..."
				}
				fmt.Printf("[%s]  %-15s  %-12s  %s%s\n", node.Name, host, t.State, desc, persistent)
			} else {
				fmt.Printf("[%s]\n", node.Name)
			}

		case pb.NodeType_NODE_INFO:
			if len(node.Info) > 0 {
				keys := make([]string, 0, len(node.Info))
				for k := range node.Info {
					keys = append(keys, k)
				}
				sort.Strings(keys)

				for _, k := range keys {
					fmt.Printf("  %-20s %s\n", k+":", node.Info[k])
				}
			}
		}
	}
}

// ============================================================================
// add command
// ============================================================================

func cmdAdd(args []string) {
	if len(args) < 3 || args[0] != "snmp" {
		fmt.Println("Usage: add snmp <host> <oid> [options]")
		return
	}

	host := args[1]
	oid := args[2]

	// Defaults
	var id, desc, community string
	var tags []string
	var interval, port, bufferSize uint32
	var persistent, useV3 bool
	var securityName, securityLevel, authProto, authPass, privProto, privPass string

	community = "public"

	// Parse options
	for i := 3; i < len(args); i++ {
		switch args[i] {
		case "--id":
			if i+1 < len(args) {
				i++
				id = args[i]
			}
		case "--desc", "--description":
			if i+1 < len(args) {
				i++
				desc = args[i]
			}
		case "--tag":
			if i+1 < len(args) {
				i++
				tags = append(tags, args[i])
			}
		case "--interval", "-i":
			if i+1 < len(args) {
				i++
				v, _ := strconv.ParseUint(args[i], 10, 32)
				interval = uint32(v)
			}
		case "--port":
			if i+1 < len(args) {
				i++
				v, _ := strconv.ParseUint(args[i], 10, 32)
				port = uint32(v)
			}
		case "--buffer":
			if i+1 < len(args) {
				i++
				v, _ := strconv.ParseUint(args[i], 10, 32)
				bufferSize = uint32(v)
			}
		case "--persistent", "-p":
			persistent = true
		case "--community", "-c":
			if i+1 < len(args) {
				i++
				community = args[i]
			}
		case "--v3":
			useV3 = true
		case "--user", "-u":
			if i+1 < len(args) {
				i++
				securityName = args[i]
			}
		case "--level", "-l":
			if i+1 < len(args) {
				i++
				securityLevel = args[i]
			}
		case "--auth-proto":
			if i+1 < len(args) {
				i++
				authProto = args[i]
			}
		case "--auth-pass":
			if i+1 < len(args) {
				i++
				authPass = args[i]
			}
		case "--priv-proto":
			if i+1 < len(args) {
				i++
				privProto = args[i]
			}
		case "--priv-pass":
			if i+1 < len(args) {
				i++
				privPass = args[i]
			}
		}
	}

	if port == 0 {
		port = 161
	}

	// Build SNMP config
	snmpCfg := &pb.SNMPTargetConfig{
		Host: host,
		Port: port,
		Oid:  oid,
	}

	if useV3 {
		if securityName == "" {
			fmt.Println("Error: --user required for SNMPv3")
			return
		}
		snmpCfg.Version = &pb.SNMPTargetConfig_V3{
			V3: &pb.SNMPv3Config{
				SecurityName:  securityName,
				SecurityLevel: securityLevel,
				AuthProtocol:  authProto,
				AuthPassword:  authPass,
				PrivProtocol:  privProto,
				PrivPassword:  privPass,
			},
		}
	} else {
		snmpCfg.Version = &pb.SNMPTargetConfig_V2C{
			V2C: &pb.SNMPv2CConfig{Community: community},
		}
	}

	req := &pb.CreateTargetRequest{
		Id:          id,
		Description: desc,
		Tags:        tags,
		Persistent:  persistent,
		IntervalMs:  interval,
		BufferSize:  bufferSize,
		Protocol:    &pb.CreateTargetRequest_Snmp{Snmp: snmpCfg},
	}

	resp, err := cli.CreateTarget(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if resp.Created {
		fmt.Printf("Created: %s\n", resp.TargetId)
	} else {
		fmt.Printf("Joined: %s (%s)\n", resp.TargetId, resp.Message)
	}
}

// ============================================================================
// rm command
// ============================================================================

func cmdRm(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: rm [-f] <target-id>")
		return
	}

	var targetID string
	var force bool

	for _, arg := range args {
		switch arg {
		case "-f", "--force":
			force = true
		default:
			if !strings.HasPrefix(arg, "-") {
				targetID = arg
			}
		}
	}

	if targetID == "" {
		fmt.Println("Usage: rm [-f] <target-id>")
		return
	}

	resp, err := cli.DeleteTarget(targetID, force)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("OK: %s\n", resp.Message)
}

// ============================================================================
// set command
// ============================================================================

func cmdSet(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: set <target-id> [options]")
		return
	}

	targetID := args[0]
	req := &pb.UpdateTargetRequest{TargetId: targetID}

	var addTags, removeTags, setTags []string

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--desc", "--description":
			if i+1 < len(args) {
				i++
				req.Description = &args[i]
			}
		case "--interval", "-i":
			if i+1 < len(args) {
				i++
				v, _ := strconv.ParseUint(args[i], 10, 32)
				val := uint32(v)
				req.IntervalMs = &val
			}
		case "--buffer", "-b":
			if i+1 < len(args) {
				i++
				v, _ := strconv.ParseUint(args[i], 10, 32)
				val := uint32(v)
				req.BufferSize = &val
			}
		case "--timeout":
			if i+1 < len(args) {
				i++
				v, _ := strconv.ParseUint(args[i], 10, 32)
				val := uint32(v)
				req.TimeoutMs = &val
			}
		case "--retries":
			if i+1 < len(args) {
				i++
				v, _ := strconv.ParseUint(args[i], 10, 32)
				val := uint32(v)
				req.Retries = &val
			}
		case "--persistent", "-p":
			val := true
			req.Persistent = &val
		case "--no-persistent":
			val := false
			req.Persistent = &val
		case "--tag":
			if i+1 < len(args) {
				i++
				tag := args[i]
				if strings.HasPrefix(tag, "+") {
					addTags = append(addTags, tag[1:])
				} else if strings.HasPrefix(tag, "-") {
					removeTags = append(removeTags, tag[1:])
				} else {
					setTags = append(setTags, tag)
				}
			}
		}
	}

	req.AddTags = addTags
	req.RemoveTags = removeTags
	req.SetTags = setTags

	resp, err := cli.UpdateTarget(req)
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
		v, _ := strconv.ParseUint(args[1], 10, 32)
		if v > 0 {
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

	fmt.Printf("%-24s  %-20s  %-6s  %s\n", "Timestamp", "Value", "Valid", "PollMs")
	fmt.Println(strings.Repeat("-", 65))

	for _, s := range resp.Samples {
		ts := time.UnixMilli(s.TimestampMs).Format("2006-01-02 15:04:05.000")
		valid := "✓"
		if !s.Valid {
			valid = "✗"
		}
		if s.Text != "" {
			fmt.Printf("%-24s  %-20s  %-6s  %d\n", ts, truncate(s.Text, 20), valid, s.PollMs)
		} else {
			fmt.Printf("%-24s  %-20d  %-6s  %d\n", ts, s.Counter, valid, s.PollMs)
		}
	}
}

// ============================================================================
// subscribe/unsubscribe commands
// ============================================================================

func cmdSubscribe(args []string) {
	var targetIDs []string
	var tags []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tag", "-t":
			if i+1 < len(args) {
				i++
				tags = append(tags, args[i])
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				targetIDs = append(targetIDs, args[i])
			}
		}
	}

	if len(targetIDs) == 0 && len(tags) == 0 {
		fmt.Println("Usage: sub <target-id>... or sub --tag <path>")
		return
	}

	resp, err := cli.SubscribeWithTags(targetIDs, tags)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(resp.Subscribed) == 0 {
		fmt.Println("No targets matched")
	} else {
		fmt.Printf("Subscribed: %v\n", resp.Subscribed)
	}
}

func cmdUnsubscribe(args []string) {
	resp, err := cli.Unsubscribe(args)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(resp.Unsubscribed) == 0 {
		fmt.Println("Unsubscribed from all")
	} else {
		fmt.Printf("Unsubscribed: %v\n", resp.Unsubscribed)
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

	fmt.Println("=== Server Info ===")
	fmt.Printf("  %-20s %s\n", "version:", cfg.Version)
	fmt.Printf("  %-20s %s\n", "uptime:", formatDuration(time.Duration(cfg.UptimeMs)*time.Millisecond))
	fmt.Println()
	fmt.Println("=== Runtime Config (changeable) ===")
	fmt.Printf("  %-20s %d\n", "default_timeout_ms:", cfg.DefaultTimeoutMs)
	fmt.Printf("  %-20s %d\n", "default_retries:", cfg.DefaultRetries)
	fmt.Printf("  %-20s %d\n", "default_buffer_size:", cfg.DefaultBufferSize)
	fmt.Printf("  %-20s %d\n", "min_interval_ms:", cfg.MinIntervalMs)
	fmt.Println()
	fmt.Println("=== Static Config (read-only) ===")
	fmt.Printf("  %-20s %d\n", "poller_workers:", cfg.PollerWorkers)
	fmt.Printf("  %-20s %d\n", "poller_queue_size:", cfg.PollerQueueSize)
	fmt.Printf("  %-20s %d sec\n", "reconnect_window:", cfg.ReconnectWindowSec)
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
	case "min_interval", "min_interval_ms":
		req.MinIntervalMs = &val
	default:
		fmt.Printf("Unknown config key: %s\n", key)
		fmt.Println("Valid keys: timeout, retries, buffer, min_interval")
		return
	}

	resp, err := cli.SetConfig(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if resp.Ok {
		fmt.Printf("Config updated: %s = %d\n", key, val)
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
