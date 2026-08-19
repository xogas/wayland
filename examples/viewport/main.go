//go:build linux

// Viewport crop-and-scale demo: a 512x512 logical buffer with a moving
// 256x256 source rectangle rotating around the buffer center, scaled to a
// configurable destination size. Demonstrates wp_viewporter without
// re-attaching the buffer each frame, buffer-scale (HiDPI) rendering and
// wl_shm format negotiation.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/stable/viewporter"
	"github.com/xogas/wayland/protocol/staging/fractionalscale"
	"github.com/xogas/wayland/wire"
)

const (
	logBufSize = 512 // logical buffer size, in surface coordinates
	logSrcW    = 256 // logical source rectangle size
	initDest   = 384
	radius     = 128.0

	keySpace = 57
	keyMinus = 12
	keyEqual = 13
	keyS     = 39
)

// viewportApp owns the buffer whose source rectangle the viewport moves.
type viewportApp struct {
	surface        *wayland.Surface
	viewport       *viewporter.Viewport
	useBufferScale bool
	scale          int32
	bufCleanup     func()
}

// rebuildBuffer recreates the buffer at the current scale and commits it.
// The content is static, so between frames only the viewport source moves.
func (a *viewportApp) rebuildBuffer(shm *wayland.Shm) error {
	if a.bufCleanup != nil {
		a.bufCleanup()
		a.bufCleanup = nil
	}
	bufSize := logBufSize * a.scale
	id, pixels, cleanup, err := shared.NewBuffer(shm, bufSize, bufSize, wayland.ShmFormatXrgb8888)
	if err != nil {
		return err
	}
	drawBuffer(pixels, int(bufSize*4), a.scale)
	if a.useBufferScale {
		if err := a.surface.SetBufferScale(a.scale); err != nil {
			cleanup()
			return err
		}
	}
	srcSize := wire.FixedFromFloat64(float64(logSrcW * a.scale))
	_ = a.viewport.SetSource(
		wire.FixedFromInt(128*a.scale), wire.FixedFromInt(128*a.scale),
		srcSize, srcSize,
	)
	_ = a.surface.Attach(id, 0, 0)
	_ = a.surface.Damage(0, 0, initDest, initDest)
	_ = a.surface.Commit()
	a.bufCleanup = cleanup
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = dpy.Close() }()

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		return err
	}

	vpG, ok := globals.Find(viewporter.InterfaceViewporter)
	if !ok {
		return fmt.Errorf("wp_viewporter not available")
	}
	viewporterObj, err := viewporter.BindViewporter(reg, vpG.Name, min(vpG.Version, viewporter.VersionViewporter))
	if err != nil {
		return fmt.Errorf("bind viewporter: %w", err)
	}

	// wl_shm format negotiation: the compositor announces accepted formats
	// right after the bind; rendering must pick one of them.
	shmFormats := map[uint32]bool{}
	core.Shm.OnFormat(func(ev wayland.ShmFormatEvent) {
		shmFormats[uint32(ev.Format)] = true
	})
	_ = dpy.Roundtrip(ctx)
	if !shmFormats[uint32(wayland.ShmFormatXrgb8888)] {
		return fmt.Errorf("compositor does not support xrgb8888")
	}
	fmt.Println("shm format xrgb8888 advertised by compositor")

	compVer, _ := globals.Version(wayland.InterfaceCompositor)
	useBufferScale := compVer >= 3 // wl_surface.set_buffer_scale, since v3
	if useBufferScale {
		fmt.Printf("buffer-scale support: yes (wl_surface v%d)\n", compVer)
	} else {
		fmt.Printf("buffer-scale support: no (wl_surface v%d < 3), S key disabled\n", compVer)
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "viewport", "viewport", initDest, initDest, nil)
	if err != nil {
		return err
	}
	surface := toplevel.Surface

	viewport, err := viewporterObj.GetViewport(wire.ObjectID(surface.Proxy().ID()))
	if err != nil {
		return fmt.Errorf("get viewport: %w", err)
	}

	// Optional: fractional-scale reports the compositor's preferred scale.
	if fsmG, ok := globals.Find(fractionalscale.InterfaceFractionalScaleManagerV1); ok {
		fsm, err := fractionalscale.BindFractionalScaleManagerV1(reg, fsmG.Name, min(fsmG.Version, fractionalscale.VersionFractionalScaleManagerV1))
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind fractional_scale_manager: %v\n", err)
		} else {
			fs, err := fsm.GetFractionalScale(wire.ObjectID(surface.Proxy().ID()))
			if err != nil {
				fmt.Fprintf(os.Stderr, "get fractional_scale: %v\n", err)
			} else {
				fs.OnPreferredScale(func(ev fractionalscale.FractionalScaleV1PreferredScaleEvent) {
					fmt.Printf("fractional-scale preferred_scale: %d (%.5f)\n", ev.Scale, float64(ev.Scale)/120.0)
				})
			}
		}
	} else {
		fmt.Println("wp_fractional_scale_manager_v1 not available")
	}

	// Optional keyboard: keys arrive on keyCh, consumed by the main loop.
	keyCh := make(chan uint32, 16)
	if seat, err := shared.BindSeat(reg, globals); err == nil {
		kbd, err := seat.GetKeyboard()
		if err != nil {
			fmt.Fprintf(os.Stderr, "get keyboard: %v\n", err)
		} else {
			kbd.OnKey(func(ev wayland.KeyboardKeyEvent) {
				if ev.State != wayland.KeyboardKeyStatePressed {
					return
				}
				select {
				case keyCh <- ev.Key:
				default:
				}
			})
		}
	}

	ap := &viewportApp{
		surface:        surface,
		viewport:       viewport,
		useBufferScale: useBufferScale,
		scale:          1,
	}
	if err := ap.rebuildBuffer(core.Shm); err != nil {
		return fmt.Errorf("buffer: %w", err)
	}
	defer func() { ap.bufCleanup() }()

	paused := false
	destSize := int32(initDest)
	handleKey := func(key uint32) {
		switch key {
		case keySpace:
			paused = !paused
			if paused {
				fmt.Println("paused")
			} else {
				fmt.Println("resumed")
			}
		case keyMinus:
			sz := destSize - 32
			if sz >= 128 {
				destSize = sz
				fmt.Printf("destination: %dx%d\n", sz, sz)
			}
		case keyEqual:
			sz := destSize + 32
			if sz <= 512 {
				destSize = sz
				fmt.Printf("destination: %dx%d\n", sz, sz)
			}
		case keyS:
			if !useBufferScale {
				fmt.Println("buffer scale unsupported on this compositor")
				return
			}
			if ap.scale == 1 {
				ap.scale = 2
			} else {
				ap.scale = 1
			}
			fmt.Printf("buffer scale: %d (buffer %dx%d)\n", ap.scale, logBufSize*ap.scale, logBufSize*ap.scale)
			if err := ap.rebuildBuffer(core.Shm); err != nil {
				fmt.Fprintf(os.Stderr, "rebuild: %v\n", err)
			}
		}
	}

	errCh := shared.DispatchLoop(ctx, dpy)
	start := time.Now()
	frames := 0

	for {
		if paused {
			select {
			case <-toplevel.Closed:
				shared.ReportFPS(start, frames)
				return nil
			case <-ctx.Done():
				shared.ReportFPS(start, frames)
				return nil
			case err := <-errCh:
				return err
			case key := <-keyCh:
				handleKey(key)
			}
			continue
		}

		done, err := shared.Frame(surface)
		if err != nil {
			shared.ReportFPS(start, frames)
			return fmt.Errorf("frame: %w", err)
		}

		// Move the viewport source along a circular path over the static
		// buffer; only the viewport and damage change between frames.
		angle := float64(frames) * 0.05
		sx := (128.0 + radius*math.Cos(angle)) * float64(ap.scale)
		sy := (128.0 + radius*math.Sin(angle)) * float64(ap.scale)
		_ = viewport.SetSource(
			wire.FixedFromFloat64(sx),
			wire.FixedFromFloat64(sy),
			wire.FixedFromFloat64(float64(logSrcW*ap.scale)),
			wire.FixedFromFloat64(float64(logSrcW*ap.scale)),
		)
		_ = viewport.SetDestination(destSize, destSize)
		_ = surface.Damage(0, 0, destSize, destSize)
		_ = surface.Commit()

		select {
		case <-toplevel.Closed:
			shared.ReportFPS(start, frames)
			return nil
		case <-ctx.Done():
			shared.ReportFPS(start, frames)
			return nil
		case err := <-errCh:
			return err
		case key := <-keyCh:
			handleKey(key)
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
