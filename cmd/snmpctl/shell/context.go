// Package shell provides the interactive shell for snmpctl.
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xtxerr/snmpproxy/internal/client"
)

// Context holds the current shell state.
type Context struct {
	Client      *client.Client
	Path        string   // Current path (e.g., "/targets", "/tags/location")
	HistoryFile string
	History     []string
}

// NewContext creates a new shell context.
func NewContext(c *client.Client) *Context {
	homeDir, _ := os.UserHomeDir()
	histFile := filepath.Join(homeDir, ".snmpctl_history")

	ctx := &Context{
		Client:      c,
		Path:        "/",
		HistoryFile: histFile,
	}
	ctx.loadHistory()
	return ctx
}

// Prompt returns the current prompt string.
func (c *Context) Prompt() string {
	status := "●"
	if !c.Client.IsConnected() {
		status = "○"
	}
	return fmt.Sprintf("%s %s> ", status, c.Path)
}

// Cd changes the current path.
func (c *Context) Cd(path string) error {
	newPath := c.ResolvePath(path)

	// Validate path exists (basic validation)
	if !c.isValidPath(newPath) {
		return fmt.Errorf("no such path: %s", newPath)
	}

	c.Path = newPath
	return nil
}

// ResolvePath resolves a relative or absolute path.
func (c *Context) ResolvePath(path string) string {
	if path == "" {
		return c.Path
	}

	// Absolute path
	if strings.HasPrefix(path, "/") {
		return normalizePath(path)
	}

	// Relative path
	if c.Path == "/" {
		return normalizePath("/" + path)
	}
	return normalizePath(c.Path + "/" + path)
}

// normalizePath cleans up a path.
func normalizePath(path string) string {
	// Handle special cases
	if path == "" || path == "/" {
		return "/"
	}

	// Split and process
	parts := strings.Split(path, "/")
	var result []string

	for _, part := range parts {
		switch part {
		case "", ".":
			// Skip empty and current dir
		case "..":
			// Go up one level
			if len(result) > 0 {
				result = result[:len(result)-1]
			}
		default:
			result = append(result, part)
		}
	}

	if len(result) == 0 {
		return "/"
	}
	return "/" + strings.Join(result, "/")
}

// isValidPath checks if a path is valid (basic structure check).
func (c *Context) isValidPath(path string) bool {
	// Root is always valid
	if path == "/" {
		return true
	}

	// Check known top-level paths
	validRoots := []string{"/targets", "/tags", "/server", "/session"}
	for _, root := range validRoots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}

	return false
}

// PathParts returns the components of the current path.
func (c *Context) PathParts() []string {
	if c.Path == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(c.Path, "/"), "/")
}

// InTargets returns true if current path is under /targets.
func (c *Context) InTargets() bool {
	return c.Path == "/targets" || strings.HasPrefix(c.Path, "/targets/")
}

// InTags returns true if current path is under /tags.
func (c *Context) InTags() bool {
	return c.Path == "/tags" || strings.HasPrefix(c.Path, "/tags/")
}

// loadHistory loads command history from file.
func (c *Context) loadHistory() {
	data, err := os.ReadFile(c.HistoryFile)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			c.History = append(c.History, line)
		}
	}
	// Keep last 1000
	if len(c.History) > 1000 {
		c.History = c.History[len(c.History)-1000:]
	}
}

// SaveHistory appends a command to history.
func (c *Context) SaveHistory(cmd string) {
	c.History = append(c.History, cmd)

	f, err := os.OpenFile(c.HistoryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(cmd + "\n")
}

// GetHistory returns the command history.
func (c *Context) GetHistory() []string {
	return c.History
}
