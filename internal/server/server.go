// Package server implements the SNMP proxy server.
package server

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/xtxerr/snmpproxy/internal/handler"
	"github.com/xtxerr/snmpproxy/internal/manager"
	"github.com/xtxerr/snmpproxy/internal/scheduler"
	"github.com/xtxerr/snmpproxy/internal/store"
	"github.com/xtxerr/snmpproxy/internal/wire"
)

// Version is set at build time.
var Version = "dev"

// Config holds server configuration.
type Config struct {
	// External manager (if provided, DBPath/SecretKeyPath are ignored)
	Manager *manager.Manager

	// Network
	Listen string

	// TLS
	TLSCertFile string
	TLSKeyFile  string

	// Auth
	Tokens []handler.TokenConfig

	// Timeouts
	AuthTimeoutSec     int
	ReconnectWindowSec int

	// Scheduler
	PollerWorkers   int
	PollerQueueSize int

	// Storage (only used if Manager is nil)
	DBPath        string
	SecretKeyPath string

	// SNMP Defaults
	DefaultTimeoutMs  uint32
	DefaultRetries    uint32
	DefaultIntervalMs uint32
	DefaultBufferSize uint32
}

// DefaultConfig returns default configuration.
func DefaultConfig() *Config {
	return &Config{
		Listen:             "0.0.0.0:9161",
		AuthTimeoutSec:     30,
		ReconnectWindowSec: 600,
		PollerWorkers:      100,
		PollerQueueSize:    10000,
		DBPath:             "snmpproxy.db",
		DefaultTimeoutMs:   5000,
		DefaultRetries:     2,
		DefaultIntervalMs:  1000,
		DefaultBufferSize:  3600,
	}
}

// Server is the v2 SNMP proxy server.
type Server struct {
	cfg      *Config
	listener net.Listener

	// Core components
	mgr       *manager.Manager
	scheduler *scheduler.Scheduler
	sessions  *handler.SessionManager
	handler   *handler.Handler

	// Handlers
	nsHandler     *handler.NamespaceHandler
	targetHandler *handler.TargetHandler
	pollerHandler *handler.PollerHandler
	browseHandler *handler.BrowseHandler
	subHandler    *handler.SubscriptionHandler
	statusHandler *handler.StatusHandler
	secretHandler *handler.SecretHandler

	// SNMP poller
	snmpPoller *scheduler.SNMPPoller

	// Control
	shutdown chan struct{}
	wg       sync.WaitGroup
}

// New creates a new server.
func New(cfg *Config) *Server {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Use provided manager or create new
	var mgr *manager.Manager
	var err error

	if cfg.Manager != nil {
		mgr = cfg.Manager
	} else {
		mgr, err = manager.New(&manager.Config{
			DBPath:        cfg.DBPath,
			SecretKeyPath: cfg.SecretKeyPath,
			Version:       Version,
		})
		if err != nil {
			log.Fatalf("Create manager: %v", err)
		}

		// Set server defaults in store
		mgr.Store().UpdateServerConfig(&store.ServerConfig{
			DefaultTimeoutMs:  cfg.DefaultTimeoutMs,
			DefaultRetries:    cfg.DefaultRetries,
			DefaultIntervalMs: cfg.DefaultIntervalMs,
			DefaultBufferSize: cfg.DefaultBufferSize,
		})
	}

	// Create session manager
	sessions := handler.NewSessionManager(&handler.SessionManagerConfig{
		ReconnectWindow: time.Duration(cfg.ReconnectWindowSec) * time.Second,
		AuthTimeout:     time.Duration(cfg.AuthTimeoutSec) * time.Second,
		Tokens:          cfg.Tokens,
	})

	// Create scheduler
	sched := scheduler.New(&scheduler.Config{
		Workers:   cfg.PollerWorkers,
		QueueSize: cfg.PollerQueueSize,
	})

	// Create SNMP poller
	snmpPoller := scheduler.NewSNMPPoller(cfg.DefaultTimeoutMs, cfg.DefaultRetries)

	// Create handler
	h := handler.NewHandler(mgr, sessions)

	srv := &Server{
		cfg:           cfg,
		mgr:           mgr,
		scheduler:     sched,
		sessions:      sessions,
		handler:       h,
		snmpPoller:    snmpPoller,
		nsHandler:     handler.NewNamespaceHandler(h),
		targetHandler: handler.NewTargetHandler(h),
		pollerHandler: handler.NewPollerHandler(h),
		browseHandler: handler.NewBrowseHandler(h),
		subHandler:    handler.NewSubscriptionHandler(h),
		statusHandler: handler.NewStatusHandler(h),
		secretHandler: handler.NewSecretHandler(h),
		shutdown:      make(chan struct{}),
	}

	// Wire up scheduler callbacks
	srv.mgr.Pollers.SetCallbacks(
		srv.onPollerCreated,
		srv.onPollerDeleted,
		srv.onPollerUpdated,
	)

	// Set poll function
	sched.SetPollFunc(srv.executePoll)

	return srv
}

// Run starts the server (blocking).
func (s *Server) Run() error {
	// Load existing data
	log.Println("Loading data from store...")
	if err := s.mgr.Load(); err != nil {
		return fmt.Errorf("load data: %w", err)
	}

	// Start sync manager
	s.mgr.Start()

	// Start scheduler
	s.scheduler.Start()

	// Start result processor
	s.wg.Add(1)
	go s.processResults()

	// Start session cleanup
	s.wg.Add(1)
	go s.cleanupLoop()

	// Schedule enabled pollers
	started := s.scheduleEnabledPollers()
	log.Printf("Scheduled %d enabled pollers", started)

	// Start listener
	var ln net.Listener
	var err error

	if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("load TLS cert: %w", err)
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		ln, err = tls.Listen("tcp", s.cfg.Listen, tlsCfg)
		if err != nil {
			return fmt.Errorf("TLS listen: %w", err)
		}
		log.Printf("Listening on %s (TLS)", s.cfg.Listen)
	} else {
		ln, err = net.Listen("tcp", s.cfg.Listen)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		log.Printf("Listening on %s (no TLS)", s.cfg.Listen)
	}

	s.listener = ln

	// Accept connections
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.shutdown:
				return nil
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}
		go s.handleConn(conn)
	}
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown() {
	log.Println("Shutting down...")
	close(s.shutdown)

	if s.listener != nil {
		s.listener.Close()
	}

	s.scheduler.Stop()
	s.wg.Wait()
	s.mgr.Stop()

	log.Println("Shutdown complete")
}

// handleConn handles a new connection.
func (s *Server) handleConn(conn net.Conn) {
	remote := conn.RemoteAddr().String()
	log.Printf("Connection from %s", remote)

	w := wire.NewConn(conn)

	// Auth with timeout
	conn.SetDeadline(time.Now().Add(s.sessions.AuthTimeout()))

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

	tokenCfg, ok := s.sessions.ValidateToken(auth.Token)
	if !ok {
		s.sendAuthResponse(w, env.Id, false, "", "invalid token")
		conn.Close()
		log.Printf("Auth failed from %s", remote)
		return
	}

	conn.SetDeadline(time.Time{}) // Clear deadline

	// Try restore or create session
	session := s.sessions.TryRestore(tokenCfg.ID, conn, w)
	if session == nil {
		session = s.sessions.CreateSession(tokenCfg.ID, conn, w)
		log.Printf("New session %s from %s (token: %s)", session.ID, remote, tokenCfg.ID)
	} else {
		log.Printf("Restored session %s from %s", session.ID, remote)
	}

	// Send auth response
	if err := s.sendAuthResponse(w, env.Id, true, session.ID, ""); err != nil {
		log.Printf("Failed to send auth response to %s: %v", remote, err)
		conn.Close()
		return
	}

	// Start writer goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		for data := range session.SendChan() {
			if _, err := conn.Write(data); err != nil {
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
		s.handleMessage(session, env)
	}

	// Disconnect
	session.MarkLost()
	log.Printf("Session %s lost", session.ID)
	<-done
}

// scheduleEnabledPollers schedules all enabled pollers.
func (s *Server) scheduleEnabledPollers() int {
	pollers := s.mgr.Pollers.GetEnabledPollers()
	count := 0

	for _, p := range pollers {
		// Get resolved config for interval
		cfg, err := s.mgr.ConfigResolver.Resolve(p.Namespace, p.Target, p.Name)
		if err != nil {
			continue
		}

		key := scheduler.PollerKey{
			Namespace: p.Namespace,
			Target:    p.Target,
			Poller:    p.Name,
		}

		s.scheduler.Add(key, cfg.IntervalMs)

		// Update state
		state := s.mgr.States.Get(p.Namespace, p.Target, p.Name)
		state.Start()
		state.MarkRunning()

		count++
	}

	return count
}

// Scheduler callbacks
func (s *Server) onPollerCreated(namespace, target, poller string, intervalMs uint32) {
	key := scheduler.PollerKey{Namespace: namespace, Target: target, Poller: poller}
	s.scheduler.Add(key, intervalMs)
}

func (s *Server) onPollerDeleted(namespace, target, poller string) {
	key := scheduler.PollerKey{Namespace: namespace, Target: target, Poller: poller}
	s.scheduler.Remove(key)
}

func (s *Server) onPollerUpdated(namespace, target, poller string, intervalMs uint32) {
	key := scheduler.PollerKey{Namespace: namespace, Target: target, Poller: poller}
	s.scheduler.UpdateInterval(key, intervalMs)
}

// executePoll executes a poll for a poller.
func (s *Server) executePoll(key scheduler.PollerKey) scheduler.PollResult {
	// Get poller
	p, err := s.mgr.Pollers.Get(key.Namespace, key.Target, key.Poller)
	if err != nil || p == nil {
		return scheduler.PollResult{
			Key:         key,
			TimestampMs: time.Now().UnixMilli(),
			Success:     false,
			Error:       "poller not found",
		}
	}

	// Parse protocol config
	switch p.Protocol {
	case "snmp":
		cfg, err := scheduler.ParseSNMPConfig(p.ProtocolConfig)
		if err != nil {
			return scheduler.PollResult{
				Key:         key,
				TimestampMs: time.Now().UnixMilli(),
				Success:     false,
				Error:       fmt.Sprintf("parse config: %v", err),
			}
		}

		// Apply resolved config
		resolved, _ := s.mgr.ConfigResolver.Resolve(key.Namespace, key.Target, key.Poller)
		if resolved != nil {
			if cfg.TimeoutMs == 0 {
				cfg.TimeoutMs = resolved.TimeoutMs
			}
			if cfg.Retries == 0 {
				cfg.Retries = resolved.Retries
			}
			// Apply SNMPv2c community from resolved config
			if cfg.Community == "" || cfg.Community == "public" {
				if resolved.Community != "" {
					cfg.Community = resolved.Community
				}
			}
		}

		return s.snmpPoller.Poll(key, cfg)

	default:
		return scheduler.PollResult{
			Key:         key,
			TimestampMs: time.Now().UnixMilli(),
			Success:     false,
			Error:       fmt.Sprintf("unknown protocol: %s", p.Protocol),
		}
	}
}

// processResults processes poll results.
func (s *Server) processResults() {
	defer s.wg.Done()

	for {
		select {
		case result := <-s.scheduler.Results():
			s.handlePollResult(result)
		case <-s.shutdown:
			return
		}
	}
}

// handlePollResult handles a poll result.
func (s *Server) handlePollResult(result scheduler.PollResult) {
	// Create sample
	sample := &store.Sample{
		Namespace:    result.Key.Namespace,
		Target:       result.Key.Target,
		Poller:       result.Key.Poller,
		TimestampMs:  result.TimestampMs,
		ValueCounter: result.Counter,
		ValueText:    result.Text,
		ValueGauge:   result.Gauge,
		Valid:        result.Success,
		Error:        result.Error,
		PollMs:       result.PollMs,
	}

	// Record result
	s.mgr.RecordPollResult(
		result.Key.Namespace,
		result.Key.Target,
		result.Key.Poller,
		result.Success,
		result.Timeout,
		result.Error,
		result.PollMs,
		sample,
	)

	// Broadcast to subscribers
	// TODO: Serialize sample to protobuf and broadcast
	subKey := result.Key.Target + "/" + result.Key.Poller
	s.subHandler.BroadcastSample(result.Key.Namespace, result.Key.Target, result.Key.Poller, nil)
	_ = subKey // Will be used when we implement protobuf serialization
}

// cleanupLoop periodically cleans up lost sessions.
func (s *Server) cleanupLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			removed := s.sessions.Cleanup()
			if removed > 0 {
				log.Printf("Cleaned up %d expired sessions", removed)
			}
		case <-s.shutdown:
			return
		}
	}
}

// Helper to send auth response (simplified - actual impl would use protobuf)
func (s *Server) sendAuthResponse(w *wire.Conn, id uint64, ok bool, sessionID, message string) error {
	// TODO: Use proper protobuf encoding
	// For now, this is a placeholder
	return nil
}

// handleMessage handles an incoming message.
func (s *Server) handleMessage(session *handler.Session, env interface{}) {
	// TODO: Implement full message routing
	// This will dispatch to the appropriate handler based on message type
}
