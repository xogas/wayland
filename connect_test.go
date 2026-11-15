package wayland

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConnectMissingSource(t *testing.T) {
	t.Setenv("WAYLAND_SOCKET", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	if _, err := Connect(); !errors.Is(err, ErrSource) {
		t.Fatalf("Connect = %v, want ErrSource", err)
	}
}

func TestConnectSway(t *testing.T) {
	d, err := Connect()
	if err != nil {
		t.Skipf("no Wayland session: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestClosedOperations(t *testing.T) {
	a, b := unixPair(t)
	d := newDisplay(a)
	_ = b.Close()
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if err := d.Dispatch(); !errors.Is(err, ErrClosed) {
		t.Fatalf("dispatch = %v, want ErrClosed", err)
	}
	if _, err := d.Sync(&callbackRecorder{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("sync = %v, want ErrClosed", err)
	}
	if _, err := d.GetRegistry(&registryRecorder{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("get_registry = %v, want ErrClosed", err)
	}
	if err := d.Roundtrip(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("roundtrip = %v, want ErrClosed", err)
	}
}

func TestRegistrySwayRoundtrip(t *testing.T) {
	d, err := Connect()
	if err != nil {
		t.Skipf("no Wayland session: %v", err)
	}
	defer d.Close()
	var h registryRecorder
	if _, err := d.GetRegistry(&h); err != nil {
		t.Fatalf("get_registry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	found := false
	for _, g := range h.globals {
		if g.Interface == "wl_compositor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("wl_compositor not in %d globals", len(h.globals))
	}
}
