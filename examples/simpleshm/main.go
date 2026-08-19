//go:build linux

// weston-simple-shm style animation: concentric rings drawn in a double
// buffer, paced by wl_surface.frame callbacks.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/xogas/wayland/examples/internal/shared"
)

const (
	winW = 250
	winH = 250
)

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

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "simple-shm", "simpleshm", winW, winH, nil)
	if err != nil {
		return err
	}

	db, err := shared.NewDoubleBuffer(core.Shm, winW, winH)
	if err != nil {
		return err
	}
	defer db.Close()

	errCh := shared.DispatchLoop(ctx, dpy)
	start := time.Now()
	frames := 0

	for {
		select {
		case <-toplevel.Closed:
			shared.ReportFPS(start, frames)
			return nil
		case <-ctx.Done():
			shared.ReportFPS(start, frames)
			return nil
		case err := <-errCh:
			return err
		case idx := <-db.Free():
			drawRings(db.Pixels[idx], int(db.Stride), frames)

			// Pace the next frame at the compositor's refresh rate.
			done, err := shared.Frame(toplevel.Surface)
			if err != nil {
				return fmt.Errorf("frame: %w", err)
			}
			_ = toplevel.Surface.Attach(db.IDs[idx], 0, 0)
			_ = toplevel.Surface.Damage(0, 0, db.W, db.H)
			_ = toplevel.Surface.Commit()

			select {
			case <-done:
			case <-toplevel.Closed:
				shared.ReportFPS(start, frames)
				return nil
			case <-ctx.Done():
				shared.ReportFPS(start, frames)
				return nil
			case err := <-errCh:
				return err
			}
			frames++
			if frames%60 == 0 {
				elapsed := time.Since(start).Seconds()
				fmt.Printf("%d frames (%.1f fps)\n", frames, float64(frames)/elapsed)
			}
		}
	}
}

// drawRings renders concentric sine-shaded rings that rotate over time.
func drawRings(data []byte, stride, frame int) {
	cx := float64(winW) * 0.5
	cy := float64(winH) * 0.5
	t := float64(frame) * 0.08

	for y := range winH {
		rowOff := y * stride
		dy := float64(y) - cy
		for x := range winW {
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
