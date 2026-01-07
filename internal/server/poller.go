package server

import (
	"container/heap"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// Target represents a monitored SNMP OID.
type Target struct {
	mu sync.RWMutex

	// Identity
	ID          string
	Name        string   // User-friendly name, optional
	Description string
	Tags        []string
	Persistent  bool
	Protocol    string // "snmp", future: "http", "icmp"

	// SNMP-specific config
	Host string
	Port uint16
	OID  string
	SNMP *pb.SNMPTargetConfig

	// Polling config
	IntervalMs uint32

	// Runtime state
	State      string // "polling", "unreachable", "error"
	LastPollMs int64
	LastError  string
	ErrCount   int
	Owners     map[string]bool // sessionID -> true, "$config" for config targets

	// Statistics
	CreatedAt   time.Time
	PollsTotal  int64
	PollsOK     int64
	PollsFailed int64
	PollMsTotal int64 // For calculating average
	PollMsMin   int32
	PollMsMax   int32

	// Ring buffer
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

// NewTarget creates a new target from a CreateTargetRequest.
func NewTarget(id string, req *pb.CreateTargetRequest, defaultBufSize uint32) *Target {
	t := &Target{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Tags:        req.Tags,
		Persistent:  req.Persistent,
		State:       "polling",
		Owners:      make(map[string]bool),
		CreatedAt:   time.Now(),
		PollMsMin:   -1, // -1 = not set
	}

	// Interval
	t.IntervalMs = req.IntervalMs
	if t.IntervalMs == 0 {
		t.IntervalMs = 1000
	}

	// Buffer size
	bufSize := int(req.BufferSize)
	if bufSize == 0 {
		bufSize = int(defaultBufSize)
	}
	t.bufSize = bufSize
	t.buffer = make([]Sample, bufSize)

	// Protocol-specific config
	if snmpCfg := req.GetSnmp(); snmpCfg != nil {
		t.Protocol = "snmp"
		t.Host = snmpCfg.Host
		t.Port = uint16(snmpCfg.Port)
		if t.Port == 0 {
			t.Port = 161
		}
		t.OID = snmpCfg.Oid
		t.SNMP = snmpCfg
	}

	return t
}

// Key returns the deduplication key.
func (t *Target) Key() string {
	return fmt.Sprintf("%s:%d/%s", t.Host, t.Port, t.OID)
}

// AddOwner adds a session as owner.
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

// IsConfigTarget returns true if this target was loaded from config.
func (t *Target) IsConfigTarget() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Owners["$config"]
}

// WriteSample adds a sample to the ring buffer and updates stats.
func (t *Target) WriteSample(s Sample) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Write to buffer
	t.buffer[t.writeIdx] = s
	t.writeIdx = (t.writeIdx + 1) % t.bufSize
	if t.count < t.bufSize {
		t.count++
	}

	// Update state
	t.LastPollMs = s.TimestampMs
	t.PollsTotal++

	if s.Valid {
		t.State = "polling"
		t.LastError = ""
		t.ErrCount = 0
		t.PollsOK++
	} else {
		t.LastError = s.Error
		t.ErrCount++
		t.PollsFailed++
		if t.ErrCount >= 3 {
			t.State = "unreachable"
		}
	}

	// Update timing stats
	t.PollMsTotal += int64(s.PollMs)
	if t.PollMsMin < 0 || s.PollMs < t.PollMsMin {
		t.PollMsMin = s.PollMs
	}
	if s.PollMs > t.PollMsMax {
		t.PollMsMax = s.PollMs
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

// ResizeBuffer changes the buffer size, preserving existing samples.
// If new size is smaller, only the most recent samples are kept.
func (t *Target) ResizeBuffer(newSize int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if newSize == t.bufSize {
		return
	}

	// Read existing samples (oldest first)
	samplesToKeep := t.count
	if samplesToKeep > newSize {
		samplesToKeep = newSize
	}

	// Extract samples from old buffer
	var samples []Sample
	if samplesToKeep > 0 {
		// Start from oldest sample we want to keep
		skipCount := t.count - samplesToKeep
		start := (t.writeIdx - t.count + skipCount + t.bufSize) % t.bufSize
		samples = make([]Sample, samplesToKeep)
		for i := 0; i < samplesToKeep; i++ {
			samples[i] = t.buffer[(start+i)%t.bufSize]
		}
	}

	// Create new buffer and copy samples
	t.buffer = make([]Sample, newSize)
	t.bufSize = newSize
	t.count = samplesToKeep
	t.writeIdx = samplesToKeep % newSize

	for i, s := range samples {
		t.buffer[i] = s
	}
}

// BufferSize returns the current buffer size.
func (t *Target) BufferSize() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.bufSize
}

// ToProto converts to protobuf Target.
func (t *Target) ToProto() *pb.Target {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var avgPollMs int32
	if t.PollsTotal > 0 {
		avgPollMs = int32(t.PollMsTotal / t.PollsTotal)
	}

	minPollMs := t.PollMsMin
	if minPollMs < 0 {
		minPollMs = 0
	}

	target := &pb.Target{
		Id:              t.ID,
		Name:            t.Name,
		Description:     t.Description,
		Tags:            t.Tags,
		Persistent:      t.Persistent,
		Protocol:        t.Protocol,
		IntervalMs:      t.IntervalMs,
		BufferSize:      uint32(t.bufSize),
		State:           t.State,
		LastPollMs:      t.LastPollMs,
		LastError:       t.LastError,
		ErrorCount:      int32(t.ErrCount),
		SamplesBuffered: int32(t.count),
		OwnerCount:      int32(len(t.Owners)),
		CreatedAtMs:     t.CreatedAt.UnixMilli(),
		PollsTotal:      t.PollsTotal,
		PollsSuccess:    t.PollsOK,
		PollsFailed:     t.PollsFailed,
		AvgPollMs:       avgPollMs,
		MinPollMs:       minPollMs,
		MaxPollMs:       t.PollMsMax,
	}

	// Add protocol-specific config
	if t.SNMP != nil {
		target.ProtocolConfig = &pb.Target_Snmp{Snmp: t.SNMP}
	}

	return target
}

// ============================================================================
// Heap-based Scheduler
// ============================================================================

// PollItem represents a target in the scheduler heap.
type PollItem struct {
	TargetID   string
	NextPollMs int64
	IntervalMs int64
	Polling    bool
	index      int
}

// PollHeap implements heap.Interface.
type PollHeap []*PollItem

func (h PollHeap) Len() int            { return len(h) }
func (h PollHeap) Less(i, j int) bool  { return h[i].NextPollMs < h[j].NextPollMs }
func (h PollHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *PollHeap) Push(x any)         { item := x.(*PollItem); item.index = len(*h); *h = append(*h, item) }
func (h *PollHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}
func (h PollHeap) Peek() *PollItem {
	if len(h) == 0 {
		return nil
	}
	return h[0]
}

// ============================================================================
// Poll Job & Result
// ============================================================================

type PollJob struct {
	TargetID string
}

type PollResult struct {
	TargetID    string
	TimestampMs int64
	Counter     uint64
	Text        string
	Valid       bool
	Error       string
	PollMs      int32
}

// ============================================================================
// Poller
// ============================================================================

type Poller struct {
	server   *Server
	jobs     chan PollJob
	results  chan PollResult
	shutdown chan struct{}
	workers  int

	mu      sync.Mutex
	heap    PollHeap
	heapIdx map[string]*PollItem
	wakeup  chan struct{}
}

func NewPoller(srv *Server, workers, queueSize int) *Poller {
	p := &Poller{
		server:   srv,
		jobs:     make(chan PollJob, queueSize),
		results:  make(chan PollResult, queueSize),
		shutdown: make(chan struct{}),
		workers:  workers,
		heap:     make(PollHeap, 0),
		heapIdx:  make(map[string]*PollItem),
		wakeup:   make(chan struct{}, 1),
	}
	heap.Init(&p.heap)
	return p
}

func (p *Poller) Start() {
	for i := 0; i < p.workers; i++ {
		go p.worker()
	}
	go p.scheduler()
	go p.dispatcher()
}

func (p *Poller) Stop() {
	close(p.shutdown)
}

// Stats returns heap size and queue usage.
func (p *Poller) Stats() (heapSize, queueUsed int) {
	p.mu.Lock()
	heapSize = p.heap.Len()
	p.mu.Unlock()
	queueUsed = len(p.jobs)
	return
}

func (p *Poller) AddTarget(targetID string, intervalMs uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.heapIdx[targetID]; exists {
		return
	}

	interval := int64(intervalMs)
	jitter := rand.Int63n(interval)

	item := &PollItem{
		TargetID:   targetID,
		NextPollMs: time.Now().UnixMilli() + jitter,
		IntervalMs: interval,
		Polling:    false,
	}

	heap.Push(&p.heap, item)
	p.heapIdx[targetID] = item
	p.signalWakeup()
}

func (p *Poller) RemoveTarget(targetID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, ok := p.heapIdx[targetID]
	if !ok {
		return
	}

	if item.index >= 0 {
		heap.Remove(&p.heap, item.index)
	}
	delete(p.heapIdx, targetID)
}

func (p *Poller) UpdateInterval(targetID string, newIntervalMs uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, ok := p.heapIdx[targetID]
	if !ok {
		return
	}

	item.IntervalMs = int64(newIntervalMs)
}

func (p *Poller) signalWakeup() {
	select {
	case p.wakeup <- struct{}{}:
	default:
	}
}

func (p *Poller) scheduler() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	for {
		p.mu.Lock()
		next := p.heap.Peek()

		if next == nil {
			p.mu.Unlock()
			select {
			case <-p.wakeup:
				continue
			case <-p.shutdown:
				return
			}
		}

		now := time.Now().UnixMilli()
		sleepMs := next.NextPollMs - now
		p.mu.Unlock()

		if sleepMs > 0 {
			timer.Reset(time.Duration(sleepMs) * time.Millisecond)
			select {
			case <-timer.C:
			case <-p.wakeup:
				continue
			case <-p.shutdown:
				return
			}
		}

		p.processDueTargets()
	}
}

func (p *Poller) processDueTargets() {
	now := time.Now().UnixMilli()

	p.mu.Lock()
	defer p.mu.Unlock()

	for p.heap.Len() > 0 {
		next := p.heap.Peek()

		if next.NextPollMs > now {
			break
		}

		item := heap.Pop(&p.heap).(*PollItem)

		if item.Polling {
			continue
		}

		item.Polling = true

		select {
		case p.jobs <- PollJob{TargetID: item.TargetID}:
			// Success
		default:
			// Queue full - retry
			item.Polling = false
			item.NextPollMs = now + 10
			heap.Push(&p.heap, item)
		}
	}
}

func (p *Poller) MarkPollComplete(targetID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, ok := p.heapIdx[targetID]
	if !ok {
		return
	}

	now := time.Now().UnixMilli()
	item.NextPollMs = now + item.IntervalMs
	item.Polling = false

	heap.Push(&p.heap, item)
	p.signalWakeup()
}

func (p *Poller) worker() {
	for {
		select {
		case job := <-p.jobs:
			result := p.poll(job.TargetID)
			p.MarkPollComplete(job.TargetID)

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

	// Get defaults
	p.server.runtimeMu.RLock()
	defaultTimeout := p.server.defaultTimeoutMs
	defaultRetries := p.server.defaultRetries
	p.server.runtimeMu.RUnlock()

	// Configure SNMP
	snmp := &gosnmp.GoSNMP{
		Target:  host,
		Port:    port,
		Timeout: time.Duration(defaultTimeout) * time.Millisecond,
		Retries: int(defaultRetries),
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

			switch v3.SecurityLevel {
			case "noAuthNoPriv":
				snmp.MsgFlags = gosnmp.NoAuthNoPriv
			case "authNoPriv":
				snmp.MsgFlags = gosnmp.AuthNoPriv
			case "authPriv":
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
	// Record stats
	p.server.RecordPoll(r.Valid)

	p.server.mu.RLock()
	t, ok := p.server.targets[r.TargetID]
	if !ok {
		p.server.mu.RUnlock()
		return
	}

	// Get old state before writing sample
	t.mu.RLock()
	oldState := t.State
	t.mu.RUnlock()

	t.WriteSample(Sample{
		TimestampMs: r.TimestampMs,
		Counter:     r.Counter,
		Text:        r.Text,
		Valid:       r.Valid,
		Error:       r.Error,
		PollMs:      r.PollMs,
	})

	// Get new state after writing sample
	t.mu.RLock()
	newState := t.State
	t.mu.RUnlock()

	// Update state index if state changed
	if oldState != newState {
		p.server.UpdateTargetState(r.TargetID, oldState, newState)
	}

	var subscribedSessions []*Session
	for _, sess := range p.server.sessions {
		if !sess.IsLost() && sess.IsSubscribed(r.TargetID) {
			subscribedSessions = append(subscribedSessions, sess)
		}
	}
	p.server.mu.RUnlock()

	if len(subscribedSessions) == 0 {
		return
	}

	env := &pb.Envelope{
		Id: 0,
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

	for _, sess := range subscribedSessions {
		sess.Send(env)
	}
}

func mapAuthProtocol(p string) gosnmp.SnmpV3AuthProtocol {
	switch p {
	case "MD5":
		return gosnmp.MD5
	case "SHA", "SHA1":
		return gosnmp.SHA
	case "SHA224":
		return gosnmp.SHA224
	case "SHA256":
		return gosnmp.SHA256
	case "SHA384":
		return gosnmp.SHA384
	case "SHA512":
		return gosnmp.SHA512
	default:
		return gosnmp.NoAuth
	}
}

func mapPrivProtocol(p string) gosnmp.SnmpV3PrivProtocol {
	switch p {
	case "DES":
		return gosnmp.DES
	case "AES", "AES128":
		return gosnmp.AES
	case "AES192":
		return gosnmp.AES192
	case "AES256":
		return gosnmp.AES256
	default:
		return gosnmp.NoPriv
	}
}
