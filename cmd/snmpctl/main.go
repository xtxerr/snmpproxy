// snmpctl is the SNMP proxy CLI client.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	pb "github.com/xtxerr/snmpproxy/internal/proto"
	"github.com/xtxerr/snmpproxy/internal/client"
)

var (
	serverAddr    = flag.String("server", "localhost:9161", "server address")
	token         = flag.String("token", "", "auth token (or SNMPPROXY_TOKEN env)")
	noTLS         = flag.Bool("no-tls", false, "disable TLS")
	tlsSkipVerify = flag.Bool("tls-skip-verify", false, "skip TLS certificate verification")
)

var (
	cli   *client.Client
	names = make(map[string]string) // targetID -> local name
)

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
	name := names[s.TargetId]
	if name == "" {
		name = s.TargetId
	}

	ts := time.UnixMilli(s.TimestampMs).Format("15:04:05.000")
	if s.Valid {
		if s.Text != "" {
			fmt.Printf("[%s] %s: %s\n", ts, name, s.Text)
		} else {
			fmt.Printf("[%s] %s: %d (%dms)\n", ts, name, s.Counter, s.PollMs)
		}
	} else {
		fmt.Printf("[%s] %s: ERROR %s\n", ts, name, s.Error)
	}
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println()
	fmt.Println("  monitor <host> <oid> [name] [options]")
	fmt.Println("    Common options:")
	fmt.Println("      -i <ms>          Poll interval in milliseconds (default: 1000)")
	fmt.Println("      -b <size>        Buffer size for samples (default: 3600)")
	fmt.Println("    SNMPv2c (default):")
	fmt.Println("      -c <community>   Community string (default: public)")
	fmt.Println("    SNMPv3:")
	fmt.Println("      -v3              Use SNMPv3")
	fmt.Println("      -u <user>        Security name (required)")
	fmt.Println("      -l <level>       Security level: noAuthNoPriv, authNoPriv, authPriv")
	fmt.Println("      -a <proto>       Auth protocol: MD5, SHA, SHA224, SHA256, SHA384, SHA512")
	fmt.Println("      -A <pass>        Auth password")
	fmt.Println("      -x <proto>       Privacy protocol: DES, AES, AES192, AES256")
	fmt.Println("      -X <pass>        Privacy password")
	fmt.Println("      -n <name>        Context name")
	fmt.Println()
	fmt.Println("  unmonitor <target-id>       Stop monitoring and remove target")
	fmt.Println("  list [host-filter]          List all targets")
	fmt.Println("  info <target-id>            Show target details and statistics")
	fmt.Println("  history <target-id> [n]     Show last n samples (default: 10)")
	fmt.Println("  subscribe <target-id>...    Subscribe to live updates")
	fmt.Println("  unsubscribe [target-id]...  Unsubscribe (all if no args)")
	fmt.Println()
	fmt.Println("  update <target-id> [options]")
	fmt.Println("      -i <ms>          Change poll interval")
	fmt.Println("      -t <ms>          Change SNMP timeout")
	fmt.Println("      -r <n>           Change SNMP retries")
	fmt.Println("      -b <size>        Change buffer size")
	fmt.Println()
	fmt.Println("  status                      Show server status")
	fmt.Println("  session                     Show current session info")
	fmt.Println("  config                      Show runtime configuration")
	fmt.Println("  config set <key> <value>    Change config (timeout, retries, buffer, min-interval)")
	fmt.Println()
	fmt.Println("  help                        Show this help")
	fmt.Println("  quit                        Disconnect and exit")
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

		parts := parseArgs(line)
		if len(parts) == 0 {
			fmt.Print("> ")
			continue
		}

		cmd := strings.ToLower(parts[0])
		args := parts[1:]

		switch cmd {
		case "monitor", "mon", "m":
			cmdMonitor(args)
		case "unmonitor", "unmon", "u":
			cmdUnmonitor(args)
		case "list", "ls", "l":
			cmdList(args)
		case "info", "i":
			cmdInfo(args)
		case "history", "hist", "h":
			cmdHistory(args)
		case "subscribe", "sub", "s":
			cmdSubscribe(args)
		case "unsubscribe", "unsub":
			cmdUnsubscribe(args)
		// NEW commands
		case "status":
			cmdStatus()
		case "session":
			cmdSession()
		case "update":
			cmdUpdate(args)
		case "config":
			cmdConfig(args)
		case "help", "?":
			printHelp()
		case "quit", "exit", "q":
			cli.Close()
			return
		default:
			fmt.Printf("Unknown: %s\n", cmd)
		}

		fmt.Print("> ")
	}
}

// parseArgs handles simple argument parsing with flag-like options
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

func cmdMonitor(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: monitor <host> <oid> [name] [options]")
		fmt.Println("  v2c: -c community -i interval_ms -b buffer_size")
		fmt.Println("  v3:  -v3 -u user -l level -a auth_proto -A auth_pass -x priv_proto -X priv_pass -i interval_ms -b buffer_size")
		return
	}

	host := args[0]
	oid := args[1]

	// Defaults
	name := host + "/" + oid
	interval := uint32(1000)
	bufferSize := uint32(3600)
	community := "public"
	useV3 := false
	securityName := ""
	securityLevel := pb.SecurityLevel_SECURITY_LEVEL_AUTH_PRIV
	authProto := pb.AuthProtocol_AUTH_PROTOCOL_SHA256
	authPass := ""
	privProto := pb.PrivProtocol_PRIV_PROTOCOL_AES
	privPass := ""
	contextName := ""

	// Parse remaining args
	i := 2
	for i < len(args) {
		arg := args[i]
		switch arg {
		case "-c":
			if i+1 < len(args) {
				community = args[i+1]
				i += 2
			} else {
				i++
			}
		case "-i":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					interval = uint32(v)
				}
				i += 2
			} else {
				i++
			}
		case "-b":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					bufferSize = uint32(v)
				}
				i += 2
			} else {
				i++
			}
		case "-v3":
			useV3 = true
			i++
		case "-u":
			if i+1 < len(args) {
				securityName = args[i+1]
				i += 2
			} else {
				i++
			}
		case "-l":
			if i+1 < len(args) {
				securityLevel = parseSecurityLevel(args[i+1])
				i += 2
			} else {
				i++
			}
		case "-a":
			if i+1 < len(args) {
				authProto = parseAuthProtocol(args[i+1])
				i += 2
			} else {
				i++
			}
		case "-A":
			if i+1 < len(args) {
				authPass = args[i+1]
				i += 2
			} else {
				i++
			}
		case "-x":
			if i+1 < len(args) {
				privProto = parsePrivProtocol(args[i+1])
				i += 2
			} else {
				i++
			}
		case "-X":
			if i+1 < len(args) {
				privPass = args[i+1]
				i += 2
			} else {
				i++
			}
		case "-n":
			if i+1 < len(args) {
				contextName = args[i+1]
				i += 2
			} else {
				i++
			}
		default:
			// Positional: name
			if !strings.HasPrefix(arg, "-") {
				name = arg
			}
			i++
		}
	}

	req := &pb.MonitorRequest{
		Host:       host,
		Port:       161,
		Oid:        oid,
		IntervalMs: interval,
		BufferSize: bufferSize,
	}

	if useV3 {
		if securityName == "" {
			fmt.Println("Error: -u (security name) required for SNMPv3")
			return
		}
		if err := validateV3Config(securityLevel, authPass, privPass); err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		req.Snmp = &pb.SNMPConfig{
			Version: &pb.SNMPConfig_V3{
				V3: &pb.SNMPv3{
					SecurityName:  securityName,
					SecurityLevel: securityLevel,
					AuthProtocol:  authProto,
					AuthPassword:  authPass,
					PrivProtocol:  privProto,
					PrivPassword:  privPass,
					ContextName:   contextName,
				},
			},
		}
	} else {
		req.Snmp = &pb.SNMPConfig{
			Version: &pb.SNMPConfig_V2C{
				V2C: &pb.SNMPv2C{Community: community},
			},
		}
	}

	resp, err := cli.Monitor(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	names[resp.TargetId] = name
	if resp.Created {
		fmt.Printf("Created: %s (%s)\n", resp.TargetId, name)
	} else {
		fmt.Printf("Joined: %s (%s)\n", resp.TargetId, name)
	}
}

func validateV3Config(level pb.SecurityLevel, authPass, privPass string) error {
	switch level {
	case pb.SecurityLevel_SECURITY_LEVEL_NO_AUTH_NO_PRIV:
		// No credentials required
	case pb.SecurityLevel_SECURITY_LEVEL_AUTH_NO_PRIV:
		if authPass == "" {
			return fmt.Errorf("authNoPriv requires -A (auth password)")
		}
	case pb.SecurityLevel_SECURITY_LEVEL_AUTH_PRIV:
		if authPass == "" {
			return fmt.Errorf("authPriv requires -A (auth password)")
		}
		if privPass == "" {
			return fmt.Errorf("authPriv requires -X (priv password)")
		}
	}
	return nil
}

func parseSecurityLevel(s string) pb.SecurityLevel {
	switch strings.ToLower(s) {
	case "noauthnopriv":
		return pb.SecurityLevel_SECURITY_LEVEL_NO_AUTH_NO_PRIV
	case "authnopriv":
		return pb.SecurityLevel_SECURITY_LEVEL_AUTH_NO_PRIV
	case "authpriv":
		return pb.SecurityLevel_SECURITY_LEVEL_AUTH_PRIV
	default:
		fmt.Printf("Warning: unknown security level '%s', using authPriv\n", s)
		return pb.SecurityLevel_SECURITY_LEVEL_AUTH_PRIV
	}
}

func parseAuthProtocol(s string) pb.AuthProtocol {
	switch strings.ToUpper(s) {
	case "MD5":
		return pb.AuthProtocol_AUTH_PROTOCOL_MD5
	case "SHA", "SHA1":
		return pb.AuthProtocol_AUTH_PROTOCOL_SHA
	case "SHA224":
		return pb.AuthProtocol_AUTH_PROTOCOL_SHA224
	case "SHA256":
		return pb.AuthProtocol_AUTH_PROTOCOL_SHA256
	case "SHA384":
		return pb.AuthProtocol_AUTH_PROTOCOL_SHA384
	case "SHA512":
		return pb.AuthProtocol_AUTH_PROTOCOL_SHA512
	default:
		fmt.Printf("Warning: unknown auth protocol '%s', using SHA256\n", s)
		return pb.AuthProtocol_AUTH_PROTOCOL_SHA256
	}
}

func parsePrivProtocol(s string) pb.PrivProtocol {
	switch strings.ToUpper(s) {
	case "DES":
		return pb.PrivProtocol_PRIV_PROTOCOL_DES
	case "AES", "AES128":
		return pb.PrivProtocol_PRIV_PROTOCOL_AES
	case "AES192":
		return pb.PrivProtocol_PRIV_PROTOCOL_AES192
	case "AES256":
		return pb.PrivProtocol_PRIV_PROTOCOL_AES256
	default:
		fmt.Printf("Warning: unknown priv protocol '%s', using AES\n", s)
		return pb.PrivProtocol_PRIV_PROTOCOL_AES
	}
}

func cmdUnmonitor(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: unmonitor <target-id>")
		return
	}

	if err := cli.Unmonitor(args[0]); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	delete(names, args[0])
	fmt.Println("OK")
}

func cmdList(args []string) {
	filter := ""
	if len(args) > 0 {
		filter = args[0]
	}

	targets, err := cli.ListTargets(filter)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(targets) == 0 {
		fmt.Println("No targets")
		return
	}

	fmt.Printf("%-10s %-20s %-40s %-8s %-5s %-10s\n",
		"ID", "Host", "OID", "Interval", "Subs", "State")
	fmt.Println(strings.Repeat("-", 95))

	for _, t := range targets {
		localName := names[t.Id]
		if localName != "" {
			localName = " (" + localName + ")"
		}
		fmt.Printf("%-10s %-20s %-40s %-8d %-5d %-10s%s\n",
			t.Id, t.Host, truncate(t.Oid, 40), t.IntervalMs, t.Subscribers, t.State, localName)
	}
}

func cmdInfo(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: info <target-id>")
		return
	}

	t, err := cli.GetTarget(args[0])
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("ID:          %s\n", t.Id)
	fmt.Printf("Host:        %s:%d\n", t.Host, t.Port)
	fmt.Printf("OID:         %s\n", t.Oid)
	fmt.Printf("Interval:    %d ms\n", t.IntervalMs)
	fmt.Printf("Buffer:      %d / %d\n", t.SamplesBuffered, t.BufferSize)
	fmt.Printf("State:       %s\n", t.State)
	fmt.Printf("Subscribers: %d\n", t.Subscribers)
	if t.LastPollMs > 0 {
		fmt.Printf("Last Poll:   %s\n", time.UnixMilli(t.LastPollMs).Format(time.RFC3339))
	}
	if t.LastError != "" {
		fmt.Printf("Last Error:  %s\n", t.LastError)
	}
	// Extended stats (if available)
	if t.CreatedAtMs > 0 {
		fmt.Printf("Created:     %s\n", time.UnixMilli(t.CreatedAtMs).Format(time.RFC3339))
	}
	if t.PollsTotal > 0 {
		fmt.Printf("Polls:       %d total, %d ok, %d failed\n", t.PollsTotal, t.PollsSuccess, t.PollsFailed)
		fmt.Printf("Poll Time:   avg %dms, min %dms, max %dms\n", t.AvgPollMs, t.MinPollMs, t.MaxPollMs)
	}
}

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

	history, err := cli.GetHistory([]string{targetID}, count)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(history) == 0 || len(history[0].Samples) == 0 {
		fmt.Println("No data")
		return
	}

	fmt.Printf("%-24s %-20s %-6s %-8s\n", "Timestamp", "Value", "Valid", "PollMs")
	fmt.Println(strings.Repeat("-", 60))

	for _, s := range history[0].Samples {
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

func cmdSubscribe(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: subscribe <target-id>...")
		return
	}

	subscribed, err := cli.Subscribe(args)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Subscribed: %v\n", subscribed)
}

func cmdUnsubscribe(args []string) {
	if err := cli.Unsubscribe(args); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(args) == 0 {
		fmt.Println("Unsubscribed from all")
	} else {
		fmt.Printf("Unsubscribed: %v\n", args)
	}
}

// ============================================================================
// NEW: Status, Session, Update, Config commands
// ============================================================================

func cmdStatus() {
	status, err := cli.GetServerStatus()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	uptime := time.Duration(status.UptimeMs) * time.Millisecond

	fmt.Println("=== Server Status ===")
	fmt.Printf("Version:       %s\n", status.Version)
	fmt.Printf("Uptime:        %s\n", formatDuration(uptime))
	fmt.Printf("Started:       %s\n", time.UnixMilli(status.StartedAtMs).Format("2006-01-02 15:04:05"))
	fmt.Println()
	fmt.Println("Sessions:")
	fmt.Printf("  Active:      %d\n", status.SessionsActive)
	fmt.Printf("  Lost:        %d\n", status.SessionsLost)
	fmt.Println()
	fmt.Println("Targets:")
	fmt.Printf("  Total:       %d\n", status.TargetsTotal)
	fmt.Printf("  Polling:     %d\n", status.TargetsPolling)
	fmt.Printf("  Unreachable: %d\n", status.TargetsUnreachable)
	fmt.Println()
	fmt.Println("Poller:")
	fmt.Printf("  Workers:     %d\n", status.PollerWorkers)
	fmt.Printf("  Queue:       %d / %d\n", status.PollerQueueUsed, status.PollerQueueCapacity)
	fmt.Printf("  Heap Size:   %d\n", status.PollerHeapSize)
	fmt.Println()
	fmt.Println("Statistics:")
	fmt.Printf("  Total Polls: %d\n", status.PollsTotal)
	fmt.Printf("  Success:     %d\n", status.PollsSuccess)
	fmt.Printf("  Failed:      %d\n", status.PollsFailed)
	if status.PollsTotal > 0 {
		successRate := float64(status.PollsSuccess) / float64(status.PollsTotal) * 100
		fmt.Printf("  Success Rate: %.1f%%\n", successRate)
	}
}

func cmdSession() {
	sess, err := cli.GetSessionInfo()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("=== Session Info ===")
	fmt.Printf("Session ID:    %s\n", sess.SessionId)
	fmt.Printf("Token ID:      %s\n", sess.TokenId)
	fmt.Printf("Created:       %s\n", time.UnixMilli(sess.CreatedAtMs).Format("2006-01-02 15:04:05"))
	fmt.Println()
	fmt.Printf("Owned Targets (%d):\n", len(sess.OwnedTargets))
	for _, id := range sess.OwnedTargets {
		name := names[id]
		if name != "" {
			fmt.Printf("  - %s (%s)\n", id, name)
		} else {
			fmt.Printf("  - %s\n", id)
		}
	}
	fmt.Println()
	fmt.Printf("Subscribed (%d):\n", len(sess.SubscribedTargets))
	for _, id := range sess.SubscribedTargets {
		name := names[id]
		if name != "" {
			fmt.Printf("  - %s (%s)\n", id, name)
		} else {
			fmt.Printf("  - %s\n", id)
		}
	}
}

func cmdUpdate(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: update <target-id> [-i interval] [-t timeout] [-r retries] [-b buffer]")
		return
	}

	targetID := args[0]
	req := &pb.UpdateTargetRequest{TargetId: targetID}

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-i", "--interval":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseUint(args[i], 10, 32); err == nil {
					req.IntervalMs = uint32(v)
				}
			}
		case "-t", "--timeout":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseUint(args[i], 10, 32); err == nil {
					req.TimeoutMs = uint32(v)
				}
			}
		case "-r", "--retries":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseUint(args[i], 10, 32); err == nil {
					req.Retries = uint32(v)
				}
			}
		case "-b", "--buffer":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseUint(args[i], 10, 32); err == nil {
					req.BufferSize = uint32(v)
				}
			}
		}
	}

	resp, err := cli.UpdateTarget(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if resp.Ok {
		fmt.Printf("Updated: %s\n", resp.Message)
		if resp.Target != nil {
			fmt.Printf("  Interval: %d ms, Buffer: %d/%d\n", 
				resp.Target.IntervalMs, resp.Target.SamplesBuffered, resp.Target.BufferSize)
		}
	} else {
		fmt.Printf("Failed: %s\n", resp.Message)
	}
}

func cmdConfig(args []string) {
	// config set <key> <value>
	if len(args) >= 3 && args[0] == "set" {
		cmdConfigSet(args[1], args[2])
		return
	}

	// config (show)
	cfg, err := cli.GetConfig()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("=== Runtime Config ===")
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
		req.DefaultTimeoutMs = val
	case "retries", "default_retries":
		req.DefaultRetries = val
	case "buffer", "default_buffer_size":
		req.DefaultBufferSize = val
	case "min-interval", "min_interval_ms":
		req.MinIntervalMs = val
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
