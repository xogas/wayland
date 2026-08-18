package wayland

import (
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/xogas/wayland/wire"
)

func swcSend(uc *net.UnixConn, id uint32, op uint16, w *wire.Writer) error {
	return wire.NewConn(uc).SendMessage(wire.ObjectID(id), op, w)
}

// TestRawProxyFDSkew verifies that a raw proxy created via NewProxy or
// Registry.Bind with a registered fd table receives fds correctly and does
// not leave them stuck in the connection-level queue, which would skew every
// subsequent fd-carrying message.
func TestRawProxyFDSkew(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck

	// Simulate a protocol interface with an fd-carrying event at opcode 0.
	// Generated code registers such tables from init; raw bindings rely on
	// the same registry (Registry.Bind looks it up automatically).
	RegisterInterfaceFDCounts("test_iface", map[uint16]int{0: 1, 1: 0})
	p := NewProxy(conn)
	if counts, ok := lookupInterfaceFDCounts("test_iface"); ok {
		p.SetEventFDCounts(counts)
	}
	conn.RegisterProxy(p)

	tmp, err := os.CreateTemp("", "rawproxy-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name()) //nolint: errcheck
	defer tmp.Close()           //nolint: errcheck
	sendFD := int(tmp.Fd())

	gotFD := -1
	p.RegisterEvent(0, func(r *wire.Reader) {
		fd, err := r.Fd()
		if err != nil {
			t.Logf("handler: Fd() error: %v", err)
			return
		}
		gotFD = fd
	})

	w := &wire.Writer{}
	_ = w.Uint32(1)
	_ = w.Fd(sendFD)
	_ = w.Uint32(1024)
	if err := swcSend(serverUC, p.ID(), 0, w); err != nil {
		t.Fatal(err)
	}
	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if gotFD < 0 {
		t.Fatal("handler did not receive the fd")
	}
	_ = syscall.Close(gotFD)

	// A following event with no fd must still decode cleanly.
	p.RegisterEvent(1, func(r *wire.Reader) {
		v, err := r.Uint32()
		if err != nil {
			t.Errorf("handler2 read: %v", err)
			return
		}
		if v != 42 {
			t.Errorf("handler2 payload = %d, want 42", v)
		}
	})
	w2 := &wire.Writer{}
	_ = w2.Uint32(42)
	if err := swcSend(serverUC, p.ID(), 1, w2); err != nil {
		t.Fatal(err)
	}
	if err := conn.Dispatch(t.Context()); err != nil {
		t.Fatalf("Dispatch2: %v", err)
	}

	left := conn.wc.TakeAllFDs()
	for _, fd := range left {
		_ = syscall.Close(fd)
	}
	if len(left) > 0 {
		t.Errorf("fd leak: %d fds left in connection queue", len(left))
	}
}

// TestRawProxyUnknownOpcodeFatal verifies that a raw proxy with a registered
// fd table rejects unknown opcodes as stream violations, like generated
// proxies, instead of silently misreading the stream.
func TestRawProxyUnknownOpcodeFatal(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck

	RegisterInterfaceFDCounts("test_iface2", map[uint16]int{0: 0})
	p := NewProxy(conn)
	if counts, ok := lookupInterfaceFDCounts("test_iface2"); ok {
		p.SetEventFDCounts(counts)
	}
	conn.RegisterProxy(p)

	w := &wire.Writer{}
	_ = w.Uint32(7)
	if err := swcSend(serverUC, p.ID(), 99, w); err != nil {
		t.Fatal(err)
	}
	err := conn.Dispatch(t.Context())
	if err == nil {
		t.Fatal("expected fatal error for unknown opcode on raw proxy")
	}
	if !conn.IsClosed() {
		t.Fatal("connection should be closed after stream violation")
	}
}
