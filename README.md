# snmpproxy

SNMP proxy server that polls network devices and streams data to subscribed clients.

## Architecture

```
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│   snmpctl   │  │   snmpctl   │  │  GUI Client │
└──────┬──────┘  └──────┬──────┘  └──────┬──────┘
       │ TLS            │ TLS            │ TLS
       └────────────────┼────────────────┘
                        ▼
              ┌──────────────────┐
              │    snmpproxyd    │
              │                  │
              │  • Sessions      │
              │  • Targets       │
              │  • Worker Pool   │
              └────────┬─────────┘
                       │ SNMP v2c/v3
       ┌───────────────┼───────────────┐
       ▼               ▼               ▼
   [Router]        [Switch]       [Firewall]
```

## Quick Start

```bash
# Install protoc-gen-go
make install-tools

# Generate protobuf and build
make all

# Generate TLS certificates
make gen-certs

# Create config
cp config.example.yaml config.yaml
export SNMPPROXY_TOKEN="your-secret-token"

# Run server
./bin/snmpproxyd -config config.yaml

# Run client (another terminal)
export SNMPPROXY_TOKEN="your-secret-token"
./bin/snmpctl -server localhost:9161 -tls-skip-verify
```

## snmpctl Commands

```
monitor <host> <oid> [name] [interval_ms] [community]
unmonitor <target-id>
list [host-filter]
info <target-id>
history <target-id> [count]
subscribe <target-id>...
unsubscribe [target-id]...
help
quit
```

## Example Session

```
> monitor 192.168.1.1 1.3.6.1.2.1.31.1.1.1.6.1 router-in 1000 public
Created: a1b2c3d4 (router-in)

> subscribe a1b2c3d4
Subscribed: [a1b2c3d4]

[14:32:45.123] router-in: 12345678901234 (23ms)
[14:32:46.125] router-in: 12345679012345 (21ms)

> list
ID         Host                 OID                                      Interval Subs  State
-----------------------------------------------------------------------------------------------
a1b2c3d4   192.168.1.1          1.3.6.1.2.1.31.1.1.1.6.1                 1000     1     polling

> history a1b2c3d4 5
Timestamp                Value                Valid    PollMs
------------------------------------------------------------
2024-01-15 14:32:45.123  12345678901234       ✓        23
2024-01-15 14:32:46.125  12345679012345       ✓        21

> unsubscribe
> quit
```

## Protocol

Length-delimited protobuf over TLS/TCP using `google.golang.org/protobuf/encoding/protodelim`.

See `proto/snmpproxy.proto` for message definitions.

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

snmp:
  timeout_ms: 5000
  retries: 2
  buffer_size: 3600  # samples per target

poller:
  workers: 100       # concurrent SNMP polls
  queue_size: 10000

session:
  auth_timeout_sec: 30
  reconnect_window_sec: 600  # preserve session for 10min after disconnect
```

## License

MIT
