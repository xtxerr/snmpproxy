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
	fmt.Println("  monitor <host> <oid> [name] [interval_ms] [community]")
	fmt.Println("  unmonitor <target-id>")
	fmt.Println("  list [host-filter]")
	fmt.Println("  info <target-id>")
	fmt.Println("  history <target-id> [count]")
	fmt.Println("  subscribe <target-id>...")
	fmt.Println("  unsubscribe [target-id]...")
	fmt.Println("  help")
	fmt.Println("  quit")
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

		parts := strings.Fields(line)
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

func cmdMonitor(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: monitor <host> <oid> [name] [interval_ms] [community]")
		return
	}

	host := args[0]
	oid := args[1]

	name := host + "/" + oid
	if len(args) > 2 {
		name = args[2]
	}

	interval := uint32(1000)
	if len(args) > 3 {
		if v, err := strconv.Atoi(args[3]); err == nil {
			interval = uint32(v)
		}
	}

	community := "public"
	if len(args) > 4 {
		community = args[4]
	}

	resp, err := cli.Monitor(&pb.MonitorRequest{
		Host:       host,
		Port:       161,
		Oid:        oid,
		IntervalMs: interval,
		BufferSize: 3600,
		Snmp: &pb.SNMPConfig{
			Version: &pb.SNMPConfig_V2C{
				V2C: &pb.SNMPv2C{Community: community},
			},
		},
	})
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
