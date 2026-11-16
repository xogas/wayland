//go:build integration

package wayland

import (
	"context"
	"testing"
	"time"
)

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
