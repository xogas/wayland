package wayland

import (
	"errors"
	"fmt"
)

var (
	// ErrSource is a missing or invalid connection source.
	ErrSource = errors.New("wayland: connection source")
	// ErrConnect is a failed connection attempt.
	ErrConnect = errors.New("wayland: connect")
	// ErrIO is a read or write failure.
	ErrIO = errors.New("wayland: io")
	// ErrClosed is an operation on a closed connection.
	ErrClosed = errors.New("wayland: closed")
	// ErrProtocolViolation is a peer or local protocol violation.
	ErrProtocolViolation = errors.New("wayland: protocol violation")
	// ErrMalformedMessage is an invalid wire message.
	ErrMalformedMessage = errors.New("wayland: malformed message")
	// ErrVersion is a version mismatch.
	ErrVersion = errors.New("wayland: version")
	// ErrSince is a request newer than the object version.
	ErrSince = errors.New("wayland: since")
	// ErrInvalidArgument is an invalid argument.
	ErrInvalidArgument = errors.New("wayland: invalid argument")
	// ErrObjectDestroyed is a request on a destroyed object.
	ErrObjectDestroyed = errors.New("wayland: object destroyed")
	// ErrIDExhausted is a full client object ID space.
	ErrIDExhausted = errors.New("wayland: object id exhausted")
	// ErrInvalidOperation is a reentrant or unsupported operation.
	ErrInvalidOperation = errors.New("wayland: invalid operation")
	// ErrNoHandler means an event arrived for an object without a sink.
	ErrNoHandler = errors.New("wayland: no event handler")
)

// ProtocolError is a wl_display.error event.
type ProtocolError struct {
	ObjectID  uint32
	Interface string
	Code      uint32
	Message   string
}

func (e *ProtocolError) Error() string {
	if e.Interface != "" {
		return fmt.Sprintf("wayland: protocol error: %s object %d code %d: %s", e.Interface, e.ObjectID, e.Code, e.Message)
	}
	return fmt.Sprintf("wayland: protocol error: object %d code %d: %s", e.ObjectID, e.Code, e.Message)
}

// Is lets errors.Is(err, ErrProtocolViolation) match a ProtocolError.
func (e *ProtocolError) Is(target error) bool {
	return target == ErrProtocolViolation
}
