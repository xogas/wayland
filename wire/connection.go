package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"syscall"
)

const (
	// Alignment pulls the real ceiling down to 65532.
	maxMessage = 0xFFFF
	maxPayload = maxMessage - 8

	// Only sets the syscall count per frame: partial frames are buffered.
	readChunk = 4096

	// More than this in one receive sets MSG_CTRUNC and is fatal; no Wayland
	// message declares that many.
	maxFDsPerRead = 32
)

// ErrMessageTooLarge means the frame will not fit 16 bits; Wayland has no
// fragmentation.
var ErrMessageTooLarge = errors.New("wire: message exceeds 65535 bytes")

// Conn is one end of a Wayland socket. SendMessage is safe for concurrent use.
// ReceiveMessage must be called from a single goroutine, which owns buf,
// readBuf and oobBuf. fdsMu guards the descriptor queue shared with the
// dispatcher. The zero value is not usable; call NewConn.
type Conn struct {
	conn *net.UnixConn

	sendMu  sync.Mutex
	sendBuf []byte

	// Shared with the dispatcher goroutine.
	fdsMu  sync.Mutex
	fds    []int
	closed bool

	// Owned by the ReceiveMessage goroutine.
	buf     []byte
	readBuf []byte
	oobBuf  []byte
}

func NewConn(conn *net.UnixConn) *Conn {
	return &Conn{
		conn:    conn,
		readBuf: make([]byte, readChunk),
		oobBuf:  make([]byte, syscall.CmsgSpace(4*maxFDsPerRead)),
	}
}

// n is the count the interface declares for the opcode. A short queue is
// clamped here and surfaces later as ErrNoFD.
func (c *Conn) TakeFDs(n int) []int {
	if n <= 0 {
		return nil
	}
	c.fdsMu.Lock()
	defer c.fdsMu.Unlock()
	n = min(n, len(c.fds))
	if n == 0 {
		return nil
	}
	// Copy out: the reader goroutine keeps appending.
	taken := slices.Clone(c.fds[:n])
	c.fds = slices.Delete(c.fds, 0, n)
	return taken
}

// TakeAllFDs drains the queue; the caller owns what comes back.
func (c *Conn) TakeAllFDs() []int {
	c.fdsMu.Lock()
	defer c.fdsMu.Unlock()
	fds := c.fds
	c.fds = nil
	return fds
}

// Close drains the queue and closes the socket. Received SCM_RIGHTS fds have no
// finalizers, so anything left behind leaks. closed is set under fdsMu so a
// racing receive cannot append after the drain.
func (c *Conn) Close() error {
	c.fdsMu.Lock()
	if c.closed {
		c.fdsMu.Unlock()
		return nil
	}
	c.closed = true
	fds := c.fds
	c.fds = nil
	c.fdsMu.Unlock()

	closeFDs(fds)
	return c.conn.Close()
}

func (c *Conn) isClosed() bool {
	c.fdsMu.Lock()
	defer c.fdsMu.Unlock()
	return c.closed
}

// SendMessage writes one frame with its fds in the same call, so they cannot
// drift to another message. Any error is fatal for the stream: a partial write
// cannot be undone.
func (c *Conn) SendMessage(obj ObjectID, opcode uint16, w *Writer) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	payload := w.Bytes()
	// Unaligned payloads have no valid length field.
	if len(payload)%4 != 0 {
		return fmt.Errorf("wire: unaligned payload of %d bytes", len(payload))
	}
	length := 8 + len(payload)
	if length > maxMessage {
		return ErrMessageTooLarge
	}

	if cap(c.sendBuf) < length {
		c.sendBuf = make([]byte, length)
	}
	buf := c.sendBuf[:length]
	binary.NativeEndian.PutUint32(buf[0:4], uint32(obj))
	binary.NativeEndian.PutUint32(buf[4:8], uint32(length)<<16|uint32(opcode))
	copy(buf[8:], payload)

	fds := w.Fds()
	if len(fds) == 0 {
		if _, err := c.conn.Write(buf); err != nil {
			return fmt.Errorf("wire: sending to object %d: %w", obj, err)
		}
		return nil
	}

	rights := syscall.UnixRights(fds...)
	n, _, err := c.conn.WriteMsgUnix(buf, rights, nil)
	if err != nil {
		return fmt.Errorf("wire: sending to object %d: %w", obj, err)
	}
	if n != len(buf) {
		return fmt.Errorf("wire: short write to object %d: %w", obj, io.ErrShortWrite)
	}
	return nil
}

// ReceiveMessage returns the next complete frame. Descriptors received with it
// stay queued until the dispatcher calls TakeFDs. Errors are fatal: a byte
// stream cannot recover after an invalid header.
func (c *Conn) ReceiveMessage() (ObjectID, uint16, *Reader, error) {
	for {
		// Checked before every return: Close drains the fd queue, so a frame
		// handed out afterwards would decode without its descriptors.
		if c.isClosed() {
			return 0, 0, nil, net.ErrClosed
		}

		if len(c.buf) >= 8 {
			f, rest, ok, err := splitFrame(c.buf)
			if err != nil {
				return 0, 0, nil, err
			}
			if ok {
				c.buf = rest
				return f.obj, f.opcode, NewReader(f.payload, nil), nil
			}
		}

		n, oobn, flags, _, err := c.conn.ReadMsgUnix(c.readBuf, c.oobBuf)
		if err != nil {
			return 0, 0, nil, err
		}
		if n == 0 {
			// A zero-length read without an error would loop forever.
			return 0, 0, nil, io.ErrNoProgress
		}

		newFds, err := parseFDs(c.oobBuf[:oobn], flags)
		if err != nil {
			return 0, 0, nil, err
		}
		if len(newFds) > 0 {
			c.fdsMu.Lock()
			if c.closed {
				c.fdsMu.Unlock()
				closeFDs(newFds)
				return 0, 0, nil, net.ErrClosed
			}
			c.fds = append(c.fds, newFds...)
			c.fdsMu.Unlock()
		}

		c.buf = append(c.buf, c.readBuf[:n]...)
	}
}

// frame does not alias buf.
type frame struct {
	obj     ObjectID
	opcode  uint16
	payload []byte
}

// length is the format's only delimiter. A non-multiple-of-4 length cannot come
// from a correct peer and would shift every later header.
func splitFrame(buf []byte) (f frame, rest []byte, ok bool, err error) {
	if len(buf) < 8 {
		return frame{}, buf, false, nil
	}
	obj := ObjectID(binary.NativeEndian.Uint32(buf[0:4]))
	word2 := binary.NativeEndian.Uint32(buf[4:8])
	length := int(word2 >> 16)
	opcode := uint16(word2)
	if length < 8 || length%4 != 0 {
		return frame{}, nil, false, fmt.Errorf("wire: invalid message length %d", length)
	}
	if len(buf) < length {
		return frame{}, buf, false, nil
	}
	payload := make([]byte, length-8)
	copy(payload, buf[8:length])
	return frame{obj: obj, opcode: opcode, payload: payload}, buf[length:], true, nil
}

// parseFDs must close whatever it already parsed: the kernel installs the
// descriptors before this runs, so a failure cannot un-install them.
func parseFDs(oob []byte, flags int) ([]int, error) {
	var fds []int
	if len(oob) > 0 {
		scms, err := syscall.ParseSocketControlMessage(oob)
		if err != nil {
			// Descriptor numbers inside a control buffer that cannot be
			// parsed are lost.
			return nil, fmt.Errorf("wire: parsing control message: %w", err)
		}
		for _, scm := range scms {
			got, err := syscall.ParseUnixRights(&scm)
			if err != nil {
				closeFDs(fds)
				return nil, fmt.Errorf("wire: parsing unix rights: %w", err)
			}
			for _, fd := range got {
				syscall.CloseOnExec(fd)
			}
			fds = append(fds, got...)
		}
	}
	if flags&syscall.MSG_CTRUNC != 0 {
		// The kernel closed the descriptors that did not fit.
		closeFDs(fds)
		return nil, fmt.Errorf("wire: control message truncated: peer packed more than %d fds into one receive call", maxFDsPerRead)
	}
	return fds, nil
}

func closeFDs(fds []int) {
	for _, fd := range fds {
		_ = syscall.Close(fd)
	}
}
