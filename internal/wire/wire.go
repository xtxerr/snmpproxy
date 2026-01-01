// Package wire implements the snmpproxy wire protocol.
package wire

import (
	"io"

	pb "github.com/xtxerr/snmpproxy/internal/proto"
	"google.golang.org/protobuf/encoding/protodelim"
)

const MaxMessageSize = 10 * 1024 * 1024 // 10 MB

// Error codes
const (
	ErrNotAuthenticated = 1
	ErrInvalidRequest   = 2
	ErrTargetNotFound   = 3
	ErrInternal         = 4
	ErrNotAuthorized    = 5
)

// Conn wraps a connection for reading/writing Envelopes.
type Conn struct {
	r io.Reader
	w io.Writer
}

// NewConn creates a new wire connection.
func NewConn(rw io.ReadWriter) *Conn {
	return &Conn{r: rw, w: rw}
}

// Read reads an Envelope from the connection.
func (c *Conn) Read() (*pb.Envelope, error) {
	env := &pb.Envelope{}
	opts := protodelim.UnmarshalOptions{MaxSize: MaxMessageSize}
	if err := opts.UnmarshalFrom(c.r, env); err != nil {
		return nil, err
	}
	return env, nil
}

// Write writes an Envelope to the connection.
func (c *Conn) Write(env *pb.Envelope) error {
	_, err := protodelim.MarshalTo(c.w, env)
	return err
}

// NewError creates an error Envelope.
func NewError(id uint64, code int, msg string) *pb.Envelope {
	return &pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_Error{
			Error: &pb.Error{
				Code:    int32(code),
				Message: msg,
			},
		},
	}
}
