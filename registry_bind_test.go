package wayland

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

// typedBinder is a generated Bind<T> helper adapted to the generic
// registry-bind test harness. Typed helpers without events are safe to call
// without a handler; event-carrying helpers wrap the proxy in the generated
// type and return it through Proxy().
type typedBinder func(*Registry, Global) (*Proxy, error)

// registryBindResult reports what a registry-bind run did. It is test-only
// bookkeeping for registry-bind and bind errors.
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

// bindUnknownRaw is the registry-bind / unknown events test-only raw bind path. It creates
// an object for an interface that has no generated metadata and sends
// wl_registry.bind through the runtime, so the dynamic new_id wire expansion
// still writes name / interface / version / new_id. The object is marked raw:
// its events are skipped by message length instead of decoded.
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
	installTestHooks(c)
	c.testSetRawName(p.ID(), g.Interface)
	// Unknown interfaces may create server-owned child objects whose events
	// the runtime cannot decode without a signature. Enable the test-only
	// broad skip for those IDs; public dispatch keeps E-07.
	c.testSetSkipUnknownServer(true)
	if err := reg.Proxy().Send(0, Uint(g.Name), NewID(p.ID())); err != nil {
		_ = p.Release()
		return nil, fmt.Errorf("raw bind %s v%d: %w", g.Interface, g.Version, err)
	}
	return p, nil
}

// bindRegistryGlobals binds every global at its advertised version. Known
// interfaces use the public Registry.Bind path, or a generated Bind<T> helper
// when the caller supplies one; unknown interfaces use the test-only raw bind
// path. Globals advertised above the generated interface version are reported
// as version skips instead of being bound at an invalid version.
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

// registryBindRoundtrip runs the final roundtrip and attaches bind context to
// a server error so bind errors reports interface, requested version and the
// server message instead of only the raw protocol error.
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

// bindDiscardSink accepts events for generically bound known objects and
// closes any descriptors the runtime delivered.
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

// TestRegistryBindError covers bind errors with a fake Unix server. The server
// reads the bind request and answers with wl_display.error; the registry-bind
// run must report the interface, requested version and server message without
// hanging.
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

// TestRawSkipMarkerOffDoesNotWeakenUnknownObjects proves the test-only broad
// skip stays off for ordinary dispatch: an unregistered server-range event is
// still E-07 and closes the connection.
func TestRawSkipMarkerOffDoesNotWeakenUnknownObjects(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })
	writeFrame(t, b, 0xFF000001, 0, appendU32(nil, 1))
	if err := d.Dispatch(); !errors.Is(err, ErrProtocolViolation) {
		t.Fatalf("dispatch = %v, want ErrProtocolViolation", err)
	}
	if !d.p.conn.isClosed() {
		t.Fatal("connection not closed after unknown server object")
	}
	if got := d.p.conn.testUnknownServerSkips(); got != 0 {
		t.Fatalf("default dispatch counted %d unknown-server skips, want 0", got)
	}
}

// TestRawBindWire checks the test-only raw bind sends name / interface /
// version / new_id through the existing dynamic wl_registry.bind expansion.
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
	c := d.p.conn
	rawName := c.testRawName(p.ID())
	if rawName != g.Interface || p.Version() != g.Version {
		t.Fatalf("raw object = %q v%d", rawName, p.Version())
	}
	if got := skippedEventCount(c, p.ID()); got != 0 {
		t.Fatalf("skipped events = %d, want 0 before any event", got)
	}
	if p.Interface() == nil || p.Interface().Name != g.Interface {
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
