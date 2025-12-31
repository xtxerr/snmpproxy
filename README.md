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
monitor <host> <oid> [name] [options]
  SNMPv2c: -c community -i interval_ms
  SNMPv3:  -v3 -u user -l level -a auth_proto -A auth_pass -x priv_proto -X priv_pass

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

## Wire Protocol

Messages are framed using **varint-prefixed length-delimited protobuf** over TLS/TCP,
compatible with Go's `google.golang.org/protobuf/encoding/protodelim`.

```
┌─────────────────────────────────────────────┐
│ [varint: message length][protobuf payload]  │
└─────────────────────────────────────────────┘
```

**Varint encoding:**
- Each byte uses 7 bits for data, 1 bit (MSB) as continuation flag
- MSB=1 means more bytes follow, MSB=0 means last byte
- Example: length 300 = 0xAC 0x02 (2 bytes)

See `proto/snmpproxy.proto` for message definitions.

### Message Format

All messages are wrapped in an `Envelope`:

```protobuf
message Envelope {
  uint64 id = 1;      // Request/Response correlation, Push uses id=0
  oneof payload {
    // Client → Server
    AuthRequest auth = 10;
    MonitorRequest monitor = 11;
    // ... etc
    
    // Server → Client
    AuthResponse auth_resp = 50;
    MonitorResponse monitor_resp = 51;
    // ... etc
    
    // Push (id=0)
    Sample sample = 80;
    
    // Error
    Error error = 99;
  }
}
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

## SNMPv3 Support

Full SNMPv3 support with all security levels:

| Level | Description |
|-------|-------------|
| `noAuthNoPriv` | No authentication, no encryption |
| `authNoPriv` | Authentication only |
| `authPriv` | Authentication + Encryption |

**Authentication protocols:** MD5, SHA, SHA-224, SHA-256, SHA-384, SHA-512

**Privacy protocols:** DES, AES, AES-192, AES-256

### Example v3 Usage

```bash
# authPriv with SHA-256 and AES
> monitor 192.168.1.1 1.3.6.1.2.1.1.1.0 sysDescr -v3 -u admin -l authPriv \
    -a SHA256 -A authpass123 -x AES -X privpass123
```

## Session Management

- Sessions persist for `reconnect_window_sec` (default: 10 min) after disconnect
- Subscriptions are preserved across reconnects
- Multiple clients can share the same target (deduplication by host:port/oid)

## License

MIT
