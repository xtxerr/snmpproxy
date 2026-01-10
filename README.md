# snmpproxy Technical Documentation

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Core Components](#3-core-components)
4. [Data Model](#4-data-model)
5. [Wire Protocol](#5-wire-protocol)
6. [Configuration](#6-configuration)
7. [Storage Layer](#7-storage-layer)
8. [Scheduling System](#8-scheduling-system)
9. [State Management](#9-state-management)
10. [Security](#10-security)
11. [Performance Characteristics](#11-performance-characteristics)
12. [Operations Guide](#12-operations-guide)

---

## 1. Overview

### 1.1 Purpose

snmpproxy is a high-performance SNMP monitoring system designed for large-scale network monitoring. It provides:

- **Centralized Polling**: Single server polls thousands of network devices
- **Real-time Streaming**: Push-based sample delivery to subscribed clients
- **Hierarchical Configuration**: Inheritance from server → namespace → target → poller
- **Virtual Filesystem**: Flexible organization via symlinks independent of physical structure
- **Zero External Dependencies**: Single binary with embedded DuckDB database

### 1.2 Design Goals

| Goal | Implementation |
|------|----------------|
| Scale to 100k targets | Heap-based scheduler, connection pooling, batch writes |
| Sub-second latency | In-memory state, push-based delivery |
| Operational simplicity | Single binary, embedded database, YAML config |
| Flexibility | Multi-tenant namespaces, virtual filesystem |
| Security | TLS, token auth, encrypted secrets |

---

## 2. Architecture

### 2.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              Clients                                     │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐    │
│  │   snmpctl   │  │   snmpctl   │  │  Custom App │  │     GUI     │    │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘    │
└─────────┼────────────────┼────────────────┼────────────────┼────────────┘
          │                │                │                │
          └────────────────┴───────┬────────┴────────────────┘
                                   │ TLS/TCP
                                   │ Protobuf (varint-delimited)
                                   ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                           snmpproxyd                                     │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                        Wire Protocol Layer                          │ │
│  │  • Message framing (varint length prefix)                          │ │
│  │  • Protobuf serialization                                          │ │
│  │  • Request/Response correlation                                    │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                   │                                      │
│  ┌────────────────────────────────┴───────────────────────────────────┐ │
│  │                         Handler Layer                               │ │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ │ │
│  │  │Namespace │ │ Target   │ │ Poller   │ │ Browse   │ │Subscribe │ │ │
│  │  │Handler   │ │ Handler  │ │ Handler  │ │ Handler  │ │Handler   │ │ │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘ └──────────┘ │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                   │                                      │
│  ┌────────────────────────────────┴───────────────────────────────────┐ │
│  │                         Manager Layer                               │ │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐               │ │
│  │  │ Namespace    │ │ Target       │ │ Poller       │               │ │
│  │  │ Manager      │ │ Manager      │ │ Manager      │               │ │
│  │  └──────────────┘ └──────────────┘ └──────────────┘               │ │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐               │ │
│  │  │ State        │ │ Stats        │ │ Config       │               │ │
│  │  │ Manager      │ │ Manager      │ │ Resolver     │               │ │
│  │  └──────────────┘ └──────────────┘ └──────────────┘               │ │
│  │  ┌──────────────┐ ┌──────────────┐                                │ │
│  │  │ Tree         │ │ Sync         │                                │ │
│  │  │ Manager      │ │ Manager      │                                │ │
│  │  └──────────────┘ └──────────────┘                                │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                   │                                      │
│  ┌────────────────────────────────┴───────────────────────────────────┐ │
│  │                        Scheduler Layer                              │ │
│  │  ┌──────────────────────────────────────────────────────────────┐ │ │
│  │  │                    Heap-based Scheduler                       │ │ │
│  │  │  • Min-heap ordered by next_poll_time                        │ │ │
│  │  │  • O(log n) insert/remove                                    │ │ │
│  │  │  • Jitter for load distribution                              │ │ │
│  │  └──────────────────────────────────────────────────────────────┘ │ │
│  │  ┌──────────────────────────────────────────────────────────────┐ │ │
│  │  │                      Worker Pool                              │ │ │
│  │  │  • Configurable worker count (default: 100)                  │ │ │
│  │  │  • Job queue with backpressure                               │ │ │
│  │  │  • SNMP v2c/v3 execution                                     │ │ │
│  │  └──────────────────────────────────────────────────────────────┘ │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                   │                                      │
│  ┌────────────────────────────────┴───────────────────────────────────┐ │
│  │                         Store Layer                                 │ │
│  │  ┌──────────────────────────────────────────────────────────────┐ │ │
│  │  │                        DuckDB                                 │ │ │
│  │  │  • Embedded SQL database                                     │ │ │
│  │  │  • Columnar storage (efficient for time-series)              │ │ │
│  │  │  • No external dependencies                                  │ │ │
│  │  └──────────────────────────────────────────────────────────────┘ │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
                                   │
                                   │ SNMP v2c/v3
                                   ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                          Network Devices                                 │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐    │
│  │   Router    │  │   Switch    │  │  Firewall   │  │   Server    │    │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘    │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Layer Responsibilities

| Layer | Responsibility | Key Files |
|-------|---------------|-----------|
| **Wire Protocol** | Message framing, serialization, request correlation | `internal/wire/wire.go` |
| **Handler** | Request validation, authorization, response building | `internal/handler/*.go` |
| **Manager** | Business logic, caching, state machines | `internal/manager/*.go` |
| **Scheduler** | Timing, job distribution, worker management | `internal/scheduler/*.go` |
| **Store** | Persistence, queries, transactions | `internal/store/*.go` |

### 2.3 Request Flow

```
Client Request
      │
      ▼
┌─────────────────┐
│ Wire: Read      │  Varint length + protobuf decode
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Dispatch        │  Route to handler by message type
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Handler         │  Validate, authorize, extract params
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Manager         │  Execute business logic
│ (+ Cache)       │  Check cache, update state
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Store           │  Persist to DuckDB (if needed)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Wire: Write     │  Protobuf encode + varint length
└────────┬────────┘
         │
         ▼
   Client Response
```

### 2.4 Poll Cycle Flow

```
┌─────────────────────────────────────────────────────────────────────────┐
│                            Scheduler Loop                                │
│                                                                          │
│  ┌─────────────────────┐                                                │
│  │ 1. Check Heap       │  Get pollers where next_poll_time <= now      │
│  │    (peek)           │                                                │
│  └──────────┬──────────┘                                                │
│             │                                                            │
│             ▼                                                            │
│  ┌─────────────────────┐                                                │
│  │ 2. Pop Due Items    │  Extract all due pollers from heap            │
│  │    (under lock)     │  Mark as "polling"                            │
│  └──────────┬──────────┘                                                │
│             │                                                            │
│             ▼                                                            │
│  ┌─────────────────────┐                                                │
│  │ 3. Queue Jobs       │  Send to job channel (backpressure)           │
│  │    (lock-free)      │  Reschedule if queue full                     │
│  └──────────┬──────────┘                                                │
│             │                                                            │
└─────────────┼────────────────────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                             Worker Pool                                  │
│                                                                          │
│  ┌─────────────────────┐                                                │
│  │ 4. Execute Poll     │  SNMP GET with timeout                        │
│  │    (SNMPPoller)     │  Parse response                               │
│  └──────────┬──────────┘                                                │
│             │                                                            │
│             ▼                                                            │
│  ┌─────────────────────┐                                                │
│  │ 5. Mark Complete    │  Update next_poll_time                        │
│  │    (re-heap)        │  Clear "polling" flag                         │
│  └──────────┬──────────┘                                                │
│             │                                                            │
│             ▼                                                            │
│  ┌─────────────────────┐                                                │
│  │ 6. Send Result      │  Push to results channel                      │
│  └──────────┬──────────┘                                                │
│             │                                                            │
└─────────────┼────────────────────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                          Result Processor                                │
│                                                                          │
│  ┌─────────────────────┐                                                │
│  │ 7. Update State     │  OperState, HealthState, ConsecutiveFailures  │
│  └──────────┬──────────┘                                                │
│             │                                                            │
│             ▼                                                            │
│  ┌─────────────────────┐                                                │
│  │ 8. Update Stats     │  PollsTotal, PollsSuccess, Timing             │
│  └──────────┬──────────┘                                                │
│             │                                                            │
│             ▼                                                            │
│  ┌─────────────────────┐                                                │
│  │ 9. Buffer Sample    │  Add to SyncManager batch                     │
│  │    (SyncManager)    │  Flush when batch full or timeout             │
│  └──────────┬──────────┘                                                │
│             │                                                            │
│             ▼                                                            │
│  ┌─────────────────────┐                                                │
│  │ 10. Broadcast       │  Push to subscribed sessions                  │
│  │     (if subscribed) │  Via reverse index lookup                     │
│  └─────────────────────┘                                                │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Core Components

### 3.1 Server (`internal/server/server.go`)

The Server is the main entry point that orchestrates all components.

```go
type Server struct {
    cfg      *Config
    listener net.Listener
    
    // Core components
    mgr       *manager.Manager      // Business logic
    scheduler *scheduler.Scheduler  // Poll scheduling
    sessions  *handler.SessionManager
    
    // Protocol handlers
    nsHandler     *handler.NamespaceHandler
    targetHandler *handler.TargetHandler
    pollerHandler *handler.PollerHandler
    // ... more handlers
    
    // SNMP execution
    snmpPoller *scheduler.SNMPPoller
}
```

**Lifecycle:**
1. `New()` - Create server, initialize components
2. `Run()` - Load data, start scheduler, accept connections
3. `Shutdown()` - Graceful stop, flush pending writes

### 3.2 Manager (`internal/manager/manager.go`)

The Manager orchestrates all entity managers and provides a unified API.

```go
type Manager struct {
    store *store.Store
    
    // Entity managers (persistent)
    Namespaces *NamespaceManager
    Targets    *TargetManager
    Pollers    *PollerManager
    Tree       *TreeManager
    
    // Runtime managers (in-memory)
    States *StateManager
    Stats  *StatsManager
    Sync   *SyncManager
    
    // Configuration
    ConfigResolver *ConfigResolver
}
```

**Key Methods:**
- `Load()` - Load all entities from database into memory
- `RecordPollResult()` - Process poll result, update state/stats/samples
- `GetPollerFullInfo()` - Get complete poller information including resolved config

### 3.3 Session Manager (`internal/handler/session.go`)

Manages client connections, authentication, and subscriptions.

```go
type SessionManager struct {
    sessions          map[string]*Session
    tokens            map[string]*TokenConfig
    subscriptionIndex map[string]map[string]struct{}  // Reverse index
    
    reconnectWindow time.Duration
    authTimeout     time.Duration
}
```

**Session Lifecycle:**
```
Connect → Authenticate → Bind Namespace → Subscribe → ... → Disconnect
                │                                              │
                └──────── Reconnect Window (10 min) ──────────┘
```

**Subscription Reverse Index:**
- Key: `namespace/target/poller`
- Value: Set of session IDs
- Enables O(1) subscriber lookup for broadcasts

### 3.4 Scheduler (`internal/scheduler/scheduler.go`)

Heap-based scheduler for precise poll timing.

```go
type Scheduler struct {
    heap    PollHeap                  // Min-heap by next_poll_time
    heapIdx map[string]*PollItem      // Key → heap item
    jobs    chan PollJob              // To workers
    results chan PollResult           // From workers
    workers int
}
```

**Heap Operations:**
| Operation | Complexity | Description |
|-----------|------------|-------------|
| Add | O(log n) | Insert new poller |
| Remove | O(log n) | Remove poller |
| Peek | O(1) | Check next due time |
| Pop | O(log n) | Extract due poller |

**Jitter:**
New pollers get random initial delay (0 to interval) to prevent thundering herd.

### 3.5 Sync Manager (`internal/manager/sync.go`)

Batches writes for database efficiency.

```go
type SyncManager struct {
    store        *store.Store
    sampleBuffer []*store.Sample
    
    // Configuration
    sampleBatchSize    int           // Default: 1000
    sampleFlushTimeout time.Duration // Default: 5s
    stateFlushInterval time.Duration // Default: 5s
    statsFlushInterval time.Duration // Default: 10s
}
```

**Flush Triggers:**
1. **Batch Size** - Flush when buffer reaches `sample_batch_size`
2. **Timeout** - Flush every `sample_flush_timeout_sec` regardless of batch size
3. **Shutdown** - Final flush on graceful shutdown

**Write Pattern:**
```
Samples:  Single INSERT with batch values
States:   UPDATE ... WHERE (ns, target, poller) IN (...)
Stats:    UPSERT (INSERT ON CONFLICT UPDATE)
```

### 3.6 Config Resolver (`internal/manager/config_resolver.go`)

Resolves inherited configuration with caching.

```go
type ConfigResolver struct {
    store    *store.Store
    cache    sync.Map              // key → *configCacheEntry
    cacheTTL time.Duration         // Default: 30s
}
```

**Inheritance Chain:**
```
Server Defaults (snmp.timeout_ms, etc.)
        ↓
Namespace Defaults (namespace.config.defaults)
        ↓
Target Defaults (target.config.defaults)
        ↓
Poller Explicit (poller.polling_config)
```

**Cache Invalidation:**
- `Invalidate(namespace, target, poller)` - Single poller
- `InvalidateTarget(namespace, target)` - All pollers in target
- `InvalidateNamespace(namespace)` - All pollers in namespace

---

## 4. Data Model

### 4.1 Entity Hierarchy

```
Server
 └── Namespace (multi-tenant isolation)
      ├── Config (defaults for all children)
      ├── Targets (physical devices)
      │    ├── Config (defaults for pollers)
      │    ├── Labels (key-value metadata)
      │    └── Pollers (monitoring points)
      │         ├── Protocol Config (SNMP settings)
      │         ├── Polling Config (interval, timeout)
      │         ├── State (oper_state, health_state)
      │         ├── Stats (counters, timing)
      │         └── Samples (time-series data)
      │
      ├── Tree (virtual filesystem)
      │    ├── Directories
      │    └── Links → Targets/Pollers
      │
      └── Secrets (encrypted credentials)
```

### 4.2 Namespace

Provides multi-tenant isolation. All entities belong to a namespace.

```go
type Namespace struct {
    Name        string           // Primary key, e.g., "production"
    Description string
    Config      *NamespaceConfig // Defaults for children
    CreatedAt   time.Time
    UpdatedAt   time.Time
    Version     int              // Optimistic locking
}

type NamespaceConfig struct {
    Defaults *PollerDefaults    // Inherited by all pollers
}
```

### 4.3 Target

Represents a network device or endpoint.

```go
type Target struct {
    Namespace   string
    Name        string            // Unique within namespace
    Description string
    Labels      map[string]string // Arbitrary metadata
    Config      *TargetConfig
    CreatedAt   time.Time
    UpdatedAt   time.Time
    Version     int
}

type TargetConfig struct {
    Host     string           // IP or hostname
    Port     uint16           // Default: 161 for SNMP
    Defaults *PollerDefaults  // Override namespace defaults
}
```

### 4.4 Poller

A single monitoring point (OID) on a target.

```go
type Poller struct {
    Namespace      string
    Target         string
    Name           string           // Unique within target
    Description    string
    Protocol       string           // "snmp", future: "http", "icmp"
    ProtocolConfig json.RawMessage  // Protocol-specific settings
    PollingConfig  *PollingConfig   // Timing overrides
    AdminState     string           // "enabled" | "disabled"
    CreatedAt      time.Time
    UpdatedAt      time.Time
    Version        int
}

type PollingConfig struct {
    IntervalMs *uint32  // nil = inherit
    TimeoutMs  *uint32
    Retries    *uint32
    BufferSize *uint32
}
```

### 4.5 Sample

A single polled value (time-series data point).

```go
type Sample struct {
    Namespace    string
    Target       string
    Poller       string
    TimestampMs  int64     // Unix milliseconds
    ValueCounter *uint64   // For Counter32/64, Gauge, Integer
    ValueText    *string   // For OctetString
    ValueGauge   *float64  // For floating-point values
    Valid        bool      // false if error
    Error        string    // Error message if !Valid
    PollMs       int       // Poll duration
}
```

### 4.6 Tree Node

Virtual filesystem entry for flexible organization.

```go
type TreeNode struct {
    Namespace   string
    Path        string    // e.g., "/dc1/routers/core-1/cpu"
    NodeType    string    // "directory" | "link"
    LinkRef     string    // "target:router" | "poller:router/cpu"
    Description string
    CreatedAt   time.Time
}
```

**Link Reference Format:**
- Target link: `target:<target_name>`
- Poller link: `poller:<target_name>/<poller_name>`

### 4.7 State Model

Runtime state for each poller (in-memory with periodic persistence).

```go
type PollerState struct {
    Namespace string
    Target    string
    Poller    string
    
    // Administrative
    AdminState string  // "enabled" | "disabled"
    
    // Operational
    OperState  string  // "stopped" | "starting" | "running" | "stopping"
    
    // Health
    HealthState         string    // "unknown" | "up" | "down" | "degraded"
    LastError           string
    ConsecutiveFailures int
    
    // Timestamps
    LastPollAt    *time.Time
    LastSuccessAt *time.Time
    LastFailureAt *time.Time
}
```

**State Transitions:**

```
AdminState: enabled ←→ disabled

OperState:
           ┌──────────────────────────────────────────┐
           │                                          │
           ▼                                          │
        stopped ──start──► starting ──► running ──stop──► stopping
           ▲                              │                  │
           └──────────────────────────────┴──────────────────┘

HealthState:
        unknown ──first poll──► up ◄──────► down
                                │            │
                                └──► degraded ◄┘
```

---

## 5. Wire Protocol

### 5.1 Message Framing

Messages use **varint-prefixed length-delimited protobuf** encoding:

```
┌─────────────────────────────────────────────────────────────┐
│ [varint: length] [protobuf: Envelope]                       │
└─────────────────────────────────────────────────────────────┘

Varint encoding (protobuf standard):
- Each byte: 7 data bits + 1 continuation bit (MSB)
- MSB=1: more bytes follow
- MSB=0: last byte

Examples:
  Length 127:  0x7F (1 byte)
  Length 128:  0x80 0x01 (2 bytes)
  Length 300:  0xAC 0x02 (2 bytes)
```

### 5.2 Envelope Structure

All messages are wrapped in an `Envelope`:

```protobuf
message Envelope {
    uint64 id = 1;  // Request/Response correlation, Push uses id=0
    
    oneof payload {
        // Client → Server (requests)
        AuthRequest auth = 10;
        BindNamespaceRequest bind_namespace = 11;
        CreateNamespaceRequest create_namespace = 12;
        // ... more request types
        
        // Server → Client (responses)
        AuthResponse auth_resp = 50;
        BindNamespaceResponse bind_namespace_resp = 51;
        // ... more response types
        
        // Server → Client (push, id=0)
        Sample sample = 80;
        
        // Error response
        Error error = 99;
    }
}
```

### 5.3 Request/Response Correlation

```
Client                          Server
   │                               │
   │  Envelope{id=1, auth}         │
   │──────────────────────────────►│
   │                               │
   │  Envelope{id=1, auth_resp}    │
   │◄──────────────────────────────│
   │                               │
   │  Envelope{id=2, list}         │
   │──────────────────────────────►│
   │                               │
   │  Envelope{id=0, sample}       │  (push, no correlation)
   │◄──────────────────────────────│
   │                               │
   │  Envelope{id=2, list_resp}    │
   │◄──────────────────────────────│
```

### 5.4 Message Types

| ID Range | Direction | Purpose |
|----------|-----------|---------|
| 10-29 | Client → Server | Requests |
| 50-69 | Server → Client | Responses |
| 80-89 | Server → Client | Push (id=0) |
| 99 | Server → Client | Error |

**Complete Message Catalog:**

```protobuf
// Authentication
AuthRequest           = 10;  // Initial authentication
AuthResponse          = 50;

// Namespace operations
BindNamespaceRequest      = 11;
BindNamespaceResponse     = 51;
CreateNamespaceRequest    = 12;
CreateNamespaceResponse   = 52;
GetNamespaceRequest       = 13;
GetNamespaceResponse      = 53;
ListNamespacesRequest     = 14;
ListNamespacesResponse    = 54;
DeleteNamespaceRequest    = 15;
DeleteNamespaceResponse   = 55;

// Target operations
CreateTargetRequest       = 16;
CreateTargetResponse      = 56;
GetTargetRequest          = 17;
GetTargetResponse         = 57;
ListTargetsRequest        = 18;
ListTargetsResponse       = 58;
UpdateTargetRequest       = 19;
UpdateTargetResponse      = 59;
DeleteTargetRequest       = 20;
DeleteTargetResponse      = 60;

// Poller operations
CreatePollerRequest       = 21;
CreatePollerResponse      = 61;
GetPollerRequest          = 22;
GetPollerResponse         = 62;
ListPollersRequest        = 23;
ListPollersResponse       = 63;
EnablePollerRequest       = 24;
EnablePollerResponse      = 64;
DisablePollerRequest      = 25;
DisablePollerResponse     = 65;
DeletePollerRequest       = 26;
DeletePollerResponse      = 66;

// Subscription
SubscribeRequest          = 27;
SubscribeResponse         = 67;
UnsubscribeRequest        = 28;
UnsubscribeResponse       = 68;

// Samples/History
GetSamplesRequest         = 29;
GetSamplesResponse        = 69;

// Push messages
Sample                    = 80;

// Error
Error                     = 99;
```

### 5.5 Error Codes

```protobuf
message Error {
    int32 code = 1;
    string message = 2;
}
```

| Code | Name | Description |
|------|------|-------------|
| 1 | ErrUnknown | Unknown error |
| 2 | ErrAuthFailed | Authentication failed |
| 3 | ErrNotAuthenticated | Request before authentication |
| 4 | ErrInvalidRequest | Malformed request |
| 5 | ErrNotFound | Entity not found |
| 6 | ErrAlreadyExists | Entity already exists |
| 7 | ErrInternal | Internal server error |
| 8 | ErrNotAuthorized | Permission denied |
| 9 | ErrNamespaceRequired | Namespace binding required |

### 5.6 Wire Implementation

```go
// internal/wire/wire.go

const MaxMessageSize = 16 * 1024 * 1024  // 16 MB

type Conn struct {
    *Reader
    *Writer
}

func (r *Reader) Read() (*pb.Envelope, error) {
    env := &pb.Envelope{}
    opts := protodelim.UnmarshalOptions{MaxSize: MaxMessageSize}
    err := opts.UnmarshalFrom(r.r, env)
    return env, err
}

func (w *Writer) Write(env *pb.Envelope) error {
    _, err := protodelim.MarshalTo(w.w, env)
    return err
}
```

---

## 6. Configuration

### 6.1 Configuration Hierarchy

```yaml
# config.yaml structure
server:     # Network settings
tls:        # TLS certificates
auth:       # Authentication tokens
session:    # Session timeouts
poller:     # Scheduler settings
storage:    # Database and batching
snmp:       # SNMP defaults
targets:    # Pre-configured targets (optional)
```

### 6.2 Configuration Sources (Priority)

1. **CLI Flags** (highest priority)
2. **Environment Variables** (`${VAR}` in YAML)
3. **Config File** (`config.yaml`)
4. **Defaults** (lowest priority)

### 6.3 Storage Configuration

```yaml
storage:
  # Database location
  db_path: "data/snmpproxy.db"
  
  # Secret encryption (32-byte key)
  secret_key_path: "secret.key"
  
  # Sample batching
  sample_batch_size: 1000        # Samples before flush
  sample_flush_timeout_sec: 5    # Max hold time
  
  # State/Stats persistence
  state_flush_interval_sec: 5    # Poller state
  stats_flush_interval_sec: 10   # Poller statistics
```

**Tuning Matrix:**

| Scenario | batch_size | flush_timeout | Rationale |
|----------|------------|---------------|-----------|
| Default | 1000 | 5s | Balanced |
| High throughput | 5000 | 10s | Fewer writes |
| Low latency | 500 | 2s | Faster availability |
| High durability | 100 | 1s | Minimal loss window |

### 6.4 SNMP Configuration Inheritance

```yaml
# Server defaults (config.yaml)
snmp:
  timeout_ms: 5000
  retries: 2

# Namespace defaults (API)
namespace:
  config:
    defaults:
      timeout_ms: 3000      # Override server

# Target defaults (API)
target:
  config:
    defaults:
      timeout_ms: 2000      # Override namespace

# Poller explicit (API)
poller:
  polling_config:
    timeout_ms: 1000        # Override target (final)
```

**Resolution Example:**
```
Config: Server=5000ms, Namespace=3000ms, Target=nil, Poller=nil
Result: timeout_ms = 3000ms (from namespace)

Config: Server=5000ms, Namespace=nil, Target=2000ms, Poller=1000ms
Result: timeout_ms = 1000ms (from poller)
```

---

## 7. Storage Layer

### 7.1 Database Schema

```sql
-- Namespaces (multi-tenant root)
CREATE TABLE namespaces (
    name VARCHAR PRIMARY KEY,
    description VARCHAR,
    config JSON,                    -- NamespaceConfig
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now(),
    version INTEGER DEFAULT 1
);

-- Targets (network devices)
CREATE TABLE targets (
    namespace VARCHAR NOT NULL,
    name VARCHAR NOT NULL,
    description VARCHAR,
    labels JSON,                    -- map[string]string
    config JSON,                    -- TargetConfig
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now(),
    version INTEGER DEFAULT 1,
    PRIMARY KEY (namespace, name)
);

-- Pollers (monitoring points)
CREATE TABLE pollers (
    namespace VARCHAR NOT NULL,
    target VARCHAR NOT NULL,
    name VARCHAR NOT NULL,
    description VARCHAR,
    protocol VARCHAR NOT NULL,      -- "snmp"
    protocol_config JSON NOT NULL,  -- SNMPConfig
    polling_config JSON,            -- PollingConfig
    admin_state VARCHAR DEFAULT 'disabled',
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now(),
    version INTEGER DEFAULT 1,
    PRIMARY KEY (namespace, target, name)
);

-- Poller State (runtime, persisted for recovery)
CREATE TABLE poller_state (
    namespace VARCHAR NOT NULL,
    target VARCHAR NOT NULL,
    poller VARCHAR NOT NULL,
    oper_state VARCHAR DEFAULT 'stopped',
    health_state VARCHAR DEFAULT 'unknown',
    last_error VARCHAR,
    consecutive_failures INTEGER DEFAULT 0,
    last_poll_at TIMESTAMP,
    last_success_at TIMESTAMP,
    last_failure_at TIMESTAMP,
    PRIMARY KEY (namespace, target, poller)
);

-- Poller Statistics
CREATE TABLE poller_stats (
    namespace VARCHAR NOT NULL,
    target VARCHAR NOT NULL,
    poller VARCHAR NOT NULL,
    polls_total BIGINT DEFAULT 0,
    polls_success BIGINT DEFAULT 0,
    polls_failed BIGINT DEFAULT 0,
    polls_timeout BIGINT DEFAULT 0,
    poll_ms_sum BIGINT DEFAULT 0,
    poll_ms_min INTEGER,
    poll_ms_max INTEGER,
    poll_ms_count INTEGER DEFAULT 0,
    PRIMARY KEY (namespace, target, poller)
);

-- Samples (time-series data)
CREATE TABLE samples (
    namespace VARCHAR NOT NULL,
    target VARCHAR NOT NULL,
    poller VARCHAR NOT NULL,
    timestamp_ms BIGINT NOT NULL,
    value_counter UBIGINT,          -- Counter/Gauge/Integer
    value_text VARCHAR,             -- OctetString
    value_gauge DOUBLE,             -- Floating-point
    valid BOOLEAN,
    error VARCHAR,
    poll_ms INTEGER
);

-- Indexes
CREATE INDEX idx_samples_lookup 
    ON samples(namespace, target, poller, timestamp_ms DESC);
CREATE INDEX idx_samples_timestamp 
    ON samples(timestamp_ms);       -- For cleanup

-- Tree Nodes (virtual filesystem)
CREATE TABLE tree_nodes (
    namespace VARCHAR NOT NULL,
    path VARCHAR NOT NULL,
    node_type VARCHAR NOT NULL,     -- "directory" | "link"
    link_ref VARCHAR,               -- "target:x" | "poller:x/y"
    description VARCHAR,
    created_at TIMESTAMP DEFAULT now(),
    PRIMARY KEY (namespace, path)
);

-- Secrets (encrypted credentials)
CREATE TABLE secrets (
    namespace VARCHAR NOT NULL,
    name VARCHAR NOT NULL,
    secret_type VARCHAR NOT NULL,   -- "snmpv3_auth", "snmpv3_priv"
    encrypted_value BLOB NOT NULL,
    nonce BLOB NOT NULL,            -- AES-GCM nonce
    created_at TIMESTAMP DEFAULT now(),
    updated_at TIMESTAMP DEFAULT now(),
    PRIMARY KEY (namespace, name)
);

-- Server Configuration (singleton)
CREATE TABLE server_config (
    id INTEGER PRIMARY KEY DEFAULT 1,
    default_timeout_ms INTEGER DEFAULT 5000,
    default_retries INTEGER DEFAULT 2,
    default_interval_ms INTEGER DEFAULT 1000,
    default_buffer_size INTEGER DEFAULT 3600,
    CHECK (id = 1)
);
```

### 7.2 Query Patterns

**Hot Path (per poll):**
```sql
-- Config resolution (cached)
SELECT * FROM server_config WHERE id = 1;
SELECT * FROM namespaces WHERE name = ?;
SELECT * FROM targets WHERE namespace = ? AND name = ?;
SELECT * FROM pollers WHERE namespace = ? AND target = ? AND name = ?;
```

**Batch Writes:**
```sql
-- Sample insertion (batch of 1000)
INSERT INTO samples (namespace, target, poller, timestamp_ms, ...)
VALUES (?, ?, ?, ?, ...), (?, ?, ?, ?, ...), ...;

-- State update (batch)
UPDATE poller_state 
SET oper_state = ?, health_state = ?, last_error = ?, ...
WHERE namespace = ? AND target = ? AND poller = ?;

-- Stats upsert
INSERT INTO poller_stats (...) VALUES (...)
ON CONFLICT (namespace, target, poller) 
DO UPDATE SET polls_total = excluded.polls_total, ...;
```

**Cleanup:**
```sql
-- Delete old samples (daily job)
DELETE FROM samples WHERE timestamp_ms < ?;
```

### 7.3 Store Implementation

```go
// internal/store/store.go

type Store struct {
    db        *sql.DB
    dbPath    string
    secretKey []byte  // 32-byte AES-256 key
}

func (s *Store) Transaction(fn func(tx *sql.Tx) error) error {
    tx, err := s.db.Begin()
    if err != nil {
        return err
    }
    
    if err := fn(tx); err != nil {
        tx.Rollback()
        return err
    }
    
    return tx.Commit()
}
```

---

## 8. Scheduling System

### 8.1 Heap-Based Scheduler

```go
// internal/scheduler/scheduler.go

type PollItem struct {
    Key        PollerKey
    NextPollMs int64     // Unix ms when next poll is due
    IntervalMs int64     // Poll interval
    Polling    bool      // Currently being polled
    index      int       // Heap index for O(log n) updates
}

type PollHeap []*PollItem

// Heap interface
func (h PollHeap) Less(i, j int) bool {
    return h[i].NextPollMs < h[j].NextPollMs
}
```

### 8.2 Scheduler Algorithm

```
Every iteration:
1. Lock mutex
2. Peek heap for earliest due item
3. If no items OR next_poll_time > now:
   - Unlock, sleep until next_poll_time (or wakeup signal)
4. Else:
   - Collect all items where next_poll_time <= now
   - Mark as "polling"
   - Unlock mutex
   - Queue jobs to workers (with backpressure handling)
5. Repeat

Worker:
1. Receive job from queue
2. Execute SNMP poll
3. Call MarkComplete(key) → re-heaps with new next_poll_time
4. Send result to results channel
```

### 8.3 Jitter and Load Distribution

```go
func (s *Scheduler) Add(key PollerKey, intervalMs uint32) {
    interval := int64(intervalMs)
    jitter := rand.Int63n(interval)  // Random 0 to interval
    
    item := &PollItem{
        Key:        key,
        NextPollMs: time.Now().UnixMilli() + jitter,  // Spread initial polls
        IntervalMs: interval,
    }
    
    heap.Push(&s.heap, item)
}
```

**Without jitter:** 10,000 pollers added → all poll at t=0
**With jitter:** 10,000 pollers spread over interval window

### 8.4 Backpressure Handling

```go
func (s *Scheduler) processDueItems() {
    // Collect due items under lock
    s.mu.Lock()
    var dueItems []*PollItem
    for s.heap.Len() > 0 && s.heap.Peek().NextPollMs <= now {
        item := heap.Pop(&s.heap).(*PollItem)
        item.Polling = true
        dueItems = append(dueItems, item)
    }
    s.mu.Unlock()
    
    // Queue without holding lock
    for _, item := range dueItems {
        select {
        case s.jobs <- PollJob{Key: item.Key}:
            // Success
        default:
            // Queue full - reschedule with 10ms delay
            s.reschedule(item, now+10)
        }
    }
}
```

### 8.5 SNMP Poller

```go
// internal/scheduler/snmp.go

type SNMPPoller struct {
    defaultTimeoutMs uint32
    defaultRetries   uint32
}

func (p *SNMPPoller) Poll(key PollerKey, cfg *SNMPConfig) PollResult {
    start := time.Now()
    
    // Configure gosnmp client
    snmp := &gosnmp.GoSNMP{
        Target:  cfg.Host,
        Port:    cfg.Port,
        Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond,
        Retries: int(cfg.Retries),
    }
    
    // Set version-specific config
    if cfg.Version == 3 {
        snmp.Version = gosnmp.Version3
        snmp.SecurityModel = gosnmp.UserSecurityModel
        snmp.SecurityParameters = &gosnmp.UsmSecurityParameters{
            UserName:                 cfg.SecurityName,
            AuthenticationProtocol:   mapAuthProtocol(cfg.AuthProtocol),
            AuthenticationPassphrase: cfg.AuthPassword,
            PrivacyProtocol:          mapPrivProtocol(cfg.PrivProtocol),
            PrivacyPassphrase:        cfg.PrivPassword,
        }
    } else {
        snmp.Version = gosnmp.Version2c
        snmp.Community = cfg.Community
    }
    
    // Execute poll
    if err := snmp.Connect(); err != nil {
        return PollResult{Success: false, Error: err.Error()}
    }
    defer snmp.Conn.Close()
    
    pdu, err := snmp.Get([]string{cfg.OID})
    if err != nil {
        return PollResult{Success: false, Error: err.Error()}
    }
    
    // Parse value
    return parseValue(pdu.Variables[0], time.Since(start))
}
```

---

## 9. State Management

### 9.1 State Manager

```go
// internal/manager/state.go

type StateManager struct {
    mu     sync.RWMutex
    states map[string]*PollerState  // key: namespace/target/poller
}

func (m *StateManager) Get(namespace, target, poller string) *PollerState {
    key := namespace + "/" + target + "/" + poller
    
    m.mu.RLock()
    state, ok := m.states[key]
    m.mu.RUnlock()
    
    if ok {
        return state
    }
    
    // Create new state
    m.mu.Lock()
    defer m.mu.Unlock()
    
    if state, ok = m.states[key]; ok {
        return state  // Double-check
    }
    
    state = NewPollerState(namespace, target, poller)
    m.states[key] = state
    return state
}
```

### 9.2 State Transitions

```go
type PollerState struct {
    mu sync.RWMutex
    
    AdminState  string  // "enabled" | "disabled"
    OperState   string  // "stopped" | "starting" | "running" | "stopping"
    HealthState string  // "unknown" | "up" | "down" | "degraded"
    
    ConsecutiveFailures int
    dirty               atomic.Bool
}

// Enable/Disable (admin operations)
func (s *PollerState) Enable() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.AdminState == AdminStateEnabled {
        return nil
    }
    
    s.AdminState = AdminStateEnabled
    s.dirty.Store(true)
    return nil
}

// Start/Stop (operational transitions)
func (s *PollerState) Start() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.AdminState != AdminStateEnabled {
        return ErrPollerDisabled
    }
    if s.OperState == OperStateRunning {
        return ErrPollerRunning
    }
    
    s.OperState = OperStateRunning
    s.dirty.Store(true)
    return nil
}

// Record poll result (called after each poll)
func (s *PollerState) RecordPollResult(success bool, errMsg string, pollMs int) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    now := time.Now()
    s.LastPollAt = &now
    
    if success {
        s.LastSuccessAt = &now
        s.ConsecutiveFailures = 0
        s.HealthState = HealthStateUp
        s.LastError = ""
    } else {
        s.LastFailureAt = &now
        s.ConsecutiveFailures++
        s.LastError = errMsg
        
        if s.ConsecutiveFailures >= 3 {
            s.HealthState = HealthStateDown
        } else {
            s.HealthState = HealthStateDegraded
        }
    }
    
    s.dirty.Store(true)
}
```

### 9.3 Stats Manager

```go
// internal/manager/stats.go

type PollerStats struct {
    mu sync.RWMutex
    
    // Atomic counters for lock-free reads
    PollsTotal   atomic.Int64
    PollsSuccess atomic.Int64
    PollsFailed  atomic.Int64
    PollsTimeout atomic.Int64
    
    // Timing (requires lock)
    pollMsSum   int64
    pollMsMin   int
    pollMsMax   int
    pollMsCount int
    
    dirty atomic.Bool
}

func (s *PollerStats) RecordPoll(success bool, timeout bool, pollMs int) {
    s.PollsTotal.Add(1)
    
    if success {
        s.PollsSuccess.Add(1)
    } else {
        s.PollsFailed.Add(1)
        if timeout {
            s.PollsTimeout.Add(1)
        }
    }
    
    // Update timing under lock
    s.mu.Lock()
    s.pollMsSum += int64(pollMs)
    s.pollMsCount++
    if s.pollMsMin < 0 || pollMs < s.pollMsMin {
        s.pollMsMin = pollMs
    }
    if pollMs > s.pollMsMax {
        s.pollMsMax = pollMs
    }
    s.mu.Unlock()
    
    s.dirty.Store(true)
}
```

### 9.4 Dirty Tracking

Both StateManager and StatsManager use dirty flags for efficient persistence:

```go
// Get all dirty states for batch update
func (m *StateManager) GetDirty() []*PollerState {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    var dirty []*PollerState
    for _, state := range m.states {
        if state.IsDirty() {
            dirty = append(dirty, state)
        }
    }
    return dirty
}

// After successful persistence
func (s *PollerState) ClearDirty() {
    s.dirty.Store(false)
}
```

---

## 10. Security

### 10.1 Authentication

**Token-Based Authentication:**

```yaml
# config.yaml
auth:
  tokens:
    - id: admin
      token: "${SNMPPROXY_TOKEN}"
    - id: readonly
      token: "${SNMPPROXY_READONLY_TOKEN}"
```

**Authentication Flow:**
```
Client                              Server
   │                                   │
   │  Connect (TLS handshake)          │
   │◄─────────────────────────────────►│
   │                                   │
   │  AuthRequest{token: "xxx"}        │
   │──────────────────────────────────►│
   │                                   │  Constant-time compare
   │                                   │  (timing attack protection)
   │  AuthResponse{ok: true}           │
   │◄──────────────────────────────────│
   │                                   │
   │  Authenticated session begins     │
```

**Implementation:**
```go
func (sm *SessionManager) ValidateToken(token string) (*TokenConfig, bool) {
    for _, t := range sm.tokens {
        // Constant-time comparison prevents timing attacks
        if subtle.ConstantTimeCompare([]byte(t.Token), []byte(token)) == 1 {
            return t, true
        }
    }
    return nil, false
}
```

### 10.2 TLS Configuration

```yaml
tls:
  cert_file: "certs/server.crt"
  key_file: "certs/server.key"
```

**Server Setup:**
```go
tlsCfg := &tls.Config{
    Certificates: []tls.Certificate{cert},
    MinVersion:   tls.VersionTLS12,  // No TLS 1.0/1.1
}
ln, err = tls.Listen("tcp", addr, tlsCfg)
```

**Certificate Generation:**
```bash
# Self-signed for development
make gen-certs

# Or manually:
openssl req -x509 -newkey rsa:4096 \
    -keyout server.key -out server.crt \
    -days 365 -nodes -subj "/CN=snmpproxy"
```

### 10.3 Secret Encryption

SNMPv3 passwords and other sensitive data are encrypted at rest.

**Key Generation:**
```bash
openssl rand -out secret.key 32
```

**Encryption Algorithm:** AES-256-GCM

```go
// internal/store/secret.go

func (s *Store) encryptSecret(plaintext []byte) (ciphertext, nonce []byte, err error) {
    block, _ := aes.NewCipher(s.secretKey)
    gcm, _ := cipher.NewGCM(block)
    
    nonce = make([]byte, gcm.NonceSize())
    io.ReadFull(rand.Reader, nonce)
    
    ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
    return
}

func (s *Store) decryptSecret(ciphertext, nonce []byte) ([]byte, error) {
    block, _ := aes.NewCipher(s.secretKey)
    gcm, _ := cipher.NewGCM(block)
    return gcm.Open(nil, nonce, ciphertext, nil)
}
```

**Secret Reference in Config:**
```yaml
# Namespace config with secret reference
namespace:
  config:
    defaults:
      snmpv3:
        auth_secret_ref: "secret:router-auth"  # References encrypted secret
```

### 10.4 Namespace Isolation

Namespaces provide multi-tenant isolation:

```go
type TokenConfig struct {
    ID         string
    Token      string
    Namespaces []string  // Allowed namespaces (empty = all)
}

func (sm *SessionManager) CanAccessNamespace(tokenID, namespace string) bool {
    t := sm.tokens[tokenID]
    
    // Empty list = unrestricted access
    if len(t.Namespaces) == 0 {
        return true
    }
    
    for _, ns := range t.Namespaces {
        if ns == namespace {
            return true
        }
    }
    return false
}
```

---

## 11. Performance Characteristics

### 11.1 Capacity Estimates

| Component | Capacity | Limiting Factor |
|-----------|----------|-----------------|
| Pollers | ~100,000 | RAM (caches) |
| Samples/sec | ~50,000 | DuckDB write throughput |
| Sessions | ~10,000 | File descriptors, goroutines |
| Subscriptions | ~100,000 | Reverse index memory |

### 11.2 Memory Usage

```
Per Poller:
  Poller struct:        ~500 bytes
  State struct:         ~200 bytes
  Stats struct:         ~100 bytes
  Config cache:         ~300 bytes
  Index entries:        ~100 bytes
  ─────────────────────────────────
  Total:                ~1.2 KB

Per Sample (in buffer):
  Sample struct:        ~100 bytes

Memory Formula:
  Base:                 ~50 MB
  + (pollers × 1.2 KB)
  + (batch_size × 100 bytes × namespaces)
  
Example (10,000 pollers, batch=1000, 5 namespaces):
  50 MB + 12 MB + 0.5 MB = ~63 MB
```

### 11.3 CPU Usage

**Hot Path Analysis:**
```
Per poll cycle:
  1. Heap operations:       O(log n)  ~1 µs
  2. Config resolution:     O(1)      ~0.1 µs (cached)
  3. SNMP poll:             O(1)      ~5-50 ms (network)
  4. State/Stats update:    O(1)      ~0.5 µs
  5. Sample buffer:         O(1)      ~0.1 µs
  6. Broadcast lookup:      O(m)      ~0.5 µs per subscriber
```

**CPU Breakdown (10,000 polls/sec):**
```
SNMP network I/O:    ~80% (waiting)
State updates:       ~5%
DB writes:           ~10%
Heap operations:     ~3%
Other:               ~2%
```

### 11.4 Optimizations Implemented

| Optimization | Impact | Description |
|--------------|--------|-------------|
| Config cache | 75% fewer DB reads | 30s TTL cache for resolved configs |
| Secondary indexes | O(n) → O(m) | PollerManager.byNamespace, byTarget |
| Subscription index | O(n) → O(1) | SessionManager.subscriptionIndex |
| Batch writes | 95% fewer writes | SyncManager batching |
| Lock reduction | Less contention | Phase-based lock in scheduler |
| String concat | 10% faster keys | `a+"/"+b` vs `fmt.Sprintf` |

### 11.5 Tuning Guidelines

**High Throughput (>50k pollers):**
```yaml
poller:
  workers: 200
  queue_size: 50000

storage:
  sample_batch_size: 5000
  sample_flush_timeout_sec: 10
```

**Low Latency (<100ms sample delivery):**
```yaml
storage:
  sample_batch_size: 100
  sample_flush_timeout_sec: 1
```

**Memory Constrained (<512 MB):**
```yaml
poller:
  workers: 50
  queue_size: 5000

storage:
  sample_batch_size: 500

snmp:
  buffer_size: 1000  # Fewer samples per poller
```

---

## 12. Operations Guide

### 12.1 Deployment

**Single Binary Deployment:**
```bash
# Build
make all

# Directory structure
/opt/snmpproxy/
├── bin/
│   ├── snmpproxyd
│   └── snmpctl
├── config.yaml
├── certs/
│   ├── server.crt
│   └── server.key
├── data/
│   └── snmpproxy.db
└── secret.key
```

**Systemd Service:**
```ini
# /etc/systemd/system/snmpproxy.service
[Unit]
Description=SNMP Proxy Server
After=network.target

[Service]
Type=simple
User=snmpproxy
WorkingDirectory=/opt/snmpproxy
ExecStart=/opt/snmpproxy/bin/snmpproxyd -config /opt/snmpproxy/config.yaml
Restart=on-failure
RestartSec=5

# Resource limits
LimitNOFILE=65536
MemoryMax=2G

[Install]
WantedBy=multi-user.target
```

### 12.2 Monitoring

**Key Metrics to Monitor:**

| Metric | Warning | Critical | Action |
|--------|---------|----------|--------|
| Heap alloc | >1 GB | >2 GB | Reduce pollers or batch size |
| Poll queue usage | >70% | >90% | Increase workers |
| DB write latency | >50ms | >200ms | Check disk I/O |
| Poll success rate | <95% | <80% | Check network/targets |
| Session count | >8000 | >9500 | Check for leaks |

**Status Command:**
```bash
snmpctl status

=== Server Status ===
Version:       v1.0.0
Uptime:        2d 5h 32m
Started:       2024-01-15 10:00:00

Sessions:
  Active:      42
  Lost:        3

Targets:
  Total:       1500
  Polling:     1480
  Unreachable: 20

Poller:
  Workers:     100
  Queue:       234 / 10000
  Heap Size:   1500

Statistics:
  Total Polls: 45,234,567
  Success:     44,987,654
  Failed:      246,913
  Success Rate: 99.5%
```

### 12.3 Backup and Recovery

**Database Backup:**
```bash
# Stop server or use online backup
sqlite3 /opt/snmpproxy/data/snmpproxy.db ".backup /backup/snmpproxy-$(date +%Y%m%d).db"

# Or with DuckDB CLI
duckdb /opt/snmpproxy/data/snmpproxy.db -c "EXPORT DATABASE '/backup/export';"
```

**Recovery:**
```bash
# Stop server
systemctl stop snmpproxy

# Restore database
cp /backup/snmpproxy-20240115.db /opt/snmpproxy/data/snmpproxy.db

# Start server
systemctl start snmpproxy
```

### 12.4 Troubleshooting

**Common Issues:**

| Symptom | Possible Cause | Solution |
|---------|---------------|----------|
| High CPU | Too many pollers | Reduce poll frequency |
| Memory growth | Sample buffer | Reduce batch_size |
| Poll timeouts | Network issues | Check routing, increase timeout |
| Queue full | Workers too slow | Increase workers or reduce pollers |
| Auth failures | Token mismatch | Check config, env vars |

**Debug Logging:**
```bash
# Increase verbosity
snmpproxyd -config config.yaml -v

# Check specific poller
snmpctl info ns/target/poller
```

### 12.5 Graceful Operations

**Graceful Shutdown:**
```bash
# Send SIGTERM
kill -TERM $(pidof snmpproxyd)

# Or via systemctl
systemctl stop snmpproxy
```

Shutdown sequence:
1. Stop accepting connections
2. Stop scheduler (no new polls)
3. Wait for in-flight polls (timeout: 30s)
4. Flush all buffers to DB
5. Close database
6. Exit

**Configuration Reload:**
Currently requires restart. Future: SIGHUP support.

---

## Appendix A: Protocol Buffer Definitions

See `proto/snmpproxy.proto` for complete definitions.

## Appendix B: Error Codes

See `internal/errors/errors.go` for complete error taxonomy.

## Appendix C: CLI Reference

```
snmpctl - SNMP Proxy CLI Client

Usage:
  snmpctl [flags] [command]

Flags:
  -server string      Server address (default "localhost:9161")
  -token string       Auth token (or SNMPPROXY_TOKEN env)
  -tls-skip-verify    Skip TLS certificate verification
  -no-tls             Disable TLS

Commands:
  Namespace:
    ns list                     List namespaces
    ns create <name>            Create namespace
    ns delete <name>            Delete namespace
    ns bind <name>              Bind to namespace

  Target:
    target list                 List targets
    target create <name> ...    Create target
    target delete <name>        Delete target
    target show <name>          Show target details

  Poller:
    poller list [target]        List pollers
    poller create ...           Create poller
    poller enable <id>          Enable poller
    poller disable <id>         Disable poller
    poller delete <id>          Delete poller
    poller show <id>            Show poller details

  Subscription:
    subscribe <target/poller>   Subscribe to samples
    unsubscribe [id...]         Unsubscribe

  Tree:
    ls [path]                   List tree contents
    mkdir <path>                Create directory
    ln <path> <ref>             Create link
    rm <path>                   Remove node

  Status:
    status                      Server status
    session                     Current session info
    config                      Runtime configuration

  Help:
    help                        Show this help
    quit                        Exit
```

---
