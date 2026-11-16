package wayland

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

const (
	clientIDMax = 0xFEFFFFFF
	serverIDMin = 0xFF000000
	messageMax  = 65535
)

// errSoftTimeout marks a read deadline that the runtime installed for a
// canceled Roundtrip. Unlike a real IO error it must not close the connection.
var errSoftTimeout = errors.New("wayland: soft read timeout")

// testHooks carries state that only tests need. It is nil for every production
// path, so the public runtime never enables the raw-bind or unknown-event skip
// behavior.
type testHooks struct {
	// rawNames marks objects created by the test-only raw bind path; their
	// events are skipped by length because no generated signature exists.
	rawNames map[uint32]string
	// skippedEvents counts raw-object events consumed by the length-based
	// skip path.
	skippedEvents map[uint32]uint32
	// skipUnknownServer enables the broad skip for unregistered server-range
	// IDs created by an unknown interface.
	skipUnknownServer bool
	// unknownServerSkips counts events and delete_id messages consumed by the
	// broad skip.
	unknownServerSkips int
	// oobLen overrides the ancillary buffer size; zero uses the default.
	oobLen int
}

type conn struct {
	uc                *net.UnixConn
	mu                sync.Mutex
	writeMu           sync.Mutex
	dispatchMu        sync.Mutex
	closeMu           sync.Mutex
	dispatchWG        sync.WaitGroup
	callbackActive    atomic.Bool
	roundtripCanceled atomic.Bool
	objects           map[uint32]*Proxy
	nextID            uint32
	free              []uint32
	closed            bool
	rbuf              []byte
	fds               []int
	// softTimeout is set only by the goroutine that currently owns
	// dispatchMu while a Roundtrip is waiting on a context.
	softTimeout bool
	// hooks is nil in production and installed only by tests.
	hooks *testHooks
}

// Connect opens a Wayland connection using WAYLAND_SOCKET, WAYLAND_DISPLAY
// and XDG_RUNTIME_DIR, in that order.
func Connect() (*Display, error) {
	path, fd, err := resolveSource()
	if err != nil {
		return nil, err
	}
	if fd >= 0 {
		return DialFD(fd)
	}
	return DialContext(context.Background(), path)
}

// DialContext opens an explicit Wayland socket path.
func DialContext(ctx context.Context, path string) (*Display, error) {
	if path == "" {
		return nil, fmt.Errorf("dial: empty path: %w", ErrSource)
	}
	d := net.Dialer{}
	raw, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w: %w", path, ErrConnect, err)
	}
	uc, ok := raw.(*net.UnixConn)
	if !ok {
		_ = raw.Close()
		return nil, fmt.Errorf("dial %s: not a Unix socket: %w", path, ErrConnect)
	}
	return newDisplay(uc), nil
}

// DialFD takes ownership of an already-connected file descriptor.
func DialFD(fd int) (*Display, error) {
	if fd < 0 {
		return nil, fmt.Errorf("dial fd %d: %w", fd, ErrSource)
	}
	f := os.NewFile(uintptr(fd), "wayland-socket")
	if f == nil {
		return nil, fmt.Errorf("dial fd %d: %w", fd, ErrConnect)
	}
	raw, err := net.FileConn(f)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("dial fd %d: %w: %w", fd, ErrConnect, err)
	}
	uc, ok := raw.(*net.UnixConn)
	if !ok {
		_ = raw.Close()
		return nil, fmt.Errorf("dial fd %d: not a Unix socket: %w", fd, ErrConnect)
	}
	return newDisplay(uc), nil
}

func resolveSource() (string, int, error) {
	if s := os.Getenv("WAYLAND_SOCKET"); s != "" {
		fd, err := strconv.Atoi(s)
		if err != nil || fd < 0 {
			return "", -1, fmt.Errorf("WAYLAND_SOCKET=%q: %w", s, ErrSource)
		}
		return "", fd, nil
	}
	display := os.Getenv("WAYLAND_DISPLAY")
	if display == "" {
		display = "wayland-0"
	}
	if strings.HasPrefix(display, "/") {
		return display, -1, nil
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", -1, fmt.Errorf("WAYLAND_DISPLAY=%q without XDG_RUNTIME_DIR: %w", display, ErrSource)
	}
	return runtimeDir + "/" + display, -1, nil
}

func newDisplay(uc *net.UnixConn) *Display {
	c := &conn{
		uc:      uc,
		objects: make(map[uint32]*Proxy),
		nextID:  2,
	}
	p := &Proxy{
		conn:    c,
		id:      1,
		version: 1,
		iface:   mustInterface("wl_display"),
		state:   stateAlive,
	}
	c.objects[1] = p
	return NewDisplay(p)
}

func (c *conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// beginDispatch registers an in-flight Dispatch/Roundtrip. It is paired with
// dispatchWG.Done by the caller; Close waits on the same WaitGroup after
// closing the socket. The Add happens under c.mu so it cannot race with the
// close transition: either the Close sees the dispatch, or beginDispatch sees
// the closed connection.
func (c *conn) beginDispatch() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.dispatchWG.Add(1)
	return nil
}

// close tears the socket down without waiting for Dispatch. Display.Close waits;
// error paths call this directly while dispatchMu may already be held.
func (c *conn) close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	uc := c.uc
	fds := c.fds
	c.fds = nil
	c.mu.Unlock()

	var err error
	if uc != nil {
		err = uc.Close()
	}
	closeFDs(fds)
	return err
}

func closeFDs(fds []int) {
	for _, fd := range fds {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
	}
}

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
	if c.hooks != nil && c.hooks.oobLen > 0 {
		return c.hooks.oobLen
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
		if c.softTimeout {
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
