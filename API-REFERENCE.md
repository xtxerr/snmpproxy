# snmpproxy API Reference

## Overview

This document provides a comprehensive API reference for developers integrating with or extending snmpproxy.

---

## 1. Wire Protocol API

### 1.1 Connection Lifecycle

```
1. TCP Connect (with optional TLS)
2. Send AuthRequest
3. Receive AuthResponse
4. (Optional) Send BindNamespaceRequest
5. Send requests / Receive responses
6. Receive push messages (samples)
7. Disconnect
```

### 1.2 Authentication

**Request:**
```protobuf
message AuthRequest {
    string token = 1;  // Pre-shared token from config
}
```

**Response:**
```protobuf
message AuthResponse {
    bool ok = 1;
    string session_id = 2;  // Use for debugging/logging
    string message = 3;     // Error message if !ok
}
```

### 1.3 Namespace Operations

#### Bind Namespace
Binds session to a namespace. Required before most operations.

```protobuf
message BindNamespaceRequest {
    string namespace = 1;
}

message BindNamespaceResponse {
    bool ok = 1;
    string namespace = 2;
}
```

#### Create Namespace
```protobuf
message CreateNamespaceRequest {
    string name = 1;
    string description = 2;
    NamespaceConfig config = 3;
}

message CreateNamespaceResponse {
    bool ok = 1;
    Namespace namespace = 2;
}
```

#### List Namespaces
```protobuf
message ListNamespacesRequest {}

message ListNamespacesResponse {
    repeated Namespace namespaces = 1;
}
```

### 1.4 Target Operations

#### Create Target
```protobuf
message CreateTargetRequest {
    string name = 1;
    string description = 2;
    map<string, string> labels = 3;
    TargetConfig config = 4;
}

message CreateTargetResponse {
    bool ok = 1;
    Target target = 2;
}
```

#### List Targets
```protobuf
message ListTargetsRequest {
    string label_selector = 1;  // Optional: "env=prod,region=us"
}

message ListTargetsResponse {
    repeated Target targets = 1;
}
```

### 1.5 Poller Operations

#### Create Poller
```protobuf
message CreatePollerRequest {
    string target = 1;
    string name = 2;
    string description = 3;
    string protocol = 4;  // "snmp"
    bytes protocol_config = 5;  // JSON
    PollingConfig polling_config = 6;
}

message PollingConfig {
    uint32 interval_ms = 1;
    uint32 timeout_ms = 2;
    uint32 retries = 3;
    uint32 buffer_size = 4;
}
```

#### Enable/Disable Poller
```protobuf
message EnablePollerRequest {
    string target = 1;
    string poller = 2;
}

message EnablePollerResponse {
    bool ok = 1;
    PollerState state = 2;
}
```

### 1.6 Subscription API

#### Subscribe
```protobuf
message SubscribeRequest {
    repeated SubscriptionSpec subscriptions = 1;
}

message SubscriptionSpec {
    string target = 1;
    string poller = 2;  // Empty = all pollers for target
}

message SubscribeResponse {
    repeated string subscribed = 1;  // Keys that were subscribed
    repeated string invalid = 2;     // Keys that failed
}
```

#### Receive Samples (Push)
```protobuf
message Sample {
    string target = 1;
    string poller = 2;
    int64 timestamp_ms = 3;
    uint64 counter = 4;      // For numeric values
    string text = 5;         // For string values
    double gauge = 6;        // For floating-point
    bool valid = 7;
    string error = 8;
    int32 poll_ms = 9;
}
```

### 1.7 History/Samples Query

```protobuf
message GetSamplesRequest {
    string target = 1;
    string poller = 2;
    int32 limit = 3;          // Max samples to return
    int64 since_ms = 4;       // Start timestamp
    int64 until_ms = 5;       // End timestamp
}

message GetSamplesResponse {
    repeated Sample samples = 1;
}
```

---

## 2. Go Client Library

### 2.1 Basic Usage

```go
import "github.com/xtxerr/snmpproxy/internal/client"

// Create client
cli := client.New(&client.Config{
    Addr:          "localhost:9161",
    Token:         os.Getenv("SNMPPROXY_TOKEN"),
    TLS:           true,
    TLSSkipVerify: false,
})

// Connect and authenticate
if err := cli.Connect(); err != nil {
    log.Fatal(err)
}
defer cli.Close()

// Bind to namespace
cli.BindNamespace("production")

// Create target
target, err := cli.CreateTarget(&pb.CreateTargetRequest{
    Name: "router-1",
    Config: &pb.TargetConfig{
        Host: "192.168.1.1",
        Port: 161,
    },
})

// Create poller
poller, err := cli.CreatePoller(&pb.CreatePollerRequest{
    Target:   "router-1",
    Name:     "cpu",
    Protocol: "snmp",
    ProtocolConfig: []byte(`{
        "oid": "1.3.6.1.4.1.9.9.109.1.1.1.1.3.1",
        "community": "public"
    }`),
    PollingConfig: &pb.PollingConfig{
        IntervalMs: 10000,
    },
})

// Enable poller
cli.EnablePoller("router-1", "cpu")

// Subscribe to samples
cli.OnSample(func(s *pb.Sample) {
    fmt.Printf("[%s/%s] %d\n", s.Target, s.Poller, s.Counter)
})
cli.Subscribe([]string{"router-1/cpu"})

// Keep running
select {}
```

### 2.2 Client Methods

```go
type Client interface {
    // Connection
    Connect() error
    Close()
    SessionID() string
    
    // Namespace
    BindNamespace(namespace string) error
    CreateNamespace(req *CreateNamespaceRequest) (*Namespace, error)
    ListNamespaces() ([]*Namespace, error)
    DeleteNamespace(name string) error
    
    // Target
    CreateTarget(req *CreateTargetRequest) (*Target, error)
    GetTarget(name string) (*Target, error)
    ListTargets(labelSelector string) ([]*Target, error)
    UpdateTarget(req *UpdateTargetRequest) (*Target, error)
    DeleteTarget(name string) error
    
    // Poller
    CreatePoller(req *CreatePollerRequest) (*Poller, error)
    GetPoller(target, name string) (*Poller, error)
    ListPollers(target string) ([]*Poller, error)
    EnablePoller(target, name string) (*PollerState, error)
    DisablePoller(target, name string) (*PollerState, error)
    DeletePoller(target, name string) error
    
    // Subscription
    Subscribe(keys []string) ([]string, error)
    Unsubscribe(keys []string) error
    OnSample(handler func(*Sample))
    
    // Query
    GetSamples(req *GetSamplesRequest) ([]*Sample, error)
    
    // Status
    GetServerStatus() (*ServerStatus, error)
    GetSessionInfo() (*SessionInfo, error)
}
```

---

## 3. Internal Go APIs

### 3.1 Manager Layer

#### PollerManager

```go
// internal/manager/poller.go

type PollerManager struct {
    // ... internal fields
}

// CRUD Operations
func (m *PollerManager) Create(p *store.Poller) error
func (m *PollerManager) Get(namespace, target, name string) (*store.Poller, error)
func (m *PollerManager) Update(p *store.Poller) error
func (m *PollerManager) Delete(namespace, target, name string) (linksDeleted int, err error)

// List Operations (O(m) via secondary index)
func (m *PollerManager) List(namespace, target string) []*store.Poller
func (m *PollerManager) ListInNamespace(namespace string) []*store.Poller
func (m *PollerManager) GetEnabledPollers() []*store.Poller

// State Operations
func (m *PollerManager) Enable(namespace, target, name string) error
func (m *PollerManager) Disable(namespace, target, name string) error
func (m *PollerManager) Start(namespace, target, name string) error
func (m *PollerManager) Stop(namespace, target, name string) error

// Info
func (m *PollerManager) GetState(namespace, target, name string) *PollerState
func (m *PollerManager) GetStats(namespace, target, name string) *PollerStats
func (m *PollerManager) GetResolvedConfig(namespace, target, name string) (*ResolvedPollerConfig, error)
func (m *PollerManager) Exists(namespace, target, name string) bool
func (m *PollerManager) Count() int
func (m *PollerManager) CountInTarget(namespace, target string) int
```

#### ConfigResolver

```go
// internal/manager/config_resolver.go

type ConfigResolver struct {
    store    *store.Store
    cache    sync.Map
    cacheTTL time.Duration
}

// Resolve with caching (4 DB queries on cache miss)
func (r *ConfigResolver) Resolve(namespace, target, poller string) (*ResolvedPollerConfig, error)

// Resolve with pre-loaded entities (0 DB queries)
func (r *ConfigResolver) ResolveWithPoller(
    serverCfg *store.ServerConfig,
    ns *store.Namespace,
    t *store.Target,
    p *store.Poller,
) *ResolvedPollerConfig

// Cache invalidation
func (r *ConfigResolver) Invalidate(namespace, target, poller string)
func (r *ConfigResolver) InvalidateTarget(namespace, target string)
func (r *ConfigResolver) InvalidateNamespace(namespace string)
func (r *ConfigResolver) InvalidateAll()

// Secret resolution
func (r *ConfigResolver) ResolveSecrets(namespace string, config *ResolvedPollerConfig) error
```

#### StateManager

```go
// internal/manager/state.go

type StateManager struct {
    mu     sync.RWMutex
    states map[string]*PollerState
}

func (m *StateManager) Get(namespace, target, poller string) *PollerState
func (m *StateManager) Remove(namespace, target, poller string)
func (m *StateManager) GetDirty() []*PollerState
func (m *StateManager) CountByOperState() map[string]int
func (m *StateManager) CountByHealthState() map[string]int
```

#### StatsManager

```go
// internal/manager/stats.go

type StatsManager struct {
    mu    sync.RWMutex
    stats map[string]*PollerStats
}

func (m *StatsManager) Get(namespace, target, poller string) *PollerStats
func (m *StatsManager) Remove(namespace, target, poller string)
func (m *StatsManager) GetDirty() []*PollerStats
func (m *StatsManager) Aggregate() (total, success, failed, timeout int64)
```

### 3.2 Scheduler Layer

```go
// internal/scheduler/scheduler.go

type Scheduler struct {
    // ... internal fields
}

// Lifecycle
func New(cfg *Config) *Scheduler
func (s *Scheduler) Start()
func (s *Scheduler) Stop()

// Poller management
func (s *Scheduler) Add(key PollerKey, intervalMs uint32)
func (s *Scheduler) Remove(key PollerKey)
func (s *Scheduler) UpdateInterval(key PollerKey, intervalMs uint32)
func (s *Scheduler) Contains(key PollerKey) bool

// Results
func (s *Scheduler) Results() <-chan PollResult
func (s *Scheduler) SetPollFunc(fn func(PollerKey) PollResult)

// Stats
func (s *Scheduler) Stats() (heapSize, queueUsed int)
```

### 3.3 Store Layer

```go
// internal/store/store.go

type Store struct {
    db        *sql.DB
    secretKey []byte
}

// Lifecycle
func New(cfg *Config) (*Store, error)
func (s *Store) Close() error

// Namespace operations
func (s *Store) CreateNamespace(ns *Namespace) error
func (s *Store) GetNamespace(name string) (*Namespace, error)
func (s *Store) ListNamespaces() ([]*Namespace, error)
func (s *Store) UpdateNamespace(ns *Namespace) error
func (s *Store) DeleteNamespace(name string) error

// Target operations
func (s *Store) CreateTarget(t *Target) error
func (s *Store) GetTarget(namespace, name string) (*Target, error)
func (s *Store) ListTargets(namespace string) ([]*Target, error)
func (s *Store) UpdateTarget(t *Target) error
func (s *Store) DeleteTarget(namespace, name string) (pollersDeleted int, err error)

// Poller operations
func (s *Store) CreatePoller(p *Poller) error
func (s *Store) GetPoller(namespace, target, name string) (*Poller, error)
func (s *Store) ListPollers(namespace, target string) ([]*Poller, error)
func (s *Store) ListAllPollers() ([]*Poller, error)
func (s *Store) UpdatePoller(p *Poller) error
func (s *Store) DeletePoller(namespace, target, name string) (linksDeleted int, err error)

// State/Stats
func (s *Store) GetPollerState(namespace, target, name string) (*PollerStateRecord, error)
func (s *Store) UpdatePollerStates(states []*PollerStateRecord) error
func (s *Store) GetPollerStats(namespace, target, name string) (*PollerStatsRecord, error)
func (s *Store) UpsertPollerStats(stats []*PollerStatsRecord) error

// Samples
func (s *Store) InsertSamples(samples []*Sample) error
func (s *Store) GetSamples(namespace, target, poller string, limit int, since, until int64) ([]*Sample, error)
func (s *Store) DeleteOldSamples(beforeMs int64) (int64, error)

// Tree operations
func (s *Store) CreateTreeDirectory(namespace, path, description string) error
func (s *Store) CreateTreeLink(namespace, path, name, linkType, linkRef string) error
func (s *Store) GetTreeNode(namespace, path string) (*TreeNode, error)
func (s *Store) ListTreeChildren(namespace, parentPath string) ([]*TreeNode, error)
func (s *Store) DeleteTreeNode(namespace, path string, recursive, force bool) (int, error)

// Secrets
func (s *Store) CreateSecret(namespace, name, secretType string, value []byte) error
func (s *Store) GetSecretValue(namespace, name string) (string, error)
func (s *Store) DeleteSecret(namespace, name string) error
func (s *Store) HasSecretKey() bool

// Server config
func (s *Store) GetServerConfig() (*ServerConfig, error)
func (s *Store) UpdateServerConfig(cfg *ServerConfig) error

// Transactions
func (s *Store) Transaction(fn func(tx *sql.Tx) error) error
```

---

## 4. Data Structures

### 4.1 Protocol Config (SNMP)

```go
type SNMPConfig struct {
    // Connection
    Host string `json:"host"`
    Port uint16 `json:"port"`  // Default: 161
    OID  string `json:"oid"`
    
    // SNMPv2c
    Community string `json:"community,omitempty"`
    
    // SNMPv3
    Version       int    `json:"version,omitempty"`       // 2 or 3
    SecurityName  string `json:"security_name,omitempty"`
    SecurityLevel string `json:"security_level,omitempty"` // noAuthNoPriv|authNoPriv|authPriv
    AuthProtocol  string `json:"auth_protocol,omitempty"`  // MD5|SHA|SHA224|SHA256|SHA384|SHA512
    AuthPassword  string `json:"auth_password,omitempty"`
    PrivProtocol  string `json:"priv_protocol,omitempty"`  // DES|AES|AES192|AES256
    PrivPassword  string `json:"priv_password,omitempty"`
    ContextName   string `json:"context_name,omitempty"`
    
    // Timing (can override defaults)
    TimeoutMs uint32 `json:"timeout_ms,omitempty"`
    Retries   uint32 `json:"retries,omitempty"`
}
```

### 4.2 Resolved Config

```go
type ResolvedPollerConfig struct {
    // Source tracking for each field
    Community       string
    CommunitySource string  // "server" | "namespace:X" | "target:Y" | "explicit"
    
    SecurityName        string
    SecurityNameSource  string
    SecurityLevel       string
    SecurityLevelSource string
    AuthProtocol        string
    AuthProtocolSource  string
    AuthPassword        string
    AuthPasswordSource  string
    PrivProtocol        string
    PrivProtocolSource  string
    PrivPassword        string
    PrivPasswordSource  string
    
    IntervalMs       uint32
    IntervalMsSource string
    TimeoutMs        uint32
    TimeoutMsSource  string
    Retries          uint32
    RetriesSource    string
    BufferSize       uint32
    BufferSizeSource string
}
```

### 4.3 Poll Result

```go
type PollResult struct {
    Key         PollerKey
    TimestampMs int64
    
    // Values (at most one is set)
    Counter *uint64
    Text    *string
    Gauge   *float64
    
    // Status
    Success bool
    Timeout bool
    Error   string
    PollMs  int
}
```

---

## 5. Error Handling

### 5.1 Defined Errors

```go
// internal/errors/errors.go

// Not found
var ErrNotFound          = errors.New("not found")
var ErrNamespaceNotFound = errors.New("namespace not found")
var ErrTargetNotFound    = errors.New("target not found")
var ErrPollerNotFound    = errors.New("poller not found")

// Already exists
var ErrAlreadyExists          = errors.New("already exists")
var ErrNamespaceAlreadyExists = errors.New("namespace already exists")

// Validation
var ErrInvalidName     = errors.New("invalid name")
var ErrInvalidConfig   = errors.New("invalid configuration")
var ErrMissingField    = errors.New("missing required field")

// State
var ErrInvalidState      = errors.New("invalid state")
var ErrInvalidTransition = errors.New("invalid state transition")
var ErrPollerDisabled    = errors.New("poller is disabled")

// Auth
var ErrNotAuthenticated  = errors.New("not authenticated")
var ErrNotAuthorized     = errors.New("not authorized")
var ErrNamespaceRequired = errors.New("namespace binding required")

// Helper functions
func IsNotFound(err error) bool
func IsAlreadyExists(err error) bool
func IsValidation(err error) bool
func IsStateError(err error) bool
func IsAuthError(err error) bool

// Wrapping
func Wrap(err error, message string) error
func NewNotFound(entityType, identifier string) error
func NewValidation(field, reason string) error
```

### 5.2 Error Handling Patterns

```go
// Handler layer
func (h *PollerHandler) Create(ctx *RequestContext, req *CreatePollerRequest) (*CreatePollerResponse, error) {
    // Validation
    if req.Name == "" {
        return nil, errors.NewValidation("name", "required")
    }
    
    // Business logic
    err := h.mgr.Pollers.Create(poller)
    if errors.IsAlreadyExists(err) {
        return nil, err  // Client sees: "poller 'X' already exists"
    }
    if err != nil {
        return nil, errors.Wrap(err, "create poller")
    }
    
    return &CreatePollerResponse{Poller: poller}, nil
}

// Client handling
resp, err := cli.CreatePoller(req)
if errors.IsAlreadyExists(err) {
    // Handle duplicate
} else if errors.IsValidation(err) {
    // Handle bad input
} else if err != nil {
    // Handle other error
}
```

---

## 6. Extension Points

### 6.1 Adding New Protocol

```go
// 1. Define protocol config
type HTTPConfig struct {
    URL     string `json:"url"`
    Method  string `json:"method"`
    Headers map[string]string `json:"headers"`
    // ...
}

// 2. Implement poller
type HTTPPoller struct {
    client *http.Client
}

func (p *HTTPPoller) Poll(key PollerKey, cfg *HTTPConfig) PollResult {
    // Implementation
}

// 3. Register in server
func (s *Server) executePoll(key scheduler.PollerKey) scheduler.PollResult {
    switch p.Protocol {
    case "snmp":
        return s.snmpPoller.Poll(key, cfg)
    case "http":
        return s.httpPoller.Poll(key, cfg)
    default:
        return PollResult{Error: "unknown protocol"}
    }
}
```

### 6.2 Adding New Handler

```go
// 1. Create handler
type CustomHandler struct {
    *Handler
}

func NewCustomHandler(h *Handler) *CustomHandler {
    return &CustomHandler{Handler: h}
}

func (h *CustomHandler) DoSomething(ctx *RequestContext, req *Request) (*Response, error) {
    // Implementation
}

// 2. Register in dispatch
case *pb.Envelope_CustomRequest:
    return s.customHandler.DoSomething(ctx, p.CustomRequest)

// 3. Add protobuf messages
message CustomRequest { ... }
message CustomResponse { ... }
```

### 6.3 Adding Metrics Export

```go
// Prometheus example
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
    info := s.mgr.GetServerInfo()
    
    fmt.Fprintf(w, "snmpproxy_pollers_total %d\n", info.PollerCount)
    fmt.Fprintf(w, "snmpproxy_pollers_running %d\n", info.PollersRunning)
    fmt.Fprintf(w, "snmpproxy_polls_total %d\n", info.PollsTotal)
    fmt.Fprintf(w, "snmpproxy_polls_success %d\n", info.PollsSuccess)
    fmt.Fprintf(w, "snmpproxy_polls_failed %d\n", info.PollsFailed)
}
```

---

## 7. Testing

### 7.1 Unit Test Pattern

```go
func TestPollerManager_Create(t *testing.T) {
    // Setup
    store := newTestStore(t)
    defer store.Close()
    
    mgr := manager.NewPollerManager(store, ...)
    
    // Create
    p := &store.Poller{
        Namespace: "test",
        Target:    "router",
        Name:      "cpu",
        Protocol:  "snmp",
    }
    
    err := mgr.Create(p)
    require.NoError(t, err)
    
    // Verify
    got, err := mgr.Get("test", "router", "cpu")
    require.NoError(t, err)
    assert.Equal(t, "cpu", got.Name)
}
```

### 7.2 Integration Test Pattern

```go
func TestFullPollCycle(t *testing.T) {
    // Start test server
    srv := startTestServer(t)
    defer srv.Shutdown()
    
    // Connect client
    cli := connectTestClient(t, srv.Addr())
    defer cli.Close()
    
    // Create poller
    cli.BindNamespace("test")
    cli.CreateTarget(...)
    cli.CreatePoller(...)
    cli.EnablePoller(...)
    
    // Subscribe and wait for sample
    samples := make(chan *pb.Sample, 1)
    cli.OnSample(func(s *pb.Sample) {
        samples <- s
    })
    cli.Subscribe([]string{"router/cpu"})
    
    select {
    case s := <-samples:
        assert.True(t, s.Valid)
    case <-time.After(5 * time.Second):
        t.Fatal("timeout waiting for sample")
    }
}
```

---

*Document Version: 1.0*
*Last Updated: 2024-01-20*:   "router-1",
    Name:     "cpu-usage",
    Protocol: "snmp",
    ProtocolConfig: []byte(`{
        "oid": "1.3.6.1.4.1.9.9.109.1.1.1.1.3.1",
        "community": "public"
    }`),
    PollingConfig: &pb.PollingConfig{
        IntervalMs: 10000,
    },
})

// Enable poller
cli.EnablePoller("router-1", "cpu-usage")

// Subscribe to samples
cli.OnSample(func(s *pb.Sample) {
    fmt.Printf("[%s/%s] %d\n", s.Target, s.Poller, s.Counter)
})
cli.Subscribe([]string{"router-1/cpu-usage"})

// Keep receiving...
select {}
```

### 2.2 Client Interface

```go
type Client interface {
    // Connection
    Connect() error
    Close()
    SessionID() string
    
    // Namespace
    BindNamespace(namespace string) error
    CreateNamespace(req *pb.CreateNamespaceRequest) (*pb.Namespace, error)
    ListNamespaces() ([]*pb.Namespace, error)
    DeleteNamespace(name string) error
    
    // Target
    CreateTarget(req *pb.CreateTargetRequest) (*pb.Target, error)
    GetTarget(name string) (*pb.Target, error)
    ListTargets(labelSelector string) ([]*pb.Target, error)
    UpdateTarget(req *pb.UpdateTargetRequest) (*pb.Target, error)
    DeleteTarget(name string) error
    
    // Poller
    CreatePoller(req *pb.CreatePollerRequest) (*pb.Poller, error)
    GetPoller(target, name string) (*pb.Poller, error)
    ListPollers(target string) ([]*pb.Poller, error)
    EnablePoller(target, name string) error
    DisablePoller(target, name string) error
    DeletePoller(target, name string) error
    
    // Subscription
    Subscribe(keys []string) ([]string, error)
    Unsubscribe(keys []string) error
    OnSample(handler func(*pb.Sample))
    
    // Samples
    GetSamples(req *pb.GetSamplesRequest) ([]*pb.Sample, error)
    
    // Status
    GetServerStatus() (*pb.ServerStatus, error)
    GetSessionInfo() (*pb.SessionInfo, error)
}
```

### 2.3 Error Handling

```go
import "github.com/xtxerr/snmpproxy/internal/errors"

target, err := cli.GetTarget("router-1")
if err != nil {
    if errors.IsNotFound(err) {
        // Target doesn't exist
        log.Println("Target not found, creating...")
        target, err = cli.CreateTarget(...)
    } else if errors.IsAuthError(err) {
        // Authentication/authorization issue
        log.Fatal("Access denied:", err)
    } else {
        // Other error
        log.Fatal("Unexpected error:", err)
    }
}
```

---

## 3. Manager API (Internal)

### 3.1 Manager Interface

```go
// internal/manager/manager.go

type Manager struct {
    // Entity managers
    Namespaces     *NamespaceManager
    Targets        *TargetManager
    Pollers        *PollerManager
    Tree           *TreeManager
    
    // Runtime managers
    States         *StateManager
    Stats          *StatsManager
    Sync           *SyncManager
    ConfigResolver *ConfigResolver
}

// Core methods
func (m *Manager) Load() error
func (m *Manager) Start()
func (m *Manager) Stop()
func (m *Manager) Store() *store.Store

// Convenience methods
func (m *Manager) RecordPollResult(namespace, target, poller string, 
    success bool, timeout bool, errMsg string, pollMs int, sample *store.Sample)
func (m *Manager) GetPollerFullInfo(namespace, target, poller string) (*PollerFullInfo, error)
func (m *Manager) StartEnabledPollers() int
```

### 3.2 PollerManager API

```go
// internal/manager/poller.go

type PollerManager struct {
    // ... internal fields
}

// CRUD operations
func (m *PollerManager) Create(p *store.Poller) error
func (m *PollerManager) Get(namespace, target, name string) (*store.Poller, error)
func (m *PollerManager) Update(p *store.Poller) error
func (m *PollerManager) Delete(namespace, target, name string) (linksDeleted int, err error)

// List operations (uses secondary indexes)
func (m *PollerManager) List(namespace, target string) []*store.Poller
func (m *PollerManager) ListInNamespace(namespace string) []*store.Poller
func (m *PollerManager) GetEnabledPollers() []*store.Poller

// State operations
func (m *PollerManager) Enable(namespace, target, name string) error
func (m *PollerManager) Disable(namespace, target, name string) error
func (m *PollerManager) Start(namespace, target, name string) error
func (m *PollerManager) Stop(namespace, target, name string) error

// Accessors
func (m *PollerManager) GetState(namespace, target, name string) *PollerState
func (m *PollerManager) GetStats(namespace, target, name string) *PollerStats
func (m *PollerManager) GetResolvedConfig(namespace, target, name string) (*ResolvedPollerConfig, error)

// Utility
func (m *PollerManager) Exists(namespace, target, name string) bool
func (m *PollerManager) Count() int
func (m *PollerManager) CountInTarget(namespace, target string) int

// Scheduler callbacks
func (m *PollerManager) SetCallbacks(
    onCreate func(namespace, target, poller string, intervalMs uint32),
    onDelete func(namespace, target, poller string),
    onUpdate func(namespace, target, poller string, intervalMs uint32),
)
```

### 3.3 ConfigResolver API

```go
// internal/manager/config_resolver.go

type ConfigResolver struct {
    // ... internal fields
}

// Resolve with DB lookups (cached)
func (r *ConfigResolver) Resolve(namespace, target, poller string) (*ResolvedPollerConfig, error)

// Resolve with pre-loaded entities (no DB lookups)
func (r *ConfigResolver) ResolveWithPoller(
    serverCfg *store.ServerConfig,
    ns *store.Namespace,
    t *store.Target,
    p *store.Poller,
) *ResolvedPollerConfig

// Resolve secret references
func (r *ConfigResolver) ResolveSecrets(namespace string, config *ResolvedPollerConfig) error

// Cache invalidation
func (r *ConfigResolver) Invalidate(namespace, target, poller string)
func (r *ConfigResolver) InvalidateTarget(namespace, target string)
func (r *ConfigResolver) InvalidateNamespace(namespace string)
func (r *ConfigResolver) InvalidateAll()
```

### 3.4 StateManager API

```go
// internal/manager/state.go

type StateManager struct {
    // ... internal fields
}

// Get or create state (thread-safe)
func (m *StateManager) Get(namespace, target, poller string) *PollerState

// Removal
func (m *StateManager) Remove(namespace, target, poller string)

// Aggregation
func (m *StateManager) CountByOperState() map[string]int
func (m *StateManager) CountByHealthState() map[string]int

// Persistence helpers
func (m *StateManager) GetDirty() []*PollerState
```

### 3.5 PollerState API

```go
// internal/manager/state.go

type PollerState struct {
    // ... internal fields
}

// State queries
func (s *PollerState) GetState() (admin, oper, health string)
func (s *PollerState) CanRun() bool
func (s *PollerState) IsRunning() bool
func (s *PollerState) GetLastError() string

// State transitions
func (s *PollerState) Enable() error
func (s *PollerState) Disable() error
func (s *PollerState) Start() error
func (s *PollerState) Stop() error
func (s *PollerState) MarkRunning()

// Poll result handling
func (s *PollerState) RecordPollResult(success bool, errMsg string, pollMs int)

// Dirty tracking
func (s *PollerState) IsDirty() bool
func (s *PollerState) ClearDirty()
```

---

## 4. Scheduler API

### 4.1 Scheduler Interface

```go
// internal/scheduler/scheduler.go

type Scheduler struct {
    // ... internal fields
}

// Lifecycle
func New(cfg *Config) *Scheduler
func (s *Scheduler) Start()
func (s *Scheduler) Stop()

// Poller management
func (s *Scheduler) Add(key PollerKey, intervalMs uint32)
func (s *Scheduler) Remove(key PollerKey)
func (s *Scheduler) UpdateInterval(key PollerKey, intervalMs uint32)
func (s *Scheduler) Contains(key PollerKey) bool

// Results channel
func (s *Scheduler) Results() <-chan PollResult

// Poll function injection
func (s *Scheduler) SetPollFunc(fn func(PollerKey) PollResult)

// Statistics
func (s *Scheduler) Stats() (heapSize, queueUsed int)
```

### 4.2 PollerKey

```go
type PollerKey struct {
    Namespace string
    Target    string
    Poller    string
}

func (k PollerKey) String() string {
    return k.Namespace + "/" + k.Target + "/" + k.Poller
}

func ParsePollerKey(s string) (PollerKey, error)
```

### 4.3 PollResult

```go
type PollResult struct {
    Key         PollerKey
    TimestampMs int64
    Success     bool
    Timeout     bool
    Error       string
    PollMs      int
    
    // Value (one of these is set)
    Counter     *uint64
    Text        *string
    Gauge       *float64
}
```

### 4.4 SNMPPoller API

```go
// internal/scheduler/snmp.go

type SNMPPoller struct {
    defaultTimeoutMs uint32
    defaultRetries   uint32
}

func NewSNMPPoller(defaultTimeoutMs, defaultRetries uint32) *SNMPPoller

func (p *SNMPPoller) Poll(key PollerKey, cfg *SNMPConfig) PollResult
```

---

## 5. Store API

### 5.1 Store Interface

```go
// internal/store/store.go

type Store struct {
    // ... internal fields
}

// Lifecycle
func New(cfg *Config) (*Store, error)
func (s *Store) Close() error

// Transaction support
func (s *Store) Transaction(fn func(tx *sql.Tx) error) error

// Server config
func (s *Store) GetServerConfig() (*ServerConfig, error)
func (s *Store) UpdateServerConfig(cfg *ServerConfig) error

// Namespace CRUD
func (s *Store) CreateNamespace(ns *Namespace) error
func (s *Store) GetNamespace(name string) (*Namespace, error)
func (s *Store) UpdateNamespace(ns *Namespace) error
func (s *Store) DeleteNamespace(name string) error
func (s *Store) ListNamespaces() ([]*Namespace, error)

// Target CRUD
func (s *Store) CreateTarget(t *Target) error
func (s *Store) GetTarget(namespace, name string) (*Target, error)
func (s *Store) UpdateTarget(t *Target) error
func (s *Store) DeleteTarget(namespace, name string) error
func (s *Store) ListTargets(namespace string) ([]*Target, error)

// Poller CRUD
func (s *Store) CreatePoller(p *Poller) error
func (s *Store) GetPoller(namespace, target, name string) (*Poller, error)
func (s *Store) UpdatePoller(p *Poller) error
func (s *Store) DeletePoller(namespace, target, name string) (linksDeleted int, err error)
func (s *Store) ListPollers(namespace, target string) ([]*Poller, error)
func (s *Store) ListAllPollers() ([]*Poller, error)

// Poller state/stats
func (s *Store) GetPollerState(namespace, target, poller string) (*PollerStateRecord, error)
func (s *Store) UpdatePollerState(state *PollerStateRecord) error
func (s *Store) BatchUpdatePollerState(states []*PollerStateRecord) error

func (s *Store) GetPollerStats(namespace, target, poller string) (*PollerStatsRecord, error)
func (s *Store) UpsertPollerStats(stats *PollerStatsRecord) error
func (s *Store) BatchUpsertPollerStats(stats []*PollerStatsRecord) error

// Samples
func (s *Store) InsertSamples(samples []*Sample) error
func (s *Store) GetSamples(namespace, target, poller string, limit int, since, until int64) ([]*Sample, error)
func (s *Store) DeleteOldSamples(beforeMs int64) (int64, error)

// Tree operations
func (s *Store) CreateTreeDirectory(namespace, path, description string) error
func (s *Store) CreateTreeLink(namespace, path, name, linkType, linkRef string) error
func (s *Store) GetTreeNode(namespace, path string) (*TreeNode, error)
func (s *Store) ListTreeChildren(namespace, parentPath string) ([]*TreeNode, error)
func (s *Store) DeleteTreeNode(namespace, path string, recursive, force bool) (int, error)

// Secrets
func (s *Store) CreateSecret(namespace, name, secretType string, value []byte) error
func (s *Store) GetSecretValue(namespace, name string) (string, error)
func (s *Store) DeleteSecret(namespace, name string) error
func (s *Store) HasSecretKey() bool
```

---

## 6. Handler API

### 6.1 Handler Structure

```go
// internal/handler/handler.go

type Handler struct {
    mgr            *manager.Manager
    sessionManager *SessionManager
}

func NewHandler(mgr *manager.Manager, sessions *SessionManager) *Handler

// Specialized handlers
func NewNamespaceHandler(h *Handler) *NamespaceHandler
func NewTargetHandler(h *Handler) *TargetHandler
func NewPollerHandler(h *Handler) *PollerHandler
func NewBrowseHandler(h *Handler) *BrowseHandler
func NewSubscriptionHandler(h *Handler) *SubscriptionHandler
func NewStatusHandler(h *Handler) *StatusHandler
func NewSecretHandler(h *Handler) *SecretHandler
```

### 6.2 RequestContext

```go
type RequestContext struct {
    Session   *Session
    RequestID uint64
}

func (c *RequestContext) MustNamespace() string
func (c *RequestContext) HasNamespace() bool

// Helper
func RequireNamespace(ctx *RequestContext) error
```

### 6.3 Session API

```go
// internal/handler/session.go

type Session struct {
    ID        string
    TokenID   string
    Namespace string
    Conn      net.Conn
    Wire      *wire.Conn
    CreatedAt time.Time
    LostAt    *time.Time
}

// Namespace binding
func (s *Session) BindNamespace(namespace string) error
func (s *Session) GetNamespace() string
func (s *Session) IsBound() bool

// Subscriptions
func (s *Session) Subscribe(key string)
func (s *Session) Unsubscribe(key string)
func (s *Session) UnsubscribeAll() []string
func (s *Session) IsSubscribed(key string) bool
func (s *Session) GetSubscriptions() []string

// Send (async)
func (s *Session) Send(data []byte) bool
func (s *Session) SendChan() <-chan []byte

// State
func (s *Session) MarkLost()
func (s *Session) IsLost() bool
func (s *Session) LostDuration() time.Duration
```

### 6.4 SessionManager API

```go
type SessionManager struct {
    // ... internal fields
}

// Session lifecycle
func (sm *SessionManager) CreateSession(tokenID string, conn net.Conn, w *wire.Conn) *Session
func (sm *SessionManager) TryRestore(tokenID string, conn net.Conn, w *wire.Conn) *Session
func (sm *SessionManager) GetSession(id string) *Session
func (sm *SessionManager) Cleanup() int

// Authentication
func (sm *SessionManager) ValidateToken(token string) (*TokenConfig, bool)

// Subscription index (O(1) lookup)
func (sm *SessionManager) GetSubscribersFor(namespace, key string) []*Session
func (sm *SessionManager) SubscribeSession(sessionID, namespace, key string)
func (sm *SessionManager) UnsubscribeSession(sessionID, namespace, key string)
func (sm *SessionManager) UnsubscribeAllForSession(sessionID, namespace string)

// Statistics
func (sm *SessionManager) Count() int
func (sm *SessionManager) CountActive() int
func (sm *SessionManager) CountLost() int
```

---

## 7. Error Types

```go
// internal/errors/errors.go

// Sentinel errors
var (
    // Not found
    ErrNotFound
    ErrNamespaceNotFound
    ErrTargetNotFound
    ErrPollerNotFound
    ErrSecretNotFound
    ErrPathNotFound
    
    // Already exists
    ErrAlreadyExists
    ErrNamespaceAlreadyExists
    ErrTargetAlreadyExists
    ErrPollerAlreadyExists
    
    // Validation
    ErrInvalidName
    ErrInvalidPath
    ErrInvalidOID
    ErrInvalidInterval
    ErrInvalidConfig
    ErrMissingField
    
    // State
    ErrInvalidState
    ErrInvalidTransition
    ErrPollerDisabled
    ErrPollerRunning
    ErrPollerStopped
    
    // Auth
    ErrNotAuthenticated
    ErrNotAuthorized
    ErrInvalidToken
    ErrSessionExpired
    ErrNamespaceRequired
    
    // Protocol
    ErrUnsupportedProtocol
    ErrSNMPError
    ErrTimeout
    
    // Internal
    ErrInternal
    ErrDatabase
    ErrEncryption
)

// Helper functions
func IsNotFound(err error) bool
func IsAlreadyExists(err error) bool
func IsValidation(err error) bool
func IsStateError(err error) bool
func IsAuthError(err error) bool

// Error constructors
func Wrap(err error, message string) error
func Wrapf(err error, format string, args ...interface{}) error
func NewNotFound(entityType, identifier string) error
func NewAlreadyExists(entityType, identifier string) error
func NewValidation(field, reason string) error
```

---

## 8. Configuration Types

```go
// config/config.go

type Config struct {
    Server  ServerConfig
    TLS     TLSConfig
    Auth    AuthConfig
    Session SessionConfig
    Poller  PollerConfig
    Storage StorageConfig
    SNMP    SNMPDefaults
    Targets []TargetConfig
}

type StorageConfig struct {
    DBPath                string
    SecretKeyPath         string
    SampleBatchSize       int    // 100-10000, default 1000
    SampleFlushTimeoutSec int    // 1-60, default 5
    StateFlushIntervalSec int    // 1-60, default 5
    StatsFlushIntervalSec int    // 1-120, default 10
}

// Methods
func Default() *Config
func Load(path string) (*Config, error)
func (c *Config) Validate() error
func (c *Config) TLSEnabled() bool
func (c *Config) AuthTimeout() time.Duration
func (c *Config) ReconnectWindow() time.Duration
```

---

*End of API Reference*
