// Package wire provides message framing for protobuf over TCP.
package wire

import (
	"bufio"
	"fmt"
	"io"
	"sync"

	pb "github.com/xtxerr/snmpproxy/internal/proto"
	"google.golang.org/protobuf/encoding/protodelim"
)

// MaxMessageSize limits message size to prevent memory exhaustion.
const MaxMessageSize = 16 * 1024 * 1024 // 16 MB

// Reader reads length-delimited protobuf envelopes.
type Reader struct {
	r  *bufio.Reader
	mu sync.Mutex
}

// NewReader creates a Reader wrapping the given io.Reader.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(r)}
}

// Read reads and unmarshals the next envelope.
func (r *Reader) Read() (*pb.Envelope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	env := &pb.Envelope{}
	opts := protodelim.UnmarshalOptions{
		MaxSize: MaxMessageSize,
	}
	if err := opts.UnmarshalFrom(r.r, env); err != nil {
		return nil, fmt.Errorf("read envelope: %w", err)
	}
	return env, nil
}

// Writer writes length-delimited protobuf envelopes.
type Writer struct {
	w  io.Writer
	mu sync.Mutex
}

// NewWriter creates a Writer wrapping the given io.Writer.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Write marshals and writes an envelope.
func (w *Writer) Write(env *pb.Envelope) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := protodelim.MarshalTo(w.w, env); err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}
	return nil
}

// Conn combines Reader and Writer for bidirectional communication.
type Conn struct {
	*Reader
	*Writer
}

// NewConn creates a Conn from an io.ReadWriter (e.g., net.Conn).
func NewConn(rw io.ReadWriter) *Conn {
	return &Conn{
		Reader: NewReader(rw),
		Writer: NewWriter(rw),
	}
}

// Error codes
const (
	ErrUnknown          = 1
	ErrAuthFailed       = 2
	ErrNotAuthenticated = 3
	ErrInvalidRequest   = 4
	ErrTargetNotFound   = 5
	ErrSNMP             = 6
	ErrInternal         = 7
	ErrNotAuthorized    = 8
)

// NewError creates an error envelope.
func NewError(id uint64, code int32, msg string) *pb.Envelope {
	return &pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_Error{
			Error: &pb.Error{
				Code:    code,
				Message: msg,
			},
		},
	}
}
