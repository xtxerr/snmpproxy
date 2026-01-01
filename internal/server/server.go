// Package server implements the SNMP proxy server.
package server

import (
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	pb "github.com/xtxerr/snmpproxy/internal/proto"
	"github.com/xtxerr/snmpproxy/config"
	"github.com/xtxerr/snmpproxy/internal/wire"
)

// Server is the SNMP proxy server.
type Server struct {
	cfg      *config.Server
	listener net.Listener
	poller   *Poller

	mu          sync.RWMutex
	sessions    map[string]*Session
	targets     map[string]*Target
	targetIndex map[string]string // "host:port/oid" -> targetID

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
		cfg:         cfg,
		sessions:    make(map[string]*Session),
		targets:     make(map[string]*Target),
		targetIndex: make(map[string]string),
		shutdown:    make(chan struct{}),
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

	port := uint16(req.Port)
	if port == 0 {
		port = 161
	}
	key := fmt.Sprintf("%s:%d/%s", req.Host, port, req.Oid)

	srv.mu.Lock()
	defer srv.mu.Unlock()

	// Check existing
	if existingID, ok := srv.targetIndex[key]; ok {
		t := srv.targets[existingID]
		t.AddSubscriber(sess.ID)
                sess.Subscribe(existingID)
		sess.Send(&pb.Envelope{
			Id: id,
			Payload: &pb.Envelope_MonitorResp{
				MonitorResp: &pb.MonitorResponse{TargetId: existingID, Created: false},
			},
		})
		log.Printf("Session %s joined target %s", sess.ID, existingID)
		return
	}

	// Create new
	targetID := srv.genID()
	t := NewTarget(targetID, req, srv.cfg.SNMP.BufferSize)
	t.AddSubscriber(sess.ID)
        sess.Subscribe(targetID)

	srv.targets[targetID] = t
	srv.targetIndex[key] = targetID

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
		return nil // Will use defaults
	}

	if v3 := cfg.GetV3(); v3 != nil {
		if v3.SecurityName == "" {
			return fmt.Errorf("SNMPv3 requires security_name")
		}

		switch v3.SecurityLevel {
		case pb.SecurityLevel_SECURITY_LEVEL_NO_AUTH_NO_PRIV:
			// No credentials required
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
	defer srv.mu.Unlock()

	t, ok := srv.targets[req.TargetId]
	if !ok {
		sess.Send(wire.NewError(id, wire.ErrTargetNotFound, "target not found"))
		return
	}

	t.RemoveSubscriber(sess.ID)
	sess.Unsubscribe(req.TargetId)

	// Remove target if no subscribers
	if !t.HasSubscribers() {
		delete(srv.targets, req.TargetId)
		delete(srv.targetIndex, t.Key())
		log.Printf("Removed target %s (no subscribers)", req.TargetId)
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
	srv.mu.Lock()
	defer srv.mu.Unlock()

	var subscribed []string
	for _, targetID := range req.TargetIds {
		t, ok := srv.targets[targetID]
		if !ok {
			continue
		}
		sess.Subscribe(targetID)
		t.AddSubscriber(sess.ID)
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
	srv.mu.Lock()
	defer srv.mu.Unlock()

	targetIDs := req.TargetIds
	if len(targetIDs) == 0 {
		targetIDs = sess.GetSubscriptions()
	}

	for _, targetID := range targetIDs {
		sess.Unsubscribe(targetID)
		if t, ok := srv.targets[targetID]; ok {
			t.RemoveSubscriber(sess.ID)
		}
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_UnsubscribeResp{
			UnsubscribeResp: &pb.UnsubscribeResponse{Ok: true},
		},
	})
}

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
			for targetID := range sess.subscriptions {
				if t, ok := srv.targets[targetID]; ok {
					t.RemoveSubscriber(id)
				}
			}
			delete(srv.sessions, id)
			log.Printf("Removed expired session %s", id)
		}
	}
}

func (srv *Server) cleanupTargets() {
	srv.mu.Lock()
	defer srv.mu.Unlock()

	for id, t := range srv.targets {
		if !t.HasSubscribers() {
			delete(srv.targets, id)
			delete(srv.targetIndex, t.Key())
			log.Printf("Removed orphan target %s", id)
		}
	}
}

func (srv *Server) genID() string {
	return uuid.New().String()[:8]
}
