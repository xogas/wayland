//go:build linux

// weston-simple-shm style animation with concentric ring patterns, double
// buffering and frame-driven rendering.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/example/internal/shared"
)

const (
	winWidth  = 250
	winHeight = 250
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

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "simple-shm", "simpleshm", winWidth, winHeight, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	db, err := shared.NewDoubleBuffer(core.Shm, winWidth, winHeight)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	errCh := shared.DispatchLoop(ctx, dpy)
	start := time.Now()
	frames := 0

	for {
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
		case idx := <-db.Free():
			drawFrame(db.Pixels[idx], int(db.Stride), frames)

			done := make(chan struct{})
			cb, err := toplevel.Surface.Frame()
			if err != nil {
				fmt.Fprintf(os.Stderr, "frame: %v\n", err)
				return
			}
			cb.OnDone(func(ev wayland.CallbackDoneEvent) {
				close(done)
			})
			_ = toplevel.Surface.Attach(db.IDs[idx], 0, 0)
			_ = toplevel.Surface.Damage(0, 0, db.W, db.H)
			_ = toplevel.Surface.Commit()

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

func drawFrame(data []byte, stride, frame int) {
	cx := float64(winWidth) * 0.5
	cy := float64(winHeight) * 0.5
	t := float64(frame) * 0.08

	for y := range winHeight {
		rowOff := y * stride
		dy := float64(y) - cy
		for x := range winWidth {
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
