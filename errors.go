package wayland

import (
	"errors"
	"fmt"
)

var (
	ErrConnClosed      = errors.New("wayland: connection closed")
	ErrObjectDeleted   = errors.New("wayland: object deleted")
	ErrVersionMismatch = errors.New("wayland: version mismatch")
)

// ProtocolError is reported by wl_display.error. Detect it with
// `var pe *ProtocolError; errors.As(err, &pe)`.
type ProtocolError struct {
	ObjectID uint32
	Code     uint32
	Message  string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("wayland protocol error on object %d: %d (%s)", e.ObjectID, e.Code, e.Message)
}
