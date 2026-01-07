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
		Payload: &pb.Envelope_AuthReq{
			AuthReq: &pb.AuthRequest{Token: c.token},
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

// IsConnected returns true if connected.
func (c *Client) IsConnected() bool {
	return c.connected.Load()
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

// ============================================================================
// Browse
// ============================================================================

// BrowseOptions contains options for Browse requests.
type BrowseOptions struct {
	Path       string
	LongFormat bool
	Tags       []string
	State      string
	Protocol   string
	Host       string
	Limit      int32
	Cursor     string
}

// Browse retrieves information at the given path with optional filters.
func (c *Client) Browse(opts *BrowseOptions) (*pb.BrowseResponse, error) {
	req := &pb.BrowseRequest{
		Path:           opts.Path,
		LongFormat:     opts.LongFormat,
		FilterTags:     opts.Tags,
		FilterState:    opts.State,
		FilterProtocol: opts.Protocol,
		FilterHost:     opts.Host,
		Limit:          opts.Limit,
		Cursor:         opts.Cursor,
	}

	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_BrowseReq{BrowseReq: req},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetBrowseResp(), nil
}

// BrowsePath is a convenience method for simple path browsing.
func (c *Client) BrowsePath(path string, longFormat bool) (*pb.BrowseResponse, error) {
	return c.Browse(&BrowseOptions{
		Path:       path,
		LongFormat: longFormat,
	})
}

// ============================================================================
// Target CRUD
// ============================================================================

// CreateTarget creates a new target.
func (c *Client) CreateTarget(req *pb.CreateTargetRequest) (*pb.CreateTargetResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_CreateTargetReq{CreateTargetReq: req},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetCreateTargetResp(), nil
}

// UpdateTarget updates a target.
func (c *Client) UpdateTarget(req *pb.UpdateTargetRequest) (*pb.UpdateTargetResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_UpdateTargetReq{UpdateTargetReq: req},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetUpdateTargetResp(), nil
}

// DeleteTarget deletes a target.
func (c *Client) DeleteTarget(targetID string, force bool) (*pb.DeleteTargetResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_DeleteTargetReq{
			DeleteTargetReq: &pb.DeleteTargetRequest{
				TargetId: targetID,
				Force:    force,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetDeleteTargetResp(), nil
}

// ============================================================================
// History
// ============================================================================

// GetHistory retrieves sample history for a target.
func (c *Client) GetHistory(targetID string, lastN uint32) (*pb.GetHistoryResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_GetHistoryReq{
			GetHistoryReq: &pb.GetHistoryRequest{
				TargetId: targetID,
				LastN:    lastN,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetGetHistoryResp(), nil
}

// ============================================================================
// Subscriptions
// ============================================================================

// Subscribe subscribes to target samples by ID and/or tags.
func (c *Client) Subscribe(targetIDs []string, tags []string) (*pb.SubscribeResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_SubscribeReq{
			SubscribeReq: &pb.SubscribeRequest{
				TargetIds: targetIDs,
				Tags:      tags,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetSubscribeResp(), nil
}

// Unsubscribe unsubscribes from targets.
func (c *Client) Unsubscribe(targetIDs []string) (*pb.UnsubscribeResponse, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_UnsubscribeReq{
			UnsubscribeReq: &pb.UnsubscribeRequest{
				TargetIds: targetIDs,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetUnsubscribeResp(), nil
}

// ============================================================================
// Config
// ============================================================================

// GetConfig retrieves the runtime configuration.
func (c *Client) GetConfig() (*pb.RuntimeConfig, error) {
	resp, err := c.request(&pb.Envelope{
		Payload: &pb.Envelope_GetConfigReq{
			GetConfigReq: &pb.GetConfigRequest{},
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
		Payload: &pb.Envelope_SetConfigReq{SetConfigReq: req},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetSetConfigResp(), nil
}
