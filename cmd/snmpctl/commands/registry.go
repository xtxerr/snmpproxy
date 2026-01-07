// Package commands provides the command registry and base types.
package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
)

// Command is the interface all commands must implement.
type Command interface {
	// Name returns the primary command name.
	Name() string

	// Aliases returns alternative names for the command.
	Aliases() []string

	// Brief returns a one-line description.
	Brief() string

	// Usage returns detailed usage information.
	Usage() string

	// Complete returns suggestions for tab completion.
	// args contains the arguments after the command name.
	// partial is the word currently being typed (may be empty).
	Complete(ctx *shell.Context, args []string, partial string) []Suggestion

	// Execute runs the command.
	// args contains the arguments after the command name.
	Execute(ctx *shell.Context, args []string) error
}

// Suggestion is a completion suggestion.
type Suggestion struct {
	Text        string
	Description string
}

// Registry holds all registered commands.
type Registry struct {
	commands map[string]Command
	aliases  map[string]string // alias -> primary name
}

// NewRegistry creates a new command registry.
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]Command),
		aliases:  make(map[string]string),
	}
}

// Register adds a command to the registry.
func (r *Registry) Register(cmd Command) {
	name := cmd.Name()
	r.commands[name] = cmd

	for _, alias := range cmd.Aliases() {
		r.aliases[alias] = name
	}
}

// Get returns a command by name or alias.
func (r *Registry) Get(name string) Command {
	// Check primary name
	if cmd, ok := r.commands[name]; ok {
		return cmd
	}
	// Check alias
	if primary, ok := r.aliases[name]; ok {
		return r.commands[primary]
	}
	return nil
}

// List returns all command names (sorted).
func (r *Registry) List() []string {
	var names []string
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// All returns all commands.
func (r *Registry) All() []Command {
	var cmds []Command
	for _, name := range r.List() {
		cmds = append(cmds, r.commands[name])
	}
	return cmds
}

// CompleteCommand returns suggestions for command names.
func (r *Registry) CompleteCommand(partial string) []Suggestion {
	var suggestions []Suggestion

	for _, cmd := range r.All() {
		name := cmd.Name()
		if partial == "" || strings.HasPrefix(name, partial) {
			suggestions = append(suggestions, Suggestion{
				Text:        name,
				Description: cmd.Brief(),
			})
		}
		// Also check aliases
		for _, alias := range cmd.Aliases() {
			if strings.HasPrefix(alias, partial) {
				suggestions = append(suggestions, Suggestion{
					Text:        alias,
					Description: fmt.Sprintf("→ %s", name),
				})
			}
		}
	}

	return suggestions
}

// Execute runs a command line.
func (r *Registry) Execute(ctx *shell.Context, line string) error {
	args := ParseArgs(line)
	if len(args) == 0 {
		return nil
	}

	cmdName := strings.ToLower(args[0])
	cmd := r.Get(cmdName)
	if cmd == nil {
		return fmt.Errorf("unknown command: %s (type 'help' for commands)", cmdName)
	}

	return cmd.Execute(ctx, args[1:])
}

// Complete returns suggestions for the current input.
func (r *Registry) Complete(ctx *shell.Context, line string, pos int) []Suggestion {
	// Get text up to cursor
	if pos > len(line) {
		pos = len(line)
	}
	text := line[:pos]

	args := ParseArgs(text)
	endsWithSpace := len(text) > 0 && text[len(text)-1] == ' '

	// No input yet - suggest commands
	if len(args) == 0 {
		return r.CompleteCommand("")
	}

	// Still typing command name
	if len(args) == 1 && !endsWithSpace {
		return r.CompleteCommand(args[0])
	}

	// Have a command - delegate to it
	cmdName := strings.ToLower(args[0])
	cmd := r.Get(cmdName)
	if cmd == nil {
		return nil
	}

	// Figure out the partial word being typed
	cmdArgs := args[1:]
	partial := ""
	if len(cmdArgs) > 0 && !endsWithSpace {
		partial = cmdArgs[len(cmdArgs)-1]
		cmdArgs = cmdArgs[:len(cmdArgs)-1]
	}

	return cmd.Complete(ctx, cmdArgs, partial)
}

// ParseArgs splits a command line into arguments.
// Handles quoted strings.
func ParseArgs(line string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range line {
		switch {
		case r == '"' || r == '\'':
			if inQuote && r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			} else {
				current.WriteRune(r)
			}
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

// FilterSuggestions filters suggestions by prefix.
func FilterSuggestions(suggestions []Suggestion, prefix string) []Suggestion {
	if prefix == "" {
		return suggestions
	}

	prefix = strings.ToLower(prefix)
	var filtered []Suggestion
	for _, s := range suggestions {
		if strings.HasPrefix(strings.ToLower(s.Text), prefix) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
