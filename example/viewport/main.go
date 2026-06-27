//go:build linux

// Viewport crop-and-scale demo: a 512x512 buffer with a moving 256x256 source
// rectangle rotating around the buffer center, scaled to a configurable
// destination size (default 384x384). Demonstrates wp_viewporter without
// re-attaching the buffer each frame.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/viewporter"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/protocol/staging/fractionalscale"
	"github.com/xogas/wayland/wire"
)

const (
	bufSize  = 512
	srcW     = 256
	srcH     = 256
	initDest = 384
)

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
		vpG   wayland.RegistryGlobalEvent
		fsmG  wayland.RegistryGlobalEvent
		seatG wayland.RegistryGlobalEvent
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
		case viewporter.InterfaceViewporter:
			vpG = g
		case fractionalscale.InterfaceFractionalScaleManagerV1:
			fsmG = g
		case wayland.InterfaceSeat:
			seatG = g
		}
	}
	if compG.Interface == "" || shmG.Interface == "" || wmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "missing required globals (compositor, shm, xdg_wm_base)")
		os.Exit(1)
	}
	if vpG.Interface == "" {
		fmt.Fprintln(os.Stderr, "wp_viewporter not available")
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
	viewporterObj, err := viewporter.BindViewporter(reg, vpG.Name, vpG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind viewporter: %v\n", err)
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

	viewport, err := viewporterObj.GetViewport(wire.ObjectID(surface.Proxy().ID()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_viewport: %v\n", err)
		os.Exit(1)
	}

	if fsmG.Interface != "" {
		fsm, err := fractionalscale.BindFractionalScaleManagerV1(reg, fsmG.Name, fsmG.Version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind fractional_scale_manager: %v\n", err)
		} else {
			fs, err := fsm.GetFractionalScale(wire.ObjectID(surface.Proxy().ID()))
			if err != nil {
				fmt.Fprintf(os.Stderr, "get_fractional_scale: %v\n", err)
			} else {
				fs.OnPreferredScale(func(ev fractionalscale.FractionalScaleV1PreferredScaleEvent) {
					fmt.Printf("fractional-scale preferred_scale: %d (%.5f)\n", ev.Scale, float64(ev.Scale)/120.0)
				})
			}
		}
	} else {
		fmt.Println("wp_fractional_scale_manager_v1 not available")
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

	_ = toplevel.SetTitle("viewport")
	_ = toplevel.SetAppID("viewport")
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

	stride := int32(bufSize * 4)
	poolSize := int64(bufSize * int(stride))

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

	wlBuf, err := pool.CreateBuffer(0, bufSize, bufSize, stride, uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
		os.Exit(1)
	}
	bufID := wire.ObjectID(wlBuf.Proxy().ID())
	drawBuffer(data, int(stride))

	paused := false
	destSize := int32(initDest)

	keyCh := make(chan uint32, 16)
	var kbd *wayland.Keyboard
	if seatG.Interface != "" {
		seat, err := wayland.BindSeat(reg, seatG.Name, seatG.Version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind seat: %v\n", err)
		} else {
			kbd, err = seat.GetKeyboard()
			if err != nil {
				fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
				kbd = nil
			} else {
				kbd.OnKey(func(ev wayland.KeyboardKeyEvent) {
					if ev.State == 1 {
						select {
						case keyCh <- ev.Key:
						default:
						}
					}
				})
			}
		}
	}

	_ = viewport.SetSource(wire.FixedFromInt(128), wire.FixedFromInt(128), wire.FixedFromInt(srcW), wire.FixedFromInt(srcH))
	_ = viewport.SetDestination(destSize, destSize)
	_ = surface.Attach(bufID, 0, 0)
	_ = surface.Damage(0, 0, destSize, destSize)
	_ = surface.Commit()

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
		if paused {
			select {
			case <-shutdown:
				printStats(start, frames)
				return
			case <-ctx.Done():
				printStats(start, frames)
				return
			case key := <-keyCh:
				handleKey(key, &paused, &destSize)
			}
			continue
		}

		done := make(chan struct{})
		cb, err := surface.Frame()
		if err != nil {
			fmt.Fprintf(os.Stderr, "frame: %v\n", err)
			return
		}
		cb.OnDone(func(ev wayland.CallbackDoneEvent) {
			close(done)
		})

		angle := float64(frames) * 0.05
		const radius = 128.0
		sx := 128.0 + radius*math.Cos(angle)
		sy := 128.0 + radius*math.Sin(angle)
		_ = viewport.SetSource(
			wire.FixedFromFloat64(sx),
			wire.FixedFromFloat64(sy),
			wire.FixedFromInt(srcW),
			wire.FixedFromInt(srcH),
		)
		_ = viewport.SetDestination(destSize, destSize)
		_ = surface.Damage(0, 0, destSize, destSize)
		_ = surface.Commit()

		select {
		case <-shutdown:
			printStats(start, frames)
			return
		case <-ctx.Done():
			printStats(start, frames)
			return
		case key := <-keyCh:
			handleKey(key, &paused, &destSize)
			if !paused {
				<-done
				frames++
			}
		case <-done:
			frames++
		}

		if frames%60 == 0 {
			elapsed := time.Since(start).Seconds()
			fmt.Printf("%d frames (%.1f fps)\n", frames, float64(frames)/elapsed)
		}
	}
}

func handleKey(key uint32, paused *bool, destSize *int32) {
	const (
		keySpace uint32 = 57
		keyMinus uint32 = 12
		keyEqual uint32 = 13
	)

	switch key {
	case keySpace:
		*paused = !*paused
		if *paused {
			fmt.Println("paused")
		} else {
			fmt.Println("resumed")
		}
	case keyMinus:
		sz := *destSize - 32
		if sz >= 128 {
			*destSize = sz
			fmt.Printf("destination: %dx%d\n", sz, sz)
		}
	case keyEqual:
		sz := *destSize + 32
		if sz <= 512 {
			*destSize = sz
			fmt.Printf("destination: %dx%d\n", sz, sz)
		}
	}
}

func drawBuffer(data []byte, stride int) {
	for y := range bufSize {
		for x := range bufSize {
			cx := x / 64
			cy := y / 64
			var r, g, b uint8
			if (cx+cy)&1 == 0 {
				r = uint8((cx * 37) % 256)
				g = uint8((cy * 53) % 256)
				b = uint8(((cx + cy) * 23) % 256)
			} else {
				r = uint8(((7 - cx) * 37) % 256)
				g = uint8(((7 - cy) * 53) % 256)
				b = uint8(((14 - cx - cy) * 23) % 256)
			}
			t := float64(x+y) / float64(bufSize*2-2)
			r = uint8(float64(r)*(1-t) + 255*t)
			g = uint8(float64(g)*(1-t) + 140*t)
			b = uint8(float64(b)*(1-t) + 60*t)
			off := y*stride + x*4
			data[off+0] = b
			data[off+1] = g
			data[off+2] = r
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
