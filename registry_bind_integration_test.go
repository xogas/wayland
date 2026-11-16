//go:build integration

package wayland

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"
)

var typedBinders = map[string]typedBinder{
	"wl_compositor": func(r *Registry, g Global) (*Proxy, error) {
		obj, err := BindCompositor(r, g)
		if err != nil {
			return nil, err
		}
		return obj.Proxy(), nil
	},
	"wl_subcompositor": func(r *Registry, g Global) (*Proxy, error) {
		obj, err := BindSubcompositor(r, g)
		if err != nil {
			return nil, err
		}
		return obj.Proxy(), nil
	},
	"wl_fixes": func(r *Registry, g Global) (*Proxy, error) {
		obj, err := BindFixes(r, g)
		if err != nil {
			return nil, err
		}
		return obj.Proxy(), nil
	},
	"wl_data_device_manager": func(r *Registry, g Global) (*Proxy, error) {
		obj, err := BindDataDeviceManager(r, g)
		if err != nil {
			return nil, err
		}
		return obj.Proxy(), nil
	},
	"wl_shm": func(r *Registry, g Global) (*Proxy, error) {
		obj, err := BindShm(r, g, shmTestHandler{})
		if err != nil {
			return nil, err
		}
		return obj.Proxy(), nil
	},
	"wl_seat": func(r *Registry, g Global) (*Proxy, error) {
		obj, err := BindSeat(r, g, seatTestHandler{})
		if err != nil {
			return nil, err
		}
		return obj.Proxy(), nil
	},
}

type shmTestHandler struct{}

func (shmTestHandler) Format(uint32) {}

type seatTestHandler struct{}

func (seatTestHandler) Capabilities(uint32) {}
func (seatTestHandler) Name(string)         {}

func TestRegistryBind(t *testing.T) {
	d, err := Connect()
	if err != nil {
		t.Skipf("no Wayland session: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	var h registryRecorder
	reg, err := d.GetRegistry(&h)
	if err != nil {
		t.Fatalf("get_registry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		t.Fatalf("initial roundtrip: %v", err)
	}
	if len(h.globals) == 0 {
		t.Fatal("no globals advertised")
	}

	res, err := bindRegistryGlobals(reg, h.globals, typedBinders)
	if err != nil {
		t.Fatalf("%v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := registryBindRoundtrip(ctx2, d, res); err != nil {
		t.Fatalf("roundtrip after binds: %v", err)
	}

	t.Logf("advertised=%d known_typed=%d known_generic=%d unknown_raw=%d skipped_error=%d version_skipped=%d",
		res.Advertised, res.KnownTyped, res.KnownGeneric, res.UnknownRaw,
		res.BindErrors, res.VersionSkipped)
	if res.KnownTyped+res.KnownGeneric+res.UnknownRaw == 0 {
		t.Fatal("no globals bound")
	}
	if res.VersionSkipped > 0 {
		t.Logf("version-skipped globals: %v", res.VersionSkipList)
	}
	if res.KnownTyped == 0 {
		t.Fatal("no generated typed Bind<T> helper was exercised")
	}
	if res.UnknownRaw == 0 {
		t.Fatal("no unknown global was raw-bound")
	}

	// The connection must still be usable after binding every global.
	ctx3, cancel3 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel3()
	if err := d.Roundtrip(ctx3); err != nil {
		t.Fatalf("connection unusable after registry bind: %v", err)
	}
	d.p.conn.mu.Lock()
	queuedFDs := len(d.p.conn.fds)
	d.p.conn.mu.Unlock()
	t.Logf("queued_fds_after_roundtrips=%d", queuedFDs)
}

func TestRegistryBindUnknownEvents(t *testing.T) {
	d, err := Connect()
	if err != nil {
		t.Skipf("no Wayland session: %v", err)
	}
	var h registryRecorder
	reg, err := d.GetRegistry(&h)
	if err != nil {
		d.Close()
		t.Fatalf("get_registry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		d.Close()
		t.Fatalf("initial roundtrip: %v", err)
	}

	// org_kde_kwin_server_decoration_manager sends default_mode immediately on
	// bind and its event carries no descriptors or child objects. The other
	// candidates are tried only when it is not advertised.
	preferred := []string{
		"org_kde_kwin_server_decoration_manager",
		"zwlr_output_power_manager_v1",
		"zwlr_output_manager_v1",
	}
	var candidates []Global
	for _, name := range preferred {
		for _, g := range h.globals {
			if g.Interface == name {
				candidates = append(candidates, g)
			}
		}
	}
	for _, g := range h.globals {
		if _, known := InterfaceByName(g.Interface); known {
			continue
		}
		seen := false
		for _, c := range candidates {
			if c.Name == g.Name {
				seen = true
				break
			}
		}
		if !seen {
			candidates = append(candidates, g)
		}
	}

	for _, g := range candidates {
		_, err := bindUnknownRaw(reg, g)
		if err != nil {
			t.Fatalf("raw bind %s v%d: %v", g.Interface, g.Version, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = d.Roundtrip(ctx)
		cancel()
		if err != nil {
			t.Fatalf("roundtrip after raw bind %s v%d: %v", g.Interface, g.Version, err)
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		err = d.Roundtrip(ctx2)
		cancel2()
		if err != nil {
			t.Fatalf("connection unusable after skipped unknown event: %v", err)
		}
		d.Close()
		return
	}
	d.Close()
	t.Logf("no unknown global emitted an event; using socketpair fake server substitution")
	testRegistryBindUnknownEventsFake(t)
}

func testRegistryBindUnknownEventsFake(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() { _ = d.Close() })
	t.Cleanup(func() { _ = b.Close() })

	serverErr := make(chan error, 1)
	go serveFakeRegistryBindUnknown(b, serverErr)

	var h registryRecorder
	reg, err := d.GetRegistry(&h)
	if err != nil {
		t.Fatalf("get_registry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		t.Fatalf("global roundtrip: %v", err)
	}
	if len(h.globals) != 1 || h.globals[0].Interface != "test_unknown_event_source" {
		t.Fatalf("globals = %+v", h.globals)
	}
	if _, err := bindUnknownRaw(reg, h.globals[0]); err != nil {
		t.Fatalf("raw bind: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := d.Roundtrip(ctx2); err != nil {
		t.Fatalf("unknown event roundtrip: %v", err)
	}
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	if err := d.Roundtrip(ctx3); err != nil {
		t.Fatalf("connection unusable after skipped unknown event: %v", err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("fake server: %v", err)
		}
	default:
	}
}

func serveFakeRegistryBindUnknown(srv *net.UnixConn, errCh chan<- error) {
	s := newTestServer(srv)
	_, opcode, _, _, err := s.nextFrame()
	if err != nil {
		errCh <- fmt.Errorf("read get_registry: %w", err)
		return
	}
	if opcode != 1 {
		errCh <- fmt.Errorf("get_registry opcode = %d", opcode)
		return
	}
	regID := uint32(2)
	if err := writeWireFrame(srv, regID, 0, registryGlobalBody(55, "test_unknown_event_source", 1)); err != nil {
		errCh <- err
		return
	}
	callbackID, err := answerSync(s)
	if err != nil {
		errCh <- err
		return
	}
	_ = callbackID

	objectID, opcode, body, _, err := s.nextFrame()
	if err != nil {
		errCh <- fmt.Errorf("read bind: %w", err)
		return
	}
	if objectID != regID || opcode != 0 {
		errCh <- fmt.Errorf("bind frame = id %d op %d", objectID, opcode)
		return
	}
	_, ifaceName, version, newID, err := parseBindBody(body)
	if err != nil {
		errCh <- err
		return
	}
	if ifaceName != "test_unknown_event_source" || version != 1 {
		errCh <- fmt.Errorf("bind = %q v%d", ifaceName, version)
		return
	}
	if err := writeWireFrame(srv, newID, 0, appendU32(nil, 7)); err != nil {
		errCh <- err
		return
	}
	if _, err := answerSync(s); err != nil {
		errCh <- err
		return
	}
	// One more roundtrip proves the connection stayed usable after the
	// skipped event; it consumes the second callback delete_id and needs a
	// sync answer.
	if _, err := answerSync(s); err != nil {
		errCh <- err
		return
	}
	errCh <- nil
}

func answerSync(s *testServer) (uint32, error) {
	objectID, opcode, body, _, err := s.nextFrame()
	if err != nil {
		return 0, fmt.Errorf("read sync: %w", err)
	}
	if objectID != 1 || opcode != 0 || len(body) != 4 {
		return 0, fmt.Errorf("sync frame = id %d op %d body %d", objectID, opcode, len(body))
	}
	callbackID := binary.LittleEndian.Uint32(body)
	if err := writeWireFrame(s.conn, callbackID, 0, appendU32(nil, 0)); err != nil {
		return 0, err
	}
	if err := writeWireFrame(s.conn, 1, 1, appendU32(nil, callbackID)); err != nil {
		return 0, err
	}
	return callbackID, nil
}

func TestRegistryBindUnknownEventsFake(t *testing.T) {
	testRegistryBindUnknownEventsFake(t)
}

func writeWireFrame(srv *net.UnixConn, objectID uint32, opcode uint16, body []byte) error {
	size := 8 + len(body)
	msg := make([]byte, 8, size)
	binary.LittleEndian.PutUint32(msg[0:4], objectID)
	binary.LittleEndian.PutUint32(msg[4:8], uint32(size)<<16|uint32(opcode))
	msg = append(msg, body...)
	_, err := srv.Write(msg)
	return err
}

func parseBindBody(body []byte) (name uint32, iface string, version, id uint32, err error) {
	if len(body) < 12 {
		return 0, "", 0, 0, fmt.Errorf("bind body too short: %d", len(body))
	}
	name = binary.LittleEndian.Uint32(body[0:4])
	n := int(binary.LittleEndian.Uint32(body[4:8]))
	off := 8
	if n < 1 || off+n > len(body) {
		return 0, "", 0, 0, fmt.Errorf("bind interface length %d", n)
	}
	iface = string(body[off : off+n-1])
	off += n
	off = (off + 3) &^ 3
	if off+8 > len(body) {
		return 0, "", 0, 0, fmt.Errorf("bind body truncated at %d", off)
	}
	version = binary.LittleEndian.Uint32(body[off : off+4])
	id = binary.LittleEndian.Uint32(body[off+4 : off+8])
	return name, iface, version, id, nil
}

type discardSink struct{}

func (discardSink) DispatchEvent(opcode uint16, args []Arg) error {
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
		// Event-created objects (for example foreign-toplevel handles)
		// arrive without a typed handler in this generic test; give them a
		// discard sink so their follow-up events are consumed too.
		if p := arg.Proxy(); p != nil {
			_ = p.SetSink(discardSink{})
		}
	}
	return nil
}

func TestBindVersions(t *testing.T) {
	d, err := Connect()
	if err != nil {
		t.Skipf("no Wayland session: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	var h registryRecorder
	reg, err := d.GetRegistry(&h)
	if err != nil {
		t.Fatalf("get_registry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		t.Fatalf("initial roundtrip: %v", err)
	}
	if len(h.globals) == 0 {
		t.Fatal("no globals advertised")
	}

	type boundGlobal struct {
		g     Global
		proxy *Proxy
	}
	var bound []boundGlobal
	var skippedUnknown, skippedVersion []string
	for _, g := range h.globals {
		iface, ok := InterfaceByName(g.Interface)
		if !ok {
			skippedUnknown = append(skippedUnknown, g.Interface)
			continue
		}
		if g.Version == 0 || iface.Version < g.Version {
			skippedVersion = append(skippedVersion, g.Interface)
			continue
		}
		p, err := reg.Bind(g, g.Version)
		if err != nil {
			t.Fatalf("bind %s v%d: %v", g.Interface, g.Version, err)
		}
		if err := p.SetSink(discardSink{}); err != nil {
			t.Fatalf("set sink %s: %v", g.Interface, err)
		}
		if p.Interface().Name != g.Interface {
			t.Fatalf("bound interface = %q, want %q", p.Interface().Name, g.Interface)
		}
		if p.Version() != g.Version {
			t.Fatalf("bound %s version = %d, want advertised %d", g.Interface, p.Version(), g.Version)
		}
		t.Logf("bound %s v%d id=%d", g.Interface, g.Version, p.ID())
		bound = append(bound, boundGlobal{g: g, proxy: p})
	}
	if len(bound) == 0 {
		t.Fatal("no global could be bound")
	}

	// Repeated bind creates a new independent object.
	first := bound[0]
	p2, err := reg.Bind(first.g, first.g.Version)
	if err != nil {
		t.Fatalf("repeat bind %s: %v", first.g.Interface, err)
	}
	if err := p2.SetSink(discardSink{}); err != nil {
		t.Fatalf("set sink repeat: %v", err)
	}
	if p2.ID() == first.proxy.ID() {
		t.Fatalf("repeat bind reused id %d", p2.ID())
	}
	if d.p.conn.lookup(first.proxy.ID()) != first.proxy || d.p.conn.lookup(p2.ID()) != p2 {
		t.Fatal("repeat bind did not keep both objects alive")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := d.Roundtrip(ctx2); err != nil {
		t.Fatalf("roundtrip after binds: %v", err)
	}
	t.Logf("advertised=%d bound=%d skipped_unknown=%d skipped_version=%d repeat=%s",
		len(h.globals), len(bound), len(skippedUnknown), len(skippedVersion), first.g.Interface)
	if len(skippedUnknown) > 0 {
		t.Logf("skipped unknown interfaces: %v", skippedUnknown)
	}
	if len(skippedVersion) > 0 {
		t.Logf("skipped version-ahead interfaces: %v", skippedVersion)
	}
}
