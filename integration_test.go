// Integration tests against a live Wayland compositor. In CI these run
// against a headless weston; locally they run against any running compositor
// (WAYLAND_DISPLAY). The test skips itself when WAYLAND_DISPLAY is not set or
// when run with -short.
package wayland_test

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

// openFDs returns the number of open file descriptors of this process.
func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot read /proc/self/fd: %v", err)
	}
	return len(ents)
}

func requireDisplay(t *testing.T) *wayland.Display {
	t.Helper()
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("WAYLAND_DISPLAY not set: skipping live-compositor integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dpy, err := wayland.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dpy.Close() })
	return dpy
}

// TestIntegrationCreateDestroyCycles opens and closes real xdg toplevel
// windows repeatedly against the live compositor, then checks that no file
// descriptors are retained. This exercises the full create -> configure ->
// destroy -> delete_id lifecycle end to end.
func TestIntegrationCreateDestroyCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live-compositor integration test in short mode")
	}
	dpy := requireDisplay(t)
	ctx := context.Background()

	reg, err := dpy.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry: %v", err)
	}
	var compG, shmG, wmG wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		switch ev.Interface {
		case wayland.InterfaceCompositor:
			compG = ev
		case wayland.InterfaceShm:
			shmG = ev
		case xdgshell.InterfaceWmBase:
			wmG = ev
		}
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		t.Fatalf("Roundtrip: %v", err)
	}
	if compG.Interface == "" || shmG.Interface == "" {
		t.Skip("wl_compositor or wl_shm global missing")
	}
	if wmG.Interface == "" {
		t.Skip("xdg_wm_base global missing")
	}

	comp, err := wayland.BindCompositor(reg, compG.Name, min(compG.Version, wayland.VersionCompositor))
	if err != nil {
		t.Fatalf("bind compositor: %v", err)
	}
	wmBase, err := xdgshell.BindWmBase(reg, wmG.Name, min(wmG.Version, xdgshell.VersionWmBase))
	if err != nil {
		t.Fatalf("bind wm_base: %v", err)
	}
	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) { _ = wmBase.Pong(ev.Serial) })

	beforeFDs := openFDs(t)
	const cycles = 20
	for i := range cycles {
		surface, err := comp.CreateSurface()
		if err != nil {
			t.Fatalf("create_surface: %v", err)
		}
		xdgSurface, err := wmBase.GetXdgSurface(wire.ObjectID(surface.Proxy().ID()))
		if err != nil {
			t.Fatalf("get_xdg_surface: %v", err)
		}
		toplevel, err := xdgSurface.GetToplevel()
		if err != nil {
			t.Fatalf("get_toplevel: %v", err)
		}

		var serial uint32
		configured := make(chan struct{})
		xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
			serial = ev.Serial
			select {
			case <-configured:
			default:
				close(configured)
			}
		})
		_ = toplevel.SetTitle(fmt.Sprintf("soak-%d", i))
		_ = toplevel.SetAppID("wayland-integration")
		_ = surface.Commit()

		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		for {
			err := dpy.Dispatch(waitCtx)
			if err != nil {
				if waitCtx.Err() != nil {
					break
				}
				cancel()
				t.Fatalf("Dispatch waiting for configure: %v", err)
			}
			select {
			case <-configured:
			default:
				continue
			}
			break
		}
		cancel()
		if serial != 0 {
			_ = xdgSurface.AckConfigure(serial)
		}

		_ = toplevel.Destroy()
		_ = xdgSurface.Destroy()
		_ = surface.Destroy()
		if err := dpy.Roundtrip(ctx); err != nil {
			t.Fatalf("Roundtrip after destroy: %v", err)
		}
	}

	runtime.GC()
	if after := openFDs(t); after > beforeFDs+8 {
		t.Errorf("fd growth: %d before, %d after %d window create/destroy cycles", beforeFDs, after, cycles)
	}
}
