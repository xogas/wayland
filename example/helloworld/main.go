//go:build linux

// A minimal xdg-shell window rendering "Hello, Wayland!" with an embedded 5x7 bitmap font.
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

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dpy, err := wayland.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	reg, err := dpy.GetRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_registry: %v\n", err)
		os.Exit(1)
	}
	var compG, shmG, wmG wayland.RegistryGlobalEvent
	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})

	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}

	for _, g := range globals {
		switch g.Interface {
		case wayland.InterfaceCompositor:
			compG = g
		case wayland.InterfaceShm:
			shmG = g
		case xdgshell.InterfaceWmBase:
			wmG = g
		}
	}
	if compG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_compositor global")
		os.Exit(1)
	}
	if shmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_shm global")
		os.Exit(1)
	}
	if wmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no xdg_wm_base global")
		os.Exit(1)
	}

	comp, err := wayland.BindCompositor(reg, compG.Name, compG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind compositor: %v\n", err)
		os.Exit(1)
	}
	shm, err := wayland.BindShm(reg, shmG.Name, shmG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind shm: %v\n", err)
		os.Exit(1)
	}
	wmBase, err := xdgshell.BindWmBase(reg, wmG.Name, wmG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind wm_base: %v\n", err)
		os.Exit(1)
	}

	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) { _ = wmBase.Pong(ev.Serial) })

	surface, err := comp.CreateSurface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_surface: %v\n", err)
		os.Exit(1)
	}
	xdgSurface, err := wmBase.GetXdgSurface(wire.ObjectID(surface.Proxy().ID()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_xdg_surface: %v\n", err)
		os.Exit(1)
	}
	toplevel, err := xdgSurface.GetToplevel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_toplevel: %v\n", err)
		os.Exit(1)
	}

	const (
		text   = "Hello, Wayland!"
		scale  = 6
		margin = 32
	)

	textW := textWidth(text, scale)
	textH := textHeight(scale)
	winW := int32(textW + 2*margin)
	winH := int32(textH + 2*margin)

	var cfgSerial uint32
	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})

	shutdown := make(chan struct{})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) { close(shutdown) })

	_ = toplevel.SetTitle(text)
	_ = toplevel.SetAppID("hello-wayland")
	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for cfgSerial == 0 {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure")
				os.Exit(1)
			}
			break
		}
	}
	if cfgSerial == 0 {
		fmt.Fprintln(os.Stderr, "no configure event received")
		os.Exit(1)
	}
	_ = xdgSurface.AckConfigure(cfgSerial)

	stride := winW * 4
	bufSize := int64(winH) * int64(stride)

	fd, closeFd, err := shmFile(bufSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shm: %v\n", err)
		os.Exit(1)
	}
	defer closeFd()

	data, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mmap: %v\n", err)
		os.Exit(1)
	}
	defer syscall.Munmap(data) //nolint: errcheck

	for i := 0; i < len(data); i += 4 {
		data[i+0] = 0xFF
		data[i+1] = 0xFF
		data[i+2] = 0xFF
		data[i+3] = 0xFF
	}

	originX := (int(winW) - textW) / 2
	originY := (int(winH) - textH) / 2
	drawText(data, int(stride), int(winW), int(winH), text, originX, originY, scale, 0x000000)

	pool, err := shm.CreatePool(fd, int32(bufSize))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Destroy() //nolint: errcheck

	buf, err := pool.CreateBuffer(0, winW, winH, stride, uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
		os.Exit(1)
	}
	defer buf.Destroy() //nolint: errcheck

	_ = surface.Attach(wire.ObjectID(buf.Proxy().ID()), 0, 0)
	_ = surface.Damage(0, 0, winW, winH)
	_ = surface.Commit()

	fmt.Printf("\"Hello, Wayland!\" window: %dx%d, waiting for close or 30s timeout.\n", winW, winH)

	for {
		select {
		case <-shutdown:
			fmt.Println("window closed by compositor.")
			return
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return
		default:
		}
		if err := dpy.Dispatch(ctx); err != nil {
			if ctx.Err() != nil {
				fmt.Println("timeout reached.")
				return
			}
			fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			break
		}
	}
}

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
