package shell

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/c-bata/go-prompt"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// Executor is a function that executes a command line.
type Executor func(ctx *Context, line string) error

// Completer is a function that provides completions.
type Completer func(ctx *Context, line string, pos int) []prompt.Suggest

// Shell is the interactive command shell.
type Shell struct {
	ctx       *Context
	executor  Executor
	completer Completer
}

// NewShell creates a new shell.
func NewShell(ctx *Context, exec Executor, comp Completer) *Shell {
	return &Shell{
		ctx:       ctx,
		executor:  exec,
		completer: comp,
	}
}

// Run starts the interactive shell.
func (s *Shell) Run() {
	// Set up sample handler for live updates
	s.ctx.Client.OnSample(s.handleSample)

	// Handle Ctrl+C gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() {
		for range sigCh {
			fmt.Println("\n(Use 'quit' to exit)")
		}
	}()

	p := prompt.New(
		s.execute,
		s.complete,
		prompt.OptionPrefix(s.ctx.Prompt()),
		prompt.OptionLivePrefix(s.livePrefix),
		prompt.OptionTitle("snmpctl"),
		prompt.OptionHistory(s.ctx.GetHistory()),
		prompt.OptionPrefixTextColor(prompt.Cyan),
		prompt.OptionPreviewSuggestionTextColor(prompt.DarkGray),
		prompt.OptionSelectedSuggestionBGColor(prompt.DarkBlue),
		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionMaxSuggestion(12),
		prompt.OptionShowCompletionAtStart(),
		prompt.OptionCompletionOnDown(),
	)

	p.Run()
}

func (s *Shell) execute(line string) {
	if line == "" {
		return
	}

	// Save to history
	s.ctx.SaveHistory(line)

	// Check connection
	if !s.ctx.Client.IsConnected() {
		fmt.Println("Connection lost. Attempting to reconnect...")
		if err := s.ctx.Client.Reconnect(); err != nil {
			fmt.Printf("Reconnect failed: %v\n", err)
			return
		}
		fmt.Println("Reconnected.")
	}

	// Execute
	if err := s.executor(s.ctx, line); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func (s *Shell) complete(d prompt.Document) []prompt.Suggest {
	text := d.TextBeforeCursor()
	if text == "" {
		return nil
	}
	return s.completer(s.ctx, text, len(text))
}

func (s *Shell) livePrefix() (string, bool) {
	return s.ctx.Prompt(), true
}

func (s *Shell) handleSample(sample *pb.Sample) {
	ts := time.UnixMilli(sample.TimestampMs).Format("15:04:05.000")

	if sample.Valid {
		if sample.Text != "" {
			fmt.Printf("\n[%s] %s: %s\n", ts, sample.TargetId, sample.Text)
		} else {
			fmt.Printf("\n[%s] %s: %d (%dms)\n", ts, sample.TargetId, sample.Counter, sample.PollMs)
		}
	} else {
		fmt.Printf("\n[%s] %s: ERROR %s\n", ts, sample.TargetId, sample.Error)
	}
}
