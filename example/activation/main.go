//go:build linux

// A two-window xdg-activation focus transfer demo.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/protocol/staging/xdgactivation"
	"github.com/xogas/wayland/wire"
)

const (
	winW   int32  = 300
	winH   int32  = 200
	keyTab uint32 = 15
)

var colorA = [4]byte{0xFF, 0x00, 0x00, 0xFF}
var colorB = [4]byte{0x00, 0x00, 0xFF, 0xFF}

type win struct {
	surface    *wayland.Surface
	xdgSurface *xdgshell.Surface
	toplevel   *xdgshell.Toplevel
	cfgSerial  uint32
}

func fillBuf(data []byte, c [4]byte) {
	for i := 0; i < len(data); i += 4 {
		data[i+0] = c[0]
		data[i+1] = c[1]
		data[i+2] = c[2]
		data[i+3] = c[3]
	}
}

func shmFile(size int64) (int, func(), error) {
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

func commitColor(surface *wayland.Surface, shm *wayland.Shm, w, h int32, c [4]byte) error {
	stride := w * 4
	bufSize := int64(h) * int64(stride)
	fd, closeFd, err := shmFile(bufSize)
	if err != nil {
		return err
	}
	defer closeFd()

	data, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	defer syscall.Munmap(data) //nolint: errcheck

	fillBuf(data, c)

	pool, err := shm.CreatePool(fd, int32(bufSize))
	if err != nil {
		return err
	}
	defer pool.Destroy() //nolint: errcheck

	buf, err := pool.CreateBuffer(0, w, h, stride, uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		return err
	}
	defer buf.Destroy() //nolint: errcheck

	_ = surface.Attach(wire.ObjectID(buf.Proxy().ID()), 0, 0)
	_ = surface.Damage(0, 0, w, h)
	_ = surface.Commit()
	return nil
}

func newWindow(comp *wayland.Compositor, wmBase *xdgshell.WmBase, title string) (*win, error) {
	s, err := comp.CreateSurface()
	if err != nil {
		return nil, err
	}
	xdgSurf, err := wmBase.GetXdgSurface(wire.ObjectID(s.Proxy().ID()))
	if err != nil {
		return nil, err
	}
	tl, err := xdgSurf.GetToplevel()
	if err != nil {
		return nil, err
	}
	_ = tl.SetTitle(title)
	_ = tl.SetAppID("activation-demo")
	_ = s.Commit()
	return &win{surface: s, xdgSurface: xdgSurf, toplevel: tl}, nil
}

func (w *win) sid() wire.ObjectID {
	return wire.ObjectID(w.surface.Proxy().ID())
}

func requestActivation(activation *xdgactivation.ActivationV1, seat *wayland.Seat, serial uint32, focusSid wire.ObjectID, targetSid wire.ObjectID, mode string) {
	fmt.Printf("[%s] requesting token: serial=%d focus=%d target=%d\n", mode, serial, focusSid, targetSid)
	token, err := activation.GetActivationToken()
	if err != nil {
		fmt.Printf("[%s] get_activation_token: %v\n", mode, err)
		return
	}
	if serial != 0 {
		_ = token.SetSerial(serial, wire.ObjectID(seat.Proxy().ID()))
	}
	if focusSid != 0 {
		_ = token.SetSurface(focusSid)
	}
	token.OnDone(func(ev xdgactivation.ActivationTokenV1DoneEvent) {
		fmt.Printf("[%s] token done: token=%q\n", mode, ev.Token)
		if err := activation.Activate(ev.Token, targetSid); err != nil {
			fmt.Printf("[%s] activate error: %v\n", mode, err)
		} else {
			fmt.Printf("[%s] activate sent: token=%q surface=%d\n", mode, ev.Token, targetSid)
		}
		_ = token.Destroy()
	})
	_ = token.Commit()
	fmt.Printf("[%s] token committed\n", mode)
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
		fmt.Fprintf(os.Stderr, "protocol error: obj=%d code=%d msg=%q\n", pe.ObjectID, pe.Code, pe.Message)
	})

	reg, err := dpy.GetRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_registry: %v\n", err)
		os.Exit(1)
	}

	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})

	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}

	var compG, shmG, wmG, seatG, actG wayland.RegistryGlobalEvent
	for _, g := range globals {
		switch g.Interface {
		case wayland.InterfaceCompositor:
			compG = g
		case wayland.InterfaceShm:
			shmG = g
		case xdgshell.InterfaceWmBase:
			wmG = g
		case wayland.InterfaceSeat:
			if seatG.Interface == "" {
				seatG = g
			}
		case xdgactivation.InterfaceActivationV1:
			actG = g
		}
	}
	if compG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_compositor global")
		os.Exit(1)
	}
	if shmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_shm global")
		os.Exit(1)
	}
	if wmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no xdg_wm_base global")
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
	seat, err := wayland.BindSeat(reg, seatG.Name, seatG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind seat: %v\n", err)
		os.Exit(1)
	}

	var activation *xdgactivation.ActivationV1
	if actG.Interface != "" {
		activation, err = xdgactivation.BindActivationV1(reg, actG.Name, actG.Version)
		if err != nil {
			fmt.Printf("bind xdg_activation_v1: %v\n", err)
		} else {
			fmt.Println("xdg_activation_v1 bound")
		}
	} else {
		fmt.Println("no xdg_activation_v1 global, activation disabled")
	}

	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) { _ = wmBase.Pong(ev.Serial) })

	var caps uint32
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}

	var kbd *wayland.Keyboard
	if caps&uint32(wayland.SeatCapabilityKeyboard) != 0 {
		kbd, err = seat.GetKeyboard()
		if err != nil {
			fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "seat has no keyboard capability")
		os.Exit(1)
	}

	winA, err := newWindow(comp, wmBase, "Window A")
	if err != nil {
		fmt.Fprintf(os.Stderr, "window A: %v\n", err)
		os.Exit(1)
	}
	winB, err := newWindow(comp, wmBase, "Window B")
	if err != nil {
		fmt.Fprintf(os.Stderr, "window B: %v\n", err)
		os.Exit(1)
	}

	doneA := make(chan struct{}, 1)
	doneB := make(chan struct{}, 1)
	winA.toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		select {
		case doneA <- struct{}{}:
		default:
		}
	})
	winB.toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		select {
		case doneB <- struct{}{}:
		default:
		}
	})

	winA.toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})
	winB.toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})

	winA.xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		if winA.cfgSerial == 0 {
			winA.cfgSerial = ev.Serial
		}
	})
	winB.xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		if winB.cfgSerial == 0 {
			winB.cfgSerial = ev.Serial
		}
	})

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for winA.cfgSerial == 0 || winB.cfgSerial == 0 {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure")
				os.Exit(1)
			}
			break
		}
	}
	if winA.cfgSerial == 0 || winB.cfgSerial == 0 {
		fmt.Fprintln(os.Stderr, "no configure event received")
		os.Exit(1)
	}
	_ = winA.xdgSurface.AckConfigure(winA.cfgSerial)
	_ = winB.xdgSurface.AckConfigure(winB.cfgSerial)

	if err := commitColor(winA.surface, shm, winW, winH, colorA); err != nil {
		fmt.Fprintf(os.Stderr, "commit A: %v\n", err)
		os.Exit(1)
	}
	if err := commitColor(winB.surface, shm, winW, winH, colorB); err != nil {
		fmt.Fprintf(os.Stderr, "commit B: %v\n", err)
		os.Exit(1)
	}

	var (
		focusSid   wire.ObjectID
		lastSerial uint32
		hadKbd     int32
	)
	sidA := winA.sid()
	sidB := winB.sid()

	kbd.OnKeymap(func(ev wayland.KeyboardKeymapEvent) { _ = syscall.Close(ev.Fd) })
	kbd.OnEnter(func(ev wayland.KeyboardEnterEvent) {
		focusSid = ev.Surface
		wn := "?"
		switch focusSid {
		case sidA:
			wn = "A"
		case sidB:
			wn = "B"
		}
		fmt.Printf("keyboard enter: window %s (serial=%d surface=%d)\n", wn, ev.Serial, ev.Surface)
	})
	kbd.OnLeave(func(ev wayland.KeyboardLeaveEvent) {
		fmt.Printf("keyboard leave: surface=%d (serial=%d)\n", ev.Surface, ev.Serial)
		focusSid = 0
	})
	kbd.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.State == 1 {
			atomic.StoreInt32(&hadKbd, 1)
			lastSerial = ev.Serial
		}
		if ev.State != 1 || ev.Key != keyTab || focusSid == 0 {
			return
		}
		if activation == nil {
			fmt.Printf("tab pressed (focus=%d) but no xdg_activation_v1 bound\n", focusSid)
			return
		}
		var target wire.ObjectID
		switch focusSid {
		case sidA:
			target = sidB
		case sidB:
			target = sidA
		default:
			return
		}
		fmt.Printf("tab pressed, focus=%d -> target=%d (serial=%d)\n", focusSid, target, lastSerial)
		requestActivation(activation, seat, lastSerial, focusSid, target, "tab")
	})

	if activation != nil {
		go func() {
			<-time.After(3 * time.Second)
			if atomic.LoadInt32(&hadKbd) == 0 {
				fmt.Println("auto: 3s elapsed with no keyboard input")
				requestActivation(activation, seat, 0, 0, sidB, "auto")
			}
		}()
	}

	fmt.Println("activation demo: Window A (red 300x200) and Window B (blue 300x200)")
	fmt.Println("press Tab to request xdg-activation focus transfer between windows")

	for {
		select {
		case <-doneA:
			fmt.Println("window A closed")
			return
		case <-doneB:
			fmt.Println("window B closed")
			return
		case <-ctx.Done():
			fmt.Println("timeout reached")
			return
		default:
		}
		if err := dpy.Dispatch(ctx); err != nil {
			if ctx.Err() != nil {
				fmt.Println("timeout reached")
				return
			}
			fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			break
		}
	}
}
