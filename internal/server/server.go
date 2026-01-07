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

// Version is set at build time via ldflags.
var Version = "dev"

// Server is the SNMP proxy server.
type Server struct {
	cfg      *config.Config
	listener net.Listener
	poller   *Poller
	indices  *Indices

	mu          sync.RWMutex
	sessions    map[string]*Session
	targets     map[string]*Target
	targetIndex map[string]string // "host:port/oid" -> targetID

	// Runtime config
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

	subscriptions map[string]bool
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

// Subscribe adds a target subscription.
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

// IsSubscribed checks if subscribed to a target.
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

// SubscriptionCount returns the number of subscriptions.
func (s *Session) SubscriptionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscriptions)
}

// Send queues an envelope for sending.
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
func New(cfg *config.Config) *Server {
	srv := &Server{
		cfg:               cfg,
		sessions:          make(map[string]*Session),
		targets:           make(map[string]*Target),
		targetIndex:       make(map[string]string),
		indices:           NewIndices(),
		shutdown:          make(chan struct{}),
		startedAt:         time.Now(),
		defaultTimeoutMs:  cfg.SNMP.TimeoutMs,
		defaultRetries:    cfg.SNMP.Retries,
		defaultBufferSize: cfg.SNMP.BufferSize,
		minIntervalMs:     100,
	}
	srv.poller = NewPoller(srv, cfg.Poller.Workers, cfg.Poller.QueueSize)
	return srv
}

// Run starts the server.
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
		ln, err = tls.Listen("tcp", srv.cfg.Server.Listen, tlsCfg)
		if err != nil {
			return fmt.Errorf("TLS listen: %w", err)
		}
		log.Printf("Listening on %s (TLS)", srv.cfg.Server.Listen)
	} else {
		ln, err = net.Listen("tcp", srv.cfg.Server.Listen)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		log.Printf("Listening on %s (no TLS)", srv.cfg.Server.Listen)
	}

	srv.listener = ln

	// Load config targets
	srv.loadConfigTargets()

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

// loadConfigTargets loads targets defined in the config file.
func (srv *Server) loadConfigTargets() {
	for _, tc := range srv.cfg.Targets {
		t := srv.createTargetFromConfig(&tc)
		if t == nil {
			continue
		}

		srv.mu.Lock()
		srv.targets[t.ID] = t
		srv.targetIndex[t.Key()] = t.ID
		srv.mu.Unlock()

		srv.indices.Add(t.ID, t.State, t.Protocol, t.Host, t.Tags)
		srv.poller.AddTarget(t.ID, t.IntervalMs)

		log.Printf("Loaded config target: %s (%s)", t.ID, t.Key())
	}
}

// createTargetFromConfig creates a Target from a config.TargetConfig.
func (srv *Server) createTargetFromConfig(tc *config.TargetConfig) *Target {
	t := &Target{
		ID:          tc.ID,
		Description: tc.Description,
		Tags:        tc.Tags,
		Persistent:  true,
		State:       "polling",
		Owners:      map[string]bool{"$config": true},
		CreatedAt:   time.Now(),
		PollMsMin:   -1,
	}

	t.IntervalMs = tc.IntervalMs
	if t.IntervalMs == 0 {
		t.IntervalMs = 1000
	}

	bufSize := int(tc.BufferSize)
	if bufSize == 0 {
		bufSize = int(srv.cfg.SNMP.BufferSize)
	}
	t.bufSize = bufSize
	t.buffer = make([]Sample, bufSize)

	if tc.SNMP != nil {
		t.Protocol = "snmp"
		t.Host = tc.SNMP.Host
		t.Port = tc.SNMP.Port
		if t.Port == 0 {
			t.Port = 161
		}
		t.OID = tc.SNMP.OID
		t.SNMP = srv.snmpConfigToProto(tc.SNMP)
	}

	return t
}

// snmpConfigToProto converts config.SNMPTargetConfig to pb.SNMPTargetConfig.
func (srv *Server) snmpConfigToProto(c *config.SNMPTargetConfig) *pb.SNMPTargetConfig {
	cfg := &pb.SNMPTargetConfig{
		Host:      c.Host,
		Port:      uint32(c.Port),
		Oid:       c.OID,
		TimeoutMs: c.TimeoutMs,
		Retries:   c.Retries,
	}

	if c.IsV3() {
		cfg.Version = &pb.SNMPTargetConfig_V3{
			V3: &pb.SNMPv3Config{
				SecurityName:  c.SecurityName,
				SecurityLevel: c.SecurityLevel,
				AuthProtocol:  c.AuthProtocol,
				AuthPassword:  c.AuthPassword,
				PrivProtocol:  c.PrivProtocol,
				PrivPassword:  c.PrivPassword,
				ContextName:   c.ContextName,
			},
		}
	} else {
		community := c.Community
		if community == "" {
			community = "public"
		}
		cfg.Version = &pb.SNMPTargetConfig_V2C{
			V2C: &pb.SNMPv2CConfig{Community: community},
		}
	}

	return cfg
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

// UpdateTargetState updates a target's state and the state index.
func (srv *Server) UpdateTargetState(targetID, oldState, newState string) {
	srv.indices.UpdateState(targetID, oldState, newState)
}

func (srv *Server) handleConn(conn net.Conn) {
	remote := conn.RemoteAddr().String()
	log.Printf("Connection from %s", remote)

	w := wire.NewConn(conn)

	conn.SetDeadline(time.Now().Add(srv.cfg.AuthTimeout()))

	env, err := w.Read()
	if err != nil {
		log.Printf("Auth read error from %s: %v", remote, err)
		conn.Close()
		return
	}

	auth := env.GetAuthReq()
	if auth == nil {
		w.Write(newError(env.Id, ErrNotAuthenticated, "first message must be auth"))
		conn.Close()
		return
	}

	tokenID, ok := srv.validateToken(auth.Token)
	if !ok {
		w.Write(&pb.Envelope{
			Id:      env.Id,
			Payload: &pb.Envelope_AuthResp{AuthResp: &pb.AuthResponse{Ok: false, Message: "invalid token"}},
		})
		conn.Close()
		log.Printf("Auth failed from %s", remote)
		return
	}

	conn.SetDeadline(time.Time{})

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

	if err := w.Write(&pb.Envelope{
		Id:      env.Id,
		Payload: &pb.Envelope_AuthResp{AuthResp: &pb.AuthResponse{Ok: true, SessionId: sess.ID}},
	}); err != nil {
		log.Printf("Failed to send auth response to %s: %v", remote, err)
		conn.Close()
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for env := range sess.sendCh {
			if err := w.Write(env); err != nil {
				return
			}
		}
	}()

	for {
		env, err := w.Read()
		if err != nil {
			break
		}
		srv.handle(sess, env)
	}

	sess.MarkLost()
	log.Printf("Session %s lost", sess.ID)
	<-done
}

func (srv *Server) handle(sess *Session, env *pb.Envelope) {
	switch p := env.Payload.(type) {
	case *pb.Envelope_BrowseReq:
		srv.handleBrowse(sess, env.Id, p.BrowseReq)
	case *pb.Envelope_CreateTargetReq:
		srv.handleCreateTarget(sess, env.Id, p.CreateTargetReq)
	case *pb.Envelope_UpdateTargetReq:
		srv.handleUpdateTarget(sess, env.Id, p.UpdateTargetReq)
	case *pb.Envelope_DeleteTargetReq:
		srv.handleDeleteTarget(sess, env.Id, p.DeleteTargetReq)
	case *pb.Envelope_GetHistoryReq:
		srv.handleGetHistory(sess, env.Id, p.GetHistoryReq)
	case *pb.Envelope_SubscribeReq:
		srv.handleSubscribe(sess, env.Id, p.SubscribeReq)
	case *pb.Envelope_UnsubscribeReq:
		srv.handleUnsubscribe(sess, env.Id, p.UnsubscribeReq)
	case *pb.Envelope_GetConfigReq:
		srv.handleGetConfig(sess, env.Id)
	case *pb.Envelope_SetConfigReq:
		srv.handleSetConfig(sess, env.Id, p.SetConfigReq)
	default:
		sess.Send(newError(env.Id, ErrInvalidRequest, "unknown request"))
	}
}

// ============================================================================
// Target CRUD
// ============================================================================

func (srv *Server) handleCreateTarget(sess *Session, id uint64, req *pb.CreateTargetRequest) {
	snmpCfg := req.GetSnmp()
	if snmpCfg == nil {
		sess.Send(newError(id, ErrInvalidRequest, "protocol config required (snmp)"))
		return
	}

	if snmpCfg.Host == "" || snmpCfg.Oid == "" {
		sess.Send(newError(id, ErrInvalidRequest, "host and oid required"))
		return
	}

	srv.runtimeMu.RLock()
	minInterval := srv.minIntervalMs
	defaultBufSize := srv.defaultBufferSize
	srv.runtimeMu.RUnlock()

	if req.IntervalMs > 0 && req.IntervalMs < minInterval {
		sess.Send(newError(id, ErrInvalidRequest,
			fmt.Sprintf("interval_ms must be >= %d", minInterval)))
		return
	}

	port := uint16(snmpCfg.Port)
	if port == 0 {
		port = 161
	}
	key := fmt.Sprintf("%s:%d/%s", snmpCfg.Host, port, snmpCfg.Oid)

	srv.mu.Lock()

	if existingID, ok := srv.targetIndex[key]; ok {
		t := srv.targets[existingID]
		t.AddOwner(sess.ID)
		srv.mu.Unlock()

		sess.Send(&pb.Envelope{
			Id: id,
			Payload: &pb.Envelope_CreateTargetResp{
				CreateTargetResp: &pb.CreateTargetResponse{
					Ok:       true,
					TargetId: existingID,
					Created:  false,
					Message:  "joined existing target",
				},
			},
		})
		log.Printf("Session %s joined target %s", sess.ID, existingID)
		return
	}

	targetID := req.Id
	if targetID == "" {
		targetID = srv.genID()
	} else if _, exists := srv.targets[targetID]; exists {
		srv.mu.Unlock()
		sess.Send(newError(id, ErrInvalidRequest, "target ID already exists"))
		return
	}

	if req.BufferSize == 0 {
		req.BufferSize = defaultBufSize
	}

	t := NewTarget(targetID, req, srv.cfg.SNMP.BufferSize)
	t.AddOwner(sess.ID)

	srv.targets[targetID] = t
	srv.targetIndex[key] = targetID
	srv.mu.Unlock()

	srv.indices.Add(targetID, t.State, t.Protocol, t.Host, t.Tags)
	srv.poller.AddTarget(targetID, t.IntervalMs)

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_CreateTargetResp{
			CreateTargetResp: &pb.CreateTargetResponse{
				Ok:       true,
				TargetId: targetID,
				Created:  true,
			},
		},
	})
	log.Printf("Session %s created target %s (%s)", sess.ID, targetID, key)
}

func (srv *Server) handleUpdateTarget(sess *Session, id uint64, req *pb.UpdateTargetRequest) {
	srv.mu.Lock()
	t, ok := srv.targets[req.TargetId]
	if !ok {
		srv.mu.Unlock()
		sess.Send(newError(id, ErrInvalidRequest, "target not found"))
		return
	}

	t.mu.RLock()
	isOwner := t.Owners[sess.ID] || t.IsConfigTarget()
	t.mu.RUnlock()

	if !isOwner {
		srv.mu.Unlock()
		sess.Send(newError(id, ErrInvalidRequest, "not owner of target"))
		return
	}

	var changes []string

	if req.Description != nil {
		t.mu.Lock()
		t.Description = *req.Description
		t.mu.Unlock()
		changes = append(changes, "description")
	}

	if req.IntervalMs != nil && *req.IntervalMs > 0 {
		srv.runtimeMu.RLock()
		minInterval := srv.minIntervalMs
		srv.runtimeMu.RUnlock()

		if *req.IntervalMs < minInterval {
			srv.mu.Unlock()
			sess.Send(newError(id, ErrInvalidRequest,
				fmt.Sprintf("interval_ms must be >= %d", minInterval)))
			return
		}

		t.mu.Lock()
		t.IntervalMs = *req.IntervalMs
		t.mu.Unlock()
		srv.poller.UpdateInterval(req.TargetId, *req.IntervalMs)
		changes = append(changes, fmt.Sprintf("interval=%dms", *req.IntervalMs))
	}

	if req.BufferSize != nil && *req.BufferSize > 0 {
		t.ResizeBuffer(int(*req.BufferSize))
		changes = append(changes, fmt.Sprintf("buffer=%d", *req.BufferSize))
	}

	if req.TimeoutMs != nil && *req.TimeoutMs > 0 {
		t.mu.Lock()
		if t.SNMP != nil {
			t.SNMP.TimeoutMs = *req.TimeoutMs
		}
		t.mu.Unlock()
		changes = append(changes, fmt.Sprintf("timeout=%dms", *req.TimeoutMs))
	}

	if req.Retries != nil && *req.Retries > 0 {
		t.mu.Lock()
		if t.SNMP != nil {
			t.SNMP.Retries = *req.Retries
		}
		t.mu.Unlock()
		changes = append(changes, fmt.Sprintf("retries=%d", *req.Retries))
	}

	if req.Persistent != nil {
		t.mu.Lock()
		t.Persistent = *req.Persistent
		t.mu.Unlock()
		changes = append(changes, fmt.Sprintf("persistent=%v", *req.Persistent))
	}

	if len(req.SetTags) > 0 || len(req.AddTags) > 0 || len(req.RemoveTags) > 0 {
		newTags := srv.indices.UpdateTags(req.TargetId, req.AddTags, req.RemoveTags, req.SetTags)
		t.mu.Lock()
		t.Tags = newTags
		t.mu.Unlock()
		changes = append(changes, "tags")
	}

	srv.mu.Unlock()

	msg := "no changes"
	if len(changes) > 0 {
		msg = "updated: " + strings.Join(changes, ", ")
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_UpdateTargetResp{
			UpdateTargetResp: &pb.UpdateTargetResponse{
				Ok:      true,
				Target:  srv.targetToProto(t, false),
				Message: msg,
			},
		},
	})
}

func (srv *Server) handleDeleteTarget(sess *Session, id uint64, req *pb.DeleteTargetRequest) {
	srv.mu.Lock()

	t, ok := srv.targets[req.TargetId]
	if !ok {
		srv.mu.Unlock()
		sess.Send(newError(id, ErrInvalidRequest, "target not found"))
		return
	}

	t.mu.RLock()
	isPersistent := t.Persistent
	isOwner := t.Owners[sess.ID]
	state := t.State
	protocol := t.Protocol
	host := t.Host
	key := t.Key()
	t.mu.RUnlock()

	if isPersistent && !req.Force {
		srv.mu.Unlock()
		sess.Send(newError(id, ErrInvalidRequest, "target is persistent, use force=true to delete"))
		return
	}

	if !isOwner && !req.Force {
		t.RemoveOwner(sess.ID)
		sess.Unsubscribe(req.TargetId)

		shouldRemove := !t.HasOwners() && !t.Persistent
		if shouldRemove {
			delete(srv.targets, req.TargetId)
			delete(srv.targetIndex, key)
			srv.mu.Unlock()
			srv.indices.Remove(req.TargetId, state, protocol, host)
			srv.poller.RemoveTarget(req.TargetId)
			log.Printf("Removed target %s (no owners)", req.TargetId)
		} else {
			srv.mu.Unlock()
		}

		sess.Send(&pb.Envelope{
			Id: id,
			Payload: &pb.Envelope_DeleteTargetResp{
				DeleteTargetResp: &pb.DeleteTargetResponse{Ok: true, Message: "removed ownership"},
			},
		})
		return
	}

	delete(srv.targets, req.TargetId)
	delete(srv.targetIndex, key)
	srv.mu.Unlock()

	srv.indices.Remove(req.TargetId, state, protocol, host)
	srv.poller.RemoveTarget(req.TargetId)
	log.Printf("Deleted target %s (force=%v)", req.TargetId, req.Force)

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_DeleteTargetResp{
			DeleteTargetResp: &pb.DeleteTargetResponse{Ok: true, Message: "deleted"},
		},
	})
}

// ============================================================================
// History
// ============================================================================

func (srv *Server) handleGetHistory(sess *Session, id uint64, req *pb.GetHistoryRequest) {
	srv.mu.RLock()
	t, ok := srv.targets[req.TargetId]
	srv.mu.RUnlock()

	if !ok {
		sess.Send(newError(id, ErrInvalidRequest, "target not found"))
		return
	}

	n := int(req.LastN)
	if n == 0 {
		n = 100
	}

	samples := t.ReadLastN(n)

	t.mu.RLock()
	totalBuffered := t.count
	t.mu.RUnlock()

	pbSamples := make([]*pb.Sample, len(samples))
	for i, s := range samples {
		pbSamples[i] = &pb.Sample{
			TargetId:    req.TargetId,
			TimestampMs: s.TimestampMs,
			Counter:     s.Counter,
			Text:        s.Text,
			Valid:       s.Valid,
			Error:       s.Error,
			PollMs:      s.PollMs,
		}
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_GetHistoryResp{
			GetHistoryResp: &pb.GetHistoryResponse{
				TargetId:      req.TargetId,
				Samples:       pbSamples,
				TotalBuffered: int32(totalBuffered),
			},
		},
	})
}

// ============================================================================
// Subscriptions
// ============================================================================

func (srv *Server) handleSubscribe(sess *Session, id uint64, req *pb.SubscribeRequest) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	subscribed := make(map[string]bool)

	// Subscribe by target IDs
	for _, targetID := range req.TargetIds {
		if _, ok := srv.targets[targetID]; ok {
			sess.Subscribe(targetID)
			subscribed[targetID] = true
		}
	}

	// Subscribe by tags
	for _, tag := range req.Tags {
		targetIDs := srv.indices.QueryByTag(tag)
		for targetID := range targetIDs {
			if _, ok := srv.targets[targetID]; ok {
				sess.Subscribe(targetID)
				subscribed[targetID] = true
			}
		}
	}

	subscribedList := make([]string, 0, len(subscribed))
	for id := range subscribed {
		subscribedList = append(subscribedList, id)
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_SubscribeResp{
			SubscribeResp: &pb.SubscribeResponse{
				Ok:              len(subscribedList) > 0,
				Subscribed:      subscribedList,
				TotalSubscribed: int32(sess.SubscriptionCount()),
			},
		},
	})
}

func (srv *Server) handleUnsubscribe(sess *Session, id uint64, req *pb.UnsubscribeRequest) {
	var unsubscribed []string

	if len(req.TargetIds) == 0 {
		unsubscribed = sess.UnsubscribeAll()
	} else {
		for _, targetID := range req.TargetIds {
			sess.Unsubscribe(targetID)
			unsubscribed = append(unsubscribed, targetID)
		}
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_UnsubscribeResp{
			UnsubscribeResp: &pb.UnsubscribeResponse{
				Ok:              true,
				Unsubscribed:    unsubscribed,
				TotalSubscribed: int32(sess.SubscriptionCount()),
			},
		},
	})
}

// ============================================================================
// Config
// ============================================================================

func (srv *Server) handleGetConfig(sess *Session, id uint64) {
	srv.runtimeMu.RLock()
	cfg := &pb.RuntimeConfig{
		DefaultTimeoutMs:   srv.defaultTimeoutMs,
		DefaultRetries:     srv.defaultRetries,
		DefaultBufferSize:  srv.defaultBufferSize,
		MinIntervalMs:      srv.minIntervalMs,
		PollerWorkers:      int32(srv.cfg.Poller.Workers),
		PollerQueueSize:    int32(srv.cfg.Poller.QueueSize),
		ReconnectWindowSec: uint32(srv.cfg.Session.ReconnectWindowSec),
		Version:            Version,
		UptimeMs:           time.Since(srv.startedAt).Milliseconds(),
		StartedAtMs:        srv.startedAt.UnixMilli(),
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

	if req.DefaultTimeoutMs != nil && *req.DefaultTimeoutMs > 0 {
		srv.defaultTimeoutMs = *req.DefaultTimeoutMs
	}
	if req.DefaultRetries != nil && *req.DefaultRetries > 0 {
		srv.defaultRetries = *req.DefaultRetries
	}
	if req.DefaultBufferSize != nil && *req.DefaultBufferSize > 0 {
		srv.defaultBufferSize = *req.DefaultBufferSize
	}
	if req.MinIntervalMs != nil && *req.MinIntervalMs > 0 {
		srv.minIntervalMs = *req.MinIntervalMs
	}

	cfg := &pb.RuntimeConfig{
		DefaultTimeoutMs:   srv.defaultTimeoutMs,
		DefaultRetries:     srv.defaultRetries,
		DefaultBufferSize:  srv.defaultBufferSize,
		MinIntervalMs:      srv.minIntervalMs,
		PollerWorkers:      int32(srv.cfg.Poller.Workers),
		PollerQueueSize:    int32(srv.cfg.Poller.QueueSize),
		ReconnectWindowSec: uint32(srv.cfg.Session.ReconnectWindowSec),
		Version:            Version,
		UptimeMs:           time.Since(srv.startedAt).Milliseconds(),
		StartedAtMs:        srv.startedAt.UnixMilli(),
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

	var toRemove []struct {
		id, state, protocol, host, key string
	}

	for id, t := range srv.targets {
		t.mu.RLock()
		isPersistent := t.Persistent
		state := t.State
		protocol := t.Protocol
		host := t.Host
		key := t.Key()
		t.mu.RUnlock()

		if isPersistent {
			continue
		}

		if !t.HasOwners() {
			toRemove = append(toRemove, struct {
				id, state, protocol, host, key string
			}{id, state, protocol, host, key})
			delete(srv.targets, id)
			delete(srv.targetIndex, key)
			log.Printf("Removed orphan target %s", id)
		}
	}
	srv.mu.Unlock()

	for _, r := range toRemove {
		srv.indices.Remove(r.id, r.state, r.protocol, r.host)
		srv.poller.RemoveTarget(r.id)
	}
}

func (srv *Server) genID() string {
	return uuid.New().String()[:8]
}
