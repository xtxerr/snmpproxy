package commands

import (
	"fmt"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
)

// ConfigCmd shows or modifies configuration.
type ConfigCmd struct{}

func (c *ConfigCmd) Name() string       { return "config" }
func (c *ConfigCmd) Aliases() []string  { return []string{"cfg"} }
func (c *ConfigCmd) Brief() string      { return "Show or modify configuration" }

func (c *ConfigCmd) Usage() string {
	return `Usage: config
       config set <key> <value>

Show or modify runtime configuration.

Commands:
  config              Show current config
  config set K V      Set config value (alias for 'set config K V')

Keys:
  timeout             Default SNMP timeout (ms)
  retries             Default SNMP retries
  buffer              Default buffer size
  min-interval        Minimum poll interval (ms)

Examples:
  config
  config set timeout 3000`
}

func (c *ConfigCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	if len(args) == 0 {
		return FilterSuggestions([]Suggestion{
			{Text: "set", Description: "Set config value"},
		}, partial)
	}
	if len(args) == 1 && args[0] == "set" {
		return FilterSuggestions(configKeys, partial)
	}
	return nil
}

func (c *ConfigCmd) Execute(ctx *shell.Context, args []string) error {
	// config set K V -> delegate to SetCmd
	if len(args) >= 3 && args[0] == "set" {
		setCmd := &SetCmd{}
		return setCmd.Execute(ctx, append([]string{"config"}, args[1:]...))
	}

	// Show config
	cfg, err := ctx.Client.GetConfig()
	if err != nil {
		return err
	}

	fmt.Println("Runtime Configuration")
	fmt.Println("=====================")
	fmt.Println()
	fmt.Println("SNMP Defaults (changeable):")
	fmt.Printf("  timeout:       %d ms\n", cfg.DefaultTimeoutMs)
	fmt.Printf("  retries:       %d\n", cfg.DefaultRetries)
	fmt.Printf("  buffer:        %d samples\n", cfg.DefaultBufferSize)
	fmt.Println()
	fmt.Println("Limits (changeable):")
	fmt.Printf("  min-interval:  %d ms\n", cfg.MinIntervalMs)
	fmt.Println()
	fmt.Println("Poller (read-only):")
	fmt.Printf("  workers:       %d\n", cfg.PollerWorkers)
	fmt.Printf("  queue-size:    %d\n", cfg.PollerQueueSize)
	fmt.Println()
	fmt.Println("Session (read-only):")
	fmt.Printf("  auth-timeout:  %d sec\n", cfg.AuthTimeoutSec)
	fmt.Printf("  reconnect:     %d sec\n", cfg.ReconnectWindowSec)
	fmt.Println()
	fmt.Println("Network (read-only):")
	fmt.Printf("  listen:        %s\n", cfg.ListenAddress)
	fmt.Printf("  tls:           %v\n", cfg.TlsEnabled)
	fmt.Println()
	fmt.Println("Server:")
	fmt.Printf("  version:       %s\n", cfg.Version)
	fmt.Printf("  uptime:        %s\n", formatDuration(cfg.UptimeMs))

	return nil
}

func formatDuration(ms int64) string {
	secs := ms / 1000
	mins := secs / 60
	hours := mins / 60
	days := hours / 24

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours%24, mins%60)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins%60)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm %ds", mins, secs%60)
	}
	return fmt.Sprintf("%ds", secs)
}
