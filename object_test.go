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
