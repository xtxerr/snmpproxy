// Package server implements the SNMP proxy server.
package server

import (
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/xtxerr/snmpproxy/config"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
	"github.com/xtxerr/snmpproxy/internal/wire"
)

// Version is set at build time via ldflags
var Version = "dev"

// Server is the SNMP proxy server.
type Server struct {
	cfg      *config.Server
	listener net.Listener
	poller   *Poller

	mu          sync.RWMutex
	sessions    map[string]*Session
	targets     map[string]*Target
	targetIndex map[string]string // "host:port/oid" -> targetID

	// Runtime config (can be changed at runtime)
	runtimeMu         sync.RWMutex
	defaultTimeoutMs  uint32
	defaultRetries    uint32
	defaultBufferSize uint32
	minIntervalMs     uint32

	// Statistics
	startedAt   time.Time
	pollsTotal  atomic.Int64
	pollsOK     atomic.Int64
	pollsFailed atomic.Int64

	shutdown chan struct{}
}

// Session represents a connected client.
type Session struct {
	mu sync.RWMutex

	ID        string
	TokenID   string
	Conn      net.Conn
	Wire      *wire.Conn
	CreatedAt time.Time
	LostAt    *time.Time

	subscriptions map[string]bool // Targets für die diese Session Live-Updates will
	sendCh        chan *pb.Envelope
}

// NewSession creates a new session.
func NewSession(id, tokenID string, conn net.Conn, w *wire.Conn) *Session {
	return &Session{
		ID:            id,
		TokenID:       tokenID,
		Conn:          conn,
		Wire:          w,
		CreatedAt:     time.Now(),
		subscriptions: make(map[string]bool),
		sendCh:        make(chan *pb.Envelope, 1000),
	}
}

// Subscribe adds a target subscription (for live updates).
func (s *Session) Subscribe(targetID string) {
	s.mu.Lock()
	s.subscriptions[targetID] = true
	s.mu.Unlock()
}

// Unsubscribe removes a target subscription.
func (s *Session) Unsubscribe(targetID string) {
	s.mu.Lock()
	delete(s.subscriptions, targetID)
	s.mu.Unlock()
}

// UnsubscribeAll removes all subscriptions and returns the IDs.
func (s *Session) UnsubscribeAll() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.subscriptions))
	for id := range s.subscriptions {
		ids = append(ids, id)
	}
	s.subscriptions = make(map[string]bool)
	return ids
}

// IsSubscribed checks if subscribed to a target (for live updates).
func (s *Session) IsSubscribed(targetID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subscriptions[targetID]
}

// GetSubscriptions returns subscribed target IDs.
func (s *Session) GetSubscriptions() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.subscriptions))
	for id := range s.subscriptions {
		ids = append(ids, id)
	}
	return ids
}

// Send queues an envelope for sending (non-blocking).
func (s *Session) Send(env *pb.Envelope) bool {
	select {
	case s.sendCh <- env:
		return true
	default:
		return false
	}
}

// MarkLost marks the session as disconnected.
func (s *Session) MarkLost() {
	s.mu.Lock()
	now := time.Now()
	s.LostAt = &now
	s.mu.Unlock()
}

// IsLost returns true if session is disconnected.
func (s *Session) IsLost() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LostAt != nil
}

// LostDuration returns how long the session has been lost.
func (s *Session) LostDuration() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.LostAt == nil {
		return 0
	}
	return time.Since(*s.LostAt)
}

// Close closes the session.
func (s *Session) Close() {
	close(s.sendCh)
	s.Conn.Close()
}

// New creates a new server.
func New(cfg *config.Server) *Server {
	srv := &Server{
		cfg:               cfg,
		sessions:          make(map[string]*Session),
		targets:           make(map[string]*Target),
		targetIndex:       make(map[string]string),
		shutdown:          make(chan struct{}),
		startedAt:         time.Now(),
		defaultTimeoutMs:  cfg.SNMP.TimeoutMs,
		defaultRetries:    cfg.SNMP.Retries,
		defaultBufferSize: cfg.SNMP.BufferSize,
		minIntervalMs:     100, // Default minimum 100ms
	}
	srv.poller = NewPoller(srv, cfg.Poller.Workers, cfg.Poller.QueueSize)
	return srv
}

// Run starts the server (blocking).
func (srv *Server) Run() error {
	var ln net.Listener
	var err error

	if srv.cfg.TLSEnabled() {
		cert, err := tls.LoadX509KeyPair(srv.cfg.TLS.CertFile, srv.cfg.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("load TLS cert: %w", err)
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		ln, err = tls.Listen("tcp", srv.cfg.Listen, tlsCfg)
		if err != nil {
			return fmt.Errorf("TLS listen: %w", err)
		}
		log.Printf("Listening on %s (TLS)", srv.cfg.Listen)
	} else {
		ln, err = net.Listen("tcp", srv.cfg.Listen)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		log.Printf("Listening on %s (no TLS)", srv.cfg.Listen)
	}

	srv.listener = ln
	srv.poller.Start()
	go srv.cleanup()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-srv.shutdown:
				return nil
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}
		go srv.handleConn(conn)
	}
}

// Shutdown stops the server.
func (srv *Server) Shutdown() {
	close(srv.shutdown)
	srv.poller.Stop()
	if srv.listener != nil {
		srv.listener.Close()
	}
	srv.mu.Lock()
	for _, s := range srv.sessions {
		s.Close()
	}
	srv.mu.Unlock()
}

// RecordPoll records poll statistics.
func (srv *Server) RecordPoll(success bool) {
	srv.pollsTotal.Add(1)
	if success {
		srv.pollsOK.Add(1)
	} else {
		srv.pollsFailed.Add(1)
	}
}

func (srv *Server) handleConn(conn net.Conn) {
	remote := conn.RemoteAddr().String()
	log.Printf("Connection from %s", remote)

	w := wire.NewConn(conn)

	// Auth with timeout
	conn.SetDeadline(time.Now().Add(srv.cfg.AuthTimeout()))

	env, err := w.Read()
	if err != nil {
		log.Printf("Auth read error from %s: %v", remote, err)
		conn.Close()
		return
	}

	auth := env.GetAuth()
	if auth == nil {
		w.Write(wire.NewError(env.Id, wire.ErrNotAuthenticated, "first message must be auth"))
		conn.Close()
		return
	}

	tokenID, ok := srv.validateToken(auth.Token)
	if !ok {
		w.Write(&pb.Envelope{
			Id: env.Id,
			Payload: &pb.Envelope_AuthResp{
				AuthResp: &pb.AuthResponse{Ok: false, Message: "invalid token"},
			},
		})
		conn.Close()
		log.Printf("Auth failed from %s", remote)
		return
	}

	conn.SetDeadline(time.Time{}) // Clear deadline

	// Try restore or create session
	sess := srv.tryRestore(tokenID, conn, w)
	if sess == nil {
		sess = NewSession(srv.genID(), tokenID, conn, w)
		srv.mu.Lock()
		srv.sessions[sess.ID] = sess
		srv.mu.Unlock()
		log.Printf("New session %s from %s (token: %s)", sess.ID, remote, tokenID)
	} else {
		log.Printf("Restored session %s from %s", sess.ID, remote)
	}

	// Send auth response
	authResp := &pb.Envelope{
		Id: env.Id,
		Payload: &pb.Envelope_AuthResp{
			AuthResp: &pb.AuthResponse{Ok: true, SessionId: sess.ID},
		},
	}
	if err := w.Write(authResp); err != nil {
		log.Printf("Failed to send auth response to %s: %v", remote, err)
		conn.Close()
		return
	}
	log.Printf("Sent auth response to %s", remote)

	// Start writer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for env := range sess.sendCh {
			if err := w.Write(env); err != nil {
				return
			}
		}
	}()

	// Read loop
	for {
		env, err := w.Read()
		if err != nil {
			break
		}
		srv.handle(sess, env)
	}

	// Disconnect
	sess.MarkLost()
	log.Printf("Session %s lost", sess.ID)
	<-done
}

func (srv *Server) handle(sess *Session, env *pb.Envelope) {
	switch p := env.Payload.(type) {
	case *pb.Envelope_Monitor:
		srv.handleMonitor(sess, env.Id, p.Monitor)
	case *pb.Envelope_Unmonitor:
		srv.handleUnmonitor(sess, env.Id, p.Unmonitor)
	case *pb.Envelope_ListTargets:
		srv.handleListTargets(sess, env.Id, p.ListTargets)
	case *pb.Envelope_GetTarget:
		srv.handleGetTarget(sess, env.Id, p.GetTarget)
	case *pb.Envelope_GetHistory:
		srv.handleGetHistory(sess, env.Id, p.GetHistory)
	case *pb.Envelope_Subscribe:
		srv.handleSubscribe(sess, env.Id, p.Subscribe)
	case *pb.Envelope_Unsubscribe:
		srv.handleUnsubscribe(sess, env.Id, p.Unsubscribe)
	// NEW handlers
	case *pb.Envelope_GetServerStatus:
		srv.handleGetServerStatus(sess, env.Id)
	case *pb.Envelope_GetSessionInfo:
		srv.handleGetSessionInfo(sess, env.Id)
	case *pb.Envelope_UpdateTarget:
		srv.handleUpdateTarget(sess, env.Id, p.UpdateTarget)
	case *pb.Envelope_GetConfig:
		srv.handleGetConfig(sess, env.Id)
	case *pb.Envelope_SetConfig:
		srv.handleSetConfig(sess, env.Id, p.SetConfig)
	default:
		sess.Send(wire.NewError(env.Id, wire.ErrInvalidRequest, "unknown request"))
	}
}

func (srv *Server) handleMonitor(sess *Session, id uint64, req *pb.MonitorRequest) {
	if req.Host == "" || req.Oid == "" {
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "host and oid required"))
		return
	}

	// Validate SNMP config
	if err := validateSNMPConfig(req.Snmp); err != nil {
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, err.Error()))
		return
	}

	// Check minimum interval
	srv.runtimeMu.RLock()
	minInterval := srv.minIntervalMs
	srv.runtimeMu.RUnlock()

	if req.IntervalMs > 0 && req.IntervalMs < minInterval {
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest,
			fmt.Sprintf("interval_ms must be >= %d", minInterval)))
		return
	}

	port := uint16(req.Port)
	if port == 0 {
		port = 161
	}
	key := fmt.Sprintf("%s:%d/%s", req.Host, port, req.Oid)

	srv.mu.Lock()

	// Check existing
	if existingID, ok := srv.targetIndex[key]; ok {
		t := srv.targets[existingID]
		t.AddOwner(sess.ID)
		srv.mu.Unlock()

		sess.Send(&pb.Envelope{
			Id: id,
			Payload: &pb.Envelope_MonitorResp{
				MonitorResp: &pb.MonitorResponse{TargetId: existingID, Created: false},
			},
		})
		log.Printf("Session %s joined target %s", sess.ID, existingID)
		return
	}

	// Apply defaults
	srv.runtimeMu.RLock()
	if req.BufferSize == 0 {
		req.BufferSize = srv.defaultBufferSize
	}
	srv.runtimeMu.RUnlock()

	// Create new
	targetID := srv.genID()
	t := NewTarget(targetID, req, srv.cfg.SNMP.BufferSize)
	t.AddOwner(sess.ID)

	srv.targets[targetID] = t
	srv.targetIndex[key] = targetID
	srv.mu.Unlock()

	// Add to scheduler (outside lock)
	srv.poller.AddTarget(targetID, t.IntervalMs)

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_MonitorResp{
			MonitorResp: &pb.MonitorResponse{TargetId: targetID, Created: true},
		},
	})
	log.Printf("Session %s created target %s (%s)", sess.ID, targetID, key)
}

func validateSNMPConfig(cfg *pb.SNMPConfig) error {
	if cfg == nil {
		return nil
	}

	if v3 := cfg.GetV3(); v3 != nil {
		if v3.SecurityName == "" {
			return fmt.Errorf("SNMPv3 requires security_name")
		}

		switch v3.SecurityLevel {
		case pb.SecurityLevel_SECURITY_LEVEL_NO_AUTH_NO_PRIV:
			// OK
		case pb.SecurityLevel_SECURITY_LEVEL_AUTH_NO_PRIV:
			if v3.AuthProtocol == pb.AuthProtocol_AUTH_PROTOCOL_UNSPECIFIED {
				return fmt.Errorf("authNoPriv requires auth_protocol")
			}
			if v3.AuthPassword == "" {
				return fmt.Errorf("authNoPriv requires auth_password")
			}
		case pb.SecurityLevel_SECURITY_LEVEL_AUTH_PRIV:
			if v3.AuthProtocol == pb.AuthProtocol_AUTH_PROTOCOL_UNSPECIFIED {
				return fmt.Errorf("authPriv requires auth_protocol")
			}
			if v3.AuthPassword == "" {
				return fmt.Errorf("authPriv requires auth_password")
			}
			if v3.PrivProtocol == pb.PrivProtocol_PRIV_PROTOCOL_UNSPECIFIED {
				return fmt.Errorf("authPriv requires priv_protocol")
			}
			if v3.PrivPassword == "" {
				return fmt.Errorf("authPriv requires priv_password")
			}
		case pb.SecurityLevel_SECURITY_LEVEL_UNSPECIFIED:
			return fmt.Errorf("SNMPv3 requires security_level")
		}
	}

	return nil
}

func (srv *Server) handleUnmonitor(sess *Session, id uint64, req *pb.UnmonitorRequest) {
	srv.mu.Lock()

	t, ok := srv.targets[req.TargetId]
	if !ok {
		srv.mu.Unlock()
		sess.Send(wire.NewError(id, wire.ErrTargetNotFound, "target not found"))
		return
	}

	t.RemoveOwner(sess.ID)
	sess.Unsubscribe(req.TargetId)

	shouldRemove := !t.HasOwners()
	if shouldRemove {
		delete(srv.targets, req.TargetId)
		delete(srv.targetIndex, t.Key())
		log.Printf("Removed target %s (no owners)", req.TargetId)
	}
	srv.mu.Unlock()

	if shouldRemove {
		srv.poller.RemoveTarget(req.TargetId)
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_UnmonitorResp{
			UnmonitorResp: &pb.UnmonitorResponse{Ok: true},
		},
	})
}

func (srv *Server) handleListTargets(sess *Session, id uint64, req *pb.ListTargetsRequest) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	var targets []*pb.Target
	for _, t := range srv.targets {
		if req.FilterHost != "" && t.Host != req.FilterHost {
			continue
		}
		targets = append(targets, t.ToProto())
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_ListTargetsResp{
			ListTargetsResp: &pb.ListTargetsResponse{Targets: targets},
		},
	})
}

func (srv *Server) handleGetTarget(sess *Session, id uint64, req *pb.GetTargetRequest) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	t, ok := srv.targets[req.TargetId]
	if !ok {
		sess.Send(wire.NewError(id, wire.ErrTargetNotFound, "target not found"))
		return
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_GetTargetResp{
			GetTargetResp: &pb.GetTargetResponse{Target: t.ToProto()},
		},
	})
}

func (srv *Server) handleGetHistory(sess *Session, id uint64, req *pb.GetHistoryRequest) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	n := int(req.LastN)
	if n == 0 {
		n = 100
	}

	var history []*pb.TargetHistory
	for _, targetID := range req.TargetIds {
		t, ok := srv.targets[targetID]
		if !ok {
			continue
		}

		samples := t.ReadLastN(n)
		pbSamples := make([]*pb.Sample, len(samples))
		for i, s := range samples {
			pbSamples[i] = &pb.Sample{
				TargetId:    targetID,
				TimestampMs: s.TimestampMs,
				Counter:     s.Counter,
				Text:        s.Text,
				Valid:       s.Valid,
				Error:       s.Error,
				PollMs:      s.PollMs,
			}
		}

		history = append(history, &pb.TargetHistory{
			TargetId: targetID,
			Samples:  pbSamples,
		})
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_GetHistoryResp{
			GetHistoryResp: &pb.GetHistoryResponse{History: history},
		},
	})
}

func (srv *Server) handleSubscribe(sess *Session, id uint64, req *pb.SubscribeRequest) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	var subscribed []string
	for _, targetID := range req.TargetIds {
		_, ok := srv.targets[targetID]
		if !ok {
			continue
		}
		sess.Subscribe(targetID)
		subscribed = append(subscribed, targetID)
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_SubscribeResp{
			SubscribeResp: &pb.SubscribeResponse{Ok: len(subscribed) > 0, Subscribed: subscribed},
		},
	})
}

func (srv *Server) handleUnsubscribe(sess *Session, id uint64, req *pb.UnsubscribeRequest) {
	targetIDs := req.TargetIds
	if len(targetIDs) == 0 {
		targetIDs = sess.GetSubscriptions()
	}

	for _, targetID := range targetIDs {
		sess.Unsubscribe(targetID)
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_UnsubscribeResp{
			UnsubscribeResp: &pb.UnsubscribeResponse{Ok: true},
		},
	})
}

// ============================================================================
// NEW: Status Handlers
// ============================================================================

func (srv *Server) handleGetServerStatus(sess *Session, id uint64) {
	srv.mu.RLock()

	var sessActive, sessLost int
	for _, s := range srv.sessions {
		if s.IsLost() {
			sessLost++
		} else {
			sessActive++
		}
	}

	var targetsPolling, targetsUnreachable int
	for _, t := range srv.targets {
		t.mu.RLock()
		switch t.State {
		case "polling":
			targetsPolling++
		case "unreachable":
			targetsUnreachable++
		}
		t.mu.RUnlock()
	}

	targetsTotal := len(srv.targets)
	srv.mu.RUnlock()

	// Get poller stats
	heapSize, queueUsed := srv.poller.Stats()

	resp := &pb.GetServerStatusResponse{
		Version:             Version,
		UptimeMs:            time.Since(srv.startedAt).Milliseconds(),
		StartedAtMs:         srv.startedAt.UnixMilli(),
		SessionsActive:      int32(sessActive),
		SessionsLost:        int32(sessLost),
		TargetsTotal:        int32(targetsTotal),
		TargetsPolling:      int32(targetsPolling),
		TargetsUnreachable:  int32(targetsUnreachable),
		PollerWorkers:       int32(srv.cfg.Poller.Workers),
		PollerQueueUsed:     int32(queueUsed),
		PollerQueueCapacity: int32(srv.cfg.Poller.QueueSize),
		PollerHeapSize:      int32(heapSize),
		PollsTotal:          srv.pollsTotal.Load(),
		PollsSuccess:        srv.pollsOK.Load(),
		PollsFailed:         srv.pollsFailed.Load(),
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_GetServerStatusResp{
			GetServerStatusResp: resp,
		},
	})
}

func (srv *Server) handleGetSessionInfo(sess *Session, id uint64) {
	srv.mu.RLock()
	var owned []string
	for targetID, t := range srv.targets {
		t.mu.RLock()
		if t.Owners[sess.ID] {
			owned = append(owned, targetID)
		}
		t.mu.RUnlock()
	}
	srv.mu.RUnlock()

	subscribed := sess.GetSubscriptions()

	sess.mu.RLock()
	createdAt := sess.CreatedAt.UnixMilli()
	sess.mu.RUnlock()

	resp := &pb.GetSessionInfoResponse{
		SessionId:         sess.ID,
		TokenId:           sess.TokenID,
		CreatedAtMs:       createdAt,
		ConnectedAtMs:     createdAt,
		OwnedTargets:      owned,
		SubscribedTargets: subscribed,
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_GetSessionInfoResp{
			GetSessionInfoResp: resp,
		},
	})
}

func (srv *Server) handleUpdateTarget(sess *Session, id uint64, req *pb.UpdateTargetRequest) {
	srv.mu.Lock()
	t, ok := srv.targets[req.TargetId]
	if !ok {
		srv.mu.Unlock()
		sess.Send(wire.NewError(id, wire.ErrTargetNotFound, "target not found"))
		return
	}

	// Check ownership
	t.mu.Lock()
	if !t.Owners[sess.ID] {
		t.mu.Unlock()
		srv.mu.Unlock()
		sess.Send(wire.NewError(id, wire.ErrInternal, "not owner of target"))
		return
	}
	t.mu.Unlock()
	srv.mu.Unlock()

	var changed bool
	var newIntervalMs uint32
	var changes []string

	// Update interval (0 = not set)
	if req.GetIntervalMs() > 0 {
		newInterval := req.GetIntervalMs()

		srv.runtimeMu.RLock()
		minInterval := srv.minIntervalMs
		srv.runtimeMu.RUnlock()

		if newInterval < minInterval {
			sess.Send(wire.NewError(id, wire.ErrInvalidRequest,
				fmt.Sprintf("interval_ms must be >= %d", minInterval)))
			return
		}

		t.mu.Lock()
		t.IntervalMs = newInterval
		t.mu.Unlock()
		newIntervalMs = newInterval
		changed = true
		changes = append(changes, fmt.Sprintf("interval=%dms", newInterval))
	}

	// Update timeout (0 = not set)
	if req.GetTimeoutMs() > 0 {
		t.mu.Lock()
		if t.SNMP == nil {
			t.SNMP = &pb.SNMPConfig{}
		}
		t.SNMP.TimeoutMs = req.GetTimeoutMs()
		t.mu.Unlock()
		changed = true
		changes = append(changes, fmt.Sprintf("timeout=%dms", req.GetTimeoutMs()))
	}

	// Update retries (0 = not set)
	if req.GetRetries() > 0 {
		t.mu.Lock()
		if t.SNMP == nil {
			t.SNMP = &pb.SNMPConfig{}
		}
		t.SNMP.Retries = req.GetRetries()
		t.mu.Unlock()
		changed = true
		changes = append(changes, fmt.Sprintf("retries=%d", req.GetRetries()))
	}

	// Update buffer size (0 = not set)
	if req.GetBufferSize() > 0 {
		newSize := int(req.GetBufferSize())
		t.ResizeBuffer(newSize)
		changed = true
		changes = append(changes, fmt.Sprintf("buffer=%d", newSize))
	}

	// Update scheduler if interval changed
	if newIntervalMs > 0 {
		srv.poller.UpdateInterval(req.TargetId, newIntervalMs)
	}

	msg := "no changes"
	if changed {
		msg = "updated: " + strings.Join(changes, ", ")
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_UpdateTargetResp{
			UpdateTargetResp: &pb.UpdateTargetResponse{
				Ok:      true,
				Target:  t.ToProto(),
				Message: msg,
			},
		},
	})
}

func (srv *Server) handleGetConfig(sess *Session, id uint64) {
	srv.runtimeMu.RLock()
	cfg := &pb.RuntimeConfig{
		DefaultTimeoutMs:   srv.defaultTimeoutMs,
		DefaultRetries:     srv.defaultRetries,
		DefaultBufferSize:  srv.defaultBufferSize,
		MinIntervalMs:      srv.minIntervalMs,
		PollerWorkers:      int32(srv.cfg.Poller.Workers),
		PollerQueueSize:    int32(srv.cfg.Poller.QueueSize),
		ReconnectWindowSec: uint32(srv.cfg.ReconnectWindow().Seconds()),
	}
	srv.runtimeMu.RUnlock()

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_GetConfigResp{
			GetConfigResp: &pb.GetConfigResponse{Config: cfg},
		},
	})
}

func (srv *Server) handleSetConfig(sess *Session, id uint64, req *pb.SetConfigRequest) {
	srv.runtimeMu.Lock()

	// Use getter methods - 0 means "not set"
	if req.GetDefaultTimeoutMs() > 0 {
		srv.defaultTimeoutMs = req.GetDefaultTimeoutMs()
	}
	if req.GetDefaultRetries() > 0 {
		srv.defaultRetries = req.GetDefaultRetries()
	}
	if req.GetDefaultBufferSize() > 0 {
		srv.defaultBufferSize = req.GetDefaultBufferSize()
	}
	if req.GetMinIntervalMs() > 0 {
		srv.minIntervalMs = req.GetMinIntervalMs()
	}

	cfg := &pb.RuntimeConfig{
		DefaultTimeoutMs:   srv.defaultTimeoutMs,
		DefaultRetries:     srv.defaultRetries,
		DefaultBufferSize:  srv.defaultBufferSize,
		MinIntervalMs:      srv.minIntervalMs,
		PollerWorkers:      int32(srv.cfg.Poller.Workers),
		PollerQueueSize:    int32(srv.cfg.Poller.QueueSize),
		ReconnectWindowSec: uint32(srv.cfg.ReconnectWindow().Seconds()),
	}
	srv.runtimeMu.Unlock()

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_SetConfigResp{
			SetConfigResp: &pb.SetConfigResponse{
				Ok:      true,
				Config:  cfg,
				Message: "config updated",
			},
		},
	})
}

// ============================================================================
// Helpers
// ============================================================================

func (srv *Server) validateToken(token string) (string, bool) {
	for _, t := range srv.cfg.Auth.Tokens {
		if subtle.ConstantTimeCompare([]byte(t.Token), []byte(token)) == 1 {
			return t.ID, true
		}
	}
	return "", false
}

func (srv *Server) tryRestore(tokenID string, conn net.Conn, w *wire.Conn) *Session {
	srv.mu.Lock()
	defer srv.mu.Unlock()

	for _, sess := range srv.sessions {
		if sess.IsLost() && sess.TokenID == tokenID {
			sess.mu.Lock()
			sess.Conn = conn
			sess.Wire = w
			sess.LostAt = nil
			sess.sendCh = make(chan *pb.Envelope, 1000)
			sess.mu.Unlock()
			return sess
		}
	}
	return nil
}

func (srv *Server) cleanup() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			srv.cleanupSessions()
			srv.cleanupTargets()
		case <-srv.shutdown:
			return
		}
	}
}

func (srv *Server) cleanupSessions() {
	window := srv.cfg.ReconnectWindow()

	srv.mu.Lock()
	defer srv.mu.Unlock()

	for id, sess := range srv.sessions {
		if sess.IsLost() && sess.LostDuration() > window {
			for _, t := range srv.targets {
				t.RemoveOwner(id)
			}
			delete(srv.sessions, id)
			log.Printf("Removed expired session %s", id)
		}
	}
}

func (srv *Server) cleanupTargets() {
	srv.mu.Lock()

	var toRemove []string
	for id, t := range srv.targets {
		if !t.HasOwners() {
			toRemove = append(toRemove, id)
			delete(srv.targets, id)
			delete(srv.targetIndex, t.Key())
			log.Printf("Removed orphan target %s", id)
		}
	}
	srv.mu.Unlock()

	for _, id := range toRemove {
		srv.poller.RemoveTarget(id)
	}
}

func (srv *Server) genID() string {
	return uuid.New().String()[:8]
}
