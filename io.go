package wayland

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

// errSoftTimeout marks a read deadline that the runtime installed for a
// canceled Roundtrip. Unlike a real IO error it must not close the connection.
var errSoftTimeout = errors.New("wayland: soft read timeout")

func (c *conn) sendRequest(p *Proxy, opcode uint16, args []Arg) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("send: %w", ErrClosed)
	}
	if p == nil || p.conn != c {
		c.mu.Unlock()
		return fmt.Errorf("send: %w", ErrInvalidArgument)
	}
	if p.state != stateAlive || p.tombstone {
		c.mu.Unlock()
		return fmt.Errorf("send object %d: %w", p.id, ErrObjectDestroyed)
	}
	if p.iface == nil {
		c.mu.Unlock()
		return fmt.Errorf("send object %d: nil interface: %w", p.id, ErrInvalidArgument)
	}
	if int(opcode) >= len(p.iface.Requests) {
		c.mu.Unlock()
		return fmt.Errorf("send %s opcode %d: %w", p.iface.Name, opcode, ErrInvalidArgument)
	}
	spec := p.iface.Requests[opcode]
	if spec.Since > p.version {
		c.mu.Unlock()
		return fmt.Errorf("send %s.%s since %d version %d: %w", p.iface.Name, spec.Name, spec.Since, p.version, ErrSince)
	}
	c.mu.Unlock()

	// Encoding a dynamic new_id resolves the target object in the object
	// table, so it must happen outside c.mu.
	data, fds, err := encodeRequest(p, opcode, spec, args)
	if err != nil {
		return err
	}
	if err := c.write(data, fds); err != nil {
		_ = c.close()
		return err
	}
	c.mu.Lock()
	p.sent = true
	if spec.Destructor {
		p.state = stateDestroyPending
	}
	c.mu.Unlock()
	return nil
}

func (c *conn) write(data []byte, fds []int) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.isClosed() {
		return fmt.Errorf("write: %w", ErrClosed)
	}
	var oob []byte
	if len(fds) > 0 {
		oob = syscall.UnixRights(fds...)
	}
	for len(data) > 0 {
		var n int
		var err error
		if len(oob) > 0 {
			n, _, err = c.uc.WriteMsgUnix(data, oob, nil)
		} else {
			n, err = c.uc.Write(data)
		}
		if err != nil {
			return fmt.Errorf("write: %w: %w", ErrIO, err)
		}
		if n == 0 {
			return fmt.Errorf("write: %w", ErrIO)
		}
		// The whole ancillary batch travels with the first bytes of the
		// message. A continuation write must not repeat it, or the peer
		// would receive duplicate descriptors.
		oob = nil
		data = data[n:]
	}
	return nil
}

func (c *conn) ensure(n int) error {
	for {
		c.mu.Lock()
		have := len(c.rbuf)
		c.mu.Unlock()
		if have >= n {
			return nil
		}
		if err := c.readMore(); err != nil {
			return err
		}
	}
}

func (c *conn) oobSize() int {
	if n := c.testOOBLen(); n > 0 {
		return n
	}
	return 4096
}

func (c *conn) readMore() error {
	buf := make([]byte, messageMax+8)
	oob := make([]byte, c.oobSize())
	n, oobn, flags, _, err := c.uc.ReadMsgUnix(buf, oob)
	if err != nil {
		if c.isClosed() {
			return fmt.Errorf("read: %w", ErrClosed)
		}
		if st := c.roundtrip; st != nil && st.softTimeout {
			var ne net.Error
			if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
				return errSoftTimeout
			}
		}
		return fmt.Errorf("read: %w: %w", ErrIO, err)
	}
	if flags&syscall.MSG_CTRUNC != 0 {
		closeOOBFds(oob[:oobn])
		return fmt.Errorf("read: control data truncated: %w", ErrMalformedMessage)
	}
	if n == 0 {
		return fmt.Errorf("read: EOF: %w", ErrIO)
	}
	var fds []int
	if oobn > 0 {
		msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			return fmt.Errorf("read control: %w: %w", ErrMalformedMessage, err)
		}
		for i := range msgs {
			rights, err := syscall.ParseUnixRights(&msgs[i])
			if err != nil {
				closeFDs(fds)
				return fmt.Errorf("read fd: %w: %w", ErrMalformedMessage, err)
			}
			fds = append(fds, rights...)
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		closeFDs(fds)
		return fmt.Errorf("read: %w", ErrClosed)
	}
	c.rbuf = append(c.rbuf, buf[:n]...)
	c.fds = append(c.fds, fds...)
	c.mu.Unlock()
	return nil
}

func (c *conn) readFrame() (uint32, uint16, []byte, error) {
	if err := c.ensure(8); err != nil {
		return 0, 0, nil, err
	}
	c.mu.Lock()
	objectID := binary.LittleEndian.Uint32(c.rbuf[0:4])
	word := binary.LittleEndian.Uint32(c.rbuf[4:8])
	c.mu.Unlock()
	size := int(word >> 16)
	opcode := uint16(word)
	if size < 8 || size > messageMax || size%4 != 0 {
		return 0, 0, nil, fmt.Errorf("frame size %d: %w", size, ErrMalformedMessage)
	}
	if err := c.ensure(size); err != nil {
		return 0, 0, nil, err
	}
	c.mu.Lock()
	body := append([]byte(nil), c.rbuf[8:size]...)
	c.rbuf = c.rbuf[size:]
	c.mu.Unlock()
	return objectID, opcode, body, nil
}

// unreadFrame puts a fully read frame back at the front of the read buffer so a
// canceled Roundtrip can leave other objects' events for the next Dispatch.
func (c *conn) unreadFrame(objectID uint32, opcode uint16, body []byte) {
	size := 8 + len(body)
	frame := make([]byte, 8, size)
	binary.LittleEndian.PutUint32(frame[0:4], objectID)
	binary.LittleEndian.PutUint32(frame[4:8], uint32(size)<<16|uint32(opcode))
	frame = append(frame, body...)
	c.mu.Lock()
	c.rbuf = append(frame, c.rbuf...)
	c.mu.Unlock()
}

func (c *conn) takeFD() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.fds) == 0 {
		return 0, fmt.Errorf("missing fd: %w", ErrMalformedMessage)
	}
	fd := c.fds[0]
	c.fds = c.fds[1:]
	return fd, nil
}
