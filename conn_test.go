package wayland

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/xogas/wayland/wire"
)

func socketPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	f0 := os.NewFile(uintptr(fds[0]), "sock0")
	f1 := os.NewFile(uintptr(fds[1]), "sock1")
	defer f0.Close() //nolint: errcheck
	defer f1.Close() //nolint: errcheck

	c0, err := net.FileConn(f0)
	if err != nil {
		t.Fatalf("FileConn f0: %v", err)
	}
	c1, err := net.FileConn(f1)
	if err != nil {
		_ = c0.Close()
		t.Fatalf("FileConn f1: %v", err)
	}
	return c0.(*net.UnixConn), c1.(*net.UnixConn)
}

type mockMarshaler struct{}

func (m mockMarshaler) Marshal(w *wire.Writer) error { return nil }

// waitForReadCh blocks until the reader goroutine has buffered at least one
// message in readCh, i.e. consumed it from the socket. Polling the actual
// condition keeps tests deterministic without fixed sleeps; the deadline only
// guards against a broken reader. startReader is idempotent, so this works
// both before and after the first Dispatch.
func waitForReadCh(t *testing.T, conn *Conn) {
	t.Helper()
	conn.startReader()
	deadline := time.Now().Add(2 * time.Second)
	for len(conn.readCh) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for reader to buffer a message")
		}
		runtime.Gosched()
	}
}

func TestDisplayID(t *testing.T) {
	if displayID != 1 {
		t.Fatalf("displayID = %d, want 1", displayID)
	}
}

func TestIDAllocation(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck

	id1 := conn.allocID()
	if id1 != 2 {
		t.Fatalf("first allocID: got %d, want 2", id1)
	}
	id2 := conn.allocID()
	if id2 != 3 {
		t.Fatalf("second allocID: got %d, want 3", id2)
	}
}

func TestRegisterLookupUnregister(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck

	p := NewProxy(conn)
	if p.ID() < 2 {
		t.Fatalf("proxy id should be >= 2, got %d", p.ID())
	}
	if p.Deleted() {
		t.Fatal("new proxy should not be deleted")
	}

	conn.RegisterProxy(p)
	got := conn.LookupProxy(p.ID())
	if got != p {
		t.Fatal("LookupProxy returned wrong proxy")
	}

	conn.UnregisterProxy(p.ID())
	if !p.Deleted() {
		t.Fatal("proxy should be deleted after UnregisterProxy")
	}
	if lp := conn.LookupProxy(p.ID()); lp != nil {
		t.Fatal("LookupProxy should return nil after UnregisterProxy")
	}
}

func TestSendRequest(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(p)

	err := conn.SendRequest(p.ID(), 2, mockMarshaler{})
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	obj, opcode, r, err := swc.ReceiveMessage()
	if err != nil {
		t.Fatalf("server ReceiveMessage: %v", err)
	}
	if uint32(obj) != displayID {
		t.Fatalf("obj: got %d, want %d", obj, displayID)
	}
	if opcode != 2 {
		t.Fatalf("opcode: got %d, want 2", opcode)
	}
	if len(r.UnconsumedFDs()) != 0 {
		t.Fatal("expected no fds")
	}
}

func TestSendRequestAfterClose(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)

	p := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(p)
	conn.Close() //nolint: errcheck

	err := conn.SendRequest(p.ID(), 0, mockMarshaler{})
	if err != ErrConnClosed {
		t.Fatalf("SendRequest after close: got %v, want ErrConnClosed", err)
	}
}

func TestSendRequestDeletedProxy(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck

	p := NewProxy(conn)
	conn.RegisterProxy(p)
	conn.UnregisterProxy(p.ID())

	err := p.SendRequest(0, mockMarshaler{})
	if err != ErrObjectDeleted {
		t.Fatalf("SendRequest on deleted proxy: got %v, want ErrObjectDeleted", err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)

	if err := conn.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close: got %v, want nil", err)
	}
	if !conn.IsClosed() {
		t.Fatal("IsClosed should return true")
	}
}

func TestCloseWithoutReader(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close without starting reader: %v", err)
	}
}

func TestGlobalEventDispatch(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(p)
	dpy := NewDisplay(p)
	wireDisplayEvents(dpy, conn)

	reg, err := dpy.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry: %v", err)
	}
	regID := reg.Proxy().ID()

	_, _, _, _ = swc.ReceiveMessage()

	var (
		gotName    uint32
		gotIface   string
		gotVersion uint32
		mu         sync.Mutex
	)
	done := make(chan struct{})

	reg.OnGlobal(func(ev RegistryGlobalEvent) {
		mu.Lock()
		gotName = ev.Name
		gotIface = ev.Interface
		gotVersion = ev.Version
		mu.Unlock()
		close(done)
	})

	w := &wire.Writer{}
	_ = w.Uint32(42)
	_ = w.String("wl_compositor")
	_ = w.Uint32(5)
	if err := swc.SendMessage(wire.ObjectID(regID), RegistryEventGlobal, w); err != nil {
		t.Fatalf("server SendMessage: %v", err)
	}

	ctx := t.Context()
	if err := conn.Dispatch(ctx); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for global event")
	}

	mu.Lock()
	if gotName != 42 {
		t.Fatalf("name: got %d, want 42", gotName)
	}
	if gotIface != "wl_compositor" {
		t.Fatalf("interface: got %q, want %q", gotIface, "wl_compositor")
	}
	if gotVersion != 5 {
		t.Fatalf("version: got %d, want 5", gotVersion)
	}
	mu.Unlock()
}

func TestDeleteIDUnregister(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	p := NewProxy(conn)
	conn.RegisterProxy(p)
	proxyID := p.ID()

	w := &wire.Writer{}
	_ = w.Uint32(proxyID)
	if err := swc.SendMessage(wire.ObjectID(displayID), DisplayEventDeleteID, w); err != nil {
		t.Fatalf("SendMessage delete_id: %v", err)
	}

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if !p.Deleted() {
		t.Fatal("proxy should be deleted after delete_id")
	}
	if lp := conn.LookupProxy(proxyID); lp != nil {
		t.Fatal("proxy should be removed from objects after delete_id")
	}
}

func TestErrorEventDispatch(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	var pe *ProtocolError
	done := make(chan struct{})
	dpy.SetOnError(func(err *ProtocolError) {
		pe = err
		close(done)
	})

	w := &wire.Writer{}
	_ = w.Object(wire.ObjectID(42))
	_ = w.Uint32(3)
	_ = w.String("test error")
	if err := swc.SendMessage(wire.ObjectID(displayID), DisplayEventError, w); err != nil {
		t.Fatalf("SendMessage error: %v", err)
	}

	err := conn.Dispatch(t.Context())
	var pe2 *ProtocolError
	if !errors.As(err, &pe2) {
		t.Fatalf("Dispatch: got %v, want *ProtocolError", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for error event")
	}

	if pe == nil {
		t.Fatal("expected ProtocolError")
	}
	if pe.ObjectID != 42 {
		t.Fatalf("ObjectID: got %d, want 42", pe.ObjectID)
	}
	if pe.Code != 3 {
		t.Fatalf("Code: got %d, want 3", pe.Code)
	}
	if pe.Message != "test error" {
		t.Fatalf("Message: got %q, want %q", pe.Message, "test error")
	}
}

// TestProtocolErrorFatal verifies that wl_display.error is fatal: the error
// is returned by Dispatch, the connection is closed, and every subsequent
// Dispatch and SendRequest fails fast with the same error.
func TestProtocolErrorFatal(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	w := &wire.Writer{}
	_ = w.Object(wire.ObjectID(7))
	_ = w.Uint32(1)
	_ = w.String("boom")
	if err := swc.SendMessage(wire.ObjectID(displayID), DisplayEventError, w); err != nil {
		t.Fatalf("SendMessage error: %v", err)
	}

	err1 := conn.Dispatch(t.Context())
	var pe *ProtocolError
	if !errors.As(err1, &pe) {
		t.Fatalf("Dispatch: got %v, want *ProtocolError", err1)
	}
	if pe.Code != 1 || pe.Message != "boom" {
		t.Fatalf("unexpected protocol error: %v", pe)
	}
	if !conn.IsClosed() {
		t.Fatal("connection should be closed after protocol error")
	}

	// Sticky: every further call fails fast with the same error, not
	// ErrConnClosed.
	err2 := conn.Dispatch(t.Context())
	if !errors.Is(err2, err1) {
		t.Fatalf("second Dispatch: got %v, want sticky %v", err2, err1)
	}
	if err := conn.DispatchPending(); !errors.Is(err, err1) {
		t.Fatalf("DispatchPending: got %v, want sticky %v", err, err1)
	}
	if err := conn.SendRequest(displayID, DisplayRequestSync, mockMarshaler{}); !errors.Is(err, err1) {
		t.Fatalf("SendRequest: got %v, want sticky %v", err, err1)
	}
}

// TestEventDecodeFailureFatal verifies that an event that cannot be decoded
// is fatal: the stream is corrupt, so the connection is terminated and
// Dispatch returns the decode error.
func TestEventDecodeFailureFatal(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(p)
	dpy := NewDisplay(p)
	wireDisplayEvents(dpy, conn)

	reg, err := dpy.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry: %v", err)
	}
	regID := reg.Proxy().ID()
	_, _, _, _ = swc.ReceiveMessage()

	// A registered handler is required for the event to reach the decoder.
	reg.OnGlobal(func(ev RegistryGlobalEvent) {})

	// Malformed global event: the string length (100) exceeds the payload.
	w := &wire.Writer{}
	_ = w.Uint32(1)
	_ = w.Uint32(100)
	if err := swc.SendMessage(wire.ObjectID(regID), RegistryEventGlobal, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	err = conn.Dispatch(t.Context())
	if err == nil {
		t.Fatal("Dispatch: expected decode error, got nil")
	}
	if !conn.IsClosed() {
		t.Fatal("connection should be closed after decode failure")
	}

	// Sticky: subsequent calls fail fast with the same error.
	err2 := conn.Dispatch(t.Context())
	if !errors.Is(err2, err) {
		t.Fatalf("second Dispatch: got %v, want sticky %v", err2, err)
	}
}

// TestUnknownObjectEventNotFatal documents the deliberate handling of events
// for objects that no longer exist: after a client-side destroy, events
// already in flight may still arrive. They carry no fds (fd-carrying objects
// keep zombie entries that drain them), so they are dropped without touching
// the connection.
func TestUnknownObjectEventNotFatal(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxy(conn)
	conn.RegisterProxy(p)
	proxyID := p.ID()
	conn.UnregisterProxy(proxyID) // client-side destroy; no fd events -> no zombie

	w := &wire.Writer{}
	_ = w.Uint32(9)
	if err := swc.SendMessage(wire.ObjectID(proxyID), 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if conn.IsClosed() {
		t.Fatal("connection must survive events for destroyed objects")
	}
	if err := conn.SendRequest(displayID, DisplayRequestSync, mockMarshaler{}); err != nil {
		t.Fatalf("SendRequest after unknown-object event: %v", err)
	}
}

func TestRoundtrip(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	go func() {
		obj, opcode, r, err := swc.ReceiveMessage()
		if err != nil {
			return
		}
		if opcode != DisplayRequestSync {
			t.Errorf("expected sync request, got opcode %d", opcode)
			return
		}
		callbackID, err := r.NewID()
		if err != nil {
			t.Errorf("newID: %v", err)
			return
		}

		w := &wire.Writer{}
		_ = w.Uint32(0)
		_ = swc.SendMessage(wire.ObjectID(callbackID), CallbackEventDone, w)

		w2 := &wire.Writer{}
		_ = w2.Uint32(uint32(callbackID))
		_ = swc.SendMessage(wire.ObjectID(obj), DisplayEventDeleteID, w2)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	if err := dpy.Roundtrip(ctx); err != nil {
		t.Fatalf("Roundtrip: %v", err)
	}
}

func TestRoundtripCancel(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	go func() {
		_, _, _, _ = swc.ReceiveMessage()
	}()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := dpy.Roundtrip(ctx)
	if err != ctx.Err() {
		t.Fatalf("Roundtrip cancelled: got %v, want %v", err, ctx.Err())
	}
}

func TestDispatchPending(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(p)
	dpy := NewDisplay(p)
	wireDisplayEvents(dpy, conn)

	reg, err := dpy.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry: %v", err)
	}
	regID := reg.Proxy().ID()

	_, _, _, _ = swc.ReceiveMessage()

	globalDone := make(chan struct{})
	reg.OnGlobal(func(ev RegistryGlobalEvent) {
		close(globalDone)
	})

	w1 := &wire.Writer{}
	_ = w1.Uint32(1)
	_ = w1.String("test_iface")
	_ = w1.Uint32(1)
	_ = swc.SendMessage(wire.ObjectID(regID), RegistryEventGlobal, w1)

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("first Dispatch: %v", err)
	}

	select {
	case <-globalDone:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for global event")
	}

	deleteDone := make(chan struct{})
	dpy.OnDeleteID(func(ev DisplayDeleteIDEvent) {
		close(deleteDone)
	})

	w2 := &wire.Writer{}
	_ = w2.Uint32(regID)
	_ = swc.SendMessage(wire.ObjectID(displayID), DisplayEventDeleteID, w2)

	// Wait for the reader to buffer the message instead of guessing with a
	// fixed sleep: DispatchPending only drains what has already been read.
	waitForReadCh(t, conn)

	if err := conn.DispatchPending(); err != nil {
		t.Fatalf("DispatchPending: %v", err)
	}

	select {
	case <-deleteDone:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for delete_id from DispatchPending")
	}
}

func TestConcurrentSendAndDispatch(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(p)
	dpy := NewDisplay(p)
	wireDisplayEvents(dpy, conn)

	const numMsgs = 100
	var (
		receivedCount uint32
		mu            sync.Mutex
		done          = make(chan struct{})
	)
	dpy.OnDeleteID(func(ev DisplayDeleteIDEvent) {
		mu.Lock()
		receivedCount++
		if receivedCount >= numMsgs {
			close(done)
		}
		mu.Unlock()
	})

	var wg sync.WaitGroup
	wg.Go(func() {
		for range uint32(numMsgs) {
			err := conn.SendRequest(displayID, DisplayRequestSync, mockMarshaler{})
			if err != nil {
				t.Errorf("SendRequest: %v", err)
				return
			}
		}
	})

	wg.Go(func() {
		for i := range numMsgs {
			obj, opcode, _, err := swc.ReceiveMessage()
			if err != nil {
				t.Errorf("server recv: %v", err)
				return
			}
			if opcode != DisplayRequestSync {
				continue
			}
			w := &wire.Writer{}
			_ = w.Uint32(uint32(i) + 100)
			_ = swc.SendMessage(obj, DisplayEventDeleteID, w)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	go func() {
		for {
			if err := conn.Dispatch(ctx); err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for all delete_id events")
	}

	wg.Wait()
}

func TestFDEventDispatch(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxy(conn)
	p.SetEventFDCounts(map[uint16]int{0: 1})
	conn.RegisterProxy(p)
	proxyID := p.ID()

	tmp, err := os.CreateTemp("", "wayland-test-fd-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name()) //nolint: errcheck
	defer tmp.Close()           //nolint: errcheck

	var sentStat syscall.Stat_t
	if err := syscall.Fstat(int(tmp.Fd()), &sentStat); err != nil {
		t.Fatalf("fstat: %v", err)
	}

	var receivedFD int
	done := make(chan struct{})
	p.RegisterEvent(0, func(r *wire.Reader) {
		fd, fdErr := r.Fd()
		if fdErr != nil {
			t.Errorf("Fd(): %v", fdErr)
			return
		}
		receivedFD = fd
		close(done)
	})

	w := &wire.Writer{}
	_ = w.Uint32(42)
	_ = w.Fd(int(tmp.Fd()))
	if err := swc.SendMessage(wire.ObjectID(proxyID), 0, w); err != nil {
		t.Fatalf("server SendMessage: %v", err)
	}

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for fd event")
	}

	var recvStat syscall.Stat_t
	if err := syscall.Fstat(receivedFD, &recvStat); err != nil {
		t.Fatalf("fstat received fd: %v", err)
	}
	_ = syscall.Close(receivedFD)

	if sentStat.Ino != recvStat.Ino {
		t.Fatalf("fd inode mismatch: sent %d, got %d", sentStat.Ino, recvStat.Ino)
	}
}

func TestFDEventNoHandler(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxy(conn)
	p.SetEventFDCounts(map[uint16]int{0: 1})
	conn.RegisterProxy(p)
	proxyID := p.ID()

	tmp, err := os.CreateTemp("", "wayland-test-fd-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name()) //nolint: errcheck
	defer tmp.Close()           //nolint: errcheck

	w := &wire.Writer{}
	_ = w.Uint32(42)
	_ = w.Fd(int(tmp.Fd()))
	if err := swc.SendMessage(wire.ObjectID(proxyID), 0, w); err != nil {
		t.Fatalf("server SendMessage: %v", err)
	}

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	fds := conn.wc.TakeAllFDs()
	for _, fd := range fds {
		_ = syscall.Close(fd)
	}
}

func TestZombieFDClose(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	p := NewProxy(conn)
	p.SetEventFDCounts(map[uint16]int{0: 1})
	conn.RegisterProxy(p)
	proxyID := p.ID()

	tmp, err := os.CreateTemp("", "wayland-test-fd-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name()) //nolint: errcheck
	defer tmp.Close()           //nolint: errcheck

	sendFD := int(tmp.Fd())

	wDelete := &wire.Writer{}
	wDelete.Uint32(proxyID) //nolint: errcheck
	if err := swc.SendMessage(wire.ObjectID(displayID), DisplayEventDeleteID, wDelete); err != nil {
		t.Fatalf("SendMessage delete_id: %v", err)
	}

	wFD := &wire.Writer{}
	_ = wFD.Uint32(99)
	_ = wFD.Fd(sendFD)
	if err := swc.SendMessage(wire.ObjectID(proxyID), 0, wFD); err != nil {
		t.Fatalf("SendMessage fd event: %v", err)
	}

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch delete_id: %v", err)
	}
	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch fd event for zombie: %v", err)
	}

	if lp := conn.LookupProxy(proxyID); lp != nil {
		t.Fatal("proxy should be removed after delete_id + dispatch")
	}
	if _, isZombie := conn.zombies[proxyID]; isZombie {
		t.Fatal("zombie entry should be cleaned up after fd event is dispatched")
	}

	fds := conn.wc.TakeAllFDs()
	for _, fd := range fds {
		_ = syscall.Close(fd)
	}
}

func TestMultiHandlerDispatch(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxy(conn)
	conn.RegisterProxy(p)
	proxyID := p.ID()

	var (
		h1Val, h2Val       uint32
		h1Called, h2Called bool
		done               = make(chan struct{})
	)

	p.RegisterEvent(0, func(r *wire.Reader) {
		v, err := r.Uint32()
		if err != nil {
			t.Errorf("handler1 uint32: %v", err)
			return
		}
		h1Val = v
		h1Called = true
	})
	p.RegisterEvent(0, func(r *wire.Reader) {
		v, err := r.Uint32()
		if err != nil {
			t.Errorf("handler2 uint32: %v", err)
			return
		}
		h2Val = v
		h2Called = true
		close(done)
	})

	w := &wire.Writer{}
	_ = w.Uint32(0xCAFEBABE)
	if err := swc.SendMessage(wire.ObjectID(proxyID), 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for handlers")
	}

	if !h1Called || !h2Called {
		t.Fatalf("h1 called=%v h2 called=%v", h1Called, h2Called)
	}
	if h1Val != 0xCAFEBABE {
		t.Fatalf("handler1: got 0x%x, want 0xCAFEBABE", h1Val)
	}
	if h2Val != 0xCAFEBABE {
		t.Fatalf("handler2: got 0x%x, want 0xCAFEBABE", h2Val)
	}
}

func TestMultiHandlerWithFD(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	p := NewProxy(conn)
	p.SetEventFDCounts(map[uint16]int{0: 2})
	conn.RegisterProxy(p)
	proxyID := p.ID()

	tmp1, _ := os.CreateTemp("", "wayland-test-*")
	tmp2, _ := os.CreateTemp("", "wayland-test-*")
	defer os.Remove(tmp1.Name()) //nolint: errcheck
	defer os.Remove(tmp2.Name()) //nolint: errcheck
	defer tmp1.Close()           //nolint: errcheck
	defer tmp2.Close()           //nolint: errcheck

	var (
		mu         sync.Mutex
		fds1, fds2 []int
		count      int
		done       = make(chan struct{})
	)

	p.RegisterEvent(0, func(r *wire.Reader) {
		f1, _ := r.Fd()
		f2, _ := r.Fd()
		mu.Lock()
		fds1 = []int{f1, f2}
		count++
		mu.Unlock()
	})
	p.RegisterEvent(0, func(r *wire.Reader) {
		f1, _ := r.Fd()
		f2, _ := r.Fd()
		mu.Lock()
		fds2 = []int{f1, f2}
		count++
		if count == 2 {
			close(done)
		}
		mu.Unlock()
	})

	w := &wire.Writer{}
	_ = w.Uint32(123)
	_ = w.Fd(int(tmp1.Fd()))
	_ = w.Fd(int(tmp2.Fd()))
	if err := swc.SendMessage(wire.ObjectID(proxyID), 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for handlers")
	}

	mu.Lock()
	if len(fds1) != 2 || len(fds2) != 2 {
		t.Fatalf("fds1=%v fds2=%v: both handlers should get 2 fds", fds1, fds2)
	}
	mu.Unlock()

	for _, fd := range fds1 {
		_ = syscall.Close(fd)
	}
	for _, fd := range fds2 {
		_ = syscall.Close(fd)
	}
}

func TestDispatchContextCancel(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := conn.Dispatch(ctx)
	if err != ctx.Err() {
		t.Fatalf("Dispatch with cancelled ctx: got %v, want %v", err, ctx.Err())
	}
}

func TestDispatchAfterClose(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)

	_ = conn.Close()

	err := conn.Dispatch(t.Context())
	if err != ErrConnClosed {
		t.Fatalf("Dispatch after close: got %v, want ErrConnClosed", err)
	}

	err = conn.DispatchPending()
	if err != ErrConnClosed {
		t.Fatalf("DispatchPending after close: got %v, want ErrConnClosed", err)
	}
}

func TestConcurrentSetOnError(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	var onErrCalled sync.WaitGroup
	onErrCalled.Add(1)
	dpy.SetOnError(func(pe *ProtocolError) {
		onErrCalled.Done()
	})

	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 50; i++ {
			dpy.SetOnError(func(pe *ProtocolError) {})
		}
	})

	wg.Go(func() {
		for i := 0; i < 50; i++ {
			conn.connMu.Lock()
			_ = conn.onError
			conn.connMu.Unlock()
		}
	})

	wg.Wait()

	w := &wire.Writer{}
	_ = w.Object(wire.ObjectID(1))
	_ = w.Uint32(0)
	_ = w.String("race test")
	_ = swc.SendMessage(wire.ObjectID(displayID), DisplayEventError, w)

	_ = conn.Dispatch(t.Context())
}

func TestCloseDrainsReadCh(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	swc := wire.NewConn(serverUC)

	p := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(p)
	dpy := NewDisplay(p)
	wireDisplayEvents(dpy, conn)

	w := &wire.Writer{}
	_ = w.Uint32(100)
	_ = swc.SendMessage(wire.ObjectID(displayID), DisplayEventDeleteID, w)

	// Ensure a message is queued in readCh before Close, so the test covers
	// closing a connection with undelivered messages in flight.
	waitForReadCh(t, conn)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStickyReadError(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck

	p := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(p)

	// Server disappears: the reader goroutine hits EOF.
	if err := serverUC.Close(); err != nil {
		t.Fatalf("server close: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	err1 := conn.Dispatch(ctx)
	if err1 == nil {
		t.Fatal("first Dispatch: expected read error, got nil")
	}

	// The error must be sticky: subsequent calls fail fast instead of
	// blocking on a reader goroutine that no longer exists.
	err2 := conn.Dispatch(ctx)
	if err2 != err1 {
		t.Fatalf("second Dispatch: got %v, want sticky %v", err2, err1)
	}
	if err := conn.DispatchPending(); err != err1 {
		t.Fatalf("DispatchPending: got %v, want sticky %v", err, err1)
	}
	if err := conn.SendRequest(displayID, DisplayRequestSync, mockMarshaler{}); err != err1 {
		t.Fatalf("SendRequest: got %v, want sticky %v", err, err1)
	}
}

func TestZombieLifecycle(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	p := NewProxy(conn)
	p.SetEventFDCounts(map[uint16]int{0: 1})
	conn.RegisterProxy(p)
	proxyID := p.ID()

	tmp, err := os.CreateTemp("", "wayland-test-fd-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name()) //nolint: errcheck
	defer tmp.Close()           //nolint: errcheck

	// Client destroys the object (destructor request path).
	conn.UnregisterProxy(proxyID)
	if _, ok := conn.zombies[proxyID]; !ok {
		t.Fatal("expected zombie entry after client-side destroy")
	}

	// An fd-carrying event already in flight arrives, then delete_id.
	wFD := &wire.Writer{}
	_ = wFD.Uint32(1)
	_ = wFD.Fd(int(tmp.Fd()))
	if err := swc.SendMessage(wire.ObjectID(proxyID), 0, wFD); err != nil {
		t.Fatalf("SendMessage fd event: %v", err)
	}
	wDel := &wire.Writer{}
	_ = wDel.Uint32(proxyID)
	if err := swc.SendMessage(wire.ObjectID(displayID), DisplayEventDeleteID, wDel); err != nil {
		t.Fatalf("SendMessage delete_id: %v", err)
	}

	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch zombie event: %v", err)
	}
	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch delete_id: %v", err)
	}

	// The zombie fd must have been drained and closed, not left in the queue.
	if fds := conn.wc.TakeAllFDs(); len(fds) != 0 {
		for _, fd := range fds {
			_ = syscall.Close(fd)
		}
		t.Fatalf("expected empty fd queue, got %d fds", len(fds))
	}
	// delete_id removes the zombie entry for good.
	if _, ok := conn.zombies[proxyID]; ok {
		t.Fatal("zombie entry should be removed after delete_id")
	}
	if !p.Deleted() {
		t.Fatal("proxy should stay deleted")
	}
}

func TestConnDrainAfterClose(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)

	var (
		mu       sync.Mutex
		gotClose bool
	)
	swc2 := wire.NewConn(serverUC)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _, _ = swc2.ReceiveMessage()
		mu.Lock()
		gotClose = true
		mu.Unlock()
	}()

	_ = conn.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}

	mu.Lock()
	if !gotClose {
		t.Log("server may not have detected close immediately")
	}
	mu.Unlock()
}
