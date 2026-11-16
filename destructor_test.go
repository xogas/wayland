package wayland

import (
	"errors"
	"testing"
)

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
