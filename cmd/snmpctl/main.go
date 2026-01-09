// snmpctl2 is the v2 SNMP proxy CLI client.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

var (
	serverAddr    = flag.String("server", "localhost:9161", "server address")
	token         = flag.String("token", "", "auth token (or SNMPPROXY_TOKEN env)")
	noTLS         = flag.Bool("no-tls", false, "disable TLS")
	tlsSkipVerify = flag.Bool("tls-skip-verify", false, "skip TLS certificate verification")
	namespace     = flag.String("namespace", "", "default namespace")
)

// State
var (
	currentPath      = "/"
	currentNamespace = ""
	sessionID        = ""
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

	if *namespace != "" {
		currentNamespace = *namespace
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)

	fmt.Printf("snmpctl v2 - Connecting to %s...\n", *serverAddr)
	// TODO: Connect to server
	sessionID = "demo-session"
	fmt.Printf("Connected (session: %s)\n", sessionID)

	if currentNamespace != "" {
		fmt.Printf("Namespace: %s\n", currentNamespace)
	}
	fmt.Println()

	printHelp()
	interactive()
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println()
	fmt.Println("  Navigation:")
	fmt.Println("    ls [path]              List entries at path (default: current)")
	fmt.Println("    cd <path>              Change current path")
	fmt.Println("    pwd                    Print current path")
	fmt.Println("    cat <path>             Show details of target/poller")
	fmt.Println("    tree [path]            Show tree structure")
	fmt.Println()
	fmt.Println("  Namespace:")
	fmt.Println("    ns                     List namespaces")
	fmt.Println("    ns use <name>          Switch to namespace")
	fmt.Println("    ns create <name>       Create namespace")
	fmt.Println("    ns delete <name>       Delete namespace")
	fmt.Println()
	fmt.Println("  Targets:")
	fmt.Println("    target create <name>   Create target")
	fmt.Println("    target delete <name>   Delete target")
	fmt.Println("    target set <name> <k=v>... Set target properties")
	fmt.Println()
	fmt.Println("  Pollers:")
	fmt.Println("    poller create <target> <name> <proto> <config>")
	fmt.Println("    poller delete <target> <name>")
	fmt.Println("    poller enable <target> <name>")
	fmt.Println("    poller disable <target> <name>")
	fmt.Println("    poller test <target> <name>    Execute single poll")
	fmt.Println()
	fmt.Println("  Monitoring:")
	fmt.Println("    watch [path...]        Subscribe to live samples")
	fmt.Println("    unwatch [path...]      Unsubscribe (all if empty)")
	fmt.Println("    history <path> [n]     Show sample history")
	fmt.Println()
	fmt.Println("  Tree:")
	fmt.Println("    mkdir <path>           Create directory in tree")
	fmt.Println("    ln <target> <path>     Create link to target")
	fmt.Println("    rm <path>              Remove tree node")
	fmt.Println()
	fmt.Println("  System:")
	fmt.Println("    status                 Server status")
	fmt.Println("    session                Session info")
	fmt.Println("    config                 Runtime config")
	fmt.Println("    help                   This help")
	fmt.Println("    quit                   Exit")
	fmt.Println()
}

func interactive() {
	scanner := bufio.NewScanner(os.Stdin)
	printPrompt()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			printPrompt()
			continue
		}

		args := parseArgs(line)
		if len(args) == 0 {
			printPrompt()
			continue
		}

		cmd := strings.ToLower(args[0])
		cmdArgs := args[1:]

		switch cmd {
		// Navigation
		case "ls":
			cmdLs(cmdArgs)
		case "cd":
			cmdCd(cmdArgs)
		case "pwd":
			cmdPwd()
		case "cat", "show", "info":
			cmdCat(cmdArgs)
		case "tree":
			cmdTree(cmdArgs)

		// Namespace
		case "ns", "namespace":
			cmdNamespace(cmdArgs)

		// Targets
		case "target":
			cmdTarget(cmdArgs)

		// Pollers
		case "poller", "poll":
			cmdPoller(cmdArgs)

		// Monitoring
		case "watch", "sub", "subscribe":
			cmdWatch(cmdArgs)
		case "unwatch", "unsub", "unsubscribe":
			cmdUnwatch(cmdArgs)
		case "history", "hist", "h":
			cmdHistory(cmdArgs)

		// Tree
		case "mkdir":
			cmdMkdir(cmdArgs)
		case "ln", "link":
			cmdLn(cmdArgs)
		case "rm", "delete":
			cmdRm(cmdArgs)

		// System
		case "status":
			cmdStatus()
		case "session":
			cmdSession()
		case "config":
			cmdConfig(cmdArgs)
		case "help", "?":
			printHelp()
		case "quit", "exit", "q":
			fmt.Println("Goodbye!")
			return

		default:
			fmt.Printf("Unknown command: %s (try 'help')\n", cmd)
		}

		printPrompt()
	}
}

func printPrompt() {
	ns := currentNamespace
	if ns == "" {
		ns = "(no namespace)"
	}
	fmt.Printf("[%s] %s> ", ns, currentPath)
}

func parseArgs(line string) []string {
	var args []string
	var current strings.Builder
	inQuote := false

	for _, r := range line {
		switch {
		case r == '"' || r == '\'':
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
// Navigation Commands
// ============================================================================

func cmdLs(args []string) {
	path := currentPath
	if len(args) > 0 {
		path = resolvePath(args[0])
	}

	if currentNamespace == "" {
		fmt.Println("No namespace selected. Use: ns use <name>")
		return
	}

	// Demo output - would call server
	fmt.Printf("Listing %s\n", path)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tNAME\tSTATUS\tDESCRIPTION")
	fmt.Fprintln(w, "----\t----\t------\t-----------")

	if path == "/" {
		fmt.Fprintln(w, "dir\ttargets\t\tAll targets")
		fmt.Fprintln(w, "dir\ttree\t\tHierarchical view")
	} else if path == "/targets" {
		// Would list targets
		fmt.Fprintln(w, "target\trouter\t2/3 up\tCore router")
		fmt.Fprintln(w, "target\tswitch\t1/1 up\tAccess switch")
	} else if strings.HasPrefix(path, "/targets/") {
		// Would list pollers for target
		fmt.Fprintln(w, "poller\tcpu\tup\tCPU utilization")
		fmt.Fprintln(w, "poller\tmemory\tup\tMemory usage")
		fmt.Fprintln(w, "poller\tifInOctets\tdown\tInterface in")
	}
	w.Flush()
}

func cmdCd(args []string) {
	if len(args) == 0 {
		currentPath = "/"
		return
	}

	newPath := resolvePath(args[0])

	// Validate path exists (would call server)
	// For now, just set it
	currentPath = newPath
}

func cmdPwd() {
	fmt.Println(currentPath)
}

func cmdCat(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: cat <path>")
		return
	}

	path := resolvePath(args[0])

	// Would call server to get details
	fmt.Printf("Details for %s:\n\n", path)

	if strings.Count(path, "/") == 2 {
		// Target
		fmt.Println("Type:        target")
		fmt.Println("Name:        router")
		fmt.Println("Description: Core router")
		fmt.Println("Labels:      site=dc1, role=core")
		fmt.Println()
		fmt.Println("Pollers:     3 total, 2 up, 1 down")
		fmt.Println("Created:     2024-01-15 10:30:00")
	} else if strings.Count(path, "/") == 3 {
		// Poller
		fmt.Println("Type:        poller")
		fmt.Println("Name:        cpu")
		fmt.Println("Protocol:    snmp")
		fmt.Println("Host:        192.168.1.1:161")
		fmt.Println("OID:         1.3.6.1.4.1.9.2.1.58.0")
		fmt.Println()
		fmt.Println("Admin State: enabled")
		fmt.Println("Oper State:  running")
		fmt.Println("Health:      up")
		fmt.Println()
		fmt.Println("Interval:    1000ms")
		fmt.Println("Timeout:     5000ms")
		fmt.Println("Retries:     2")
		fmt.Println()
		fmt.Println("Last Poll:   2024-01-15 14:32:45.123")
		fmt.Println("Last Value:  42")
		fmt.Println("Avg Poll:    23ms")
	}
}

func cmdTree(args []string) {
	path := "/tree"
	if len(args) > 0 {
		path = resolvePath(args[0])
	}

	// Demo output
	fmt.Printf("Tree at %s:\n", path)
	fmt.Println(".")
	fmt.Println("├── dc1/")
	fmt.Println("│   ├── network/")
	fmt.Println("│   │   ├── core-router -> target:router")
	fmt.Println("│   │   └── access-switch -> target:switch")
	fmt.Println("│   └── compute/")
	fmt.Println("└── dc2/")
	fmt.Println("    └── network/")
}

// ============================================================================
// Namespace Commands
// ============================================================================

func cmdNamespace(args []string) {
	if len(args) == 0 {
		// List namespaces
		fmt.Println("Namespaces:")
		fmt.Println("  * prod     (current)")
		fmt.Println("    dev")
		fmt.Println("    staging")
		return
	}

	switch args[0] {
	case "use", "switch":
		if len(args) < 2 {
			fmt.Println("Usage: ns use <name>")
			return
		}
		currentNamespace = args[1]
		currentPath = "/"
		fmt.Printf("Switched to namespace: %s\n", currentNamespace)

	case "create":
		if len(args) < 2 {
			fmt.Println("Usage: ns create <name>")
			return
		}
		// Would call server
		fmt.Printf("Created namespace: %s\n", args[1])

	case "delete":
		if len(args) < 2 {
			fmt.Println("Usage: ns delete <name>")
			return
		}
		// Would call server
		fmt.Printf("Deleted namespace: %s\n", args[1])

	default:
		fmt.Printf("Unknown namespace command: %s\n", args[0])
	}
}

// ============================================================================
// Target Commands
// ============================================================================

func cmdTarget(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: target <create|delete|set> ...")
		return
	}

	switch args[0] {
	case "create":
		if len(args) < 2 {
			fmt.Println("Usage: target create <name> [description]")
			return
		}
		// Would call server
		fmt.Printf("Created target: %s\n", args[1])

	case "delete":
		if len(args) < 2 {
			fmt.Println("Usage: target delete <name>")
			return
		}
		// Would call server
		fmt.Printf("Deleted target: %s\n", args[1])

	case "set":
		if len(args) < 3 {
			fmt.Println("Usage: target set <name> <key=value>...")
			return
		}
		// Would call server
		fmt.Printf("Updated target: %s\n", args[1])

	default:
		fmt.Printf("Unknown target command: %s\n", args[0])
	}
}

// ============================================================================
// Poller Commands
// ============================================================================

func cmdPoller(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: poller <create|delete|enable|disable|test> ...")
		return
	}

	switch args[0] {
	case "create":
		if len(args) < 4 {
			fmt.Println("Usage: poller create <target> <name> <host:oid> [options]")
			fmt.Println("  Options: -c community -i interval_ms -v3 ...")
			return
		}
		// Would call server
		fmt.Printf("Created poller: %s/%s\n", args[1], args[2])

	case "delete":
		if len(args) < 3 {
			fmt.Println("Usage: poller delete <target> <name>")
			return
		}
		// Would call server
		fmt.Printf("Deleted poller: %s/%s\n", args[1], args[2])

	case "enable":
		if len(args) < 3 {
			fmt.Println("Usage: poller enable <target> <name>")
			return
		}
		// Would call server
		fmt.Printf("Enabled poller: %s/%s\n", args[1], args[2])

	case "disable":
		if len(args) < 3 {
			fmt.Println("Usage: poller disable <target> <name>")
			return
		}
		// Would call server
		fmt.Printf("Disabled poller: %s/%s\n", args[1], args[2])

	case "test":
		if len(args) < 3 {
			fmt.Println("Usage: poller test <target> <name>")
			return
		}
		// Would call server and execute single poll
		fmt.Printf("Testing poller: %s/%s...\n", args[1], args[2])
		fmt.Println("Result: 42 (23ms)")

	default:
		fmt.Printf("Unknown poller command: %s\n", args[0])
	}
}

// ============================================================================
// Monitoring Commands
// ============================================================================

func cmdWatch(args []string) {
	if len(args) == 0 {
		// Watch current path
		fmt.Printf("Subscribing to %s...\n", currentPath)
	} else {
		paths := make([]string, len(args))
		for i, a := range args {
			paths[i] = resolvePath(a)
		}
		fmt.Printf("Subscribing to: %s\n", strings.Join(paths, ", "))
	}
	// Would set up subscription
	fmt.Println("(Live samples will appear here)")
}

func cmdUnwatch(args []string) {
	if len(args) == 0 {
		fmt.Println("Unsubscribed from all")
	} else {
		fmt.Printf("Unsubscribed from: %s\n", strings.Join(args, ", "))
	}
}

func cmdHistory(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: history <path> [count]")
		return
	}

	path := resolvePath(args[0])
	count := 10
	if len(args) > 1 {
		fmt.Sscanf(args[1], "%d", &count)
	}

	fmt.Printf("Last %d samples for %s:\n\n", count, path)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIMESTAMP\tVALUE\tPOLL_MS\tSTATUS")
	fmt.Fprintln(w, "---------\t-----\t-------\t------")
	fmt.Fprintln(w, "14:32:45.123\t42\t23\tok")
	fmt.Fprintln(w, "14:32:44.121\t41\t21\tok")
	fmt.Fprintln(w, "14:32:43.119\t40\t25\tok")
	w.Flush()
}

// ============================================================================
// Tree Commands
// ============================================================================

func cmdMkdir(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: mkdir <path>")
		return
	}
	path := resolvePath(args[0])
	// Would call server
	fmt.Printf("Created directory: %s\n", path)
}

func cmdLn(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: ln <target> <path>")
		fmt.Println("       ln <target/poller> <path>")
		return
	}
	// Would call server
	fmt.Printf("Created link: %s -> %s\n", args[1], args[0])
}

func cmdRm(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: rm <path>")
		return
	}
	path := resolvePath(args[0])
	// Would call server
	fmt.Printf("Removed: %s\n", path)
}

// ============================================================================
// System Commands
// ============================================================================

func cmdStatus() {
	fmt.Println("=== Server Status ===")
	fmt.Println("Version:        v2.0.0")
	fmt.Println("Uptime:         2h 34m")
	fmt.Println()
	fmt.Println("Namespaces:     3")
	fmt.Println("Targets:        42")
	fmt.Println("Pollers:        156 (142 running)")
	fmt.Println()
	fmt.Println("Health:         148 up, 8 down")
	fmt.Println()
	fmt.Println("Sessions:       5 active")
	fmt.Println()
	fmt.Println("Polls:")
	fmt.Println("  Total:        1,234,567")
	fmt.Println("  Success:      1,234,000 (99.95%)")
	fmt.Println("  Failed:       567")
}

func cmdSession() {
	fmt.Println("=== Session Info ===")
	fmt.Printf("Session ID:     %s\n", sessionID)
	fmt.Println("Token ID:       admin")
	fmt.Printf("Namespace:      %s\n", currentNamespace)
	fmt.Println("Connected:      2024-01-15 12:00:00")
	fmt.Println("Subscriptions:  3")
}

func cmdConfig(args []string) {
	if len(args) >= 2 && args[0] == "set" {
		// Would call server
		fmt.Printf("Config updated: %s\n", strings.Join(args[1:], " "))
		return
	}

	fmt.Println("=== Runtime Config ===")
	fmt.Println("Changeable:")
	fmt.Println("  default_timeout_ms:  5000")
	fmt.Println("  default_retries:     2")
	fmt.Println("  default_interval_ms: 1000")
	fmt.Println("  default_buffer_size: 3600")
	fmt.Println()
	fmt.Println("Read-only:")
	fmt.Println("  poller_workers:      100")
	fmt.Println("  poller_queue_size:   10000")
}

// ============================================================================
// Path Helpers
// ============================================================================

func resolvePath(path string) string {
	if path == "" {
		return currentPath
	}

	// Handle special cases
	if path == "~" || path == "/" {
		return "/"
	}

	// Handle relative paths
	if !strings.HasPrefix(path, "/") {
		if currentPath == "/" {
			path = "/" + path
		} else {
			path = currentPath + "/" + path
		}
	}

	// Normalize: handle .. and .
	parts := strings.Split(path, "/")
	var normalized []string

	for _, p := range parts {
		switch p {
		case "", ".":
			continue
		case "..":
			if len(normalized) > 0 {
				normalized = normalized[:len(normalized)-1]
			}
		default:
			normalized = append(normalized, p)
		}
	}

	if len(normalized) == 0 {
		return "/"
	}

	return "/" + strings.Join(normalized, "/")
}

// Keep sort package referenced
var _ = sort.Strings
