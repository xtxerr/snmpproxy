// Package server implements the SNMP proxy server.
package server

import (
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sort"
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
	tagIndex *TagIndex

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
		tagIndex:          NewTagIndex(),
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

		srv.targets[t.ID] = t
		srv.targetIndex[t.Key()] = t.ID
		srv.tagIndex.Add(t.ID, t.Tags)
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
		Persistent:  true, // Config targets are always persistent
		State:       "polling",
		Owners:      map[string]bool{"$config": true},
		CreatedAt:   time.Now(),
		PollMsMin:   -1,
	}

	// Interval
	t.IntervalMs = tc.IntervalMs
	if t.IntervalMs == 0 {
		t.IntervalMs = 1000
	}

	// Buffer size
	bufSize := int(tc.BufferSize)
	if bufSize == 0 {
		bufSize = int(srv.cfg.SNMP.BufferSize)
	}
	t.bufSize = bufSize
	t.buffer = make([]Sample, bufSize)

	// SNMP config
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

	auth := env.GetAuthReq()
	if auth == nil {
		w.Write(wire.NewError(env.Id, wire.ErrNotAuthenticated, "first message must be auth"))
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
	if err := w.Write(&pb.Envelope{
		Id:      env.Id,
		Payload: &pb.Envelope_AuthResp{AuthResp: &pb.AuthResponse{Ok: true, SessionId: sess.ID}},
	}); err != nil {
		log.Printf("Failed to send auth response to %s: %v", remote, err)
		conn.Close()
		return
	}

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
		sess.Send(wire.NewError(env.Id, wire.ErrInvalidRequest, "unknown request"))
	}
}

// ============================================================================
// Browse Handler
// ============================================================================

func (srv *Server) handleBrowse(sess *Session, id uint64, req *pb.BrowseRequest) {
	path := strings.Trim(req.Path, "/")
	parts := strings.Split(path, "/")
	if path == "" {
		parts = nil
	}

	var resp *pb.BrowseResponse
	var err error

	if len(parts) == 0 {
		resp = srv.browseRoot()
	} else {
		switch parts[0] {
		case "targets":
			resp, err = srv.browseTargets(parts[1:], req)
		case "server":
			resp, err = srv.browseServer(parts[1:])
		case "session":
			resp, err = srv.browseSession(sess, parts[1:])
		default:
			err = fmt.Errorf("unknown path: %s", parts[0])
		}
	}

	if err != nil {
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, err.Error()))
		return
	}

	sess.Send(&pb.Envelope{Id: id, Payload: &pb.Envelope_BrowseResp{BrowseResp: resp}})
}

func (srv *Server) browseRoot() *pb.BrowseResponse {
	srv.mu.RLock()
	targetCount := int32(len(srv.targets))
	srv.mu.RUnlock()

	return &pb.BrowseResponse{
		Path: "/",
		Nodes: []*pb.BrowseNode{
			{Name: "targets", Type: pb.NodeType_NODE_DIRECTORY, TargetCount: targetCount},
			{Name: "server", Type: pb.NodeType_NODE_DIRECTORY},
			{Name: "session", Type: pb.NodeType_NODE_DIRECTORY},
		},
	}
}

func (srv *Server) browseTargets(parts []string, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	if len(parts) == 0 {
		// /targets - show tag root categories + "all"
		dirs := srv.tagIndex.GetRootCategories()
		nodes := make([]*pb.BrowseNode, 0, len(dirs)+1)

		for _, dir := range dirs {
			nodes = append(nodes, &pb.BrowseNode{
				Name:        dir,
				Type:        pb.NodeType_NODE_DIRECTORY,
				TargetCount: int32(srv.tagIndex.Count(dir)),
			})
		}

		nodes = append(nodes, &pb.BrowseNode{
			Name:        "all",
			Type:        pb.NodeType_NODE_DIRECTORY,
			TargetCount: int32(len(srv.targets)),
		})

		return &pb.BrowseResponse{Path: "targets", Nodes: nodes}, nil
	}

	// /targets/all
	if parts[0] == "all" {
		if len(parts) == 1 {
			return srv.browseAllTargets(req)
		}
		// /targets/all/<target-id>
		return srv.browseSingleTarget(parts[1])
	}

	// Try as direct target ID first (single segment)
	if len(parts) == 1 {
		if t, ok := srv.targets[parts[0]]; ok {
			return srv.targetToResponse("targets/"+parts[0], t)
		}
	}

	// Tag path
	tagPath := strings.Join(parts, "/")

	// Check if last segment is a target ID under a tag path
	if len(parts) > 1 {
		possibleTargetID := parts[len(parts)-1]
		if t, ok := srv.targets[possibleTargetID]; ok {
			parentPath := strings.Join(parts[:len(parts)-1], "/")
			if srv.tagIndex.HasTag(possibleTargetID, parentPath) {
				return srv.targetToResponse("targets/"+tagPath, t)
			}
		}
	}

	// List directory contents
	dirs, targetIDs := srv.tagIndex.ListChildren(tagPath)

	// If no children found, check if it's a leaf tag with targets
	if len(dirs) == 0 && len(targetIDs) == 0 {
		targets := srv.tagIndex.Query(tagPath)
		if len(targets) > 0 {
			nodes := make([]*pb.BrowseNode, 0, len(targets))
			for _, tid := range targets {
				if t, ok := srv.targets[tid]; ok {
					node := &pb.BrowseNode{Name: tid, Type: pb.NodeType_NODE_TARGET}
					if req.LongFormat {
						node.Target = t.ToProto()
					}
					nodes = append(nodes, node)
				}
			}
			return &pb.BrowseResponse{Path: "targets/" + tagPath, Nodes: nodes}, nil
		}
		return nil, fmt.Errorf("path not found: %s", tagPath)
	}

	nodes := make([]*pb.BrowseNode, 0, len(dirs)+len(targetIDs))

	for _, dir := range dirs {
		fullPath := tagPath + "/" + dir
		nodes = append(nodes, &pb.BrowseNode{
			Name:        dir,
			Type:        pb.NodeType_NODE_DIRECTORY,
			TargetCount: int32(srv.tagIndex.Count(fullPath)),
		})
	}

	for _, tid := range targetIDs {
		if t, ok := srv.targets[tid]; ok {
			node := &pb.BrowseNode{Name: tid, Type: pb.NodeType_NODE_TARGET}
			if req.LongFormat {
				node.Target = t.ToProto()
			}
			nodes = append(nodes, node)
		}
	}

	return &pb.BrowseResponse{Path: "targets/" + tagPath, Nodes: nodes}, nil
}

func (srv *Server) browseAllTargets(req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	nodes := make([]*pb.BrowseNode, 0, len(srv.targets))

	ids := make([]string, 0, len(srv.targets))
	for id := range srv.targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		t := srv.targets[id]
		node := &pb.BrowseNode{Name: id, Type: pb.NodeType_NODE_TARGET}
		if req.LongFormat {
			node.Target = t.ToProto()
		}
		nodes = append(nodes, node)
	}

	return &pb.BrowseResponse{Path: "targets/all", Nodes: nodes}, nil
}

func (srv *Server) browseSingleTarget(targetID string) (*pb.BrowseResponse, error) {
	t, ok := srv.targets[targetID]
	if !ok {
		return nil, fmt.Errorf("target not found: %s", targetID)
	}
	return srv.targetToResponse("targets/all/"+targetID, t)
}

func (srv *Server) targetToResponse(path string, t *Target) (*pb.BrowseResponse, error) {
	return &pb.BrowseResponse{
		Path: path,
		Nodes: []*pb.BrowseNode{{
			Name:   t.ID,
			Type:   pb.NodeType_NODE_TARGET,
			Target: t.ToProto(),
		}},
	}, nil
}

func (srv *Server) browseServer(parts []string) (*pb.BrowseResponse, error) {
	if len(parts) == 0 {
		return &pb.BrowseResponse{
			Path: "server",
			Nodes: []*pb.BrowseNode{
				{Name: "status", Type: pb.NodeType_NODE_INFO},
				{Name: "config", Type: pb.NodeType_NODE_INFO},
				{Name: "sessions", Type: pb.NodeType_NODE_DIRECTORY},
			},
		}, nil
	}

	switch parts[0] {
	case "status":
		return srv.browseServerStatus()
	case "config":
		return srv.browseServerConfig()
	case "sessions":
		return srv.browseServerSessions(parts[1:])
	default:
		return nil, fmt.Errorf("unknown server path: %s", parts[0])
	}
}

func (srv *Server) browseServerStatus() (*pb.BrowseResponse, error) {
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

	heapSize, queueUsed := srv.poller.Stats()

	info := map[string]string{
		"version":             Version,
		"uptime":              formatDuration(time.Since(srv.startedAt)),
		"started_at":          srv.startedAt.Format(time.RFC3339),
		"sessions_active":     fmt.Sprintf("%d", sessActive),
		"sessions_lost":       fmt.Sprintf("%d", sessLost),
		"targets_total":       fmt.Sprintf("%d", targetsTotal),
		"targets_polling":     fmt.Sprintf("%d", targetsPolling),
		"targets_unreachable": fmt.Sprintf("%d", targetsUnreachable),
		"poller_workers":      fmt.Sprintf("%d", srv.cfg.Poller.Workers),
		"poller_queue":        fmt.Sprintf("%d/%d", queueUsed, srv.cfg.Poller.QueueSize),
		"poller_heap":         fmt.Sprintf("%d", heapSize),
		"polls_total":         fmt.Sprintf("%d", srv.pollsTotal.Load()),
		"polls_success":       fmt.Sprintf("%d", srv.pollsOK.Load()),
		"polls_failed":        fmt.Sprintf("%d", srv.pollsFailed.Load()),
	}

	return &pb.BrowseResponse{
		Path:  "server/status",
		Nodes: []*pb.BrowseNode{{Name: "status", Type: pb.NodeType_NODE_INFO, Info: info}},
	}, nil
}

func (srv *Server) browseServerConfig() (*pb.BrowseResponse, error) {
	srv.runtimeMu.RLock()
	info := map[string]string{
		"default_timeout_ms":  fmt.Sprintf("%d", srv.defaultTimeoutMs),
		"default_retries":     fmt.Sprintf("%d", srv.defaultRetries),
		"default_buffer_size": fmt.Sprintf("%d", srv.defaultBufferSize),
		"min_interval_ms":     fmt.Sprintf("%d", srv.minIntervalMs),
		"poller_workers":      fmt.Sprintf("%d", srv.cfg.Poller.Workers),
		"poller_queue_size":   fmt.Sprintf("%d", srv.cfg.Poller.QueueSize),
		"reconnect_window":    fmt.Sprintf("%ds", srv.cfg.Session.ReconnectWindowSec),
	}
	srv.runtimeMu.RUnlock()

	return &pb.BrowseResponse{
		Path:  "server/config",
		Nodes: []*pb.BrowseNode{{Name: "config", Type: pb.NodeType_NODE_INFO, Info: info}},
	}, nil
}

func (srv *Server) browseServerSessions(parts []string) (*pb.BrowseResponse, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	if len(parts) == 0 {
		nodes := make([]*pb.BrowseNode, 0, len(srv.sessions))
		for id, sess := range srv.sessions {
			status := "connected"
			if sess.IsLost() {
				status = "lost"
			}
			nodes = append(nodes, &pb.BrowseNode{
				Name: id,
				Type: pb.NodeType_NODE_INFO,
				Info: map[string]string{
					"token_id": sess.TokenID,
					"status":   status,
					"created":  sess.CreatedAt.Format(time.RFC3339),
				},
			})
		}
		return &pb.BrowseResponse{Path: "server/sessions", Nodes: nodes}, nil
	}

	sess, ok := srv.sessions[parts[0]]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", parts[0])
	}

	status := "connected"
	if sess.IsLost() {
		status = "lost"
	}

	info := map[string]string{
		"session_id":    sess.ID,
		"token_id":      sess.TokenID,
		"status":        status,
		"created":       sess.CreatedAt.Format(time.RFC3339),
		"subscriptions": fmt.Sprintf("%d", len(sess.GetSubscriptions())),
	}

	return &pb.BrowseResponse{
		Path:  "server/sessions/" + parts[0],
		Nodes: []*pb.BrowseNode{{Name: parts[0], Type: pb.NodeType_NODE_INFO, Info: info}},
	}, nil
}

func (srv *Server) browseSession(sess *Session, parts []string) (*pb.BrowseResponse, error) {
	if len(parts) == 0 {
		return &pb.BrowseResponse{
			Path: "session",
			Nodes: []*pb.BrowseNode{
				{Name: "info", Type: pb.NodeType_NODE_INFO},
				{Name: "subscriptions", Type: pb.NodeType_NODE_DIRECTORY},
				{Name: "owned", Type: pb.NodeType_NODE_DIRECTORY},
			},
		}, nil
	}

	switch parts[0] {
	case "info":
		return srv.browseSessionInfo(sess)
	case "subscriptions":
		return srv.browseSessionSubscriptions(sess)
	case "owned":
		return srv.browseSessionOwned(sess)
	default:
		return nil, fmt.Errorf("unknown session path: %s", parts[0])
	}
}

func (srv *Server) browseSessionInfo(sess *Session) (*pb.BrowseResponse, error) {
	subs := sess.GetSubscriptions()

	srv.mu.RLock()
	var owned []string
	for tid, t := range srv.targets {
		t.mu.RLock()
		if t.Owners[sess.ID] {
			owned = append(owned, tid)
		}
		t.mu.RUnlock()
	}
	srv.mu.RUnlock()

	info := map[string]string{
		"session_id":    sess.ID,
		"token_id":      sess.TokenID,
		"created":       sess.CreatedAt.Format(time.RFC3339),
		"subscriptions": fmt.Sprintf("%d", len(subs)),
		"owned_targets": fmt.Sprintf("%d", len(owned)),
	}

	return &pb.BrowseResponse{
		Path:  "session/info",
		Nodes: []*pb.BrowseNode{{Name: "info", Type: pb.NodeType_NODE_INFO, Info: info}},
	}, nil
}

func (srv *Server) browseSessionSubscriptions(sess *Session) (*pb.BrowseResponse, error) {
	subs := sess.GetSubscriptions()

	srv.mu.RLock()
	defer srv.mu.RUnlock()

	nodes := make([]*pb.BrowseNode, 0, len(subs))
	for _, tid := range subs {
		if t, ok := srv.targets[tid]; ok {
			nodes = append(nodes, &pb.BrowseNode{
				Name:   tid,
				Type:   pb.NodeType_NODE_TARGET,
				Target: t.ToProto(),
			})
		}
	}

	return &pb.BrowseResponse{Path: "session/subscriptions", Nodes: nodes}, nil
}

func (srv *Server) browseSessionOwned(sess *Session) (*pb.BrowseResponse, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	var nodes []*pb.BrowseNode
	for tid, t := range srv.targets {
		t.mu.RLock()
		isOwner := t.Owners[sess.ID]
		t.mu.RUnlock()
		if isOwner {
			nodes = append(nodes, &pb.BrowseNode{
				Name:   tid,
				Type:   pb.NodeType_NODE_TARGET,
				Target: t.ToProto(),
			})
		}
	}

	return &pb.BrowseResponse{Path: "session/owned", Nodes: nodes}, nil
}

// ============================================================================
// Target CRUD Handlers
// ============================================================================

func (srv *Server) handleCreateTarget(sess *Session, id uint64, req *pb.CreateTargetRequest) {
	// Validate protocol config
	snmpCfg := req.GetSnmp()
	if snmpCfg == nil {
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "protocol config required (snmp)"))
		return
	}

	if snmpCfg.Host == "" || snmpCfg.Oid == "" {
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "host and oid required"))
		return
	}

	// Validate interval
	srv.runtimeMu.RLock()
	minInterval := srv.minIntervalMs
	defaultBufSize := srv.defaultBufferSize
	srv.runtimeMu.RUnlock()

	if req.IntervalMs > 0 && req.IntervalMs < minInterval {
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest,
			fmt.Sprintf("interval_ms must be >= %d", minInterval)))
		return
	}

	// Build key for deduplication
	port := uint16(snmpCfg.Port)
	if port == 0 {
		port = 161
	}
	key := fmt.Sprintf("%s:%d/%s", snmpCfg.Host, port, snmpCfg.Oid)

	srv.mu.Lock()

	// Check for existing target by key
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

	// Check for ID collision
	targetID := req.Id
	if targetID == "" {
		targetID = srv.genID()
	} else if _, exists := srv.targets[targetID]; exists {
		srv.mu.Unlock()
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "target ID already exists"))
		return
	}

	// Apply buffer size default
	if req.BufferSize == 0 {
		req.BufferSize = defaultBufSize
	}

	// Create target
	t := NewTarget(targetID, req, srv.cfg.SNMP.BufferSize)
	t.AddOwner(sess.ID)

	srv.targets[targetID] = t
	srv.targetIndex[key] = targetID
	srv.tagIndex.Add(targetID, t.Tags)
	srv.mu.Unlock()

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
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "target not found"))
		return
	}

	// Check ownership (config targets can be updated by anyone)
	t.mu.RLock()
	isOwner := t.Owners[sess.ID] || t.IsConfigTarget()
	t.mu.RUnlock()

	if !isOwner {
		srv.mu.Unlock()
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "not owner of target"))
		return
	}

	var changes []string

	// Update description
	if req.Description != nil {
		t.mu.Lock()
		t.Description = *req.Description
		t.mu.Unlock()
		changes = append(changes, "description")
	}

	// Update interval
	if req.IntervalMs != nil && *req.IntervalMs > 0 {
		srv.runtimeMu.RLock()
		minInterval := srv.minIntervalMs
		srv.runtimeMu.RUnlock()

		if *req.IntervalMs < minInterval {
			srv.mu.Unlock()
			sess.Send(wire.NewError(id, wire.ErrInvalidRequest,
				fmt.Sprintf("interval_ms must be >= %d", minInterval)))
			return
		}

		t.mu.Lock()
		t.IntervalMs = *req.IntervalMs
		t.mu.Unlock()
		srv.poller.UpdateInterval(req.TargetId, *req.IntervalMs)
		changes = append(changes, fmt.Sprintf("interval=%dms", *req.IntervalMs))
	}

	// Update buffer size
	if req.BufferSize != nil && *req.BufferSize > 0 {
		t.ResizeBuffer(int(*req.BufferSize))
		changes = append(changes, fmt.Sprintf("buffer=%d", *req.BufferSize))
	}

	// Update timeout
	if req.TimeoutMs != nil && *req.TimeoutMs > 0 {
		t.mu.Lock()
		if t.SNMP != nil {
			t.SNMP.TimeoutMs = *req.TimeoutMs
		}
		t.mu.Unlock()
		changes = append(changes, fmt.Sprintf("timeout=%dms", *req.TimeoutMs))
	}

	// Update retries
	if req.Retries != nil && *req.Retries > 0 {
		t.mu.Lock()
		if t.SNMP != nil {
			t.SNMP.Retries = *req.Retries
		}
		t.mu.Unlock()
		changes = append(changes, fmt.Sprintf("retries=%d", *req.Retries))
	}

	// Update persistent
	if req.Persistent != nil {
		t.mu.Lock()
		t.Persistent = *req.Persistent
		t.mu.Unlock()
		changes = append(changes, fmt.Sprintf("persistent=%v", *req.Persistent))
	}

	// Update tags
	if len(req.SetTags) > 0 || len(req.AddTags) > 0 || len(req.RemoveTags) > 0 {
		newTags := srv.tagIndex.UpdateTags(req.TargetId, req.AddTags, req.RemoveTags, req.SetTags)
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
				Target:  t.ToProto(),
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
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "target not found"))
		return
	}

	t.mu.RLock()
	isPersistent := t.Persistent
	isOwner := t.Owners[sess.ID]
	key := t.Key()
	t.mu.RUnlock()

	// Check if deletion is allowed
	if isPersistent && !req.Force {
		srv.mu.Unlock()
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "target is persistent, use force=true to delete"))
		return
	}

	if !isOwner && !req.Force {
		// Just remove ownership
		t.RemoveOwner(sess.ID)
		sess.Unsubscribe(req.TargetId)

		// Check if target should be removed (no owners and not persistent)
		shouldRemove := !t.HasOwners() && !t.Persistent
		if shouldRemove {
			delete(srv.targets, req.TargetId)
			delete(srv.targetIndex, key)
			srv.tagIndex.Remove(req.TargetId)
			srv.mu.Unlock()
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

	// Force delete or owner deleting
	delete(srv.targets, req.TargetId)
	delete(srv.targetIndex, key)
	srv.tagIndex.Remove(req.TargetId)
	srv.mu.Unlock()

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
// History Handler
// ============================================================================

func (srv *Server) handleGetHistory(sess *Session, id uint64, req *pb.GetHistoryRequest) {
	srv.mu.RLock()
	t, ok := srv.targets[req.TargetId]
	srv.mu.RUnlock()

	if !ok {
		sess.Send(wire.NewError(id, wire.ErrInvalidRequest, "target not found"))
		return
	}

	n := int(req.LastN)
	if n == 0 {
		n = 100
	}

	samples := t.ReadLastN(n)
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
				TargetId: req.TargetId,
				Samples:  pbSamples,
			},
		},
	})
}

// ============================================================================
// Subscription Handlers
// ============================================================================

func (srv *Server) handleSubscribe(sess *Session, id uint64, req *pb.SubscribeRequest) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	var subscribed []string

	// Subscribe by target IDs
	for _, targetID := range req.TargetIds {
		if _, ok := srv.targets[targetID]; ok {
			sess.Subscribe(targetID)
			subscribed = append(subscribed, targetID)
		}
	}

	// Subscribe by tags
	for _, tag := range req.Tags {
		targets := srv.tagIndex.Query(tag)
		for _, targetID := range targets {
			if _, ok := srv.targets[targetID]; ok {
				sess.Subscribe(targetID)
				subscribed = append(subscribed, targetID)
			}
		}
	}

	// Deduplicate
	seen := make(map[string]bool)
	unique := make([]string, 0, len(subscribed))
	for _, s := range subscribed {
		if !seen[s] {
			seen[s] = true
			unique = append(unique, s)
		}
	}

	sess.Send(&pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_SubscribeResp{
			SubscribeResp: &pb.SubscribeResponse{
				Ok:         len(unique) > 0,
				Subscribed: unique,
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
				Ok:           true,
				Unsubscribed: unsubscribed,
			},
		},
	})
}

// ============================================================================
// Config Handlers
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

	var toRemove []string
	for id, t := range srv.targets {
		// Skip persistent targets
		t.mu.RLock()
		isPersistent := t.Persistent
		key := t.Key()
		t.mu.RUnlock()

		if isPersistent {
			continue
		}

		if !t.HasOwners() {
			toRemove = append(toRemove, id)
			delete(srv.targets, id)
			delete(srv.targetIndex, key)
			srv.tagIndex.Remove(id)
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

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
