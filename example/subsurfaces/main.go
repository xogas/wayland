//go:build linux

// Subsurface demo: animated child surface moving on a circular path with
// sync/desync toggle and z-order control.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const (
	mainW = 400
	mainH = 400
	subW  = 120
	subH  = 120
	keyS  = 31
	keyR  = 19
)

type subBufSlot struct {
	id   wire.ObjectID
	wl   *wayland.Buffer
	base []byte
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

	var compG, shmG, wmG, subcompG, seatG wayland.RegistryGlobalEvent
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
		case wayland.InterfaceSubcompositor:
			subcompG = g
		case wayland.InterfaceSeat:
			seatG = g
		}
	}
	if compG.Interface == "" || shmG.Interface == "" || wmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "missing required globals (compositor, shm, xdg_wm_base)")
		os.Exit(1)
	}
	if subcompG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_subcompositor global")
		os.Exit(1)
	}
	if seatG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_seat global")
		os.Exit(1)
	}

	comp, err := wayland.BindCompositor(reg, compG.Name, compG.Version)
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
	subcomp, err := wayland.BindSubcompositor(reg, subcompG.Name, subcompG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind subcompositor: %v\n", err)
		os.Exit(1)
	}
	seat, err := wayland.BindSeat(reg, seatG.Name, seatG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind seat: %v\n", err)
		os.Exit(1)
	}

	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) {
		_ = wmBase.Pong(ev.Serial)
	})

	mainSurface, err := comp.CreateSurface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_surface: %v\n", err)
		os.Exit(1)
	}
	mainID := wire.ObjectID(mainSurface.Proxy().ID())

	xdgSurface, err := wmBase.GetXdgSurface(mainID)
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

	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		close(shutdown)
	})

	_ = toplevel.SetTitle("subsurfaces")
	_ = toplevel.SetAppID("subsurfaces-demo")
	_ = mainSurface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for cfgSerial == 0 {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure")
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

	mainStride := int32(mainW * 4)
	subStride := int32(subW * 4)
	mainBufSize := int64(mainH * mainStride)
	subBufSize := int64(subH * subStride)
	poolSize := mainBufSize + subBufSize*2

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

	mainBuf, err := pool.CreateBuffer(0, mainW, mainH, mainStride, uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_buffer main: %v\n", err)
		os.Exit(1)
	}
	defer mainBuf.Destroy() //nolint: errcheck
	mainBufID := wire.ObjectID(mainBuf.Proxy().ID())

	drawGradient(data[0:mainBufSize], int(mainW), int(mainH), int(mainStride))

	subSurfaceWL, err := comp.CreateSurface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_surface sub: %v\n", err)
		os.Exit(1)
	}
	subSurfaceID := wire.ObjectID(subSurfaceWL.Proxy().ID())

	subsurface, err := subcomp.GetSubsurface(subSurfaceID, mainID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_subsurface: %v\n", err)
		os.Exit(1)
	}
	_ = subsurface.SetPosition(int32((mainW-subW)/2), int32((mainH-subH)/2))

	subBufs := [2]subBufSlot{}
	subBufOff0 := int32(mainBufSize)
	subBufOff1 := int32(mainBufSize + subBufSize)
	for i := 0; i < 2; i++ {
		off := int32(i)
		switch off {
		case 0:
			off = subBufOff0
		case 1:
			off = subBufOff1
		}
		wlBuf, err := pool.CreateBuffer(off, subW, subH, subStride, uint32(wayland.ShmFormatXrgb8888))
		if err != nil {
			fmt.Fprintf(os.Stderr, "create_buffer sub %d: %v\n", i, err)
			os.Exit(1)
		}
		subBufs[i] = subBufSlot{
			id:   wire.ObjectID(wlBuf.Proxy().ID()),
			wl:   wlBuf,
			base: data[off : off+int32(subBufSize)],
		}
	}

	freeCh := make(chan int, 2)
	subBufs[0].wl.OnRelease(func(ev wayland.BufferReleaseEvent) {
		freeCh <- 0
	})
	subBufs[1].wl.OnRelease(func(ev wayland.BufferReleaseEvent) {
		freeCh <- 1
	})
	freeCh <- 0
	freeCh <- 1

	desyncMode := false
	placeAbove := true

	kbd, err := seat.GetKeyboard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
		os.Exit(1)
	}
	kbd.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.State != uint32(wayland.KeyboardKeyStatePressed) {
			return
		}
		switch ev.Key {
		case keyS:
			if desyncMode {
				_ = subsurface.SetSync()
				desyncMode = false
				fmt.Println("mode: sync")
			} else {
				_ = subsurface.SetDesync()
				desyncMode = true
				fmt.Println("mode: desync")
			}
		case keyR:
			if placeAbove {
				_ = subsurface.PlaceBelow(mainID)
				placeAbove = false
				fmt.Println("place_below parent")
			} else {
				_ = subsurface.PlaceAbove(mainID)
				placeAbove = true
				fmt.Println("place_above parent")
			}
		}
	})

	_ = mainSurface.Attach(mainBufID, 0, 0)
	_ = mainSurface.Damage(0, 0, mainW, mainH)
	_ = mainSurface.Commit()

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
		select {
		case <-shutdown:
			printStats(start, frames)
			return
		case <-ctx.Done():
			printStats(start, frames)
			return
		case idx := <-freeCh:
			drawSub(subBufs[idx].base, subW, subH, int(subStride), frames)
			px, py := subPosition(frames)
			_ = subsurface.SetPosition(int32(px), int32(py))

			done := make(chan struct{})
			cb, err := mainSurface.Frame()
			if err != nil {
				fmt.Fprintf(os.Stderr, "frame: %v\n", err)
				return
			}
			cb.OnDone(func(ev wayland.CallbackDoneEvent) {
				close(done)
			})

			if err := subSurfaceWL.Attach(subBufs[idx].id, 0, 0); err != nil {
				fmt.Fprintf(os.Stderr, "sub attach: %v\n", err)
				return
			}
			if err := subSurfaceWL.Damage(0, 0, subW, subH); err != nil {
				fmt.Fprintf(os.Stderr, "sub damage: %v\n", err)
				return
			}
			if err := subSurfaceWL.Commit(); err != nil {
				fmt.Fprintf(os.Stderr, "sub commit: %v\n", err)
				return
			}

			if err := mainSurface.Commit(); err != nil {
				fmt.Fprintf(os.Stderr, "main commit: %v\n", err)
				return
			}

			select {
			case <-shutdown:
				printStats(start, frames)
				return
			case <-ctx.Done():
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

func drawGradient(data []byte, w, h, stride int) {
	for y := 0; y < h; y++ {
		rowOff := y * stride
		for x := 0; x < w; x++ {
			off := rowOff + x*4
			data[off+0] = uint8(float64(x) / float64(w) * 96)
			data[off+1] = uint8(float64(y) / float64(h) * 128)
			data[off+2] = uint8(40 + float64(x)/float64(w)*64 + float64(y)/float64(h)*40)
			data[off+3] = 0xff
		}
	}
}

func drawSub(data []byte, w, h, stride, frame int) {
	cx := float64(w) * 0.5
	cy := float64(h) * 0.5
	t := float64(frame) * 0.06

	for y := 0; y < h; y++ {
		rowOff := y * stride
		dy := float64(y) - cy
		for x := 0; x < w; x++ {
			dx := float64(x) - cx
			d := math.Sqrt(dx*dx + dy*dy)
			rMax := 40.0 + 20.0*math.Sin(t*1.3)
			off := rowOff + x*4
			if d < rMax {
				hue := math.Atan2(dy, dx) + t
				r := uint8((math.Sin(hue) + 1) * 0.5 * 255)
				g := uint8((math.Sin(hue+2.094) + 1) * 0.5 * 255)
				b := uint8((math.Sin(hue+4.189) + 1) * 0.5 * 255)
				fade := 1.0 - d/rMax
				data[off+0] = uint8(float64(b) * fade)
				data[off+1] = uint8(float64(g) * fade)
				data[off+2] = uint8(float64(r) * fade)
				data[off+3] = 0xff
			} else {
				data[off+0] = 0
				data[off+1] = 0
				data[off+2] = 0
				data[off+3] = 0
			}
		}
	}
}

func subPosition(frame int) (int, int) {
	radius := float64((mainW-subW)/2 - 20)
	t := float64(frame) * 0.04
	px := int(float64(mainW)*0.5 - float64(subW)*0.5 + radius*math.Cos(t))
	py := int(float64(mainH)*0.5 - float64(subH)*0.5 + radius*math.Sin(t))
	return px, py
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
