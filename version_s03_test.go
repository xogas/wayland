package wayland

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func newRegistryForTest(t *testing.T, d *Display) (*Registry, *Proxy) {
	t.Helper()
	p, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return NewRegistry(p, &registryRecorder{}), p
}

// TestBindVersion covers AC-V01.
func TestBindVersion(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	reg, _ := newRegistryForTest(t, d)

	cases := []struct {
		name    string
		global  Global
		version uint32
	}{
		{"above_advertised", Global{Name: 7, Interface: "xdg_surface", Version: 5}, 6},
		{"above_interface", Global{Name: 7, Interface: "xdg_surface", Version: 9}, 8},
		{"zero", Global{Name: 7, Interface: "xdg_surface", Version: 7}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reg.Bind(tc.global, tc.version)
			if !errors.Is(err, ErrVersion) {
				t.Fatalf("bind = %v, want ErrVersion", err)
			}
			if !strings.Contains(err.Error(), tc.global.Interface) {
				t.Fatalf("error lacks interface context: %v", err)
			}
			assertNoBytes(t, b)
		})
	}

	p, err := reg.Bind(Global{Name: 42, Interface: "xdg_surface", Version: 7}, 5)
	if err != nil {
		t.Fatalf("valid bind: %v", err)
	}
	if p.Interface().Name != "xdg_surface" || p.Version() != 5 {
		t.Fatalf("bound object = %s v%d", p.Interface().Name, p.Version())
	}
	srv := newTestServer(b)
	objectID, opcode, body, _ := srv.readFrame(t)
	if objectID != reg.Proxy().ID() || opcode != 0 {
		t.Fatalf("bind frame = id %d op %d", objectID, opcode)
	}
	name, ifaceName, version, id := parseBindFrame(t, body)
	if name != 42 || ifaceName != "xdg_surface" || version != 5 || id != p.ID() {
		t.Fatalf("bind frame = name %d iface %q v%d id %d", name, ifaceName, version, id)
	}
}

func parseBindFrame(t *testing.T, body []byte) (uint32, string, uint32, uint32) {
	t.Helper()
	if len(body) < 12 {
		t.Fatalf("bind body too short: %d", len(body))
	}
	name := binary.LittleEndian.Uint32(body[0:4])
	n := int(binary.LittleEndian.Uint32(body[4:8]))
	off := 8
	if n < 1 || off+n > len(body) {
		t.Fatalf("bind interface length %d", n)
	}
	ifaceName := string(body[off : off+n-1])
	off += n
	off = (off + 3) &^ 3
	if off+8 > len(body) {
		t.Fatalf("bind body truncated at %d", off)
	}
	version := binary.LittleEndian.Uint32(body[off : off+4])
	id := binary.LittleEndian.Uint32(body[off+4 : off+8])
	return name, ifaceName, version, id
}

// TestSinceGuard covers AC-V02.
func TestSinceGuard(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	iface := testInterface("s03_since", 4, []Message{
		{Name: "early", Since: 1},
		{Name: "late", Since: 4},
	}, nil)
	p := newTestProxy(t, d, iface, 3, nil)

	err := p.Send(1)
	if !errors.Is(err, ErrSince) {
		t.Fatalf("send since 4 on v3 = %v, want ErrSince", err)
	}
	if !strings.Contains(err.Error(), "s03_since") || !strings.Contains(err.Error(), "late") {
		t.Fatalf("error lacks interface/message context: %v", err)
	}
	assertNoBytes(t, b)

	if err := p.Send(0); err != nil {
		t.Fatalf("send early: %v", err)
	}
	srv := newTestServer(b)
	objectID, opcode, body, _ := srv.readFrame(t)
	if objectID != p.ID() || opcode != 0 || len(body) != 0 {
		t.Fatalf("early frame = id %d op %d body %d", objectID, opcode, len(body))
	}
}

// TestDynamicNewID covers AC-V04.
func TestDynamicNewID(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	reg, regP := newRegistryForTest(t, d)

	p, err := reg.Bind(Global{Name: 7, Interface: "wl_compositor", Version: 6}, 3)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if p.Interface().Name != "wl_compositor" {
		t.Fatalf("interface = %q, want wl_compositor", p.Interface().Name)
	}
	if p.Version() != 3 {
		t.Fatalf("version = %d, want requested 3", p.Version())
	}
	if p.ID() < 1 || p.ID() > clientIDMax {
		t.Fatalf("id %d outside client range", p.ID())
	}

	srv := newTestServer(b)
	objectID, opcode, body, _ := srv.readFrame(t)
	if objectID != regP.ID() || opcode != 0 {
		t.Fatalf("bind frame = id %d op %d", objectID, opcode)
	}
	name, ifaceName, version, id := parseBindFrame(t, body)
	if name != 7 || ifaceName != "wl_compositor" || version != 3 || id != p.ID() {
		t.Fatalf("bind frame = name %d iface %q v%d id %d", name, ifaceName, version, id)
	}
	if d.p.conn.lookup(p.ID()) != p {
		t.Fatal("dynamic new_id object not registered")
	}
}

// TestDynamicNewIDUnknown covers AC-V05.
func TestDynamicNewIDUnknown(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	reg, _ := newRegistryForTest(t, d)

	_, err := reg.Bind(Global{Name: 3, Interface: "s03_no_such_interface", Version: 1}, 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bind unknown = %v, want ErrInvalidArgument", err)
	}
	if !strings.Contains(err.Error(), "bind") || !strings.Contains(err.Error(), "s03_no_such_interface") {
		t.Fatalf("error lacks operation/interface context: %v", err)
	}
	assertNoBytes(t, b)
}

type orderedRegistryRecorder struct {
	events []string
}

func (r *orderedRegistryRecorder) Global(g Global) {
	r.events = append(r.events, "global:"+g.Interface)
}

func (r *orderedRegistryRecorder) GlobalRemove(name uint32) {
	r.events = append(r.events, fmt.Sprintf("remove:%d", name))
}

// TestRegistryEventOrder checks that global and global_remove events are
// delivered in server order (AC-V03 event ordering).
func TestRegistryEventOrder(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	p, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	rec := &orderedRegistryRecorder{}
	_ = NewRegistry(p, rec)

	writeFrame(t, b, p.ID(), 0, registryGlobalBody(7, "wl_compositor", 6))
	writeFrame(t, b, p.ID(), 1, appendU32(nil, 7))
	writeFrame(t, b, p.ID(), 0, registryGlobalBody(9, "wl_shm", 2))
	for i := 0; i < 3; i++ {
		if err := d.Dispatch(); err != nil {
			t.Fatalf("dispatch %d: %v", i, err)
		}
	}
	want := []string{"global:wl_compositor", "remove:7", "global:wl_shm"}
	if len(rec.events) != len(want) {
		t.Fatalf("events = %v, want %v", rec.events, want)
	}
	for i := range want {
		if rec.events[i] != want[i] {
			t.Fatalf("event %d = %q, want %q", i, rec.events[i], want[i])
		}
	}
}
