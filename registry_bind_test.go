package wayland

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

type typedBinder func(*Registry, Global) (*Proxy, error)

type registryBindResult struct {
	Advertised      int
	KnownTyped      int
	KnownGeneric    int
	UnknownRaw      int
	VersionSkipped  int
	BindErrors      int
	RegistryID      uint32
	Binds           []registryBind
	VersionSkipList []Global
}

type registryBind struct {
	Global Global
	Proxy  *Proxy
	Typed  bool
	Raw    bool
}

func bindUnknownRaw(reg *Registry, g Global) (*Proxy, error) {
	if reg == nil || reg.Proxy() == nil {
		return nil, fmt.Errorf("raw bind %s: nil registry: %w", g.Interface, ErrInvalidArgument)
	}
	if g.Interface == "" || g.Name == 0 || g.Version < 1 {
		return nil, fmt.Errorf("raw bind %q name %d v%d: %w", g.Interface, g.Name, g.Version, ErrInvalidArgument)
	}
	if _, known := InterfaceByName(g.Interface); known {
		return nil, fmt.Errorf("raw bind %s: interface has generated metadata: %w", g.Interface, ErrInvalidArgument)
	}
	c := reg.Proxy().conn
	// A synthetic metadata value carries the unknown name and version so the
	// existing dynamic new_id expansion can serialize the bind frame. The
	// raw name hook keeps dispatch away from the empty signature.
	iface := &Interface{Name: g.Interface, Version: g.Version}
	p, err := c.newObject(iface, g.Version, false)
	if err != nil {
		return nil, fmt.Errorf("raw bind %s v%d: %w", g.Interface, g.Version, err)
	}
	if err := reg.Proxy().Send(0, Uint(g.Name), NewID(p.ID())); err != nil {
		_ = p.Release()
		return nil, fmt.Errorf("raw bind %s v%d: %w", g.Interface, g.Version, err)
	}
	return p, nil
}

func bindRegistryGlobals(reg *Registry, globals []Global, typed map[string]typedBinder) (*registryBindResult, error) {
	res := &registryBindResult{Advertised: len(globals), RegistryID: reg.Proxy().ID()}
	for _, g := range globals {
		iface, known := InterfaceByName(g.Interface)
		if g.Version == 0 || (known && iface.Version < g.Version) {
			res.VersionSkipped++
			res.VersionSkipList = append(res.VersionSkipList, g)
			continue
		}
		var (
			p   *Proxy
			err error
		)
		switch {
		case !known:
			p, err = bindUnknownRaw(reg, g)
			if err == nil {
				res.UnknownRaw++
			}
		default:
			if binder, ok := typed[g.Interface]; ok {
				p, err = binder(reg, g)
				if err == nil {
					res.KnownTyped++
				}
			} else {
				p, err = reg.Bind(g, g.Version)
				if err == nil {
					res.KnownGeneric++
					if sinkErr := p.SetSink(bindDiscardSink{}); sinkErr != nil {
						err = fmt.Errorf("set sink %s v%d: %w", g.Interface, g.Version, sinkErr)
					}
				}
			}
		}
		if err != nil {
			res.BindErrors++
			return res, fmt.Errorf("registry bind %s v%d: %w", g.Interface, g.Version, err)
		}
		res.Binds = append(res.Binds, registryBind{
			Global: g,
			Proxy:  p,
			Typed:  known && typed[g.Interface] != nil,
			Raw:    !known,
		})
	}
	return res, nil
}

func registryBindRoundtrip(ctx context.Context, d *Display, res *registryBindResult) error {
	err := d.Roundtrip(ctx)
	if err == nil {
		return nil
	}
	var pe *ProtocolError
	if !errors.As(err, &pe) {
		return err
	}
	if len(res.Binds) == 0 {
		return err
	}
	for _, b := range res.Binds {
		if b.Proxy != nil && b.Proxy.ID() == pe.ObjectID {
			return fmt.Errorf("registry bind %s v%d: %w", b.Global.Interface, b.Global.Version, err)
		}
	}
	if pe.ObjectID == res.RegistryID || len(res.Binds) == 1 {
		last := res.Binds[len(res.Binds)-1]
		return fmt.Errorf("registry bind %s v%d: %w", last.Global.Interface, last.Global.Version, err)
	}
	return fmt.Errorf("registry bind after %d binds: %w", len(res.Binds), err)
}

type bindDiscardSink struct{}

func (bindDiscardSink) DispatchEvent(_ uint16, args []Arg) error {
	seen := make(map[int]bool)
	for _, arg := range args {
		if arg.typ == ArgFD {
			fd := arg.FD()
			if fd >= 0 && !seen[fd] {
				seen[fd] = true
				_ = syscall.Close(fd)
			}
			continue
		}
		if p := arg.Proxy(); p != nil {
			_ = p.SetSink(bindDiscardSink{})
		}
	}
	return nil
}

func TestRegistryBindError(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })

	reg, err := d.GetRegistry(&registryRecorder{})
	if err != nil {
		t.Fatalf("get_registry: %v", err)
	}
	global := Global{Name: 41, Interface: "wl_compositor", Version: 6}
	res, err := bindRegistryGlobals(reg, []Global{global}, nil)
	if err != nil {
		t.Fatalf("bind setup: %v", err)
	}
	if len(res.Binds) != 1 {
		t.Fatalf("binds = %d, want 1", len(res.Binds))
	}
	bound := res.Binds[0]

	srv := newTestServer(b)
	objectID, opcode, body, _ := srv.readFrame(t)
	if objectID != d.p.ID() || opcode != 1 {
		t.Fatalf("first frame = id %d op %d, want display get_registry", objectID, opcode)
	}
	_ = body
	objectID, opcode, body, _ = srv.readFrame(t)
	if objectID != reg.Proxy().ID() || opcode != 0 {
		t.Fatalf("second frame = id %d op %d, want bind", objectID, opcode)
	}
	bindName, bindIface, bindVersion, bindID := parseBindFrame(t, body)
	if bindName != global.Name || bindIface != global.Interface || bindVersion != global.Version || bindID != bound.Proxy.ID() {
		t.Fatalf("bind frame = name %d iface %q v%d id %d", bindName, bindIface, bindVersion, bindID)
	}

	const serverMessage = "bind rejected by fake server"
	spec := mustInterface("wl_display").Events[0]
	errorBody, _, err := encodeRequest(d.p, 0, spec, []Arg{
		Object(bound.Proxy.ID()), Uint(3), String(ptr(serverMessage)),
	})
	if err != nil {
		t.Fatalf("encode wl_display.error: %v", err)
	}
	writeFrame(t, b, 1, 0, errorBody[8:])

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	err = registryBindRoundtrip(ctx, d, res)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("registry-bind succeeded, want bind failure report")
	}
	report := err.Error()
	for _, want := range []string{global.Interface, fmt.Sprintf("v%d", global.Version), serverMessage} {
		if !strings.Contains(report, want) {
			t.Fatalf("report %q lacks %q", report, want)
		}
	}
	if elapsed > 2*time.Second {
		t.Fatalf("bind failure report took %v, want no hang", elapsed)
	}
	t.Logf("bind failure report: %s", report)
}

func ptr(s string) *string { return &s }

func TestRawBindWire(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	reg, _ := newRegistryForTest(t, d)

	g := Global{Name: 77, Interface: "test_unknown_interface", Version: 3}
	p, err := bindUnknownRaw(reg, g)
	if err != nil {
		t.Fatalf("raw bind: %v", err)
	}
	if p.Interface() == nil || p.Interface().Name != g.Interface || p.Version() != g.Version {
		t.Fatalf("raw interface metadata = %+v", p.Interface())
	}
	srv := newTestServer(b)
	objectID, opcode, body, _ := srv.readFrame(t)
	if objectID != reg.Proxy().ID() || opcode != 0 {
		t.Fatalf("bind frame = id %d op %d", objectID, opcode)
	}
	name, ifaceName, version, id := parseBindFrame(t, body)
	if name != g.Name || ifaceName != g.Interface || version != g.Version || id != p.ID() {
		t.Fatalf("bind frame = name %d iface %q v%d id %d", name, ifaceName, version, id)
	}
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

func newRegistryForTest(t *testing.T, d *Display) (*Registry, *Proxy) {
	t.Helper()
	p, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return NewRegistry(p, &registryRecorder{}), p
}

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

type orderedRegistryRecorder struct {
	events []string
}

func (r *orderedRegistryRecorder) Global(g Global) {
	r.events = append(r.events, "global:"+g.Interface)
}

func (r *orderedRegistryRecorder) GlobalRemove(name uint32) {
	r.events = append(r.events, fmt.Sprintf("remove:%d", name))
}

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
