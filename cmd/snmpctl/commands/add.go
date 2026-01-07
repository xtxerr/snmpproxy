package commands

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xtxerr/snmpproxy/cmd/snmpctl/oids"
	"github.com/xtxerr/snmpproxy/cmd/snmpctl/shell"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
	"golang.org/x/term"
)

// AddCmd handles 'add' command with sub-commands.
type AddCmd struct{}

func (c *AddCmd) Name() string       { return "add" }
func (c *AddCmd) Aliases() []string  { return []string{"a"} }
func (c *AddCmd) Brief() string      { return "Add targets, tags, or aliases" }

func (c *AddCmd) Usage() string {
	return `Usage: add <type> ...

Add a new target, tag, or alias.

Types:
  target snmp <host> <oid> [name]     Add SNMPv2c target
  target snmpv3 <host> <oid> [name]   Add SNMPv3 target (interactive)
  target http <url> [name]            Add HTTP target (future)
  target icmp <host> [name]           Add ICMP target (future)
  tag <path>                          Create a tag
  alias <name> <oid>                  Create an OID alias

Target Options:
  --community=STRING    SNMPv2c community (default: public)
  --port=INT            Port number (default: 161 for SNMP)
  --interval=INT        Poll interval in ms (default: 1000)
  --tag=STRING          Add tag (repeatable)
  --persistent          Don't auto-delete when idle

OID Aliases:
  Common OIDs can be specified by name instead of number:
  sysUpTime, sysDescr, sysName, ifHCInOctets, etc.
  Use <alias>.N for indexed OIDs (e.g., ifHCInOctets.1)

Examples:
  add target snmp 192.168.1.1 sysUpTime
  add target snmp 192.168.1.1 ifHCInOctets.1 wan-in --interval=5000
  add target snmp 10.0.0.1 .1.3.6.1.2.1.1.3.0 --community=private
  add target snmpv3 192.168.1.1 sysUpTime router
  add tag location/dc1
  add alias cpu .1.3.6.1.4.1.9.9.109.1.1.1.1.4.1`
}

func (c *AddCmd) Complete(ctx *shell.Context, args []string, partial string) []Suggestion {
	// add [TAB] -> types
	if len(args) == 0 {
		return FilterSuggestions(addTypes, partial)
	}

	addType := strings.ToLower(args[0])

	switch addType {
	case "target":
		return c.completeTarget(ctx, args[1:], partial)
	case "tag":
		return c.completeTag(ctx, args[1:], partial)
	case "alias":
		return nil // No completion for alias
	default:
		// Still typing type
		if len(args) == 1 && partial == "" {
			return addTypes
		}
		return FilterSuggestions(addTypes, partial)
	}
}

var addTypes = []Suggestion{
	{Text: "target", Description: "Add monitoring target"},
	{Text: "tag", Description: "Create a tag"},
	{Text: "alias", Description: "Create OID alias"},
}

func (c *AddCmd) completeTarget(ctx *shell.Context, args []string, partial string) []Suggestion {
	// add target [TAB] -> protocols
	if len(args) == 0 {
		return FilterSuggestions(targetProtocols, partial)
	}

	protocol := strings.ToLower(args[0])

	switch protocol {
	case "snmp", "snmpv3":
		return c.completeSNMP(args[1:], partial)
	case "http", "icmp":
		return nil // Future
	default:
		if len(args) == 1 {
			return FilterSuggestions(targetProtocols, partial)
		}
	}
	return nil
}

var targetProtocols = []Suggestion{
	{Text: "snmp", Description: "SNMPv2c target"},
	{Text: "snmpv3", Description: "SNMPv3 target (interactive setup)"},
	{Text: "http", Description: "HTTP/HTTPS endpoint (future)"},
	{Text: "icmp", Description: "ICMP ping target (future)"},
}

func (c *AddCmd) completeSNMP(args []string, partial string) []Suggestion {
	// add target snmp [TAB] -> host placeholder
	if len(args) == 0 {
		if partial == "" {
			return []Suggestion{{Text: "<host>", Description: "Hostname or IP address"}}
		}
		return nil
	}

	// add target snmp host [TAB] -> OID aliases
	if len(args) == 1 {
		return c.completeOID(partial)
	}

	// add target snmp host oid [TAB] -> name or flags
	if len(args) == 2 {
		if strings.HasPrefix(partial, "-") {
			return FilterSuggestions(snmpFlags, partial)
		}
		if partial == "" {
			suggestions := []Suggestion{{Text: "<name>", Description: "Optional friendly name"}}
			suggestions = append(suggestions, snmpFlags...)
			return suggestions
		}
		return nil
	}

	// After name -> flags only
	return FilterSuggestions(snmpFlags, partial)
}

func (c *AddCmd) completeOID(partial string) []Suggestion {
	oidList := oids.List()
	var suggestions []Suggestion

	for _, o := range oidList {
		if partial == "" || strings.HasPrefix(strings.ToLower(o.Name), strings.ToLower(partial)) {
			suggestions = append(suggestions, Suggestion{
				Text:        o.Name,
				Description: o.Description,
			})
		}
	}

	// Also suggest raw OID input
	if partial == "" || strings.HasPrefix(partial, ".") || strings.HasPrefix(partial, "1.") {
		suggestions = append(suggestions, Suggestion{
			Text:        "<oid>",
			Description: "Enter numeric OID (e.g., .1.3.6.1.2.1.1.3.0)",
		})
	}

	return suggestions
}

var snmpFlags = []Suggestion{
	{Text: "--community=", Description: "Community string (default: public)"},
	{Text: "--port=", Description: "SNMP port (default: 161)"},
	{Text: "--interval=", Description: "Poll interval in ms (default: 1000)"},
	{Text: "--tag=", Description: "Add tag (repeatable)"},
	{Text: "--persistent", Description: "Persistent target"},
}

func (c *AddCmd) completeTag(ctx *shell.Context, args []string, partial string) []Suggestion {
	// Suggest existing tag paths for completion
	req := &pb.BrowseRequest{Path: "/tags", Limit: 50}
	resp, err := ctx.Client.Browse(req)
	if err != nil {
		return nil
	}

	var suggestions []Suggestion
	for _, node := range resp.Nodes {
		if node.Type == pb.NodeType_NODE_DIRECTORY {
			name := node.Name
			if partial == "" || strings.HasPrefix(name, partial) {
				suggestions = append(suggestions, Suggestion{
					Text:        name + "/",
					Description: node.Description,
				})
			}
		}
	}
	return suggestions
}

func (c *AddCmd) Execute(ctx *shell.Context, args []string) error {
	if len(args) == 0 {
		fmt.Println(c.Usage())
		return nil
	}

	addType := strings.ToLower(args[0])

	switch addType {
	case "target":
		return c.executeTarget(ctx, args[1:])
	case "tag":
		return c.executeTag(ctx, args[1:])
	case "alias":
		return c.executeAlias(ctx, args[1:])
	default:
		return fmt.Errorf("unknown type: %s (use: target, tag, alias)", addType)
	}
}

func (c *AddCmd) executeTarget(ctx *shell.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: add target <protocol> <host> <oid> [name] [options]\nProtocols: snmp, snmpv3, http, icmp")
	}

	protocol := strings.ToLower(args[0])

	switch protocol {
	case "snmp":
		return c.executeSNMP(ctx, args[1:], false)
	case "snmpv3":
		return c.executeSNMP(ctx, args[1:], true)
	case "http":
		return fmt.Errorf("HTTP targets not yet implemented")
	case "icmp":
		return fmt.Errorf("ICMP targets not yet implemented")
	default:
		return fmt.Errorf("unknown protocol: %s", protocol)
	}
}

func (c *AddCmd) executeSNMP(ctx *shell.Context, args []string, isV3 bool) error {
	if len(args) < 2 {
		if isV3 {
			return fmt.Errorf("usage: add target snmpv3 <host> <oid> [name] [options]")
		}
		return fmt.Errorf("usage: add target snmp <host> <oid> [name] [options]")
	}

	host := args[0]
	oidArg := args[1]

	// Resolve OID alias
	oid := oids.Resolve(oidArg)

	// Parse remaining args
	name := ""
	community := "public"
	port := uint32(161)
	interval := uint32(1000)
	var tags []string
	persistent := false

	for i := 2; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--community="):
			community = strings.TrimPrefix(arg, "--community=")
		case strings.HasPrefix(arg, "--port="):
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil {
				port = uint32(v)
			}
		case strings.HasPrefix(arg, "--interval="):
			if v, err := strconv.Atoi(strings.TrimPrefix(arg, "--interval=")); err == nil {
				interval = uint32(v)
			}
		case strings.HasPrefix(arg, "--tag="):
			tags = append(tags, strings.TrimPrefix(arg, "--tag="))
		case arg == "--persistent":
			persistent = true
		case !strings.HasPrefix(arg, "-"):
			if name == "" {
				name = arg
			}
		}
	}

	// Build SNMP config
	snmpCfg := &pb.SNMPTargetConfig{
		Host: host,
		Port: port,
		Oid:  oid,
	}

	if isV3 {
		// Interactive wizard for SNMPv3
		v3, err := c.snmpv3Wizard()
		if err != nil {
			return err
		}
		snmpCfg.Version = &pb.SNMPTargetConfig_V3{V3: v3}
	} else {
		snmpCfg.Version = &pb.SNMPTargetConfig_V2C{
			V2C: &pb.SNMPv2CConfig{Community: community},
		}
	}

	// Create request
	req := &pb.CreateTargetRequest{
		Name:       name,
		Tags:       tags,
		Persistent: persistent,
		IntervalMs: interval,
		Protocol:   &pb.CreateTargetRequest_Snmp{Snmp: snmpCfg},
	}

	resp, err := ctx.Client.CreateTarget(req)
	if err != nil {
		return err
	}

	// Display result
	displayName := resp.TargetId
	if resp.TargetName != "" {
		displayName = fmt.Sprintf("%s (%s)", resp.TargetName, resp.TargetId)
	}

	if resp.Created {
		fmt.Printf("Created: %s\n", displayName)
	} else {
		fmt.Printf("Joined: %s (%s)\n", displayName, resp.Message)
	}

	return nil
}

func (c *AddCmd) snmpv3Wizard() (*pb.SNMPv3Config, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\nSNMPv3 Configuration")
	fmt.Println("--------------------")

	// Security Name
	fmt.Print("Security Name: ")
	secName, _ := reader.ReadString('\n')
	secName = strings.TrimSpace(secName)
	if secName == "" {
		return nil, fmt.Errorf("security name is required")
	}

	// Security Level
	fmt.Print("Security Level [noAuthNoPriv/authNoPriv/authPriv] (default: authPriv): ")
	secLevel, _ := reader.ReadString('\n')
	secLevel = strings.TrimSpace(secLevel)
	if secLevel == "" {
		secLevel = "authPriv"
	}

	v3 := &pb.SNMPv3Config{
		SecurityName:  secName,
		SecurityLevel: secLevel,
	}

	// Auth configuration
	if secLevel == "authNoPriv" || secLevel == "authPriv" {
		fmt.Print("Auth Protocol [MD5/SHA/SHA224/SHA256/SHA384/SHA512] (default: SHA256): ")
		authProto, _ := reader.ReadString('\n')
		authProto = strings.TrimSpace(authProto)
		if authProto == "" {
			authProto = "SHA256"
		}
		v3.AuthProtocol = strings.ToUpper(authProto)

		fmt.Print("Auth Password: ")
		authPass, err := readPassword()
		if err != nil {
			return nil, err
		}
		if authPass == "" {
			return nil, fmt.Errorf("auth password is required for %s", secLevel)
		}
		v3.AuthPassword = authPass
		fmt.Println()
	}

	// Priv configuration
	if secLevel == "authPriv" {
		fmt.Print("Priv Protocol [DES/AES/AES192/AES256] (default: AES256): ")
		privProto, _ := reader.ReadString('\n')
		privProto = strings.TrimSpace(privProto)
		if privProto == "" {
			privProto = "AES256"
		}
		v3.PrivProtocol = strings.ToUpper(privProto)

		fmt.Print("Priv Password: ")
		privPass, err := readPassword()
		if err != nil {
			return nil, err
		}
		if privPass == "" {
			return nil, fmt.Errorf("priv password is required for authPriv")
		}
		v3.PrivPassword = privPass
		fmt.Println()
	}

	fmt.Println()
	return v3, nil
}

func readPassword() (string, error) {
	// Try to read password without echo
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		pass, err := term.ReadPassword(fd)
		if err != nil {
			return "", err
		}
		return string(pass), nil
	}

	// Fallback for non-terminal
	reader := bufio.NewReader(os.Stdin)
	pass, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(pass), nil
}

func (c *AddCmd) executeTag(ctx *shell.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: add tag <path>")
	}

	// Tags are created implicitly when used, but we can validate the path
	tagPath := args[0]
	if !strings.Contains(tagPath, "/") && len(tagPath) < 2 {
		return fmt.Errorf("tag path should be hierarchical (e.g., location/dc1)")
	}

	fmt.Printf("Tag '%s' will be created when first used.\n", tagPath)
	fmt.Println("Use: set <target> --tag=" + tagPath)
	return nil
}

func (c *AddCmd) executeAlias(ctx *shell.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: add alias <name> <oid>")
	}

	// Note: Runtime aliases would need to be stored somewhere
	// For now, just show the built-in aliases
	fmt.Println("Custom aliases not yet implemented.")
	fmt.Println("Built-in aliases are available:")
	for _, o := range oids.List() {
		fmt.Printf("  %-20s %s\n", o.Name, o.Description)
	}
	return nil
}
