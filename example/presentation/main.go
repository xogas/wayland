//go:build linux

// Moving block animation with presentation-time feedback statistics.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/presentationtime"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const (
	winWidth  = 256
	winHeight = 256
)

type bufSlot struct {
	id   wire.ObjectID
	wl   *wayland.Buffer
	base []byte
}

type latencyStats struct {
	mu        sync.Mutex
	n         int
	minNS     int64
	maxNS     int64
	totalNS   int64
	refresh   uint32
	flags     uint32
	discarded int
}

func (s *latencyStats) record(ns int64, refresh, flags uint32) {
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

func flagsString(f uint32) string {
	s := ""
	if f&uint32(presentationtime.PresentationFeedbackKindVsync) != 0 {
		s += "vsync "
	}
	if f&uint32(presentationtime.PresentationFeedbackKindHwClock) != 0 {
		s += "hw_clock "
	}
	if f&uint32(presentationtime.PresentationFeedbackKindHwCompletion) != 0 {
		s += "hw_completion "
	}
	if f&uint32(presentationtime.PresentationFeedbackKindZeroCopy) != 0 {
		s += "zero_copy "
	}
	if s == "" {
		return "none"
	}
	return s[:len(s)-1]
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
		presG wayland.RegistryGlobalEvent
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
		case presentationtime.InterfacePresentation:
			presG = g
		}
	}
	if compG.Interface == "" || shmG.Interface == "" || wmG.Interface == "" || presG.Interface == "" {
		fmt.Fprintln(os.Stderr, "missing required globals (compositor, shm, xdg_wm_base, wp_presentation)")
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
	presentation, err := presentationtime.BindPresentation(reg, presG.Name, presG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind wp_presentation: %v\n", err)
		os.Exit(1)
	}

	presentation.OnClockID(func(ev presentationtime.PresentationClockIDEvent) {
		fmt.Printf("wp_presentation: clock_id = %d\n", ev.ClkID)
	})

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

	_ = toplevel.SetTitle("presentation-shm")
	_ = toplevel.SetAppID("presentationshm")
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
	for i := 0; i < 2; i++ {
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

	var stats latencyStats
	frames := 0

	for {
		select {
		case <-shutdown:
			stats.report()
			return
		case <-ctx.Done():
			stats.report()
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

			var ts syscall.Timespec
			_, _, _ = syscall.Syscall(syscall.SYS_CLOCK_GETTIME, 1, uintptr(unsafe.Pointer(&ts)), 0)
			commitNS := ts.Sec*1e9 + ts.Nsec

			feedback, err := presentation.Feedback(wire.ObjectID(surface.Proxy().ID()))
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
				stats.report()
				return
			case <-ctx.Done():
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

func drawFrame(data []byte, w, h, stride, frame int) {
	for y := 0; y < h; y++ {
		rowOff := y * stride
		for x := 0; x < w; x++ {
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
	bx := phase * (w - blockSize) / cycle
	by := phase * (h - blockSize) / cycle

	r := uint8((frame * 3) % 256)
	g := uint8((frame*3 + 85) % 256)
	b := uint8((frame*3 + 170) % 256)

	yEnd := by + blockSize
	if yEnd > h {
		yEnd = h
	}
	xEnd := bx + blockSize
	if xEnd > w {
		xEnd = w
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
