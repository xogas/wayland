//go:build linux

// Software-rendered rotating 3D cube: perspective projection, backface
// culling, painter's algorithm and scanline rasterization, all on the CPU.
// Frames are paced by wl_surface.frame callbacks with a fallback timer so
// the animation continues while the window is hidden.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
)

const (
	winW     = 480
	winH     = 480
	stride   = winW * 4
	focal    = 300.0
	cubeDist = 4.0
	speedY   = 0.9
	speedX   = 0.6
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

	// SIGINT/SIGTERM also stop the animation.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = dpy.Close() }()

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		return err
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Rotating Cube", "go-wayland-cube", winW, winH, nil)
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
	bi := db.Next()

	// The first frame is drawn immediately; later frames wait for the
	// previous frame callback.
	frameReady := make(chan struct{}, 1)
	clearBlack(db.Pixels[bi])
	renderCube(db.Pixels[bi], 0, 0)
	_ = toplevel.Surface.Attach(db.IDs[bi], 0, 0)
	_ = toplevel.Surface.Damage(0, 0, winW, winH)
	_ = toplevel.Surface.Commit()
	frames = 1

	fmt.Printf("cube: %dx%d, animating...\n", winW, winH)

	for {
		select {
		case <-toplevel.Closed:
			shared.ReportFPS(start, frames)
			return nil
		case <-ctx.Done():
			shared.ReportFPS(start, frames)
			return nil
		case err := <-errCh:
			shared.ReportFPS(start, frames)
			return err
		case <-frameReady:
		case <-time.After(time.Second):
			// No frame callback (window hidden): keep rendering, throttled
			// by the buffer release below.
		}

		select {
		case bi = <-db.Free():
		case <-toplevel.Closed:
			shared.ReportFPS(start, frames)
			return nil
		case <-ctx.Done():
			shared.ReportFPS(start, frames)
			return nil
		case err := <-errCh:
			shared.ReportFPS(start, frames)
			return err
		case <-time.After(time.Second):
			continue
		}

		elapsed := time.Since(start).Seconds()
		clearBlack(db.Pixels[bi])
		renderCube(db.Pixels[bi], elapsed*speedY, elapsed*speedX)

		cb, err := toplevel.Surface.Frame()
		if err != nil {
			shared.ReportFPS(start, frames)
			return fmt.Errorf("frame: %w", err)
		}
		cb.OnDone(func(ev wayland.CallbackDoneEvent) {
			select {
			case frameReady <- struct{}{}:
			default:
			}
		})

		_ = toplevel.Surface.Attach(db.IDs[bi], 0, 0)
		_ = toplevel.Surface.Damage(0, 0, winW, winH)
		_ = toplevel.Surface.Commit()

		frames++
	}
}
