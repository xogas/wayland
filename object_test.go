package wayland

import (
	"errors"
	"testing"
)

func newTestConn() *conn {
	return &conn{objects: make(map[uint32]*Proxy), nextID: 2}
}

func TestObjectIDAllocation(t *testing.T) {
	c := newTestConn()
	iface := mustInterface("wl_registry")
	p1, err := c.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new p1: %v", err)
	}
	if p1.ID() != 2 {
		t.Fatalf("p1 id = %d, want 2", p1.ID())
	}
	p2, err := c.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new p2: %v", err)
	}
	if p2.ID() != 3 {
		t.Fatalf("p2 id = %d, want 3", p2.ID())
	}
	if err := c.release(p1); err != nil {
		t.Fatalf("release p1: %v", err)
	}
	p3, err := c.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new p3: %v", err)
	}
	if p3.ID() != 2 {
		t.Fatalf("p3 id = %d, want reused 2", p3.ID())
	}
	p2.state = stateDestroyPending
	if err := c.deleteID(p2.ID()); err != nil {
		t.Fatalf("delete p2: %v", err)
	}
	if c.lookup(p2.ID()) != nil {
		t.Fatal("p2 still in object table")
	}
	if err := c.deleteID(9999); !errors.Is(err, ErrProtocolViolation) {
		t.Fatalf("unknown delete_id = %v, want ErrProtocolViolation", err)
	}
}

func TestObjectReleaseSent(t *testing.T) {
	c := newTestConn()
	p, err := c.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	p.sent = true
	if err := c.release(p); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("release sent = %v, want ErrInvalidOperation", err)
	}
	if c.lookup(p.ID()) == nil {
		t.Fatal("sent object removed by failed release")
	}
}

func TestObjectVersionAndExhaustion(t *testing.T) {
	c := newTestConn()
	if _, err := c.newObject(mustInterface("wl_registry"), 2, false); !errors.Is(err, ErrVersion) {
		t.Fatalf("version 2 = %v, want ErrVersion", err)
	}
	if _, err := c.newObject(nil, 1, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil iface = %v, want ErrInvalidArgument", err)
	}
	c2 := &conn{objects: make(map[uint32]*Proxy), nextID: clientIDMax}
	p, err := c2.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("max id: %v", err)
	}
	if p.ID() != clientIDMax {
		t.Fatalf("max id = %d, want %d", p.ID(), clientIDMax)
	}
	if _, err := c2.newObject(mustInterface("wl_registry"), 1, false); !errors.Is(err, ErrIDExhausted) {
		t.Fatalf("exhausted = %v, want ErrIDExhausted", err)
	}
}

func TestObjectClosed(t *testing.T) {
	c := newTestConn()
	c.closed = true
	if _, err := c.newObject(mustInterface("wl_registry"), 1, false); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed new = %v, want ErrClosed", err)
	}
}

func testInterface(_ string, version uint32, requests, events []Message) *Interface {
	return &Interface{Name: "wl_surface", Version: version, Requests: requests, Events: events}
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

type funcSink struct {
	fn func(opcode uint16, args []Arg) error
}

func (s funcSink) DispatchEvent(opcode uint16, args []Arg) error {
	return s.fn(opcode, args)
}

func TestDestructorMarksDestroyPending(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })

	iface := &Interface{
		Name:    "test_destructor",
		Version: 1,
		Requests: []Message{
			{Name: "destroy", Since: 1, Destructor: true},
		},
	}
	p, err := d.p.conn.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new object: %v", err)
	}
	if err := p.Send(0); err != nil {
		t.Fatalf("destructor send: %v", err)
	}
	if err := p.Send(0); !errors.Is(err, ErrObjectDestroyed) {
		t.Fatalf("second send = %v, want ErrObjectDestroyed", err)
	}
}

func TestNonDestructorStaysAlive(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })

	iface := &Interface{
		Name:    "test_alive",
		Version: 1,
		Requests: []Message{
			{Name: "ping", Since: 1},
		},
	}
	p, err := d.p.conn.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new object: %v", err)
	}
	for n := 0; n < 2; n++ {
		if err := p.Send(0); err != nil {
			t.Fatalf("send %d: %v", n, err)
		}
	}
}

func TestUnknownObject(t *testing.T) {
	mustClosed := func(t *testing.T, d *Display) {
		t.Helper()
		if !d.p.conn.isClosed() {
			t.Fatal("connection not closed after protocol violation")
		}
	}

	t.Run("unknown_event_object", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		writeFrame(t, b, 99, 0, nil)
		if err := d.Dispatch(); !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("dispatch = %v, want ErrProtocolViolation", err)
		}
		mustClosed(t, d)
	})

	t.Run("unknown_server_range_event", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		writeFrame(t, b, serverIDMin, 0, nil)
		if err := d.Dispatch(); err != nil {
			t.Fatalf("dispatch = %v, want nil for unknown server-range object", err)
		}
		if d.p.conn.isClosed() {
			t.Fatal("connection closed for unknown server-range object")
		}
	})

	t.Run("unknown_server_range_delete_id", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		writeFrame(t, b, 1, 1, appendU32(nil, serverIDMin+1))
		if err := d.Dispatch(); err != nil {
			t.Fatalf("dispatch = %v, want nil for unknown server-range delete_id", err)
		}
		if d.p.conn.isClosed() {
			t.Fatal("connection closed for unknown server-range delete_id")
		}
	})

	t.Run("unknown_opcode", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		iface := testInterface("test_opcode", 1, nil, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		writeFrame(t, b, p.ID(), 9, nil)
		if err := d.Dispatch(); !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("dispatch = %v, want ErrProtocolViolation", err)
		}
		mustClosed(t, d)
	})

	t.Run("unknown_delete_id", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		writeFrame(t, b, 1, 1, appendU32(nil, 999))
		if err := d.Dispatch(); !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("dispatch = %v, want ErrProtocolViolation", err)
		}
		mustClosed(t, d)
	})

	t.Run("live_client_delete_id", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		iface := testInterface("test_live", 1, nil, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		writeFrame(t, b, 1, 1, appendU32(nil, p.ID()))
		if err := d.Dispatch(); !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("dispatch = %v, want ErrProtocolViolation", err)
		}
		mustClosed(t, d)
	})

	t.Run("display_delete_id", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		writeFrame(t, b, 1, 1, appendU32(nil, 1))
		if err := d.Dispatch(); !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("dispatch = %v, want ErrProtocolViolation", err)
		}
		mustClosed(t, d)
	})

	t.Run("server_owned_delete_id", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		p, err := d.p.conn.newIncomingObject(mustInterface("wl_callback"), 1, 0xFF000001)
		if err != nil {
			t.Fatalf("incoming callback: %v", err)
		}
		writeFrame(t, b, 1, 1, appendU32(nil, p.ID()))
		if err := d.Dispatch(); err != nil {
			t.Fatalf("delete server-owned: %v", err)
		}
		if d.p.conn.lookup(p.ID()) != nil {
			t.Fatal("server-owned object survived delete_id")
		}
		if d.p.conn.isClosed() {
			t.Fatal("connection closed on valid delete_id")
		}
	})

	t.Run("destroy_pending_delete_id", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		iface := testInterface("test_pending", 1, []Message{
			{Name: "destroy", Since: 1, Destructor: true},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		if err := p.Send(0); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		writeFrame(t, b, 1, 1, appendU32(nil, p.ID()))
		if err := d.Dispatch(); err != nil {
			t.Fatalf("delete destroyPending: %v", err)
		}
		if d.p.conn.lookup(p.ID()) != nil {
			t.Fatal("destroyPending object survived delete_id")
		}
	})

	t.Run("repeated_delete_id", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		p, err := d.p.conn.newIncomingObject(mustInterface("wl_callback"), 1, 0xFF000002)
		if err != nil {
			t.Fatalf("incoming callback: %v", err)
		}
		body := appendU32(nil, p.ID())
		writeFrame(t, b, 1, 1, body)
		if err := d.Dispatch(); err != nil {
			t.Fatalf("first delete_id: %v", err)
		}
		writeFrame(t, b, 1, 1, body)
		if err := d.Dispatch(); err != nil {
			t.Fatalf("second delete_id = %v, want nil for unknown server-range", err)
		}
		if d.p.conn.isClosed() {
			t.Fatal("connection closed by repeated server delete_id")
		}
	})
}
