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
	messageMax  = 65535
)

// errSoftTimeout marks a read deadline that the runtime installed for a
// canceled Roundtrip. Unlike a real IO error it must not close the connection.
var errSoftTimeout = errors.New("wayland: soft read timeout")

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
	// oobLen overrides the ancillary buffer size; zero uses the default.
	// It exists so tests can exercise MSG_CTRUNC without exhausting fds.
	oobLen int
	// skipUnknownServer is set only by the test-only raw bind path. Unknown
	// interfaces may create child objects whose IDs cannot be resolved without
	// a generated signature; while this marker is set, events and delete_id
	// messages for unregistered server-range IDs are skipped by length instead
	// of treated as protocol errors. It stays false for all public runtime
	// paths, so AC-O03 behavior is unchanged.
	skipUnknownServer bool
	// unknownServerSkips counts events and delete_id messages consumed by the
	// test-only broad skip path.
	unknownServerSkips int
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
		return nil, fmt.Errorf("dial %s: %w: %v", path, ErrConnect, err)
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
		return nil, fmt.Errorf("dial fd %d: %w: %v", fd, ErrConnect, err)
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

func (c *conn) lookup(id uint32) *Proxy {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.objects[id]
}

func (c *conn) newObject(iface *Interface, version uint32, serverOwned bool) (*Proxy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if iface == nil {
		return nil, fmt.Errorf("new object: nil interface: %w", ErrInvalidArgument)
	}
	if version < 1 || version > iface.Version {
		return nil, fmt.Errorf("new %s v%d: %w", iface.Name, version, ErrVersion)
	}
	id, err := c.allocIDLocked()
	if err != nil {
		return nil, fmt.Errorf("new %s v%d: %w", iface.Name, version, err)
	}
	p := &Proxy{
		conn:        c,
		id:          id,
		version:     version,
		iface:       iface,
		state:       stateAlive,
		serverOwned: serverOwned,
	}
	c.objects[id] = p
	return p, nil
}

func (c *conn) newIncomingObject(iface *Interface, version uint32, id uint32) (*Proxy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if iface == nil || id == 0 || version < 1 || version > iface.Version {
		return nil, fmt.Errorf("incoming %v id %d v%d: %w", iface, id, version, ErrProtocolViolation)
	}
	if _, exists := c.objects[id]; exists {
		return nil, fmt.Errorf("incoming object %d: %w", id, ErrProtocolViolation)
	}
	p := &Proxy{
		conn:        c,
		id:          id,
		version:     version,
		iface:       iface,
		state:       stateAlive,
		serverOwned: true,
	}
	c.objects[id] = p
	return p, nil
}

func (c *conn) allocIDLocked() (uint32, error) {
	for n := len(c.free); n > 0; n = len(c.free) {
		id := c.free[n-1]
		c.free = c.free[:n-1]
		if id >= 1 && id <= clientIDMax {
			return id, nil
		}
	}
	// ID 0 is never an object and ID 1 is reserved for wl_display.
	if c.nextID < 2 {
		c.nextID = 2
	}
	if c.nextID > clientIDMax {
		return 0, ErrIDExhausted
	}
	id := c.nextID
	c.nextID++
	return id, nil
}

func (c *conn) release(p *Proxy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p == nil || p.conn != c {
		return fmt.Errorf("release: %w", ErrInvalidArgument)
	}
	if p.sent {
		return fmt.Errorf("release %d: %w", p.id, ErrInvalidOperation)
	}
	if p.state == stateDestroyed {
		return nil
	}
	delete(c.objects, p.id)
	if p.id >= 1 && p.id <= clientIDMax {
		c.free = append(c.free, p.id)
	}
	p.state = stateDestroyed
	return nil
}

// deleteID handles wl_display.delete_id. A live client-owned object may not be
// deleted: only destroyPending objects and server-owned objects (for example a
// wl_callback that received done) are eligible.
func (c *conn) deleteID(id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.objects[id]
	if !ok {
		if c.skipUnknownServer && id >= 0xFF000000 {
			c.unknownServerSkips++
			return nil
		}
		return fmt.Errorf("delete_id %d: %w", id, ErrProtocolViolation)
	}
	if !p.serverOwned && p.state != stateDestroyPending {
		return fmt.Errorf("delete_id %d for live %s: %w", id, p.iface.Name, ErrProtocolViolation)
	}
	delete(c.objects, id)
	p.state = stateDestroyed
	if id >= 1 && id <= clientIDMax {
		c.free = append(c.free, id)
	}
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

func closeArgFDs(args []Arg) {
	for _, arg := range args {
		if arg.typ == ArgFD && arg.fd >= 0 {
			_ = syscall.Close(arg.fd)
		}
	}
}

func (c *conn) markTombstone(p *Proxy) {
	if p == nil {
		return
	}
	c.mu.Lock()
	if c.objects[p.id] == p {
		p.tombstone = true
	}
	c.mu.Unlock()
}

func (c *conn) markDestroyPending(p *Proxy) {
	if p == nil {
		return
	}
	c.mu.Lock()
	if c.objects[p.id] == p && p.state == stateAlive {
		p.state = stateDestroyPending
	}
	c.mu.Unlock()
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
			return fmt.Errorf("write: %w: %v", ErrIO, err)
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
	if c.oobLen > 0 {
		return c.oobLen
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
		return fmt.Errorf("read: %w: %v", ErrIO, err)
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
			return fmt.Errorf("read control: %w: %v", ErrMalformedMessage, err)
		}
		for i := range msgs {
			rights, err := syscall.ParseUnixRights(&msgs[i])
			if err != nil {
				closeFDs(fds)
				return fmt.Errorf("read fd: %w: %v", ErrMalformedMessage, err)
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

func closeOOBFds(oob []byte) {
	if len(oob) == 0 {
		return
	}
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return
	}
	for i := range msgs {
		rights, err := syscall.ParseUnixRights(&msgs[i])
		if err != nil {
			continue
		}
		closeFDs(rights)
	}
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

func encodeRequest(p *Proxy, opcode uint16, spec Message, args []Arg) ([]byte, []int, error) {
	if p == nil || p.iface == nil {
		return nil, nil, fmt.Errorf("encode: nil object: %w", ErrInvalidArgument)
	}
	if len(args) != len(spec.Args) {
		return nil, nil, fmt.Errorf("encode %s.%s: got %d args, want %d: %w", p.iface.Name, spec.Name, len(args), len(spec.Args), ErrInvalidArgument)
	}
	body := make([]byte, 0, 64)
	var fds []int
	for i, as := range spec.Args {
		arg := args[i]
		switch as.Type {
		case ArgInt:
			if arg.typ != ArgInt {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not int: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, uint32(arg.i32))
		case ArgUint:
			if arg.typ != ArgUint {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not uint: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, arg.u32)
		case ArgFixed:
			if arg.typ != ArgFixed {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not fixed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, uint32(arg.fixedRaw()))
		case ArgString:
			if arg.typ != ArgString {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not string: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if arg.str == nil {
				if !as.AllowNull {
					return nil, nil, fmt.Errorf("encode %s.%s.%s: null string not allowed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
				}
				body = appendU32(body, 0)
				break
			}
			body = appendStringField(body, *arg.str)
		case ArgArray:
			if arg.typ != ArgArray {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not array: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if arg.bytes == nil {
				if !as.AllowNull {
					return nil, nil, fmt.Errorf("encode %s.%s.%s: null array not allowed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
				}
				body = appendU32(body, 0)
				break
			}
			body = appendU32(body, uint32(len(arg.bytes)))
			body = append(body, arg.bytes...)
			body = pad4(body)
		case ArgFD:
			if arg.typ != ArgFD {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not fd: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if !validFD(arg.fd) {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: invalid fd %d: %w", p.iface.Name, spec.Name, as.Name, arg.fd, ErrInvalidArgument)
			}
			fds = append(fds, arg.fd)
		case ArgNewID:
			if arg.typ != ArgNewID {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not new_id: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if arg.u32 == 0 && !as.AllowNull {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: null new_id not allowed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if as.Interface == "" {
				// wl_registry.bind uses a dynamic new_id. The wire format
				// carries the requested interface name and version in
				// addition to the object ID even though the frozen XML
				// signature only names the new_id.
				if arg.u32 == 0 {
					body = appendStringField(body, "")
					body = appendU32(body, 0)
					body = appendU32(body, 0)
					break
				}
				target, err := dynamicNewIDTarget(p, arg.u32)
				if err != nil {
					return nil, nil, fmt.Errorf("encode %s.%s.%s: %w", p.iface.Name, spec.Name, as.Name, err)
				}
				body = appendStringField(body, target.iface.Name)
				body = appendU32(body, target.version)
			}
			body = appendU32(body, arg.u32)
		case ArgObject:
			if arg.typ != ArgObject {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not object: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if arg.u32 == 0 && !as.AllowNull {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: null object not allowed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, arg.u32)
		default:
			return nil, nil, fmt.Errorf("encode %s.%s.%s: unknown arg type: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
		}
	}
	size := 8 + len(body)
	if size > messageMax {
		return nil, nil, fmt.Errorf("encode %s.%s: size %d: %w", p.iface.Name, spec.Name, size, ErrMalformedMessage)
	}
	data := make([]byte, 8, size)
	binary.LittleEndian.PutUint32(data[0:4], p.id)
	binary.LittleEndian.PutUint32(data[4:8], uint32(size)<<16|uint32(opcode))
	data = append(data, body...)
	return data, fds, nil
}

// validFD reports whether fd refers to an open descriptor in this process.
// Only the standard library is available, so this uses fstat(2).
func validFD(fd int) bool {
	if fd < 0 {
		return false
	}
	var st syscall.Stat_t
	return syscall.Fstat(fd, &st) == nil
}

func appendStringField(body []byte, s string) []byte {
	body = appendU32(body, uint32(len(s)+1))
	body = append(body, s...)
	body = append(body, 0)
	return pad4(body)
}

// dynamicNewIDTarget resolves the interface and version of a dynamic new_id
// from the client object that was allocated for it.
func dynamicNewIDTarget(p *Proxy, id uint32) (*Proxy, error) {
	if p == nil || p.conn == nil {
		return nil, fmt.Errorf("dynamic new_id %d: no connection: %w", id, ErrInvalidArgument)
	}
	target := p.conn.lookup(id)
	if target == nil || target.iface == nil {
		return nil, fmt.Errorf("dynamic new_id %d: object not registered: %w", id, ErrInvalidArgument)
	}
	if target.version < 1 || target.version > target.iface.Version {
		return nil, fmt.Errorf("dynamic new_id %d: %s v%d: %w", id, target.iface.Name, target.version, ErrVersion)
	}
	return target, nil
}

func (c *conn) decodeArgs(body []byte, specs []ArgSpec, parent *Proxy) ([]Arg, error) {
	off := 0
	readU32 := func() (uint32, error) {
		if off+4 > len(body) {
			return 0, fmt.Errorf("decode: truncated uint: %w", ErrMalformedMessage)
		}
		v := binary.LittleEndian.Uint32(body[off : off+4])
		off += 4
		return v, nil
	}
	readStringField := func() (string, error) {
		n, err := readU32()
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "", nil
		}
		if int(n) > len(body)-off || n < 1 {
			return "", fmt.Errorf("decode string length %d: %w", n, ErrMalformedMessage)
		}
		raw := body[off : off+int(n)]
		if raw[len(raw)-1] != 0 {
			return "", fmt.Errorf("decode string: missing NUL: %w", ErrMalformedMessage)
		}
		s := string(raw[:len(raw)-1])
		off += int(n)
		off = align4(off)
		return s, nil
	}
	args := make([]Arg, 0, len(specs))
	ok := false
	defer func() {
		if !ok {
			closeArgFDs(args)
		}
	}()
	for _, as := range specs {
		switch as.Type {
		case ArgInt:
			v, err := readU32()
			if err != nil {
				return nil, err
			}
			args = append(args, Int(int32(v)))
		case ArgUint:
			v, err := readU32()
			if err != nil {
				return nil, err
			}
			args = append(args, Uint(v))
		case ArgFixed:
			v, err := readU32()
			if err != nil {
				return nil, err
			}
			args = append(args, Arg{typ: ArgFixed, f64: float64(int32(v)) / 256})
		case ArgString:
			n, err := readU32()
			if err != nil {
				return nil, err
			}
			if n == 0 {
				if !as.AllowNull {
					return nil, fmt.Errorf("decode %s: null string not allowed: %w", as.Name, ErrInvalidArgument)
				}
				args = append(args, String(nil))
				break
			}
			if int(n) > len(body)-off || n < 1 {
				return nil, fmt.Errorf("decode string length %d: %w", n, ErrMalformedMessage)
			}
			raw := body[off : off+int(n)]
			if raw[len(raw)-1] != 0 {
				return nil, fmt.Errorf("decode string: missing NUL: %w", ErrMalformedMessage)
			}
			s := string(raw[:len(raw)-1])
			off += int(n)
			off = align4(off)
			args = append(args, String(&s))
		case ArgArray:
			n, err := readU32()
			if err != nil {
				return nil, err
			}
			if n == 0 {
				// A zero-length array is indistinguishable from a null
				// array on the wire; the protocol treats it as empty.
				args = append(args, Array(nil))
				break
			}
			if int(n) > len(body)-off {
				return nil, fmt.Errorf("decode array length %d: %w", n, ErrMalformedMessage)
			}
			b := append([]byte(nil), body[off:off+int(n)]...)
			off += int(n)
			off = align4(off)
			args = append(args, Array(b))
		case ArgFD:
			fd, err := c.takeFD()
			if err != nil {
				return nil, err
			}
			args = append(args, FD(fd))
		case ArgNewID:
			if as.Interface == "" {
				// Dynamic new_id (wl_registry.bind): the wire carries the
				// requested interface name, its version and the object ID.
				ifaceName, err := readStringField()
				if err != nil {
					return nil, err
				}
				version, err := readU32()
				if err != nil {
					return nil, err
				}
				id, err := readU32()
				if err != nil {
					return nil, err
				}
				if id == 0 {
					if !as.AllowNull {
						return nil, fmt.Errorf("decode %s: null new_id not allowed: %w", as.Name, ErrInvalidArgument)
					}
					args = append(args, NewID(0))
					break
				}
				iface, okInterface := InterfaceByName(ifaceName)
				if !okInterface || version < 1 || version > iface.Version {
					return nil, fmt.Errorf("decode dynamic new_id %q v%d: %w", ifaceName, version, ErrInvalidArgument)
				}
				p, err := c.newIncomingObject(iface, version, id)
				if err != nil {
					return nil, err
				}
				args = append(args, Arg{typ: ArgNewID, u32: id, proxy: p})
				break
			}
			id, err := readU32()
			if err != nil {
				return nil, err
			}
			if id == 0 {
				if !as.AllowNull {
					return nil, fmt.Errorf("decode %s: null new_id not allowed: %w", as.Name, ErrInvalidArgument)
				}
				args = append(args, NewID(0))
				break
			}
			iface, okInterface := InterfaceByName(as.Interface)
			if !okInterface {
				return nil, fmt.Errorf("decode new_id interface %q: %w", as.Interface, ErrProtocolViolation)
			}
			version := parent.version
			if iface.Version < version {
				version = iface.Version
			}
			p, err := c.newIncomingObject(iface, version, id)
			if err != nil {
				return nil, err
			}
			args = append(args, Arg{typ: ArgNewID, u32: id, proxy: p})
		case ArgObject:
			id, err := readU32()
			if err != nil {
				return nil, err
			}
			if id == 0 {
				if !as.AllowNull {
					return nil, fmt.Errorf("decode %s: null object not allowed: %w", as.Name, ErrInvalidArgument)
				}
				args = append(args, Object(0))
				break
			}
			p := c.lookup(id)
			if p == nil {
				return nil, fmt.Errorf("decode object %d: %w", id, ErrProtocolViolation)
			}
			args = append(args, Arg{typ: ArgObject, u32: id, proxy: p})
		default:
			return nil, fmt.Errorf("decode %s: unknown arg type: %w", as.Name, ErrMalformedMessage)
		}
	}
	if off != len(body) {
		return nil, fmt.Errorf("decode: %d trailing bytes: %w", len(body)-off, ErrMalformedMessage)
	}
	ok = true
	return args, nil
}

func appendU32(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func align4(n int) int {
	if n%4 == 0 {
		return n
	}
	return n + (4 - n%4)
}
