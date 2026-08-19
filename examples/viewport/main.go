//go:build linux

// Viewport crop-and-scale demo: a 512x512 logical buffer with a moving 256x256
// source rectangle rotating around the buffer center, scaled to a configurable
// destination size (default 384x384). Demonstrates wp_viewporter without
// re-attaching the buffer each frame, buffer-scale (HiDPI) rendering, and
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

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: object=%d code=%d message=%q\n", pe.ObjectID, pe.Code, pe.Message)
	})

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	vpG, ok := globals.Find(viewporter.InterfaceViewporter)
	if !ok {
		fmt.Fprintln(os.Stderr, "wp_viewporter not available")
		os.Exit(1)
	}
	viewporterObj, err := viewporter.BindViewporter(reg, vpG.Name, min(vpG.Version, viewporter.VersionViewporter))
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind viewporter: %v\n", err)
		os.Exit(1)
	}

	// wl_shm format negotiation: the compositor announces the formats it
	// accepts right after the bind; rendering must pick one of them.
	shmFormats := map[uint32]bool{}
	core.Shm.OnFormat(func(ev wayland.ShmFormatEvent) {
		shmFormats[uint32(ev.Format)] = true
	})
	_ = dpy.Roundtrip(ctx)
	if !shmFormats[uint32(wayland.ShmFormatXrgb8888)] {
		fmt.Fprintln(os.Stderr, "compositor does not support xrgb8888")
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	surface := toplevel.Surface

	viewport, err := viewporterObj.GetViewport(wire.ObjectID(surface.Proxy().ID()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_viewport: %v\n", err)
		os.Exit(1)
	}

	// Optional: fractional-scale reports the compositor's preferred scale.
	if fsmG, ok := globals.Find(fractionalscale.InterfaceFractionalScaleManagerV1); ok {
		fsm, err := fractionalscale.BindFractionalScaleManagerV1(reg, fsmG.Name, min(fsmG.Version, fractionalscale.VersionFractionalScaleManagerV1))
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

	// Optional keyboard for interactive keys.
	keyCh := make(chan uint32, 16)
	if seat, err := shared.BindSeat(reg, globals); err == nil {
		kbd, err := seat.GetKeyboard()
		if err != nil {
			fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
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

	// The buffer is recreated at the current scale; its content is static, so
	// only the viewport source moves between frames.
	scale := int32(1)
	var bufCleanup func()
	rebuildBuffer := func() error {
		if bufCleanup != nil {
			bufCleanup()
			bufCleanup = nil
		}
		bufSize := logBufSize * scale
		id, pixels, cleanup, err := shared.NewBuffer(core.Shm, bufSize, bufSize, wayland.ShmFormatXrgb8888)
		if err != nil {
			return err
		}
		drawBuffer(pixels, int(bufSize*4), scale)
		if useBufferScale {
			if err := surface.SetBufferScale(scale); err != nil {
				cleanup()
				return err
			}
		}
		srcSize := wire.FixedFromFloat64(float64(logSrcW * scale))
		_ = viewport.SetSource(
			wire.FixedFromInt(128*scale), wire.FixedFromInt(128*scale),
			srcSize, srcSize,
		)
		_ = surface.Attach(id, 0, 0)
		_ = surface.Damage(0, 0, initDest, initDest)
		_ = surface.Commit()
		bufCleanup = cleanup
		return nil
	}
	if err := rebuildBuffer(); err != nil {
		fmt.Fprintf(os.Stderr, "buffer: %v\n", err)
		os.Exit(1)
	}

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
			if scale == 1 {
				scale = 2
			} else {
				scale = 1
			}
			fmt.Printf("buffer scale: %d (buffer %dx%d)\n", scale, logBufSize*scale, logBufSize*scale)
			if err := rebuildBuffer(); err != nil {
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
				printStats(start, frames)
				return
			case <-ctx.Done():
				printStats(start, frames)
				return
			case err := <-errCh:
				if err != nil {
					fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
				}
				printStats(start, frames)
				return
			case key := <-keyCh:
				handleKey(key)
			}
			continue
		}

		done := make(chan struct{})
		cb, err := surface.Frame()
		if err != nil {
			fmt.Fprintf(os.Stderr, "frame: %v\n", err)
			printStats(start, frames)
			return
		}
		cb.OnDone(func(ev wayland.CallbackDoneEvent) {
			close(done)
		})

		angle := float64(frames) * 0.05
		sx := (128.0 + radius*math.Cos(angle)) * float64(scale)
		sy := (128.0 + radius*math.Sin(angle)) * float64(scale)
		_ = viewport.SetSource(
			wire.FixedFromFloat64(sx),
			wire.FixedFromFloat64(sy),
			wire.FixedFromFloat64(float64(logSrcW*scale)),
			wire.FixedFromFloat64(float64(logSrcW*scale)),
		)
		_ = viewport.SetDestination(destSize, destSize)
		_ = surface.Damage(0, 0, destSize, destSize)
		_ = surface.Commit()

		select {
		case <-toplevel.Closed:
			printStats(start, frames)
			return
		case <-ctx.Done():
			printStats(start, frames)
			return
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			}
			printStats(start, frames)
			return
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

// drawBuffer renders the checkerboard pattern at physical resolution: each
// logical pixel of the 512x512 design becomes a scale x scale block.
func drawBuffer(data []byte, stride int, scale int32) {
	size := logBufSize * scale
	for y := 0; y < int(size); y++ {
		for x := 0; x < int(size); x++ {
			lx := x / int(scale)
			ly := y / int(scale)
			cx := lx / 64
			cy := ly / 64
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
			t := float64(lx+ly) / float64(logBufSize*2-2)
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
