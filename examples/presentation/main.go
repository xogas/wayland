//go:build linux

// Moving block animation with presentation-time feedback statistics.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/example/internal/shared"
	"github.com/xogas/wayland/protocol/stable/presentationtime"
	"github.com/xogas/wayland/wire"
)

const (
	winWidth  = 256
	winHeight = 256
)

type latencyStats struct {
	mu        sync.Mutex
	n         int
	minNS     int64
	maxNS     int64
	totalNS   int64
	refresh   uint32
	flags     presentationtime.PresentationFeedbackKind
	discarded int
}

func (s *latencyStats) record(ns int64, refresh uint32, flags presentationtime.PresentationFeedbackKind) {
	s.mu.Lock()
	s.n++
	s.totalNS += ns
	if s.minNS == 0 || ns < s.minNS {
		s.minNS = ns
	}
	if ns > s.maxNS {
		s.maxNS = ns
	}
	s.refresh = refresh
	s.flags = flags
	s.mu.Unlock()
}

func (s *latencyStats) discard() {
	s.mu.Lock()
	s.discarded++
	s.mu.Unlock()
}

func (s *latencyStats) report() {
	s.mu.Lock()
	if s.n == 0 {
		s.mu.Unlock()
		return
	}
	avgMS := float64(s.totalNS/int64(s.n)) / 1e6
	minMS := float64(s.minNS) / 1e6
	maxMS := float64(s.maxNS) / 1e6
	refresh := s.refresh
	flags := s.flags
	discarded := s.discarded
	s.n = 0
	s.minNS = 0
	s.maxNS = 0
	s.totalNS = 0
	s.refresh = 0
	s.flags = 0
	s.discarded = 0
	s.mu.Unlock()

	fmt.Printf("presentation: avg %.2f ms min %.2f ms max %.2f ms | refresh %d ns | flags %s",
		avgMS, minMS, maxMS, refresh, flagsString(flags))
	if discarded > 0 {
		fmt.Printf(" | discarded %d", discarded)
	}
	fmt.Println()
}

func flagsString(f presentationtime.PresentationFeedbackKind) string {
	s := ""
	if f&presentationtime.PresentationFeedbackKindVsync != 0 {
		s += "vsync "
	}
	if f&presentationtime.PresentationFeedbackKindHwClock != 0 {
		s += "hw_clock "
	}
	if f&presentationtime.PresentationFeedbackKindHwCompletion != 0 {
		s += "hw_completion "
	}
	if f&presentationtime.PresentationFeedbackKindZeroCopy != 0 {
		s += "zero_copy "
	}
	if s == "" {
		return "none"
	}
	return s[:len(s)-1]
}

// monotonicNow reads the compositor's presentation clock (CLOCK_MONOTONIC by
// default; use the clock_id reported by wp_presentation for the exact clock).
func monotonicNow(clkID int32) int64 {
	var ts syscall.Timespec
	_, _, e1 := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, uintptr(clkID), uintptr(unsafe.Pointer(&ts)), 0)
	if e1 != 0 {
		return 0
	}
	return ts.Sec*1e9 + ts.Nsec
}

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

	presG, ok := globals.Find(presentationtime.InterfacePresentation)
	if !ok {
		fmt.Fprintln(os.Stderr, "wp_presentation not available")
		os.Exit(1)
	}
	presentation, err := presentationtime.BindPresentation(reg, presG.Name, min(presG.Version, presentationtime.VersionPresentation))
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind wp_presentation: %v\n", err)
		os.Exit(1)
	}

	var clkID atomic.Int32 // default CLOCK_MONOTONIC, overridden by the clock_id event
	clkID.Store(1)
	presentation.OnClockID(func(ev presentationtime.PresentationClockIDEvent) {
		clkID.Store(int32(ev.ClkID))
		fmt.Printf("wp_presentation: clock_id = %d\n", ev.ClkID)
	})

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "presentation-shm", "presentationshm", winWidth, winHeight, nil)
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
	var stats latencyStats
	frames := 0

	for {
		select {
		case <-toplevel.Closed:
			stats.report()
			return
		case <-ctx.Done():
			stats.report()
			return
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			}
			stats.report()
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

			commitNS := monotonicNow(clkID.Load())
			feedback, err := presentation.Feedback(wire.ObjectID(toplevel.Surface.Proxy().ID()))
			if err != nil {
				fmt.Fprintf(os.Stderr, "presentation feedback: %v\n", err)
				return
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
			case <-toplevel.Closed:
				stats.report()
				return
			case <-ctx.Done():
				stats.report()
				return
			case err := <-errCh:
				if err != nil {
					fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
				}
				stats.report()
				return
			case <-done:
			}
			frames++
			if frames%60 == 0 {
				stats.report()
			}
		}
	}
}

func drawFrame(data []byte, stride, frame int) {
	for y := 0; y < winHeight; y++ {
		rowOff := y * stride
		for x := 0; x < winWidth; x++ {
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
	bx := phase * (winWidth - blockSize) / cycle
	by := phase * (winHeight - blockSize) / cycle

	r := uint8((frame * 3) % 256)
	g := uint8((frame*3 + 85) % 256)
	b := uint8((frame*3 + 170) % 256)

	yEnd := by + blockSize
	if yEnd > winHeight {
		yEnd = winHeight
	}
	xEnd := bx + blockSize
	if xEnd > winWidth {
		xEnd = winWidth
	}
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
