package wayland

import (
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/xogas/wayland/wire"
)

// countOpenFDs returns the number of open file descriptors of this process.
func countOpenFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot read /proc/self/fd: %v", err)
	}
	return len(ents)
}

// TestSoakCreateDestroyCycles exercises repeated create/destroy with
// in-flight events and delete_id confirmations, asserting no fd growth and
// no buildup in the objects or zombies maps. This is the destroy race under
// load.
func TestSoakCreateDestroyCycles(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	swc := wire.NewConn(serverUC)

	dpyProxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(dpyProxy)
	dpy := NewDisplay(dpyProxy)
	wireDisplayEvents(dpy, conn)

	const cycles = 2000
	before := countOpenFDs(t)

	for i := 0; i < cycles; i++ {
		p := NewProxy(conn)
		id := p.ID()
		conn.RegisterProxy(p)
		// Pointer-like event table: opcode 2 (motion) carries no fds.
		p.SetEventFDCounts(map[uint16]int{2: 0})

		// Client destroys the object; an event is already in flight.
		conn.UnregisterProxy(id)
		w := &wire.Writer{}
		_ = w.Uint32(1)
		if err := swc.SendMessage(wire.ObjectID(id), 2, w); err != nil {
			t.Fatalf("SendMessage in-flight event: %v", err)
		}
		if err := conn.Dispatch(t.Context()); err != nil {
			t.Fatalf("Dispatch in-flight event: %v", err)
		}

		// The server confirms the destruction with delete_id.
		wDel := &wire.Writer{}
		_ = wDel.Uint32(id)
		if err := swc.SendMessage(wire.ObjectID(displayID), DisplayEventDeleteID, wDel); err != nil {
			t.Fatalf("SendMessage delete_id: %v", err)
		}
		if err := conn.Dispatch(t.Context()); err != nil {
			t.Fatalf("Dispatch delete_id: %v", err)
		}
	}

	conn.objectsMu.RLock()
	nObjects := len(conn.objects)
	nZombies := len(conn.zombies)
	conn.objectsMu.RUnlock()
	if nObjects != 1 {
		t.Fatalf("objects map: %d entries, want 1 (display proxy)", nObjects)
	}
	if nZombies != 0 {
		t.Fatalf("zombies map: %d entries, want 0", nZombies)
	}

	runtime.GC()
	if after := countOpenFDs(t); after > before+4 {
		t.Errorf("fd leak: %d before, %d after %d create/destroy cycles", before, after, cycles)
	}
}

// TestSoakFDThroughput hammers fd-carrying events through the full dispatch
// path (SCM_RIGHTS receive, handler close) and checks that fds do not
// accumulate in the process.
func TestSoakFDThroughput(t *testing.T) {
	clientUC, serverUC := socketPair(t)
	defer clientUC.Close() //nolint: errcheck
	defer serverUC.Close() //nolint: errcheck

	wc := wire.NewConn(clientUC)
	conn := newConn(clientUC, wc)
	defer conn.Close() //nolint: errcheck
	swc := wire.NewConn(serverUC)

	kb := NewKeyboard(NewProxyWithID(conn, 6))
	conn.RegisterProxy(kb.Proxy())
	kb.Proxy().SetVersion(6)
	var closed atomic.Int64
	kb.OnKeymap(func(ev KeyboardKeymapEvent) {
		_ = syscall.Close(ev.Fd)
		closed.Add(1)
	})

	tmp, err := os.CreateTemp("", "wayland-soak-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name()) //nolint: errcheck
	defer tmp.Close()           //nolint: errcheck
	sendFD := int(tmp.Fd())

	before := countOpenFDs(t)
	const n = 200
	for range n {
		w := &wire.Writer{}
		_ = w.Uint32(1)
		_ = w.Fd(sendFD)
		_ = w.Uint32(1024)
		if err := swc.SendMessage(6, KeyboardEventKeymap, w); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		if err := conn.Dispatch(t.Context()); err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
	}
	if got := closed.Load(); got != n {
		t.Fatalf("handler ran %d times, want %d", got, n)
	}
	runtime.GC()
	if after := countOpenFDs(t); after > before+4 {
		t.Errorf("fd leak: %d before, %d after %d fd events", before, after, n)
	}
}

// TestSoakRoundtrip runs many sync roundtrips against a fake server and
// verifies the connection stays healthy.
func TestSoakRoundtrip(t *testing.T) {
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
		for {
			obj, _, r, err := swc.ReceiveMessage()
			if err != nil {
				return
			}
			if obj != 1 {
				continue
			}
			cbID, err := r.NewID()
			if err != nil {
				return
			}
			w := &wire.Writer{}
			_ = w.Uint32(0)
			if err := swc.SendMessage(wire.ObjectID(cbID), 0, w); err != nil {
				return
			}
		}
	}()

	start := time.Now()
	for i := range 500 {
		if err := dpy.Roundtrip(t.Context()); err != nil {
			t.Fatalf("Roundtrip %d: %v", i, err)
		}
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("500 roundtrips took %v", d)
	}
	if conn.IsClosed() {
		t.Fatal("connection closed during roundtrip soak")
	}
}
