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
		Subscribers:     int32(len(t.Owners)),
		SamplesBuffered: int32(t.count),
	}
}

// ============================================================================
// Heap-based Scheduler
// ============================================================================

// PollItem represents a target in the scheduler heap.
type PollItem struct {
	TargetID   string
	NextPollMs int64 // Wann der nächste Poll fällig ist
	IntervalMs int64 // Poll-Interval
	Polling    bool  // true wenn Poll gerade läuft
	index      int   // Index im Heap (für heap.Fix/Remove)
}

// PollHeap implements heap.Interface - Min-Heap sortiert nach NextPollMs.
type PollHeap []*PollItem

func (h PollHeap) Len() int { return len(h) }

func (h PollHeap) Less(i, j int) bool {
	return h[i].NextPollMs < h[j].NextPollMs
}

func (h PollHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *PollHeap) Push(x any) {
	item := x.(*PollItem)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *PollHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // Avoid memory leak
	item.index = -1
	*h = old[0 : n-1]
	return item
}

// Peek returns the next item without removing it.
func (h PollHeap) Peek() *PollItem {
	if len(h) == 0 {
		return nil
	}
	return h[0]
}

// ============================================================================
// Poll Job & Result
// ============================================================================

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

// ============================================================================
// Poller
// ============================================================================

// Poller manages SNMP polling with a heap-based scheduler and worker pool.
type Poller struct {
	server   *Server
	jobs     chan PollJob
	results  chan PollResult
	shutdown chan struct{}
	workers  int

	// Heap-basierter Scheduler
	mu      sync.Mutex
	heap    PollHeap
	heapIdx map[string]*PollItem // targetID -> PollItem für O(1) Lookup
	wakeup  chan struct{}        // Signal wenn neues Target hinzugefügt
}

// NewPoller creates a new poller with heap-based scheduling.
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

// Start starts the poller workers, scheduler, and dispatcher.
func (p *Poller) Start() {
	// Worker-Pool starten
	for i := 0; i < p.workers; i++ {
		go p.worker()
	}
	// Heap-basierter Scheduler
	go p.scheduler()
	// Ergebnis-Dispatcher
	go p.dispatcher()
}

// Stop stops the poller.
func (p *Poller) Stop() {
	close(p.shutdown)
}

// AddTarget fügt ein Target zum Scheduler hinzu.
// Verwendet Jitter um Thundering-Herd zu vermeiden.
func (p *Poller) AddTarget(targetID string, intervalMs uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Bereits vorhanden?
	if _, exists := p.heapIdx[targetID]; exists {
		return
	}

	interval := int64(intervalMs)

	// Jitter: Verteile initialen Poll zufällig über das Interval
	// Damit nicht alle Targets gleichzeitig pollen
	jitter := rand.Int63n(interval)

	item := &PollItem{
		TargetID:   targetID,
		NextPollMs: time.Now().UnixMilli() + jitter,
		IntervalMs: interval,
		Polling:    false,
	}

	heap.Push(&p.heap, item)
	p.heapIdx[targetID] = item

	// Scheduler aufwecken falls er schläft
	p.signalWakeup()
}

// RemoveTarget entfernt ein Target vom Scheduler.
func (p *Poller) RemoveTarget(targetID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, ok := p.heapIdx[targetID]
	if !ok {
		return
	}

	// Nur aus Heap entfernen wenn noch drin (index >= 0)
	if item.index >= 0 {
		heap.Remove(&p.heap, item.index)
	}
	delete(p.heapIdx, targetID)
}

// UpdateInterval ändert das Poll-Interval eines Targets.
func (p *Poller) UpdateInterval(targetID string, newIntervalMs uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, ok := p.heapIdx[targetID]
	if !ok {
		return
	}

	item.IntervalMs = int64(newIntervalMs)
	// NextPollMs bleibt gleich - neues Interval gilt ab nächstem Poll
}

// signalWakeup sendet ein nicht-blockierendes Signal an den Scheduler.
func (p *Poller) signalWakeup() {
	select {
	case p.wakeup <- struct{}{}:
	default:
		// Channel ist bereits voll, Scheduler wird sowieso aufwachen
	}
}

// scheduler ist der Heap-basierte Scheduler.
// Schläft bis zum nächsten fälligen Poll statt busy-waiting.
func (p *Poller) scheduler() {
	timer := time.NewTimer(time.Hour) // Initial lang, wird sofort überschrieben
	defer timer.Stop()

	for {
		p.mu.Lock()

		// Nächstes fälliges Target finden
		next := p.heap.Peek()

		if next == nil {
			// Keine Targets - warte auf Signal oder Shutdown
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
			// Schlafe bis zum nächsten Poll
			timer.Reset(time.Duration(sleepMs) * time.Millisecond)
			select {
			case <-timer.C:
				// Zeit für Poll
			case <-p.wakeup:
				// Neues Target oder Änderung - neu prüfen
				continue
			case <-p.shutdown:
				return
			}
		}

		// Alle fälligen Targets verarbeiten
		p.processDueTargets()
	}
}

// processDueTargets verarbeitet alle Targets deren NextPollMs <= now ist.
func (p *Poller) processDueTargets() {
	now := time.Now().UnixMilli()

	p.mu.Lock()
	defer p.mu.Unlock()

	for p.heap.Len() > 0 {
		next := p.heap.Peek()

		// Nicht fällig? Fertig.
		if next.NextPollMs > now {
			break
		}

		// Pop aus dem Heap
		item := heap.Pop(&p.heap).(*PollItem)

		// Bereits am Pollen? Überspringe (sollte nicht passieren)
		if item.Polling {
			continue
		}

		// Als "polling" markieren
		item.Polling = true

		// Job erstellen
		select {
		case p.jobs <- PollJob{TargetID: item.TargetID}:
			// Erfolgreich eingereicht
			// Target ist jetzt aus dem Heap, wird nach Completion wieder eingefügt
		default:
			// Queue voll - sofort wieder einreihen mit Retry-Delay
			item.Polling = false
			item.NextPollMs = now + 10
			heap.Push(&p.heap, item)
		}
	}
}

// MarkPollComplete wird aufgerufen wenn ein Poll abgeschlossen ist.
// Setzt NextPollMs und fügt das Target wieder in den Heap ein.
func (p *Poller) MarkPollComplete(targetID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, ok := p.heapIdx[targetID]
	if !ok {
		// Target wurde zwischenzeitlich entfernt
		return
	}

	now := time.Now().UnixMilli()
	item.NextPollMs = now + item.IntervalMs
	item.Polling = false

	// Wieder in den Heap einfügen
	heap.Push(&p.heap, item)

	// Scheduler aufwecken
	p.signalWakeup()
}

// worker verarbeitet Poll-Jobs aus der Queue.
func (p *Poller) worker() {
	for {
		select {
		case job := <-p.jobs:
			result := p.poll(job.TargetID)

			// Poll abgeschlossen - Scheduler informieren
			p.MarkPollComplete(job.TargetID)

			// Ergebnis an Dispatcher
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

// poll führt den eigentlichen SNMP-Poll durch.
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

// dispatcher verteilt Poll-Ergebnisse an subscribed Sessions.
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

// dispatchSample schreibt das Sample in den Buffer und sendet an Subscriber.
func (p *Poller) dispatchSample(r PollResult) {
	p.server.mu.RLock()
	t, ok := p.server.targets[r.TargetID]
	if !ok {
		p.server.mu.RUnlock()
		return
	}

	// Write sample to ring buffer
	t.WriteSample(Sample{
		TimestampMs: r.TimestampMs,
		Counter:     r.Counter,
		Text:        r.Text,
		Valid:       r.Valid,
		Error:       r.Error,
		PollMs:      r.PollMs,
	})

	// Sammle alle Sessions die für dieses Target subscribed sind
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

// ============================================================================
// SNMP Protocol Mapping
// ============================================================================

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
