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
	State      string // "polling", "unreachable", "error"
	LastPollMs int64
	LastError  string
	ErrCount   int
	Owners     map[string]bool // Sessions die dieses Target "besitzen" (via monitor)
	Polling    bool            // true wenn ein Poll in Arbeit ist

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
		ID:         id,
		Host:       req.Host,
		Port:       port,
		OID:        req.Oid,
		IntervalMs: interval,
		SNMP:       req.Snmp,
		State:      "polling",
		Owners:     make(map[string]bool),
		buffer:     make([]Sample, bufSize),
		bufSize:    bufSize,
		Polling:    false,
	}
}

// Key returns the deduplication key (host:port/oid).
func (t *Target) Key() string {
	return fmt.Sprintf("%s:%d/%s", t.Host, t.Port, t.OID)
}

// AddOwner adds a session as owner (keeps target alive).
func (t *Target) AddOwner(sessionID string) {
	t.mu.Lock()
	t.Owners[sessionID] = true
	t.mu.Unlock()
}

// RemoveOwner removes a session from owners.
func (t *Target) RemoveOwner(sessionID string) {
	t.mu.Lock()
	delete(t.Owners, sessionID)
	t.mu.Unlock()
}

// GetOwners returns a copy of owner IDs.
func (t *Target) GetOwners() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	owners := make([]string, 0, len(t.Owners))
	for id := range t.Owners {
		owners = append(owners, id)
	}
	return owners
}

// HasOwners returns true if any owners exist.
func (t *Target) HasOwners() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.Owners) > 0
}

// OwnerCount returns the number of owners.
func (t *Target) OwnerCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.Owners)
}

// WriteSample adds a sample to the ring buffer.
func (t *Target) WriteSample(s Sample) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Reset polling flag
	t.Polling = false

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

// ResetPolling resets the polling flag (used when target not found during poll).
func (t *Target) ResetPolling() {
	t.mu.Lock()
	t.Polling = false
	t.mu.Unlock()
}

// TryStartPolling attempts to mark the target as polling.
// Returns true if successful, false if already polling or not time yet.
func (t *Target) TryStartPolling(now int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Bereits ein Poll in Arbeit
	if t.Polling {
		return false
	}

	// Prüfe ob es Zeit für den nächsten Poll ist
	nextPoll := t.LastPollMs + int64(t.IntervalMs)
	if now < nextPoll {
		return false
	}

	// Markiere als "polling in progress"
	t.Polling = true
	return true
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
		Subscribers:     int32(len(t.Owners)), // Für Protokoll-Kompatibilität
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
		// TryStartPolling prüft atomar:
		// 1. Ob bereits ein Poll läuft (Polling flag)
		// 2. Ob es Zeit für den nächsten Poll ist (LastPollMs + IntervalMs)
		// Und setzt das Polling flag wenn erfolgreich
		if !t.TryStartPolling(now) {
			continue
		}

		// Versuche Job in Queue zu schieben
		select {
		case p.jobs <- PollJob{TargetID: t.ID}:
			// OK - Job wurde eingereicht
		default:
			// Queue voll - Polling flag zurücksetzen damit später erneut versucht wird
			t.ResetPolling()
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
		if snmpCfg.TimeoutMs > 0 {
			snmp.Timeout = time.Duration(snmpCfg.TimeoutMs) * time.Millisecond
		}
		if snmpCfg.Retries > 0 {
			snmp.Retries = int(snmpCfg.Retries)
		}

		if v2c := snmpCfg.GetV2C(); v2c != nil {
			snmp.Version = gosnmp.Version2c
			snmp.Community = v2c.Community
		} else if v3 := snmpCfg.GetV3(); v3 != nil {
			snmp.Version = gosnmp.Version3
			snmp.SecurityModel = gosnmp.UserSecurityModel
			snmp.ContextName = v3.ContextName

			// Security level
			switch v3.SecurityLevel {
			case pb.SecurityLevel_SECURITY_LEVEL_NO_AUTH_NO_PRIV:
				snmp.MsgFlags = gosnmp.NoAuthNoPriv
			case pb.SecurityLevel_SECURITY_LEVEL_AUTH_NO_PRIV:
				snmp.MsgFlags = gosnmp.AuthNoPriv
			case pb.SecurityLevel_SECURITY_LEVEL_AUTH_PRIV:
				snmp.MsgFlags = gosnmp.AuthPriv
			default:
				snmp.MsgFlags = gosnmp.AuthPriv
			}

			snmp.SecurityParameters = &gosnmp.UsmSecurityParameters{
				UserName:                 v3.SecurityName,
				AuthenticationProtocol:   mapAuthProtocol(v3.AuthProtocol),
				AuthenticationPassphrase: v3.AuthPassword,
				PrivacyProtocol:          mapPrivProtocol(v3.PrivProtocol),
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

	// Write sample to ring buffer (also resets Polling flag)
	t.WriteSample(Sample{
		TimestampMs: r.TimestampMs,
		Counter:     r.Counter,
		Text:        r.Text,
		Valid:       r.Valid,
		Error:       r.Error,
		PollMs:      r.PollMs,
	})

	// Sammle alle Sessions die für dieses Target subscribed sind
	// WICHTIG: Wir iterieren über ALLE Sessions, nicht nur über t.Owners
	// Denn Owners = "wer hält das Target am Leben" (via monitor)
	//      Subscribers = "wer will Live-Updates" (via subscribe, gespeichert in Session)
	var subscribedSessions []*Session
	for _, sess := range p.server.sessions {
		if !sess.IsLost() && sess.IsSubscribed(r.TargetID) {
			subscribedSessions = append(subscribedSessions, sess)
		}
	}
	p.server.mu.RUnlock()

	// Keine Subscriber? Fertig.
	if len(subscribedSessions) == 0 {
		return
	}

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

	// Send to all subscribed sessions
	for _, sess := range subscribedSessions {
		sess.Send(env)
	}
}

func mapAuthProtocol(p pb.AuthProtocol) gosnmp.SnmpV3AuthProtocol {
	switch p {
	case pb.AuthProtocol_AUTH_PROTOCOL_MD5:
		return gosnmp.MD5
	case pb.AuthProtocol_AUTH_PROTOCOL_SHA:
		return gosnmp.SHA
	case pb.AuthProtocol_AUTH_PROTOCOL_SHA224:
		return gosnmp.SHA224
	case pb.AuthProtocol_AUTH_PROTOCOL_SHA256:
		return gosnmp.SHA256
	case pb.AuthProtocol_AUTH_PROTOCOL_SHA384:
		return gosnmp.SHA384
	case pb.AuthProtocol_AUTH_PROTOCOL_SHA512:
		return gosnmp.SHA512
	default:
		return gosnmp.NoAuth
	}
}

func mapPrivProtocol(p pb.PrivProtocol) gosnmp.SnmpV3PrivProtocol {
	switch p {
	case pb.PrivProtocol_PRIV_PROTOCOL_DES:
		return gosnmp.DES
	case pb.PrivProtocol_PRIV_PROTOCOL_AES:
		return gosnmp.AES
	case pb.PrivProtocol_PRIV_PROTOCOL_AES192:
		return gosnmp.AES192
	case pb.PrivProtocol_PRIV_PROTOCOL_AES256:
		return gosnmp.AES256
	default:
		return gosnmp.NoPriv
	}
}
