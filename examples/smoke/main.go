//go:build linux

// A Jos Stam fluid smoke simulation in a Wayland window, inspired by weston
// smoke. Moving the pointer stirs the fluid; idle smoke clouds are injected
// periodically.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
)

const (
	winW   = 400
	winH   = 400
	stride = winW * 4
)

// motion is one pointer movement, in simulation-grid coordinates.
type motion struct {
	x, y int
	dx   float64
	dy   float64
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

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Smoke", "go-wayland-smoke", winW, winH, nil)
	if err != nil {
		return err
	}

	db, err := shared.NewDoubleBuffer(core.Shm, winW, winH)
	if err != nil {
		return err
	}
	defer db.Close()

	sim := newSim()
	sim.injectRandom()

	// Pointer handlers only enqueue movements; the main loop applies them to
	// the simulation, so the sim is never touched from two goroutines.
	motionCh := make(chan motion, 32)
	if seat, err := shared.BindSeat(reg, globals); err == nil {
		ptr, err := seat.GetPointer()
		if err != nil {
			fmt.Fprintf(os.Stderr, "get pointer: %v\n", err)
		} else {
			var last struct{ x, y float64 }
			entered := false
			ptr.OnEnter(func(ev wayland.PointerEnterEvent) {
				entered = true
				last.x = ev.SurfaceX.Float64()
				last.y = ev.SurfaceY.Float64()
			})
			ptr.OnLeave(func(ev wayland.PointerLeaveEvent) {
				entered = false
			})
			ptr.OnMotion(func(ev wayland.PointerMotionEvent) {
				if !entered {
					return
				}
				nx, ny := ev.SurfaceX.Float64(), ev.SurfaceY.Float64()
				dx, dy := nx-last.x, ny-last.y
				last.x, last.y = nx, ny
				if dx == 0 && dy == 0 {
					return
				}
				select {
				case motionCh <- motion{int(nx) / simScale, int(ny) / simScale, dx, dy}:
				default: // drop when the queue is full
				}
			})
		}
	}

	errCh := shared.DispatchLoop(ctx, dpy)
	frames := 0
	start := time.Now()
	bi := 0
	var frameDone <-chan struct{}

	for {
		select {
		case <-toplevel.Closed:
			reportFPS("closed", start, frames)
			return nil
		case <-ctx.Done():
			reportFPS("timeout", start, frames)
			return nil
		case err := <-errCh:
			return err
		case m := <-motionCh:
			sim.injectMotion(m.x, m.y, m.dx, m.dy)
		case <-frameDone:
			frameDone = nil
		case bi = <-db.Free():
			sim.step(simDt)
			render(db.Pixels[bi], sim)

			frameDone, err = shared.Frame(toplevel.Surface)
			if err != nil {
				return fmt.Errorf("frame: %w", err)
			}
			_ = toplevel.Surface.Attach(db.IDs[bi], 0, 0)
			_ = toplevel.Surface.Damage(0, 0, winW, winH)
			_ = toplevel.Surface.Commit()
			frames++

			if frames%60 == 0 {
				elapsed := time.Since(start)
				fmt.Printf("frames=%d elapsed=%.1fs fps=%.1f\n", frames, elapsed.Seconds(), float64(frames)/elapsed.Seconds())
			}
		}
	}
}

// reportFPS prints the final frame statistics.
func reportFPS(reason string, start time.Time, frames int) {
	elapsed := time.Since(start).Seconds()
	fmt.Printf("%s. frames=%d fps=%.1f\n", reason, frames, float64(frames)/elapsed)
}
