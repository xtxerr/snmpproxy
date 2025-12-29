package server

import (
	"fmt"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// Target represents a monitored SNMP OID.
type Target struct {
	mu sync.RWMutex

	ID         string
	Host       string
	Port       uint16
	OID        string
	IntervalMs uint32
	SNMP       *pb.SNMPConfig

	// Runtime
	State       string // "polling", "unreachable", "error"
	LastPollMs  int64
	LastError   string
	ErrCount    int
	Subscribers map[string]bool

	// Buffer
	buffer   []Sample
	bufSize  int
	writeIdx int
	count    int
}

// Sample is a single polled value.
type Sample struct {
	TimestampMs int64
	Counter     uint64
	Text        string
	Valid       bool
	Error       string
	PollMs      int32
}

// NewTarget creates a new target.
func NewTarget(id string, req *pb.MonitorRequest, defaultBufSize uint32) *Target {
	port := uint16(req.Port)
	if port == 0 {
		port = 161
	}
	interval := req.IntervalMs
	if interval == 0 {
		interval = 1000
	}
	bufSize := int(req.BufferSize)
	if bufSize == 0 {
		bufSize = int(defaultBufSize)
	}

	return &Target{
		ID:          id,
		Host:        req.Host,
		Port:        port,
		OID:         req.Oid,
		IntervalMs:  interval,
		SNMP:        req.Snmp,
		State:       "polling",
		Subscribers: make(map[string]bool),
		buffer:      make([]Sample, bufSize),
		bufSize:     bufSize,
	}
}

// Key returns the deduplication key (host:port/oid).
func (t *Target) Key() string {
	return fmt.Sprintf("%s:%d/%s", t.Host, t.Port, t.OID)
}

// AddSubscriber adds a session as subscriber.
func (t *Target) AddSubscriber(sessionID string) {
	t.mu.Lock()
	t.Subscribers[sessionID] = true
	t.mu.Unlock()
}

// RemoveSubscriber removes a session from subscribers.
func (t *Target) RemoveSubscriber(sessionID string) {
	t.mu.Lock()
	delete(t.Subscribers, sessionID)
	t.mu.Unlock()
}

// GetSubscribers returns a copy of subscriber IDs.
func (t *Target) GetSubscribers() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	subs := make([]string, 0, len(t.Subscribers))
	for id := range t.Subscribers {
		subs = append(subs, id)
	}
	return subs
}

// HasSubscribers returns true if any subscribers exist.
func (t *Target) HasSubscribers() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.Subscribers) > 0
}

// WriteSample adds a sample to the ring buffer.
func (t *Target) WriteSample(s Sample) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buffer[t.writeIdx] = s
	t.writeIdx = (t.writeIdx + 1) % t.bufSize
	if t.count < t.bufSize {
		t.count++
	}

	t.LastPollMs = s.TimestampMs
	if s.Valid {
		t.State = "polling"
		t.LastError = ""
		t.ErrCount = 0
	} else {
		t.LastError = s.Error
		t.ErrCount++
		if t.ErrCount >= 3 {
			t.State = "unreachable"
		}
	}
}

// ReadLastN returns the last n samples (oldest first).
func (t *Target) ReadLastN(n int) []Sample {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if n > t.count {
		n = t.count
	}
	if n == 0 {
		return nil
	}

	result := make([]Sample, n)
	start := (t.writeIdx - n + t.bufSize) % t.bufSize
	for i := 0; i < n; i++ {
		result[i] = t.buffer[(start+i)%t.bufSize]
	}
	return result
}

// ToProto converts to protobuf Target.
func (t *Target) ToProto() *pb.Target {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return &pb.Target{
		Id:              t.ID,
		Host:            t.Host,
		Port:            uint32(t.Port),
		Oid:             t.OID,
		IntervalMs:      t.IntervalMs,
		BufferSize:      uint32(t.bufSize),
		State:           t.State,
		LastPollMs:      t.LastPollMs,
		LastError:       t.LastError,
		Subscribers:     int32(len(t.Subscribers)),
		SamplesBuffered: int32(t.count),
	}
}

// PollJob is a job for the worker pool.
type PollJob struct {
	TargetID string
}

// PollResult is the result of a poll.
type PollResult struct {
	TargetID    string
	TimestampMs int64
	Counter     uint64
	Text        string
	Valid       bool
	Error       string
	PollMs      int32
}

// Poller manages SNMP polling with a worker pool.
type Poller struct {
	server   *Server
	jobs     chan PollJob
	results  chan PollResult
	shutdown chan struct{}
	workers  int
}

// NewPoller creates a new poller.
func NewPoller(srv *Server, workers, queueSize int) *Poller {
	return &Poller{
		server:   srv,
		jobs:     make(chan PollJob, queueSize),
		results:  make(chan PollResult, queueSize),
		shutdown: make(chan struct{}),
		workers:  workers,
	}
}

// Start starts the poller workers and scheduler.
func (p *Poller) Start() {
	for i := 0; i < p.workers; i++ {
		go p.worker()
	}
	go p.scheduler()
	go p.dispatcher()
}

// Stop stops the poller.
func (p *Poller) Stop() {
	close(p.shutdown)
}

func (p *Poller) scheduler() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.schedulePolls()
		case <-p.shutdown:
			return
		}
	}
}

func (p *Poller) schedulePolls() {
	now := time.Now().UnixMilli()

	p.server.mu.RLock()
	defer p.server.mu.RUnlock()

	for _, t := range p.server.targets {
		t.mu.RLock()
		nextPoll := t.LastPollMs + int64(t.IntervalMs)
		t.mu.RUnlock()

		if now >= nextPoll {
			select {
			case p.jobs <- PollJob{TargetID: t.ID}:
			default:
				// Queue full
			}
		}
	}
}

func (p *Poller) worker() {
	for {
		select {
		case job := <-p.jobs:
			result := p.poll(job.TargetID)
			select {
			case p.results <- result:
			case <-p.shutdown:
				return
			}
		case <-p.shutdown:
			return
		}
	}
}

func (p *Poller) poll(targetID string) PollResult {
	start := time.Now()

	p.server.mu.RLock()
	t, ok := p.server.targets[targetID]
	if !ok {
		p.server.mu.RUnlock()
		return PollResult{TargetID: targetID, Valid: false, Error: "target not found"}
	}

	t.mu.RLock()
	host := t.Host
	port := t.Port
	oid := t.OID
	snmpCfg := t.SNMP
	t.mu.RUnlock()
	p.server.mu.RUnlock()

	// Configure SNMP
	snmp := &gosnmp.GoSNMP{
		Target:  host,
		Port:    port,
		Timeout: 5 * time.Second,
		Retries: 2,
	}

	if snmpCfg != nil {
		if v2c := snmpCfg.GetV2C(); v2c != nil {
			snmp.Version = gosnmp.Version2c
			snmp.Community = v2c.Community
		} else if v3 := snmpCfg.GetV3(); v3 != nil {
			snmp.Version = gosnmp.Version3
			snmp.SecurityModel = gosnmp.UserSecurityModel
			snmp.MsgFlags = gosnmp.AuthPriv
			snmp.SecurityParameters = &gosnmp.UsmSecurityParameters{
				UserName:                 v3.SecurityName,
				AuthenticationProtocol:   mapAuthProto(v3.AuthProtocol),
				AuthenticationPassphrase: v3.AuthPassword,
				PrivacyProtocol:          mapPrivProto(v3.PrivProtocol),
				PrivacyPassphrase:        v3.PrivPassword,
			}
		}
	} else {
		snmp.Version = gosnmp.Version2c
		snmp.Community = "public"
	}

	result := PollResult{
		TargetID:    targetID,
		TimestampMs: time.Now().UnixMilli(),
	}

	if err := snmp.Connect(); err != nil {
		result.Valid = false
		result.Error = err.Error()
		result.PollMs = int32(time.Since(start).Milliseconds())
		return result
	}
	defer snmp.Conn.Close()

	pdu, err := snmp.Get([]string{oid})
	if err != nil {
		result.Valid = false
		result.Error = err.Error()
		result.PollMs = int32(time.Since(start).Milliseconds())
		return result
	}

	if len(pdu.Variables) == 0 {
		result.Valid = false
		result.Error = "no variables returned"
		result.PollMs = int32(time.Since(start).Milliseconds())
		return result
	}

	v := pdu.Variables[0]
	switch v.Type {
	case gosnmp.Counter64, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.Integer, gosnmp.Uinteger32, gosnmp.TimeTicks:
		result.Counter = gosnmp.ToBigInt(v.Value).Uint64()
		result.Valid = true
	case gosnmp.OctetString:
		if b, ok := v.Value.([]byte); ok {
			result.Text = string(b)
		}
		result.Valid = true
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance:
		result.Valid = false
		result.Error = "no such object"
	default:
		result.Valid = false
		result.Error = "unsupported type"
	}

	result.PollMs = int32(time.Since(start).Milliseconds())
	return result
}

func (p *Poller) dispatcher() {
	for {
		select {
		case r := <-p.results:
			p.dispatchSample(r)
		case <-p.shutdown:
			return
		}
	}
}

func (p *Poller) dispatchSample(r PollResult) {
	p.server.mu.RLock()
	t, ok := p.server.targets[r.TargetID]
	if !ok {
		p.server.mu.RUnlock()
		return
	}

	// Write to buffer
	t.WriteSample(Sample{
		TimestampMs: r.TimestampMs,
		Counter:     r.Counter,
		Text:        r.Text,
		Valid:       r.Valid,
		Error:       r.Error,
		PollMs:      r.PollMs,
	})

	// Get subscribers
	subs := t.GetSubscribers()
	p.server.mu.RUnlock()

	// Build sample envelope
	env := &pb.Envelope{
		Id: 0, // Push message
		Payload: &pb.Envelope_Sample{
			Sample: &pb.Sample{
				TargetId:    r.TargetID,
				TimestampMs: r.TimestampMs,
				Counter:     r.Counter,
				Text:        r.Text,
				Valid:       r.Valid,
				Error:       r.Error,
				PollMs:      r.PollMs,
			},
		},
	}

	// Send to subscribers
	for _, sessionID := range subs {
		p.server.mu.RLock()
		sess, ok := p.server.sessions[sessionID]
		p.server.mu.RUnlock()

		if ok && sess.IsSubscribed(r.TargetID) {
			sess.Send(env)
		}
	}
}

func mapAuthProto(s string) gosnmp.SnmpV3AuthProtocol {
	switch s {
	case "MD5":
		return gosnmp.MD5
	case "SHA":
		return gosnmp.SHA
	case "SHA256":
		return gosnmp.SHA256
	case "SHA512":
		return gosnmp.SHA512
	default:
		return gosnmp.NoAuth
	}
}

func mapPrivProto(s string) gosnmp.SnmpV3PrivProtocol {
	switch s {
	case "DES":
		return gosnmp.DES
	case "AES":
		return gosnmp.AES
	case "AES192":
		return gosnmp.AES192
	case "AES256":
		return gosnmp.AES256
	default:
		return gosnmp.NoPriv
	}
}
