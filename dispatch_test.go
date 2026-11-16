package wayland

import (
	"encoding/binary"
	"errors"
	"testing"
)

func writeFrame(t *testing.T, srv interface{ Write([]byte) (int, error) }, objectID uint32, opcode uint16, body []byte) {
	t.Helper()
	size := 8 + len(body)
	if size > messageMax {
		t.Fatalf("frame too large: %d", size)
	}
	msg := make([]byte, 8, size)
	binary.LittleEndian.PutUint32(msg[0:4], objectID)
	binary.LittleEndian.PutUint32(msg[4:8], uint32(size)<<16|uint32(opcode))
	msg = append(msg, body...)
	n, err := srv.Write(msg)
	if err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("short write: %d/%d", n, len(msg))
	}
}

func registryGlobalBody(name uint32, iface string, version uint32) []byte {
	body := appendU32(nil, name)
	body = appendU32(body, uint32(len(iface)+1))
	body = append(body, iface...)
	body = append(body, 0)
	body = pad4(body)
	body = appendU32(body, version)
	return body
}

func TestDispatchRegistryGlobal(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	p, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	h := &registryRecorder{}
	r := &Registry{p: p, h: h}
	if err := p.SetSink(r); err != nil {
		t.Fatalf("set sink: %v", err)
	}
	writeFrame(t, b, p.ID(), 0, registryGlobalBody(7, "wl_compositor", 6))
	if err := d.Dispatch(); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(h.globals) != 1 {
		t.Fatalf("globals = %d, want 1", len(h.globals))
	}
	g := h.globals[0]
	if g.Name != 7 || g.Interface != "wl_compositor" || g.Version != 6 {
		t.Fatalf("global = %+v", g)
	}
}

func TestDispatchUnknownObject(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	writeFrame(t, b, 99, 0, nil)
	if err := d.Dispatch(); !errors.Is(err, ErrProtocolViolation) {
		t.Fatalf("dispatch unknown = %v, want ErrProtocolViolation", err)
	}
	if !d.p.conn.isClosed() {
		t.Fatal("connection not closed after protocol violation")
	}
}

func TestDispatchDeleteID(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	p, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	p.state = stateDestroyPending
	body := appendU32(nil, p.ID())
	writeFrame(t, b, 1, 1, body)
	if err := d.Dispatch(); err != nil {
		t.Fatalf("dispatch delete_id: %v", err)
	}
	if d.p.conn.lookup(p.ID()) != nil {
		t.Fatal("object not removed by delete_id")
	}
}

func TestDispatchNoHandler(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	p, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	writeFrame(t, b, p.ID(), 0, registryGlobalBody(7, "wl_compositor", 6))
	if err := d.Dispatch(); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("dispatch no handler = %v, want ErrNoHandler", err)
	}
	if d.p.conn.isClosed() {
		t.Fatal("connection closed on ErrNoHandler")
	}
}
