// Package handler implements the v2 wire protocol handlers.
package handler

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xtxerr/snmpproxy/internal/wire"
)

// Session represents a connected client session.
type Session struct {
	mu sync.RWMutex

	ID        string
	TokenID   string
	Namespace string // Bound namespace (empty = not bound)
	Conn      net.Conn
	Wire      *wire.Conn
	CreatedAt time.Time
	LostAt    *time.Time

	// Subscriptions: poller keys this session wants live updates for
	// Key format: "target/poller"
	subscriptions map[string]bool

	// Send channel for async message delivery
	sendCh chan []byte

	// Callbacks
	onClose func(sessionID string)
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
		sendCh:        make(chan []byte, 1000),
	}
}

// SetOnClose sets the close callback.
func (s *Session) SetOnClose(fn func(sessionID string)) {
	s.mu.Lock()
	s.onClose = fn
	s.mu.Unlock()
}

// BindNamespace binds the session to a namespace.
func (s *Session) BindNamespace(namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Namespace != "" && s.Namespace != namespace {
		return fmt.Errorf("session already bound to namespace %s", s.Namespace)
	}

	s.Namespace = namespace
	return nil
}

// GetNamespace returns the bound namespace.
func (s *Session) GetNamespace() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Namespace
}

// IsBound returns true if session is bound to a namespace.
func (s *Session) IsBound() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Namespace != ""
}

// Subscribe adds a subscription for live updates.
// Key format: "target/poller"
func (s *Session) Subscribe(key string) {
	s.mu.Lock()
	s.subscriptions[key] = true
	s.mu.Unlock()
}

// Unsubscribe removes a subscription.
func (s *Session) Unsubscribe(key string) {
	s.mu.Lock()
	delete(s.subscriptions, key)
	s.mu.Unlock()
}

// UnsubscribeAll removes all subscriptions and returns the keys.
func (s *Session) UnsubscribeAll() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]string, 0, len(s.subscriptions))
	for key := range s.subscriptions {
		keys = append(keys, key)
	}
	s.subscriptions = make(map[string]bool)
	return keys
}

// IsSubscribed checks if subscribed to a key.
func (s *Session) IsSubscribed(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subscriptions[key]
}

// GetSubscriptions returns all subscription keys.
func (s *Session) GetSubscriptions() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.subscriptions))
	for key := range s.subscriptions {
		keys = append(keys, key)
	}
	return keys
}

// SubscriptionCount returns the number of subscriptions.
func (s *Session) SubscriptionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscriptions)
}

// Send queues a message for sending.
func (s *Session) Send(data []byte) bool {
	select {
	case s.sendCh <- data:
		return true
	default:
		return false // Channel full
	}
}

// SendChan returns the send channel.
func (s *Session) SendChan() <-chan []byte {
	return s.sendCh
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

// Restore restores a lost session with a new connection.
func (s *Session) Restore(conn net.Conn, w *wire.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Conn = conn
	s.Wire = w
	s.LostAt = nil
	s.sendCh = make(chan []byte, 1000)
}

// Close closes the session.
func (s *Session) Close() {
	s.mu.Lock()
	onClose := s.onClose
	s.mu.Unlock()

	close(s.sendCh)
	s.Conn.Close()

	if onClose != nil {
		onClose(s.ID)
	}
}

// ============================================================================
// Session Manager
// ============================================================================

// TokenConfig holds token configuration.
type TokenConfig struct {
	ID         string
	Token      string
	Namespaces []string // Allowed namespaces (empty = all)
}

// SessionManager manages client sessions.
type SessionManager struct {
	mu sync.RWMutex

	sessions map[string]*Session       // sessionID -> session
	tokens   map[string]*TokenConfig   // tokenID -> config
	byToken  map[string][]*Session     // tokenID -> sessions

	reconnectWindow time.Duration
	authTimeout     time.Duration

	// Callback when session is closed
	onSessionClosed func(session *Session)
}

// SessionManagerConfig holds session manager configuration.
type SessionManagerConfig struct {
	ReconnectWindow time.Duration
	AuthTimeout     time.Duration
	Tokens          []TokenConfig
}

// NewSessionManager creates a new session manager.
func NewSessionManager(cfg *SessionManagerConfig) *SessionManager {
	sm := &SessionManager{
		sessions:        make(map[string]*Session),
		tokens:          make(map[string]*TokenConfig),
		byToken:         make(map[string][]*Session),
		reconnectWindow: cfg.ReconnectWindow,
		authTimeout:     cfg.AuthTimeout,
	}

	for i := range cfg.Tokens {
		t := &cfg.Tokens[i]
		sm.tokens[t.ID] = t
	}

	return sm
}

// SetOnSessionClosed sets the callback for closed sessions.
func (sm *SessionManager) SetOnSessionClosed(fn func(session *Session)) {
	sm.mu.Lock()
	sm.onSessionClosed = fn
	sm.mu.Unlock()
}

// ValidateToken validates a token and returns the token config.
func (sm *SessionManager) ValidateToken(token string) (*TokenConfig, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, t := range sm.tokens {
		if subtle.ConstantTimeCompare([]byte(t.Token), []byte(token)) == 1 {
			return t, true
		}
	}
	return nil, false
}

// CanAccessNamespace checks if a token can access a namespace.
func (sm *SessionManager) CanAccessNamespace(tokenID, namespace string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	t, ok := sm.tokens[tokenID]
	if !ok {
		return false
	}

	// Empty = all namespaces allowed
	if len(t.Namespaces) == 0 {
		return true
	}

	for _, ns := range t.Namespaces {
		if ns == namespace {
			return true
		}
	}
	return false
}

// CreateSession creates a new session.
func (sm *SessionManager) CreateSession(tokenID string, conn net.Conn, w *wire.Conn) *Session {
	id := uuid.New().String()[:8]

	session := NewSession(id, tokenID, conn, w)
	session.SetOnClose(func(sessionID string) {
		sm.removeSession(sessionID)
	})

	sm.mu.Lock()
	sm.sessions[id] = session
	sm.byToken[tokenID] = append(sm.byToken[tokenID], session)
	sm.mu.Unlock()

	log.Printf("Created session %s (token: %s)", id, tokenID)
	return session
}

// GetSession returns a session by ID.
func (sm *SessionManager) GetSession(id string) *Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[id]
}

// TryRestore tries to restore a lost session for the given token.
func (sm *SessionManager) TryRestore(tokenID string, conn net.Conn, w *wire.Conn) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessions := sm.byToken[tokenID]
	for _, s := range sessions {
		if s.IsLost() && s.LostDuration() < sm.reconnectWindow {
			s.Restore(conn, w)
			log.Printf("Restored session %s", s.ID)
			return s
		}
	}
	return nil
}

// removeSession removes a session from the manager.
func (sm *SessionManager) removeSession(id string) {
	sm.mu.Lock()
	session, ok := sm.sessions[id]
	if !ok {
		sm.mu.Unlock()
		return
	}

	delete(sm.sessions, id)

	// Remove from byToken
	tokenSessions := sm.byToken[session.TokenID]
	for i, s := range tokenSessions {
		if s.ID == id {
			sm.byToken[session.TokenID] = append(tokenSessions[:i], tokenSessions[i+1:]...)
			break
		}
	}

	onClosed := sm.onSessionClosed
	sm.mu.Unlock()

	if onClosed != nil {
		onClosed(session)
	}

	log.Printf("Removed session %s", id)
}

// GetSessions returns all active sessions.
func (sm *SessionManager) GetSessions() []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sessions := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// GetSessionsInNamespace returns sessions bound to a namespace.
func (sm *SessionManager) GetSessionsInNamespace(namespace string) []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var sessions []*Session
	for _, s := range sm.sessions {
		if s.GetNamespace() == namespace {
			sessions = append(sessions, s)
		}
	}
	return sessions
}

// GetSubscribersFor returns sessions subscribed to a key in a namespace.
// Key format: "target/poller"
func (sm *SessionManager) GetSubscribersFor(namespace, key string) []*Session {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var subscribers []*Session
	for _, s := range sm.sessions {
		if !s.IsLost() && s.GetNamespace() == namespace && s.IsSubscribed(key) {
			subscribers = append(subscribers, s)
		}
	}
	return subscribers
}

// Count returns the number of sessions.
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// CountActive returns the number of active (not lost) sessions.
func (sm *SessionManager) CountActive() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	count := 0
	for _, s := range sm.sessions {
		if !s.IsLost() {
			count++
		}
	}
	return count
}

// CountLost returns the number of lost sessions.
func (sm *SessionManager) CountLost() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	count := 0
	for _, s := range sm.sessions {
		if s.IsLost() {
			count++
		}
	}
	return count
}

// Cleanup removes expired lost sessions.
func (sm *SessionManager) Cleanup() int {
	sm.mu.Lock()

	var toRemove []string
	for id, s := range sm.sessions {
		if s.IsLost() && s.LostDuration() > sm.reconnectWindow {
			toRemove = append(toRemove, id)
		}
	}
	sm.mu.Unlock()

	for _, id := range toRemove {
		sm.removeSession(id)
	}

	return len(toRemove)
}

// AuthTimeout returns the auth timeout.
func (sm *SessionManager) AuthTimeout() time.Duration {
	return sm.authTimeout
}

// ReconnectWindow returns the reconnect window.
func (sm *SessionManager) ReconnectWindow() time.Duration {
	return sm.reconnectWindow
}
