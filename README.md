# snmpproxy

High-performance SNMP proxy server with hierarchical organization, persistent storage, and real-time streaming.

## Features

- **Zero-dependency deployment** - Single binary, embedded DuckDB storage
- **Hierarchical organization** - Namespaces, targets, and pollers with inheritance
- **Virtual filesystem** - Tree structure for organizing targets by location/function
- **Protocol-agnostic** - SNMP v2c/v3 now, HTTP/ICMP planned
- **Real-time streaming** - Subscribe to live metrics with efficient push
- **Config-as-code** - YAML configuration with hot reload
- **Secure** - TLS, token auth, encrypted secrets, namespace isolation

## Architecture

```
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│   snmpctl   │  │   snmpctl   │  │  Dashboard  │
└──────┬──────┘  └──────┬──────┘  └──────┬──────┘
       │ TLS            │ TLS            │ TLS
       └────────────────┼────────────────┘
                        ▼
              ┌──────────────────┐
              │    snmpproxyd    │
              │                  │
              │  • Namespaces    │
              │  • Scheduler     │
              │  • DuckDB Store  │
              └────────┬─────────┘
                       │ SNMP v2c/v3
       ┌───────────────┼───────────────┐
       ▼               ▼               ▼
   [Router]        [Switch]       [Firewall]
```

## Quick Start

```bash
# Build
make all

# Generate TLS certificates
make gen-certs

# Generate secret key for encrypted credentials
make gen-secret-key

# Create config
cp examples/config.yaml config.yaml
export SNMPPROXY_TOKEN="your-secret-token"

# Run server
./bin/snmpproxyd -config config.yaml

# Run CLI (in another terminal)
./bin/snmpctl -server localhost:9161 -tls-skip-verify
```

## CLI Commands

```
Navigation:
  ls [path]              List entries at path
  cd <path>              Change current path
  pwd                    Print current path
  cat <path>             Show details
  tree [path]            Show tree structure

Namespace:
  ns                     List namespaces
  ns use <name>          Switch namespace
  ns create <name>       Create namespace
  ns delete <name>       Delete namespace

Targets:
  target create <name>   Create target
  target delete <name>   Delete target
  target set <n> k=v...  Set properties

Pollers:
  poller create <target> <name> <host:oid> [options]
  poller delete <target> <name>
  poller enable <target> <name>
  poller disable <target> <name>
  poller test <target> <name>

Monitoring:
  watch [path...]        Subscribe to live samples
  unwatch [path...]      Unsubscribe
  history <path> [n]     Show sample history

Tree:
  mkdir <path>           Create directory
  ln <target> <path>     Create link
  rm <path>              Remove node

System:
  status                 Server status
  session                Session info
  config                 Runtime config
  help                   This help
  quit                   Exit
```

## Example Session

```
$ ./bin/snmpctl -server localhost:9161 -tls-skip-verify
snmpctl - Connecting to localhost:9161...
Connected (session: a1b2c3d4)

> ns
Namespaces:
  * prod     (current)
    dev

> ls /targets
TYPE    NAME          STATUS  DESCRIPTION
----    ----          ------  -----------
target  core-router   3/3 up  Core Router - DC1
target  access-switch 1/1 up  Access Switch

> cd /targets/core-router
> ls
TYPE    NAME      STATUS  DESCRIPTION
----    ----      ------  -----------
poller  cpu       up      CPU utilization
poller  memory    up      Memory usage
poller  traffic   up      Interface counters

> cat cpu
Type:        poller
Name:        cpu
Protocol:    snmp
Host:        192.168.1.1:161
OID:         1.3.6.1.4.1.9.2.1.58.0

Admin State: enabled
Oper State:  running
Health:      up

Interval:    5000ms
Last Poll:   2024-01-15 14:32:45.123
Last Value:  42
Avg Poll:    23ms

> watch cpu memory
Subscribing to: cpu, memory
[14:32:46.125] cpu: 43 (21ms)
[14:32:46.130] memory: 67% (18ms)
[14:32:51.122] cpu: 41 (23ms)
^C
> quit
```

## Configuration

```yaml
listen: "0.0.0.0:9161"

tls:
  cert_file: "certs/server.crt"
  key_file: "certs/server.key"

auth:
  tokens:
    - id: admin
      token: "${SNMPPROXY_TOKEN}"
    - id: readonly
      token: "${SNMPPROXY_RO_TOKEN}"
      namespaces: ["prod"]  # Restrict access

storage:
  db_path: "snmpproxy.db"
  secret_key_path: "secret.key"

snmp:
  timeout_ms: 5000
  retries: 2
  interval_ms: 1000
  buffer_size: 3600

poller:
  workers: 100
  queue_size: 10000

# Include namespace files
include:
  - "namespaces/*.yaml"

# Or define inline
namespaces:
  prod:
    description: "Production"
    targets:
      router:
        description: "Core router"
        pollers:
          cpu:
            protocol: snmp
            config:
              host: "192.168.1.1"
              oid: "1.3.6.1.4.1.9.2.1.58.0"
            admin_state: enabled
```

## Data Model

```
Namespace
├── Targets
│   └── Pollers (actual SNMP/HTTP/ICMP monitors)
├── Tree (virtual filesystem for organization)
│   └── Directories and Links to targets/pollers
└── Secrets (encrypted credentials)
```

**Config Inheritance:**
```
Server Defaults → Namespace Defaults → Target Defaults → Poller Config
```

## SNMPv3 Support

```yaml
targets:
  router:
    defaults:
      snmp:
        v3:
          security_name: "monitor"
          security_level: "authPriv"
          auth_protocol: "SHA256"
          auth_password: "secret:router-auth"  # Reference to secret
          priv_protocol: "AES"
          priv_password: "secret:router-priv"
```

**Security Levels:** noAuthNoPriv, authNoPriv, authPriv  
**Auth Protocols:** MD5, SHA, SHA-224, SHA-256, SHA-384, SHA-512  
**Privacy Protocols:** DES, AES, AES-192, AES-256

## Wire Protocol

Length-prefixed protobuf over TLS/TCP:
```
┌─────────────────────────────────────────────┐
│ [varint: message length][protobuf payload]  │
└─────────────────────────────────────────────┘
```

See `proto/snmpproxy.proto` for message definitions.

## Development

```bash
# Generate protobuf
make proto

# Run tests
make test

# Run with coverage
make test-cover

# Format code
make fmt

# Build release binaries
make release
```

## License

MIT
