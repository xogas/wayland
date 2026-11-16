package wayland

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func unixPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	mk := func(fd int, name string) *net.UnixConn {
		f := os.NewFile(uintptr(fd), name)
		raw, err := net.FileConn(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("fileconn: %v", err)
		}
		uc, ok := raw.(*net.UnixConn)
		if !ok {
			_ = raw.Close()
			t.Fatalf("fileconn returned %T", raw)
		}
		return uc
	}
	return mk(fds[0], "wayland-a"), mk(fds[1], "wayland-b")
}

type registryRecorder struct {
	globals []Global
}

func (r *registryRecorder) Global(g Global) { r.globals = append(r.globals, g) }

func (r *registryRecorder) GlobalRemove(name uint32) {
	for i := range r.globals {
		if r.globals[i].Name == name {
			r.globals = append(r.globals[:i], r.globals[i+1:]...)
			return
		}
	}
}

type callbackRecorder struct {
	done uint32
}

func (c *callbackRecorder) Done(v uint32) { c.done = v }

// testServer reads framed messages from the peer end of a socketpair. It keeps
// a byte buffer and collected descriptors so partial reads and ancillary data
// are handled uniformly.
type testServer struct {
	conn *net.UnixConn
	buf  []byte
	fds  []int
}

func newTestServer(c *net.UnixConn) *testServer {
	return &testServer{conn: c}
}

func (s *testServer) nextFrame() (uint32, uint16, []byte, []int, error) {
	for {
		if len(s.buf) >= 8 {
			size := int(binary.LittleEndian.Uint32(s.buf[4:8]) >> 16)
			if size < 8 {
				return 0, 0, nil, nil, fmt.Errorf("server frame size %d", size)
			}
			if len(s.buf) >= size {
				objectID := binary.LittleEndian.Uint32(s.buf[0:4])
				opcode := uint16(binary.LittleEndian.Uint32(s.buf[4:8]))
				body := append([]byte(nil), s.buf[8:size]...)
				s.buf = s.buf[size:]
				fds := s.fds
				s.fds = nil
				return objectID, opcode, body, fds, nil
			}
		}
		tmp := make([]byte, messageMax+8)
		oob := make([]byte, 4096)
		n, oobn, _, _, err := s.conn.ReadMsgUnix(tmp, oob)
		if err != nil {
			return 0, 0, nil, nil, err
		}
		if n == 0 {
			return 0, 0, nil, nil, io.ErrUnexpectedEOF
		}
		s.buf = append(s.buf, tmp[:n]...)
		if oobn > 0 {
			msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
			if err != nil {
				return 0, 0, nil, nil, err
			}
			for i := range msgs {
				rights, err := syscall.ParseUnixRights(&msgs[i])
				if err != nil {
					return 0, 0, nil, nil, err
				}
				s.fds = append(s.fds, rights...)
			}
		}
	}
}

func (s *testServer) readFrame(t *testing.T) (uint32, uint16, []byte, []int) {
	t.Helper()
	objectID, opcode, body, fds, err := s.nextFrame()
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	return objectID, opcode, body, fds
}

func writeFrameMsg(t *testing.T, srv *net.UnixConn, objectID uint32, opcode uint16, body []byte, fds []int) {
	t.Helper()
	size := 8 + len(body)
	if size > messageMax {
		t.Fatalf("frame too large: %d", size)
	}
	msg := make([]byte, 8, size)
	binary.LittleEndian.PutUint32(msg[0:4], objectID)
	binary.LittleEndian.PutUint32(msg[4:8], uint32(size)<<16|uint32(opcode))
	msg = append(msg, body...)
	var oob []byte
	if len(fds) > 0 {
		oob = syscall.UnixRights(fds...)
	}
	for len(msg) > 0 {
		var n int
		var err error
		if len(oob) > 0 {
			n, _, err = srv.WriteMsgUnix(msg, oob, nil)
		} else {
			n, err = srv.Write(msg)
		}
		if err != nil {
			t.Fatalf("write frame: %v", err)
		}
		if n == 0 {
			t.Fatalf("write frame: short write")
		}
		oob = nil
		msg = msg[n:]
	}
}

func writeRawHeader(t *testing.T, srv *net.UnixConn, objectID uint32, opcode uint16, size uint16) {
	t.Helper()
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], objectID)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(size)<<16|uint32(opcode))
	if _, err := srv.Write(hdr[:]); err != nil {
		t.Fatalf("write header: %v", err)
	}
}

func assertNoBytes(t *testing.T, srv *net.UnixConn) {
	t.Helper()
	if err := srv.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	defer func() { _ = srv.SetReadDeadline(time.Time{}) }()
	var buf [1]byte
	n, err := srv.Read(buf[:])
	if n > 0 {
		t.Fatalf("unexpected %d byte(s) written", n)
	}
	if err == nil {
		t.Fatal("read returned nil error with no bytes")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("expected read timeout, got %v", err)
	}
}

func pipeFDs(t *testing.T) (int, int) {
	t.Helper()
	var fds [2]int
	if err := syscall.Pipe2(fds[:], syscall.O_CLOEXEC); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	return fds[0], fds[1]
}

func fdIdentity(t *testing.T, fd int) (uint64, uint64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		t.Fatalf("fstat fd %d: %v", fd, err)
	}
	return uint64(st.Dev), st.Ino
}

func sameFD(t *testing.T, a, b int) bool {
	t.Helper()
	da, ia := fdIdentity(t, a)
	db, ib := fdIdentity(t, b)
	return da == db && ia == ib
}

func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := strings.Fields(string(buf[:n]))
	if len(fields) < 2 {
		return 0
	}
	id, _ := strconv.ParseUint(fields[1], 10, 64)
	return id
}

func testInterface(name string, version uint32, requests, events []Message) *Interface {
	return &Interface{Name: name, Version: version, Requests: requests, Events: events}
}

func newTestProxy(t *testing.T, d *Display, iface *Interface, version uint32, sink EventSink) *Proxy {
	t.Helper()
	p, err := d.p.conn.newObject(iface, version, false)
	if err != nil {
		t.Fatalf("new object %s: %v", iface.Name, err)
	}
	if sink != nil {
		if err := p.SetSink(sink); err != nil {
			t.Fatalf("set sink: %v", err)
		}
	}
	return p
}

// installTestHooks installs the test-only conn hooks with initialized counters.
// Production paths leave c.hooks nil.
func installTestHooks(c *conn) *testHooks {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		c.hooks = &testHooks{
			rawNames:      make(map[uint32]string),
			skippedEvents: make(map[uint32]uint32),
		}
	}
	return c.hooks
}

// skippedEventCount reports how many raw-object events the length-based skip
// path consumed for id.
func skippedEventCount(c *conn, id uint32) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hooks == nil {
		return 0
	}
	return c.hooks.skippedEvents[id]
}

type funcSink struct {
	fn func(opcode uint16, args []Arg) error
}

func (s funcSink) DispatchEvent(opcode uint16, args []Arg) error {
	return s.fn(opcode, args)
}
