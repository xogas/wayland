//go:build linux

// Interactive window management demo: pointer move/resize, keyboard state
// toggles, configure-driven buffer resizing, optional server-side decoration.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/protocol/unstable/xdgdecorationunstable"
	"github.com/xogas/wayland/wire"
)

const (
	keyQ    = 16
	keyM    = 50
	keyF    = 33
	keyN    = 49
	keyUp   = 103
	keyDown = 108

	btnLeft = 272
)

// app carries the window state shared by the input handlers and the loop.
type app struct {
	core     *shared.Core
	toplevel *shared.Toplevel
	quit     chan struct{}

	winW   int32
	winH   int32
	states []xdgshell.ToplevelState
	ptrX   int32
	ptrY   int32
}

// onConfigure handles xdg_toplevel.configure: prints state changes, resizes
// the window and redraws.
func (a *app) onConfigure(w, h int32, states []xdgshell.ToplevelState) {
	added, removed := diffStates(a.states, states)
	a.states = states
	for _, s := range removed {
		fmt.Printf("  -%s\n", stateName(s))
	}
	for _, s := range added {
		fmt.Printf("  +%s\n", stateName(s))
	}
	if w != a.winW || h != a.winH {
		fmt.Printf("configure: %dx%d -> %dx%d\n", a.winW, a.winH, w, h)
		a.winW, a.winH = w, h
	}
	if err := a.redraw(); err != nil {
		fmt.Fprintf(os.Stderr, "redraw: %v\n", err)
	}
}

// redraw renders the window at its current size and state.
func (a *app) redraw() error {
	return redraw(a.toplevel, a.core, a.winW, a.winH, a.states)
}

// onKey handles one key press.
func (a *app) onKey(key uint32) {
	switch key {
	case keyQ:
		select {
		case a.quit <- struct{}{}:
		default:
		}
	case keyM:
		if hasState(a.states, xdgshell.ToplevelStateMaximized) {
			_ = a.toplevel.Toplevel.UnsetMaximized()
		} else {
			_ = a.toplevel.Toplevel.SetMaximized()
		}
	case keyF:
		if hasState(a.states, xdgshell.ToplevelStateFullscreen) {
			_ = a.toplevel.Toplevel.UnsetFullscreen()
		} else {
			_ = a.toplevel.Toplevel.SetFullscreen(0)
		}
	case keyN:
		_ = a.toplevel.Toplevel.SetMinimized()
	case keyUp:
		a.winH = min(a.winH+30, 1280)
		a.resize()
	case keyDown:
		a.winH = max(a.winH-30, 150)
		a.resize()
	}
}

// resize manually changes the window height: the geometry hint is a
// double-buffered state, so a redraw commit applies it.
func (a *app) resize() {
	_ = a.toplevel.XdgSurface.SetWindowGeometry(0, 0, a.winW, a.winH)
	if err := a.redraw(); err != nil {
		fmt.Fprintf(os.Stderr, "redraw: %v\n", err)
	}
}

// onButton starts a move or resize interaction on the left button.
func (a *app) onButton(seat *wayland.Seat, serial uint32) {
	edge := edgeFromCoords(a.ptrX, a.ptrY, a.winW, a.winH)
	seatID := wire.ObjectID(seat.Proxy().ID())
	if edge == xdgshell.ToplevelResizeEdgeNone {
		_ = a.toplevel.Toplevel.Move(seatID, serial)
	} else {
		_ = a.toplevel.Toplevel.Resize(seatID, serial, edge)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	ap := &app{
		core: core,
		quit: make(chan struct{}, 1),
		winW: 400,
		winH: 300,
		ptrX: -1,
		ptrY: -1,
	}
	ap.toplevel, err = shared.NewToplevel(ctx, dpy, core, "resizor", "go-wayland-resizor", ap.winW, ap.winH,
		func(t *shared.Toplevel, w, h int32, states []xdgshell.ToplevelState) {
			ap.onConfigure(w, h, states)
		})
	if err != nil {
		return err
	}

	// Manual resize bounds for the arrow keys.
	_ = ap.toplevel.Toplevel.SetMinSize(200, 150)
	_ = ap.toplevel.Toplevel.SetMaxSize(1280, 1024)

	// Optional server-side decoration.
	if decoG, ok := globals.Find(xdgdecorationunstable.InterfaceDecorationManagerV1); ok {
		decoMan, err := xdgdecorationunstable.BindDecorationManagerV1(reg, decoG.Name, min(decoG.Version, xdgdecorationunstable.VersionDecorationManagerV1))
		if err == nil {
			td, err := decoMan.GetToplevelDecoration(wire.ObjectID(ap.toplevel.Toplevel.Proxy().ID()))
			if err == nil {
				_ = td.SetMode(xdgdecorationunstable.ToplevelDecorationV1ModeServerSide)
				fmt.Println("requested server-side decoration")
			}
		}
	} else {
		fmt.Println("zxdg_decoration_manager_v1 not available, using client-side decoration")
	}

	// Bind the seat devices when their capabilities are advertised.
	var kb *wayland.Keyboard
	var ptr *wayland.Pointer
	if seat, err := shared.BindSeat(reg, globals); err == nil {
		seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
			if ev.Capabilities&wayland.SeatCapabilityKeyboard != 0 && kb == nil {
				if k, err := seat.GetKeyboard(); err == nil {
					kb = k
					k.OnKey(func(ev wayland.KeyboardKeyEvent) {
						if ev.State == wayland.KeyboardKeyStatePressed {
							ap.onKey(ev.Key)
						}
					})
				}
			}
			if ev.Capabilities&wayland.SeatCapabilityPointer != 0 && ptr == nil {
				if p, err := seat.GetPointer(); err == nil {
					ptr = p
					p.OnMotion(func(ev wayland.PointerMotionEvent) {
						ap.ptrX = ev.SurfaceX.Int()
						ap.ptrY = ev.SurfaceY.Int()
					})
					p.OnButton(func(ev wayland.PointerButtonEvent) {
						if ev.Button == btnLeft && ev.State == wayland.PointerButtonStatePressed {
							ap.onButton(seat, ev.Serial)
						}
					})
				}
			}
		})
		if err := dpy.Roundtrip(ctx); err != nil {
			return fmt.Errorf("seat roundtrip: %w", err)
		}
	} else {
		fmt.Println("no wl_seat global: keyboard and pointer interaction disabled")
	}

	fmt.Printf("resizor: %dx%d, keys: m=Max f=Full n=Min Up/Dn=Resize q=Quit, mouse: drag=Move edge=Resize\n", ap.winW, ap.winH)
	fmt.Printf("initial states: ")
	for _, s := range ap.states {
		fmt.Printf("%s ", stateName(s))
	}
	fmt.Println()

	errCh := shared.DispatchLoop(ctx, dpy)
	for {
		select {
		case <-ap.toplevel.Closed:
			fmt.Println("closed by compositor.")
			return nil
		case <-ap.quit:
			fmt.Println("quit.")
			return nil
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return nil
		case err := <-errCh:
			return err
		}
	}
}
