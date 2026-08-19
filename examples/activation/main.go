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
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/protocol/staging/xdgactivation"
	"github.com/xogas/wayland/wire"
)

const (
	winW   = 300
	winH   = 200
	keyTab = 15
)

var colorA = [4]byte{0xFF, 0x00, 0x00, 0xFF} // BGR order, window A is blue
var colorB = [4]byte{0x00, 0x00, 0xFF, 0xFF} // BGR order, window B is red

func commitColor(t *shared.Toplevel, core *shared.Core, c [4]byte) error {
	bufID, data, cleanup, err := shared.NewBuffer(core.Shm, winW, winH, wayland.ShmFormatXrgb8888)
	if err != nil {
		return err
	}
	defer cleanup()
	shared.FillSolid(data, c[2], c[1], c[0])
	_ = t.Surface.Attach(bufID, 0, 0)
	_ = t.Surface.Damage(0, 0, winW, winH)
	_ = t.Surface.Commit()
	return nil
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

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: obj=%d code=%d msg=%q\n", pe.ObjectID, pe.Code, pe.Message)
	})

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	var activation *xdgactivation.ActivationV1
	if actG, ok := globals.Find(xdgactivation.InterfaceActivationV1); ok {
		activation, err = xdgactivation.BindActivationV1(reg, actG.Name, min(actG.Version, xdgactivation.VersionActivationV1))
		if err != nil {
			fmt.Printf("bind xdg_activation_v1: %v\n", err)
		} else {
			fmt.Println("xdg_activation_v1 bound")
		}
	} else {
		fmt.Println("no xdg_activation_v1 global, activation disabled")
	}

	var caps wayland.SeatCapability
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}
	if caps&wayland.SeatCapabilityKeyboard == 0 {
		fmt.Fprintln(os.Stderr, "seat has no keyboard capability")
		os.Exit(1)
	}
	kbd, err := seat.GetKeyboard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
		os.Exit(1)
	}
	kbd.OnKeymap(func(ev wayland.KeyboardKeymapEvent) { _ = syscall.Close(ev.Fd) })

	winA, err := shared.NewToplevel(ctx, dpy, core, "Window A", "activation-demo", winW, winH, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "window A: %v\n", err)
		os.Exit(1)
	}
	winB, err := shared.NewToplevel(ctx, dpy, core, "Window B", "activation-demo", winW, winH, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "window B: %v\n", err)
		os.Exit(1)
	}

	if err := commitColor(winA, core, colorA); err != nil {
		fmt.Fprintf(os.Stderr, "commit A: %v\n", err)
		os.Exit(1)
	}
	if err := commitColor(winB, core, colorB); err != nil {
		fmt.Fprintf(os.Stderr, "commit B: %v\n", err)
		os.Exit(1)
	}

	doneA := make(chan struct{}, 1)
	doneB := make(chan struct{}, 1)
	winA.Toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		select {
		case doneA <- struct{}{}:
		default:
		}
	})
	winB.Toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		select {
		case doneB <- struct{}{}:
		default:
		}
	})

	sidA := wire.ObjectID(winA.Surface.Proxy().ID())
	sidB := wire.ObjectID(winB.Surface.Proxy().ID())

	var (
		focusSid   wire.ObjectID
		lastSerial uint32
		hadKbd     int32
	)

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
		if ev.State == wayland.KeyboardKeyStatePressed {
			atomic.StoreInt32(&hadKbd, 1)
			lastSerial = ev.Serial
		}
		if ev.State != wayland.KeyboardKeyStatePressed || ev.Key != keyTab || focusSid == 0 {
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

	errCh := shared.DispatchLoop(ctx, dpy)
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
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			}
			return
		}
	}
}
