package wayland

import (
	"errors"
	"strings"
	"testing"
)

// TestObjectLifecycle covers object lifecycle.
func TestObjectLifecycle(t *testing.T) {
	c := newTestConn()
	iface := mustInterface("wl_registry")
	p1, err := c.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new p1: %v", err)
	}
	p2, err := c.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new p2: %v", err)
	}
	for _, p := range []*Proxy{p1, p2} {
		if p.ID() < 1 || p.ID() > clientIDMax {
			t.Fatalf("allocated id %d outside client range", p.ID())
		}
		if p.ID() == 0 {
			t.Fatal("allocated id 0")
		}
	}

	if _, err := c.newIncomingObject(iface, 1, 0); !errors.Is(err, ErrProtocolViolation) {
		t.Fatalf("incoming id 0 = %v, want ErrProtocolViolation", err)
	}
	if c.lookup(0) != nil {
		t.Fatal("id 0 became an object")
	}

	// A live client object cannot be deleted; destroyPending can.
	if err := c.deleteID(p2.ID()); !errors.Is(err, ErrProtocolViolation) {
		t.Fatalf("delete live object = %v, want ErrProtocolViolation", err)
	}
	p1.state = stateDestroyPending
	if err := c.deleteID(p1.ID()); err != nil {
		t.Fatalf("delete destroyPending: %v", err)
	}
	if c.lookup(p1.ID()) != nil {
		t.Fatal("destroyPending object still in table after delete_id")
	}
	p3, err := c.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new p3: %v", err)
	}
	if p3.ID() != p1.ID() {
		t.Fatalf("reused id = %d, want %d", p3.ID(), p1.ID())
	}

	// Server-range identifiers are removed but never handed to the client
	// allocator.
	sp, err := c.newIncomingObject(iface, 1, 0xFF000001)
	if err != nil {
		t.Fatalf("incoming server object: %v", err)
	}
	if err := c.deleteID(sp.ID()); err != nil {
		t.Fatalf("delete server object: %v", err)
	}
	p4, err := c.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new p4: %v", err)
	}
	if p4.ID() > clientIDMax {
		t.Fatalf("client allocator reused server id %d", p4.ID())
	}

	// Allocation starts above the reserved wl_display ID.
	c2 := &conn{objects: make(map[uint32]*Proxy), nextID: 0}
	p5, err := c2.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new p5: %v", err)
	}
	if p5.ID() == 0 || p5.ID() == 1 {
		t.Fatalf("allocated reserved id %d", p5.ID())
	}

	// The whole client range can be consumed.
	c3 := &conn{objects: make(map[uint32]*Proxy), nextID: clientIDMax}
	pm, err := c3.newObject(iface, 1, false)
	if err != nil {
		t.Fatalf("new at max: %v", err)
	}
	if pm.ID() != clientIDMax {
		t.Fatalf("max id = %d, want %d", pm.ID(), clientIDMax)
	}
	if _, err := c3.newObject(iface, 1, false); !errors.Is(err, ErrIDExhausted) {
		t.Fatalf("id after max = %v, want ErrIDExhausted", err)
	}
}

// TestDestroyedSend covers destroyed objects.
func TestDestroyedSend(t *testing.T) {
	t.Run("destroy_pending", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() { _ = d.Close() })
		t.Cleanup(func() { _ = b.Close() })
		iface := testInterface("test_destroy", 1, []Message{
			{Name: "destroy", Since: 1, Destructor: true},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		if err := p.Send(0); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		srv := newTestServer(b)
		if _, _, _, _ = srv.readFrame(t); p.state != stateDestroyPending {
			t.Fatalf("state = %v, want destroyPending", p.state)
		}
		err := p.Send(0)
		if !errors.Is(err, ErrObjectDestroyed) {
			t.Fatalf("send after destroy = %v, want ErrObjectDestroyed", err)
		}
		if !strings.Contains(err.Error(), "send object") {
			t.Fatalf("error message lacks operation context: %v", err)
		}
		assertNoBytes(t, b)
	})

	t.Run("destroyed", func(t *testing.T) {
		c := newTestConn()
		iface := testInterface("test_destroyed", 1, []Message{
			{Name: "ping", Since: 1},
		}, nil)
		p, err := c.newObject(iface, 1, false)
		if err != nil {
			t.Fatalf("new object: %v", err)
		}
		p.state = stateDestroyed
		if err := p.Send(0); !errors.Is(err, ErrObjectDestroyed) {
			t.Fatalf("send on destroyed = %v, want ErrObjectDestroyed", err)
		}
	})
}

// TestUnknownObject covers unknown objects.
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
		if err := d.Dispatch(); !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("second delete_id = %v, want ErrProtocolViolation", err)
		}
		mustClosed(t, d)
	})
}

// TestObjectIDExhausted covers id exhaustion.
func TestObjectIDExhausted(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	d.p.conn.nextID = clientIDMax
	p, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("new at max: %v", err)
	}
	if p.ID() != clientIDMax {
		t.Fatalf("max id = %d, want %d", p.ID(), clientIDMax)
	}
	_, err = d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
	if !errors.Is(err, ErrIDExhausted) {
		t.Fatalf("allocation = %v, want ErrIDExhausted", err)
	}
	if !strings.Contains(err.Error(), "new wl_registry") {
		t.Fatalf("error message lacks context: %v", err)
	}
	assertNoBytes(t, b)
}
