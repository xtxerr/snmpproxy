// snmpctl is the SNMP proxy CLI client.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/c-bata/go-prompt"
	"github.com/xtxerr/snmpproxy/cmd/snmpctl/commands"
	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
	"github.com/xtxerr/snmpproxy/internal/client"
)

var (
	serverAddr    = flag.String("server", "localhost:9161", "server address")
	token         = flag.String("token", "", "auth token (or SNMPPROXY_TOKEN env)")
	noTLS         = flag.Bool("no-tls", false, "disable TLS")
	tlsSkipVerify = flag.Bool("tls-skip-verify", false, "skip TLS certificate verification")
)

func main() {
	flag.Parse()

	// Get auth token
	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("SNMPPROXY_TOKEN")
	}
	if authToken == "" {
		log.Fatal("Token required: use -token or set SNMPPROXY_TOKEN")
	}

	log.SetFlags(log.Ltime | log.Lmicroseconds)

	// Create client
	cli := client.New(&client.Config{
		Addr:          *serverAddr,
		Token:         authToken,
		TLS:           !*noTLS,
		TLSSkipVerify: *tlsSkipVerify,
	})

	// Connect
	fmt.Printf("Connecting to %s...\n", *serverAddr)
	if err := cli.Connect(); err != nil {
		log.Fatalf("Connect failed: %v", err)
	}
	fmt.Printf("Connected (session: %s)\n\n", cli.SessionID())

	// Create shell context
	ctx := shell.NewContext(cli)

	// Create command registry and register all commands
	registry := commands.NewRegistry()
	registerCommands(registry)

	// Create executor and completer
	executor := func(ctx *shell.Context, line string) error {
		return registry.Execute(ctx, line)
	}
	completer := func(ctx *shell.Context, line string, pos int) []prompt.Suggest {
		suggestions := registry.Complete(ctx, line, pos)
		return toPromptSuggestions(suggestions)
	}

	// Print welcome
	printWelcome()

	// Run shell
	sh := shell.NewShell(ctx, executor, completer)
	sh.Run()
}

func registerCommands(r *commands.Registry) {
	// Navigation
	r.Register(&commands.PwdCmd{})
	r.Register(&commands.CdCmd{})
	r.Register(&commands.LsCmd{})
	r.Register(&commands.CatCmd{})

	// Target management
	r.Register(&commands.AddCmd{})
	r.Register(&commands.RmCmd{})
	r.Register(&commands.SetCmd{})

	// Subscriptions
	r.Register(&commands.SubCmd{})
	r.Register(&commands.UnsubCmd{})
	r.Register(&commands.WatchCmd{})
	r.Register(&commands.HistoryCmd{})

	// Config
	r.Register(&commands.ConfigCmd{})

	// Help
	r.Register(commands.NewHelpCmd(r))
}

func toPromptSuggestions(suggestions []commands.Suggestion) []prompt.Suggest {
	result := make([]prompt.Suggest, len(suggestions))
	for i, s := range suggestions {
		result[i] = prompt.Suggest{
			Text:        s.Text,
			Description: s.Description,
		}
	}
	return result
}

func printWelcome() {
	fmt.Println("Commands:")
	fmt.Println("  ls [path]                    List contents")
	fmt.Println("  cd <path>                    Change path")
	fmt.Println("  add target snmp <host> <oid> Add SNMP target")
	fmt.Println("  rm target <id>               Remove target")
	fmt.Println("  set target <id> <options>    Modify target")
	fmt.Println("  watch <id>                   Watch live updates")
	fmt.Println("  help [command]               Show help")
	fmt.Println()
	fmt.Println("Paths: /targets, /tags, /server, /session")
	fmt.Println("Type 'help' for more information.")
	fmt.Println()
}
