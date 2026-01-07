package commands

import (
	"fmt"
	"strings"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
)

// HelpCmd shows help information.
type HelpCmd struct {
	registry *Registry
}

// NewHelpCmd creates a help command.
func NewHelpCmd(r *Registry) *HelpCmd {
	return &HelpCmd{registry: r}
}

func (c *HelpCmd) Name() string     { return "help" }
func (c *HelpCmd) Aliases() []string { return []string{"?"} }
func (c *HelpCmd) Brief() string    { return "Show help information" }

func (c *HelpCmd) Usage() string {
	return `Usage: help [command]

Show help for a command, or list all commands if no argument given.

Examples:
  help          List all commands
  help add      Show help for 'add' command
  help ls       Show help for 'ls' command`
}

func (c *HelpCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	// Suggest command names
	return c.registry.CompleteCommand(partial)
}

func (c *HelpCmd) Execute(ctx *shell.Context, args []string) error {
	if len(args) == 0 {
		return c.showAllCommands()
	}

	cmdName := args[0]
	cmd := c.registry.Get(cmdName)
	if cmd == nil {
		return fmt.Errorf("unknown command: %s", cmdName)
	}

	fmt.Println(cmd.Usage())
	return nil
}

func (c *HelpCmd) showAllCommands() error {
	fmt.Println("Commands:")
	fmt.Println()

	// Group commands by category
	categories := map[string][]Command{
		"Navigation": {},
		"Targets":    {},
		"Other":      {},
	}

	navCmds := map[string]bool{"ls": true, "cd": true, "pwd": true, "cat": true}
	targetCmds := map[string]bool{"add": true, "rm": true, "set": true, "watch": true, "sub": true, "unsub": true}

	for _, cmd := range c.registry.All() {
		name := cmd.Name()
		if navCmds[name] {
			categories["Navigation"] = append(categories["Navigation"], cmd)
		} else if targetCmds[name] {
			categories["Targets"] = append(categories["Targets"], cmd)
		} else {
			categories["Other"] = append(categories["Other"], cmd)
		}
	}

	order := []string{"Navigation", "Targets", "Other"}
	for _, cat := range order {
		cmds := categories[cat]
		if len(cmds) == 0 {
			continue
		}

		fmt.Printf("  %s:\n", cat)
		for _, cmd := range cmds {
			aliases := ""
			if len(cmd.Aliases()) > 0 {
				aliases = " (" + strings.Join(cmd.Aliases(), ", ") + ")"
			}
			fmt.Printf("    %-12s %s%s\n", cmd.Name(), cmd.Brief(), aliases)
		}
		fmt.Println()
	}

	fmt.Println("Type 'help <command>' for detailed help on a command.")
	return nil
}
