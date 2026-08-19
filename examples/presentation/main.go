//go:build linux

// Moving-block animation with presentation-time feedback: measures the
// commit-to-present delay on every frame and reports its statistics.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/stable/presentationtime"
	"github.com/xogas/wayland/wire"
)

const (
	winW = 256
	winH = 256
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

	presG, ok := globals.Find(presentationtime.InterfacePresentation)
	if !ok {
		return fmt.Errorf("wp_presentation not available")
	}
	presentation, err := presentationtime.BindPresentation(reg, presG.Name, min(presG.Version, presentationtime.VersionPresentation))
	if err != nil {
		return fmt.Errorf("bind wp_presentation: %w", err)
	}

	// CLOCK_MONOTONIC is the default; the clock_id event overrides it.
	var clkID atomic.Int32
	clkID.Store(1)
	presentation.OnClockID(func(ev presentationtime.PresentationClockIDEvent) {
		clkID.Store(int32(ev.ClkID))
		fmt.Printf("wp_presentation: clock_id = %d\n", ev.ClkID)
	})

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "presentation-shm", "presentationshm", winW, winH, nil)
	if err != nil {
		return err
	}

	db, err := shared.NewDoubleBuffer(core.Shm, winW, winH)
	if err != nil {
		return err
	}
	defer db.Close()

	errCh := shared.DispatchLoop(ctx, dpy)
	var stats latencyStats
	frames := 0

	for {
		select {
		case <-toplevel.Closed:
			stats.report()
			return nil
		case <-ctx.Done():
			stats.report()
			return nil
		case err := <-errCh:
			stats.report()
			return err
		case idx := <-db.Free():
			drawFrame(db.Pixels[idx], int(db.Stride), frames)

			done, err := shared.Frame(toplevel.Surface)
			if err != nil {
				return fmt.Errorf("frame: %w", err)
			}

			// Ask for presentation feedback on this commit and measure the
			// delay until the compositor presents it.
			commitNS := monotonicNow(clkID.Load())
			feedback, err := presentation.Feedback(wire.ObjectID(toplevel.Surface.Proxy().ID()))
			if err != nil {
				return fmt.Errorf("presentation feedback: %w", err)
			}
			feedback.OnPresented(func(ev presentationtime.PresentationFeedbackPresentedEvent) {
				presentNS := (int64(ev.TvSecHi)<<32|int64(ev.TvSecLo))*1e9 + int64(ev.TvNsec)
				stats.record(presentNS-commitNS, ev.Refresh, ev.Flags)
			})
			feedback.OnDiscarded(func(ev presentationtime.PresentationFeedbackDiscardedEvent) {
				stats.discard()
			})

			_ = toplevel.Surface.Attach(db.IDs[idx], 0, 0)
			_ = toplevel.Surface.Damage(0, 0, db.W, db.H)
			_ = toplevel.Surface.Commit()

			select {
			case <-done:
			case <-toplevel.Closed:
				stats.report()
				return nil
			case <-ctx.Done():
				stats.report()
				return nil
			case err := <-errCh:
				stats.report()
				return err
			}
			frames++
			if frames%60 == 0 {
				stats.report()
			}
		}
	}
}

// drawFrame renders the background and a moving block whose color cycles.
func drawFrame(data []byte, stride, frame int) {
	for y := range winH {
		rowOff := y * stride
		for x := range winW {
			off := rowOff + x*4
			data[off+0] = 0x18
			data[off+1] = 0x18
			data[off+2] = 0x20
			data[off+3] = 0xff
		}
	}

	blockSize := 64
	cycle := 200
	phase := frame % cycle
	bx := phase * (winW - blockSize) / cycle
	by := phase * (winH - blockSize) / cycle

	r := uint8((frame * 3) % 256)
	g := uint8((frame*3 + 85) % 256)
	b := uint8((frame*3 + 170) % 256)

	yEnd := min(by+blockSize, winH)
	xEnd := min(bx+blockSize, winW)
	for y := by; y < yEnd; y++ {
		rowOff := y * stride
		for x := bx; x < xEnd; x++ {
			off := rowOff + x*4
			data[off+0] = b
			data[off+1] = g
			data[off+2] = r
			data[off+3] = 0xff
		}
	}
}
