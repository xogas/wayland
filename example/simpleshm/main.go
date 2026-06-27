//go:build linux

// weston-simple-shm style animation with concentric ring patterns, double
// buffering and frame-driven rendering.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const (
	winWidth  = 250
	winHeight = 250
)

type bufSlot struct {
	id   wire.ObjectID
	wl   *wayland.Buffer
	base []byte
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dpy, err := wayland.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: object=%d code=%d message=%q\n", pe.ObjectID, pe.Code, pe.Message)
	})

	reg, err := dpy.GetRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_registry: %v\n", err)
		os.Exit(1)
	}

	var (
		compG wayland.RegistryGlobalEvent
		shmG  wayland.RegistryGlobalEvent
		wmG   wayland.RegistryGlobalEvent
	)
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
	if compG.Interface == "" || shmG.Interface == "" || wmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "missing required globals (compositor, shm, xdg_wm_base)")
		os.Exit(1)
	}

	compositor, err := wayland.BindCompositor(reg, compG.Name, compG.Version)
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

	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) {
		_ = wmBase.Pong(ev.Serial)
	})

	surface, err := compositor.CreateSurface()
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

	shutdown := make(chan struct{})
	var cfgSerial uint32
	cfgDone := false

	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {
		cfgDone = true
	})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		close(shutdown)
	})

	_ = toplevel.SetTitle("simple-shm")
	_ = toplevel.SetAppID("simpleshm")
	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for cfgSerial == 0 || !cfgDone {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure events")
				return
			}
			break
		}
	}
	if cfgSerial == 0 {
		fmt.Fprintln(os.Stderr, "no configure serial received")
		return
	}
	_ = xdgSurface.AckConfigure(cfgSerial)

	stride := int32(winWidth * 4)
	bufH := int32(winHeight)
	oneSize := int64(bufH * stride)
	poolSize := oneSize * 2

	fd, closeFd, err := shmFile(poolSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shm: %v\n", err)
		os.Exit(1)
	}
	defer closeFd()

	data, err := syscall.Mmap(fd, 0, int(poolSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mmap: %v\n", err)
		os.Exit(1)
	}
	defer syscall.Munmap(data) //nolint: errcheck

	pool, err := shm.CreatePool(fd, int32(poolSize))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Destroy() //nolint: errcheck

	bufs := [2]bufSlot{}
	for i := range 2 {
		off := int32(i) * int32(oneSize)
		wlBuf, err := pool.CreateBuffer(off, int32(winWidth), bufH, stride, uint32(wayland.ShmFormatXrgb8888))
		if err != nil {
			fmt.Fprintf(os.Stderr, "create_buffer %d: %v\n", i, err)
			os.Exit(1)
		}
		bufs[i] = bufSlot{
			id:   wire.ObjectID(wlBuf.Proxy().ID()),
			wl:   wlBuf,
			base: data[off : off+int32(oneSize)],
		}
	}

	freeCh := make(chan int, 2)
	bufs[0].wl.OnRelease(func(ev wayland.BufferReleaseEvent) {
		freeCh <- 0
	})
	bufs[1].wl.OnRelease(func(ev wayland.BufferReleaseEvent) {
		freeCh <- 1
	})
	freeCh <- 0
	freeCh <- 1

	go func() {
		for {
			if err := dpy.Dispatch(ctx); err != nil {
				return
			}
		}
	}()

	start := time.Now()
	frames := 0

	for {
		select {
		case <-shutdown:
			printStats(start, frames)
			return
		case <-ctx.Done():
			printStats(start, frames)
			return
		case idx := <-freeCh:
			drawFrame(bufs[idx].base, winWidth, winHeight, int(stride), frames)
			done := make(chan struct{})
			cb, err := surface.Frame()
			if err != nil {
				fmt.Fprintf(os.Stderr, "frame: %v\n", err)
				return
			}
			cb.OnDone(func(ev wayland.CallbackDoneEvent) {
				close(done)
			})
			if err := surface.Attach(bufs[idx].id, 0, 0); err != nil {
				fmt.Fprintf(os.Stderr, "attach: %v\n", err)
				return
			}
			if err := surface.Damage(0, 0, int32(winWidth), bufH); err != nil {
				fmt.Fprintf(os.Stderr, "damage: %v\n", err)
				return
			}
			if err := surface.Commit(); err != nil {
				fmt.Fprintf(os.Stderr, "commit: %v\n", err)
				return
			}
			select {
			case <-shutdown:
				printStats(start, frames)
				return
			case <-ctx.Done():
				printStats(start, frames)
				return
			case <-done:
			}
			frames++
			if frames%60 == 0 {
				elapsed := time.Since(start).Seconds()
				fmt.Printf("%d frames (%.1f fps)\n", frames, float64(frames)/elapsed)
			}
		}
	}
}

func drawFrame(data []byte, w, h, stride, frame int) {
	cx := float64(w) * 0.5
	cy := float64(h) * 0.5
	t := float64(frame) * 0.08

	for y := range h {
		rowOff := y * stride
		dy := float64(y) - cy
		for x := range w {
			dx := float64(x) - cx
			d := math.Sqrt(dx*dx + dy*dy)
			vr := math.Sin(d*0.12 - t)
			vg := math.Sin(d*0.12 - t + 2.094)
			vb := math.Sin(d*0.12 - t + 4.189)
			off := rowOff + x*4
			data[off+0] = uint8((vb*0.5 + 0.5) * 255)
			data[off+1] = uint8((vg*0.5 + 0.5) * 255)
			data[off+2] = uint8((vr*0.5 + 0.5) * 255)
			data[off+3] = 0xff
		}
	}
}

func printStats(start time.Time, frames int) {
	elapsed := time.Since(start).Seconds()
	if elapsed > 0 {
		fmt.Printf("%d frames in %.1fs (%.1f fps)\n", frames, elapsed, float64(frames)/elapsed)
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
