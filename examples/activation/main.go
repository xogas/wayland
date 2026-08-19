//go:build linux

// A two-window xdg-activation demo: pressing Tab requests a focus transfer
// between the red and blue windows; if the keyboard is untouched for 3
// seconds, window B is activated automatically.
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

	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		return err
	}

	// Optional xdg_activation_v1: Tab transfers only work when present.
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

	// The example needs a keyboard.
	var caps wayland.SeatCapability
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		return err
	}
	if caps&wayland.SeatCapabilityKeyboard == 0 {
		return fmt.Errorf("seat has no keyboard capability")
	}
	kbd, err := seat.GetKeyboard()
	if err != nil {
		return fmt.Errorf("get keyboard: %w", err)
	}
	kbd.OnKeymap(func(ev wayland.KeyboardKeymapEvent) { _ = syscall.Close(ev.Fd) })

	// Two windows, one red and one blue.
	winA, err := shared.NewToplevel(ctx, dpy, core, "Window A", "activation-demo", winW, winH, nil)
	if err != nil {
		return fmt.Errorf("window A: %w", err)
	}
	winB, err := shared.NewToplevel(ctx, dpy, core, "Window B", "activation-demo", winW, winH, nil)
	if err != nil {
		return fmt.Errorf("window B: %w", err)
	}
	if err := commitColor(winA, core, colorA); err != nil {
		return fmt.Errorf("commit A: %w", err)
	}
	if err := commitColor(winB, core, colorB); err != nil {
		return fmt.Errorf("commit B: %w", err)
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
		hadKbd     atomic.Int32
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
			hadKbd.Store(1)
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

	// If the keyboard stays untouched for 3 seconds, activate window B.
	if activation != nil {
		go func() {
			<-time.After(3 * time.Second)
			if hadKbd.Load() == 0 {
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
			return nil
		case <-doneB:
			fmt.Println("window B closed")
			return nil
		case <-ctx.Done():
			fmt.Println("timeout reached")
			return nil
		case err := <-errCh:
			return err
		}
	}
}
