//go:build integration

package wayland

import (
	"context"
	"syscall"
	"testing"
	"time"
)

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

func TestRoundtrip(t *testing.T) {
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
	if reg == nil {
		t.Fatal("nil registry")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if len(h.globals) == 0 {
		t.Fatal("roundtrip delivered no globals")
	}
	found := false
	for _, g := range h.globals {
		if g.Interface == "wl_compositor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("wl_compositor not delivered before sync done (%d globals)", len(h.globals))
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := d.Roundtrip(ctx2); err != nil {
		t.Fatalf("second roundtrip: %v", err)
	}
	t.Logf("roundtrip delivered %d globals in server order", len(h.globals))
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
		t.Fatal("no sway global could be bound")
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
