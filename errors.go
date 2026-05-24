package wayland

import (
	"errors"
	"fmt"
)

var ErrConnClosed = errors.New("wayland: connection closed")
var ErrObjectDeleted = errors.New("wayland: object deleted")
var ErrVersionMismatch = errors.New("wayland: version mismatch")

type ProtocolError struct {
	ObjectID uint32
	Code     uint32
	Message  string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("wayland protocol error on object %d: %d (%s)", e.ObjectID, e.Code, e.Message)
}
