package wayland

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	clientIDMax = 0xFEFFFFFF
	messageMax  = 65535
)

type conn struct {
	uc         *net.UnixConn
	mu         sync.Mutex
	writeMu    sync.Mutex
	dispatchMu sync.Mutex
	objects    map[uint32]*Proxy
	nextID     uint32
	free       []uint32
	closed     bool
	rbuf       []byte
	fds        []int
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
	return &Display{p: p}
}

func (c *conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
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
		return nil, err
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
	if n := len(c.free); n > 0 {
		id := c.free[n-1]
		c.free = c.free[:n-1]
		return id, nil
	}
	if c.nextID == 0 || c.nextID > clientIDMax {
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
	c.free = append(c.free, p.id)
	p.state = stateDestroyed
	return nil
}

func (c *conn) deleteID(id uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.objects[id]
	if !ok {
		return fmt.Errorf("delete_id %d: %w", id, ErrProtocolViolation)
	}
	delete(c.objects, id)
	c.free = append(c.free, id)
	p.state = stateDestroyed
	return nil
}

func (c *conn) close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	err := c.uc.Close()
	for _, fd := range c.fds {
		_ = syscall.Close(fd)
	}
	c.fds = nil
	c.mu.Unlock()
	return err
}

func (c *conn) sendRequest(p *Proxy, opcode uint16, args []Arg) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if p == nil || p.conn != c {
		c.mu.Unlock()
		return fmt.Errorf("send: %w", ErrInvalidArgument)
	}
	if p.state != stateAlive {
		c.mu.Unlock()
		return fmt.Errorf("send object %d: %w", p.id, ErrObjectDestroyed)
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
	data, fds, err := encodeRequest(p, opcode, spec, args)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if err := c.write(data, fds); err != nil {
		_ = c.close()
		return err
	}
	c.mu.Lock()
	p.sent = true
	c.mu.Unlock()
	return nil
}

func (c *conn) write(data []byte, fds []int) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.isClosed() {
		return ErrClosed
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
			oob = nil
		} else {
			n, err = c.uc.Write(data)
		}
		if err != nil {
			return fmt.Errorf("write: %w: %v", ErrIO, err)
		}
		if n == 0 {
			return fmt.Errorf("write: %w", ErrIO)
		}
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

func (c *conn) readMore() error {
	buf := make([]byte, messageMax+8)
	oob := make([]byte, 4096)
	n, oobn, flags, _, err := c.uc.ReadMsgUnix(buf, oob)
	if err != nil {
		if c.isClosed() {
			return ErrClosed
		}
		return fmt.Errorf("read: %w: %v", ErrIO, err)
	}
	if flags&syscall.MSG_CTRUNC != 0 {
		return fmt.Errorf("read: control data truncated: %w", ErrMalformedMessage)
	}
	if n == 0 {
		return fmt.Errorf("read: EOF: %w", ErrIO)
	}
	c.mu.Lock()
	c.rbuf = append(c.rbuf, buf[:n]...)
	c.mu.Unlock()
	if oobn > 0 {
		msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			return fmt.Errorf("read control: %w: %v", ErrMalformedMessage, err)
		}
		var fds []int
		for i := range msgs {
			rights, err := syscall.ParseUnixRights(&msgs[i])
			if err != nil {
				return fmt.Errorf("read fd: %w: %v", ErrMalformedMessage, err)
			}
			fds = append(fds, rights...)
		}
		c.mu.Lock()
		c.fds = append(c.fds, fds...)
		c.mu.Unlock()
	}
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
				body = appendU32(body, 0)
				break
			}
			s := []byte(*arg.str)
			body = appendU32(body, uint32(len(s)+1))
			body = append(body, s...)
			body = append(body, 0)
			body = pad4(body)
		case ArgArray:
			if arg.typ != ArgArray {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not array: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, uint32(len(arg.bytes)))
			body = append(body, arg.bytes...)
			body = pad4(body)
		case ArgFD:
			if arg.typ != ArgFD || arg.fd < 0 {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: invalid fd: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			fds = append(fds, arg.fd)
		case ArgNewID:
			if arg.typ != ArgNewID || arg.u32 == 0 {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: invalid new_id: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, arg.u32)
		case ArgObject:
			if arg.typ != ArgObject {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not object: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
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
	args := make([]Arg, 0, len(specs))
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
			id, err := readU32()
			if err != nil {
				return nil, err
			}
			if id == 0 {
				args = append(args, NewID(0))
				break
			}
			if as.Interface == "" {
				return nil, fmt.Errorf("decode new_id: missing interface: %w", ErrMalformedMessage)
			}
			iface, ok := InterfaceByName(as.Interface)
			if !ok {
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
