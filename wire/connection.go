package wire

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"syscall"
)

// Conn wraps a Unix domain socket connection for sending and receiving Wayland messages.
type Conn struct {
	conn  *net.UnixConn
	buf   []byte
	fdsMu sync.Mutex
	fds   []int
}

// NewConn creates a new Conn wrapping a UnixConn.
func NewConn(conn *net.UnixConn) *Conn {
	return &Conn{conn: conn}
}

// TakeFDs removes and returns up to n file descriptors from the front of the
// connection-level fd queue. Returns fewer than n if the queue is exhausted.
// Safe for concurrent use with ReceiveMessage, TakeFDs, and TakeAllFDs.
func (c *Conn) TakeFDs(n int) []int {
	if n <= 0 {
		return nil
	}
	c.fdsMu.Lock()
	defer c.fdsMu.Unlock()
	if n > len(c.fds) {
		n = len(c.fds)
	}
	taken := c.fds[:n]
	c.fds = c.fds[n:]
	return taken
}

// TakeAllFDs removes and returns all remaining file descriptors from the queue.
// Safe for concurrent use with ReceiveMessage, TakeFDs, and TakeAllFDs.
func (c *Conn) TakeAllFDs() []int {
	c.fdsMu.Lock()
	defer c.fdsMu.Unlock()
	fds := c.fds
	c.fds = nil
	return fds
}

// Close closes the underlying connection.
func (c *Conn) Close() error {
	return c.conn.Close()
}

var ErrMessageTooLarge = fmt.Errorf("wire: message size exceeds 65535 bytes")

// SendMessage sends a Wayland message.
// Frame: [object_id:uint32][length<<16|opcode:uint32][payload...].
// FDs from w.Fds() are sent as SCM_RIGHTS ancillary data.
func (c *Conn) SendMessage(obj ObjectID, opcode uint16, w *Writer) error {
	payload := w.Bytes()
	length := 8 + len(payload)
	if length > 0xFFFF {
		return ErrMessageTooLarge
	}
	pad := (4 - len(payload)%4) % 4

	buf := make([]byte, 8+len(payload)+pad)
	binary.NativeEndian.PutUint32(buf[0:4], uint32(obj))
	binary.NativeEndian.PutUint32(buf[4:8], uint32(length)<<16|uint32(opcode))
	copy(buf[8:], payload)

	if len(w.Fds()) == 0 {
		_, err := c.conn.Write(buf)
		return err
	}

	rights := syscall.UnixRights(w.Fds()...)
	_, _, err := c.conn.WriteMsgUnix(buf, rights, nil)
	return err
}

// ReceiveMessage reads the next complete Wayland message.
// Received FDs are queued at the connection level (matching libwayland);
// callers must use TakeFDs + Reader.SetFDs to assign them to a message.
func (c *Conn) ReceiveMessage() (ObjectID, uint16, *Reader, error) {
	for {
		if len(c.buf) >= 8 {
			obj := ObjectID(binary.NativeEndian.Uint32(c.buf[0:4]))
			word2 := binary.NativeEndian.Uint32(c.buf[4:8])
			length := int(word2 >> 16)
			opcode := uint16(word2 & 0xFFFF)

			if length < 8 {
				return 0, 0, nil, fmt.Errorf("wire: invalid message length %d", length)
			}

			if len(c.buf) >= length {
				payload := make([]byte, length-8)
				copy(payload, c.buf[8:length])
				c.buf = c.buf[length:]

				r := NewReader(payload, nil)

				return obj, opcode, r, nil
			}
		}

		buf := make([]byte, 4096)
		oob := make([]byte, syscall.CmsgSpace(4*28))

		n, oobn, flags, _, err := c.conn.ReadMsgUnix(buf, oob)
		if err != nil {
			return 0, 0, nil, err
		}
		if flags&syscall.MSG_CTRUNC != 0 {
			return 0, 0, nil, fmt.Errorf("wire: control message truncated (OOB buffer too small)")
		}

		var newFds []int
		if oobn > 0 {
			scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
			if err != nil {
				return 0, 0, nil, fmt.Errorf("wire: parsing control message: %w", err)
			}
			for _, scm := range scms {
				parsedFds, err := syscall.ParseUnixRights(&scm)
				if err != nil {
					return 0, 0, nil, fmt.Errorf("wire: parsing unix rights: %w", err)
				}
				for _, fd := range parsedFds {
					syscall.CloseOnExec(fd)
				}
				newFds = append(newFds, parsedFds...)
			}
		}

		c.fdsMu.Lock()
		c.fds = append(c.fds, newFds...)
		c.fdsMu.Unlock()
		c.buf = append(c.buf, buf[:n]...)
	}
}
