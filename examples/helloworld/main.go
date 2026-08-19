//go:build linux

// A minimal xdg-shell window rendering "Hello, Wayland!" with an embedded
// 8x8 bitmap font. Kept fully self-contained as a copy-paste starting point.
package main

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const (
	text   = "Hello, Wayland!"
	scale  = 6
	margin = 32
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dpy, err := wayland.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = dpy.Close() }()

	// Discover the globals we need: wl_compositor, wl_shm, xdg_wm_base.
	reg, err := dpy.GetRegistry()
	if err != nil {
		return fmt.Errorf("get registry: %w", err)
	}
	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		return fmt.Errorf("roundtrip: %w", err)
	}

	compG, err := findGlobal(globals, wayland.InterfaceCompositor)
	if err != nil {
		return err
	}
	shmG, err := findGlobal(globals, wayland.InterfaceShm)
	if err != nil {
		return err
	}
	wmG, err := findGlobal(globals, xdgshell.InterfaceWmBase)
	if err != nil {
		return err
	}

	comp, err := wayland.BindCompositor(reg, compG.Name, min(compG.Version, wayland.VersionCompositor))
	if err != nil {
		return fmt.Errorf("bind compositor: %w", err)
	}
	shm, err := wayland.BindShm(reg, shmG.Name, min(shmG.Version, wayland.VersionShm))
	if err != nil {
		return fmt.Errorf("bind shm: %w", err)
	}
	wmBase, err := xdgshell.BindWmBase(reg, wmG.Name, min(wmG.Version, xdgshell.VersionWmBase))
	if err != nil {
		return fmt.Errorf("bind wm_base: %w", err)
	}
	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) { _ = wmBase.Pong(ev.Serial) })

	surface, err := comp.CreateSurface()
	if err != nil {
		return fmt.Errorf("create surface: %w", err)
	}
	xdgSurface, err := wmBase.GetXdgSurface(wire.ObjectID(surface.Proxy().ID()))
	if err != nil {
		return fmt.Errorf("get xdg surface: %w", err)
	}
	toplevel, err := xdgSurface.GetToplevel()
	if err != nil {
		return fmt.Errorf("get toplevel: %w", err)
	}

	// Track the configure serial: every configure must be acked before the
	// next commit, and we render only after the first one proves the window
	// is mapped. Dispatch runs on this goroutine, so the flag is race-free.
	var cfgSerial uint32
	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
		_ = xdgSurface.AckConfigure(ev.Serial)
	})
	shutdown := make(chan struct{})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) { close(shutdown) })

	winW := int32(textWidth(text, scale) + 2*margin)
	winH := int32(textHeight(scale) + 2*margin)

	_ = toplevel.SetTitle(text)
	_ = toplevel.SetAppID("hello-wayland")
	_ = surface.Commit()

	// Wait for the first configure so the window is mapped before rendering.
	if err := waitConfigure(ctx, dpy, &cfgSerial); err != nil {
		return err
	}

	if err := renderText(shm, surface, winW, winH); err != nil {
		return err
	}

	fmt.Printf("\"Hello, Wayland!\" window: %dx%d, waiting for close or 30s timeout.\n", winW, winH)

	for {
		select {
		case <-shutdown:
			fmt.Println("window closed by compositor.")
			return nil
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return nil
		default:
		}
		if err := dpy.Dispatch(ctx); err != nil {
			if ctx.Err() != nil {
				fmt.Println("timeout reached.")
				return nil
			}
			return fmt.Errorf("dispatch: %w", err)
		}
	}
}

// findGlobal returns the registry event advertising iface.
func findGlobal(globals []wayland.RegistryGlobalEvent, iface string) (wayland.RegistryGlobalEvent, error) {
	for _, g := range globals {
		if g.Interface == iface {
			return g, nil
		}
	}
	return wayland.RegistryGlobalEvent{}, fmt.Errorf("no %s global", iface)
}

// waitConfigure dispatches until the first configure arrives (5s timeout).
func waitConfigure(ctx context.Context, dpy *wayland.Display, cfgSerial *uint32) error {
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for *cfgSerial == 0 {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				return fmt.Errorf("timeout waiting for configure")
			}
			return fmt.Errorf("dispatch: %w", err)
		}
	}
	return nil
}

// renderText draws the greeting into an shm buffer and commits it.
func renderText(shm *wayland.Shm, surface *wayland.Surface, winW, winH int32) error {
	stride := winW * 4
	bufSize := int64(winH) * int64(stride)

	fd, closeFd, err := shmFile(bufSize)
	if err != nil {
		return fmt.Errorf("shm: %w", err)
	}
	defer closeFd()

	data, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}
	defer func() { _ = syscall.Munmap(data) }()

	fillSolid(data, 0xFF, 0xFF, 0xFF)
	originX := (int(winW) - textWidth(text, scale)) / 2
	originY := (int(winH) - textHeight(scale)) / 2
	drawText(data, int(stride), int(winW), int(winH), text, originX, originY, scale, 0x000000)

	pool, err := shm.CreatePool(fd, int32(bufSize))
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	defer func() { _ = pool.Destroy() }()

	buf, err := pool.CreateBuffer(0, winW, winH, stride, wayland.ShmFormatXrgb8888)
	if err != nil {
		return fmt.Errorf("create buffer: %w", err)
	}
	defer func() { _ = buf.Destroy() }()

	_ = surface.Attach(wire.ObjectID(buf.Proxy().ID()), 0, 0)
	_ = surface.Damage(0, 0, winW, winH)
	_ = surface.Commit()
	return nil
}

// fillSolid fills the whole buffer with one opaque color.
func fillSolid(data []byte, r, g, b byte) {
	for i := 0; i < len(data); i += 4 {
		data[i+0] = b
		data[i+1] = g
		data[i+2] = r
		data[i+3] = 0xff
	}
}

// shmFile creates an anonymous shared-memory file of the given size.
func shmFile(size int64) (fd int, closeFn func(), err error) {
	f, err := os.CreateTemp("", "wayland-shm-*")
	if err != nil {
		return 0, nil, err
	}
	_ = os.Remove(f.Name())
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return 0, nil, err
	}
	return int(f.Fd()), func() { _ = f.Close() }, nil
}
