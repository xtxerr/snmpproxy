// Package client provides a client for the SNMP proxy.
package client

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/xtxerr/snmpproxy/internal/proto"
	"github.com/xtxerr/snmpproxy/internal/wire"
)

// Client connects to an SNMP proxy server.
type Client struct {
	addr      string
	token     string
	tlsConfig *tls.Config

	conn      net.Conn
	wire      *wire.Conn
	sessionID string

	mu        sync.RWMutex
	pending   map[uint64]chan *pb.Envelope
	requestID uint64

	onSample  func(*pb.Sample)
	connected atomic.Bool
	shutdown  chan struct{}
}

// Config holds client configuration.
type Config struct {
	Addr          string
	Token         string
	TLS           bool
	TLSSkipVerify bool
}

// New creates a new client.
func New(cfg *Config) *Client {
	c := &Client{
		addr:     cfg.Addr,
		token:    cfg.Token,
		pending:  make(map[uint64]chan *pb.Envelope),
		shutdown: make(chan struct{}),
	}
	if cfg.TLS {
		c.tlsConfig = &tls.Config{
			InsecureSkipVerify: cfg.TLSSkipVerify,
		}
	}
	return c
}

// Connect connects and authenticates.
func (c *Client) Connect() error {
	var conn net.Conn
	var err error

	if c.tlsConfig != nil {
		conn, err = tls.Dial("tcp", c.addr, c.tlsConfig)
	} else {
		conn, err = net.Dial("tcp", c.addr)
	}
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	c.conn = conn
	c.wire = wire.NewConn(conn)

	// Send auth
	if err := c.wire.Write(&pb.Envelope{
		Id: 1,
		Payload: &pb.Envelope_Auth{
			Auth: &pb.AuthRequest{Token: c.token},
		},
	}); err != nil {
		conn.Close()
		return fmt.Errorf("send auth: %w", err)
	}

	// Read auth response
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	env, err := c.wire.Read()
	conn.SetDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return fmt.Errorf("read auth response: %w", err)
	}

	if e := env.GetError(); e != nil {
		conn.Close()
		return fmt.Errorf("auth error: %s", e.Message)
	}

	resp := env.GetAuthResp()
	if resp == nil || !resp.Ok {
		conn.Close()
		msg := "authentication failed"
		if resp != nil && resp.Message != "" {
			msg = resp.Message
		}
		return fmt.Errorf(msg)
	}

	c.sessionID = resp.SessionId
	c.connected.Store(true)

	go c.readLoop()

	return nil
}

// Close disconnects.
func (c *Client) Close() {
	c.connected.Store(false)
	close(c.shutdown)
	if c.conn != nil {
		c.conn.Close()
	}
}

// SessionID returns the session ID.
func (c *Client) SessionID() string {
	return c.sessionID
}

// OnSample sets the handler for pushed samples.
func (c *Client) OnSample(fn func(*pb.Sample)) {
	c.mu.Lock()
	c.onSample = fn
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	for {
		env, err := c.wire.Read()
		if err != nil {
			c.connected.Store(false)
			return
		}

		// Pushed sample
		if sample := env.GetSample(); sample != nil {
			c.mu.RLock()
			fn := c.onSample
			c.mu.RUnlock()
			if fn != nil {
				fn(sample)
			}
			continue
		}

		// Response to request
		c.mu.RLock()
		ch, ok := c.pending[env.Id]
		c.mu.RUnlock()
		if ok {
			select {
			case ch <- env:
			default:
			}
		}
	}
}

func (c *Client) request(env *pb.Envelope) (*pb.Envelope, error) {
	id := atomic.AddUint64(&c.requestID, 1)
	env.Id = id

	ch := make(chan *pb.Envelope, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.wire.Write(env); err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		if e := resp.GetError(); e != nil {
			return nil, fmt.Errorf("error %d: %s", e.Code, e.Message)
		}
		return resp, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout")
	case <-c.shutdown:
		return nil, fmt.Errorf("shutdown")
	}
}

// Monitor starts monitoring a target.
func (c *Client) Monitor(req *pb.MonitorRequest) (*pb.MonitorResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_Monitor{Monitor: req},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetMonitorResp(), nil
}

// Unmonitor stops monitoring a target.
func (c *Client) Unmonitor(targetID string) error {
	_, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_Unmonitor{
			Unmonitor: &pb.UnmonitorRequest{TargetId: targetID},
		},
	})
	return err
}

// ListTargets lists all targets.
func (c *Client) ListTargets(filterHost string) ([]*pb.Target, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_ListTargets{
			ListTargets: &pb.ListTargetsRequest{FilterHost: filterHost},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetListTargetsResp().GetTargets(), nil
}

// GetTarget gets target info.
func (c *Client) GetTarget(targetID string) (*pb.Target, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_GetTarget{
			GetTarget: &pb.GetTargetRequest{TargetId: targetID},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetGetTargetResp().GetTarget(), nil
}

// GetHistory gets historical samples.
func (c *Client) GetHistory(targetIDs []string, lastN uint32) ([]*pb.TargetHistory, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_GetHistory{
			GetHistory: &pb.GetHistoryRequest{TargetIds: targetIDs, LastN: lastN},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetGetHistoryResp().GetHistory(), nil
}

// Subscribe subscribes to live samples.
func (c *Client) Subscribe(targetIDs []string) ([]string, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_Subscribe{
			Subscribe: &pb.SubscribeRequest{TargetIds: targetIDs},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetSubscribeResp().GetSubscribed(), nil
}

// Unsubscribe unsubscribes from live samples.
func (c *Client) Unsubscribe(targetIDs []string) error {
	_, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_Unsubscribe{
			Unsubscribe: &pb.UnsubscribeRequest{TargetIds: targetIDs},
		},
	})
	return err
}

// ============================================================================
// NEW: Status, Session, Update, Config
// ============================================================================

// GetServerStatus returns server status information.
func (c *Client) GetServerStatus() (*pb.GetServerStatusResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_GetServerStatus{
			GetServerStatus: &pb.GetServerStatusRequest{},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetGetServerStatusResp(), nil
}

// GetSessionInfo returns information about the current session.
func (c *Client) GetSessionInfo() (*pb.GetSessionInfoResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_GetSessionInfo{
			GetSessionInfo: &pb.GetSessionInfoRequest{},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetGetSessionInfoResp(), nil
}

// UpdateTarget updates target settings (interval, timeout, retries).
func (c *Client) UpdateTarget(req *pb.UpdateTargetRequest) (*pb.UpdateTargetResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_UpdateTarget{
			UpdateTarget: req,
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetUpdateTargetResp(), nil
}

// GetConfig returns the current runtime configuration.
func (c *Client) GetConfig() (*pb.RuntimeConfig, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_GetConfig{
			GetConfig: &pb.GetConfigRequest{},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetGetConfigResp().GetConfig(), nil
}

// SetConfig updates the runtime configuration.
func (c *Client) SetConfig(req *pb.SetConfigRequest) (*pb.SetConfigResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_SetConfig{
			SetConfig: req,
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetSetConfigResp(), nil
}
