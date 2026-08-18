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
	"github.com/xogas/wayland/example/internal/shared"
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

func stateName(s xdgshell.ToplevelState) string {
	switch s {
	case xdgshell.ToplevelStateMaximized:
		return "maximized"
	case xdgshell.ToplevelStateFullscreen:
		return "fullscreen"
	case xdgshell.ToplevelStateResizing:
		return "resizing"
	case xdgshell.ToplevelStateActivated:
		return "activated"
	case xdgshell.ToplevelStateTiledLeft:
		return "tiled_left"
	case xdgshell.ToplevelStateTiledRight:
		return "tiled_right"
	case xdgshell.ToplevelStateTiledTop:
		return "tiled_top"
	case xdgshell.ToplevelStateTiledBottom:
		return "tiled_bottom"
	case xdgshell.ToplevelStateSuspended:
		return "suspended"
	case xdgshell.ToplevelStateConstrainedLeft:
		return "constrained_left"
	case xdgshell.ToplevelStateConstrainedRight:
		return "constrained_right"
	case xdgshell.ToplevelStateConstrainedTop:
		return "constrained_top"
	case xdgshell.ToplevelStateConstrainedBottom:
		return "constrained_bottom"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

func hasState(states []xdgshell.ToplevelState, target xdgshell.ToplevelState) bool {
	for _, s := range states {
		if s == target {
			return true
		}
	}
	return false
}

func diffStates(old, new []xdgshell.ToplevelState) (added, removed []xdgshell.ToplevelState) {
	oldSet := make(map[xdgshell.ToplevelState]bool)
	newSet := make(map[xdgshell.ToplevelState]bool)
	for _, s := range old {
		oldSet[s] = true
	}
	for _, s := range new {
		newSet[s] = true
	}
	for _, s := range new {
		if !oldSet[s] {
			added = append(added, s)
		}
	}
	for _, s := range old {
		if !newSet[s] {
			removed = append(removed, s)
		}
	}
	return
}

// redraw renders the background, a state-colored border, the size label and
// the current toplevel states into a fresh buffer and commits it.
func redraw(t *shared.Toplevel, core *shared.Core, w, h int32, states []xdgshell.ToplevelState) error {
	bufID, data, cleanup, err := shared.NewBuffer(core.Shm, w, h, wayland.ShmFormatXrgb8888)
	if err != nil {
		return err
	}
	defer cleanup()

	stride := int(w) * 4
	shared.FillRect(data, stride, int(w), int(h), 0, 0, int(w), int(h), 0xDD, 0xCC, 0xCC)
	const border = 4
	borderColor := [3]byte{0x99, 0x88, 0x88}
	if hasState(states, xdgshell.ToplevelStateActivated) {
		borderColor = [3]byte{0xCC, 0x88, 0x44}
	}
	if hasState(states, xdgshell.ToplevelStateResizing) {
		borderColor = [3]byte{0x44, 0x88, 0xCC}
	}
	shared.FillRect(data, stride, int(w), int(h), 0, 0, int(w), border, borderColor[0], borderColor[1], borderColor[2])
	shared.FillRect(data, stride, int(w), int(h), 0, int(h)-border, int(w), border, borderColor[0], borderColor[1], borderColor[2])
	shared.FillRect(data, stride, int(w), int(h), 0, 0, border, int(h), borderColor[0], borderColor[1], borderColor[2])
	shared.FillRect(data, stride, int(w), int(h), int(w)-border, 0, border, int(h), borderColor[0], borderColor[1], borderColor[2])

	const scale = 3
	textSize := fmt.Sprintf("%dx%d", w, h)
	textW := shared.TextWidth(textSize, scale)
	textH := shared.TextHeight(scale)
	centerX := (int(w) - textW) / 2
	centerY := (int(h) - textH) / 2
	shared.DrawText(data, stride, int(w), int(h), textSize, centerX, centerY, scale, 0x000000)

	stY := centerY + textH + 2*scale
	lineH := textH + scale
	for i, s := range states {
		label := stateName(s)
		lw := shared.TextWidth(label, scale)
		lx := (int(w) - lw) / 2
		ly := stY + i*lineH
		shared.DrawText(data, stride, int(w), int(h), label, lx, ly, scale, 0x000000)
	}

	_ = t.Surface.Attach(bufID, 0, 0)
	_ = t.Surface.Damage(0, 0, w, h)
	_ = t.Surface.Commit()
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	quit := make(chan struct{}, 1)
	winW := int32(400)
	winH := int32(300)
	var currentStates []xdgshell.ToplevelState

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "resizor", "go-wayland-resizor", winW, winH,
		func(t *shared.Toplevel, w, h int32, states []xdgshell.ToplevelState) {
			prevStates := currentStates
			currentStates = states
			added, removed := diffStates(prevStates, currentStates)
			for _, s := range removed {
				fmt.Printf("  -%s\n", stateName(s))
			}
			for _, s := range added {
				fmt.Printf("  +%s\n", stateName(s))
			}
			if w != winW || h != winH {
				fmt.Printf("configure: %dx%d -> %dx%d\n", winW, winH, w, h)
				winW, winH = w, h
			}
			if err := redraw(t, core, winW, winH, currentStates); err != nil {
				fmt.Fprintf(os.Stderr, "redraw: %v\n", err)
			}
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Double-buffered state: applies at the next commit.
	_ = toplevel.Toplevel.SetMinSize(200, 150)
	_ = toplevel.Toplevel.SetMaxSize(1280, 1024)

	if decoG, ok := globals.Find(xdgdecorationunstable.InterfaceDecorationManagerV1); ok {
		decoMan, err := xdgdecorationunstable.BindDecorationManagerV1(reg, decoG.Name, min(decoG.Version, xdgdecorationunstable.VersionDecorationManagerV1))
		if err == nil {
			td, err := decoMan.GetToplevelDecoration(wire.ObjectID(toplevel.Toplevel.Proxy().ID()))
			if err == nil {
				_ = td.SetMode(xdgdecorationunstable.ToplevelDecorationV1ModeServerSide)
				fmt.Println("requested server-side decoration")
			}
		}
	} else {
		fmt.Println("zxdg_decoration_manager_v1 not available, using client-side decoration")
	}

	var kb *wayland.Keyboard
	var ptr *wayland.Pointer
	var ptrX, ptrY int32
	if seat, err := shared.BindSeat(reg, globals); err == nil {
		seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
			if ev.Capabilities&wayland.SeatCapabilityKeyboard != 0 && kb == nil {
				k, err := seat.GetKeyboard()
				if err == nil {
					kb = k
					k.OnKey(func(ev wayland.KeyboardKeyEvent) {
						if ev.State != wayland.KeyboardKeyStatePressed {
							return
						}
						switch ev.Key {
						case keyQ:
							select {
							case quit <- struct{}{}:
							default:
							}
						case keyM:
							if hasState(currentStates, xdgshell.ToplevelStateMaximized) {
								_ = toplevel.Toplevel.UnsetMaximized()
							} else {
								_ = toplevel.Toplevel.SetMaximized()
							}
						case keyF:
							if hasState(currentStates, xdgshell.ToplevelStateFullscreen) {
								_ = toplevel.Toplevel.UnsetFullscreen()
							} else {
								_ = toplevel.Toplevel.SetFullscreen(0)
							}
						case keyN:
							_ = toplevel.Toplevel.SetMinimized()
						case keyUp:
							winH += 30
							if winH > 1280 {
								winH = 1280
							}
							_ = toplevel.XdgSurface.SetWindowGeometry(0, 0, winW, winH)
							if err := redraw(toplevel, core, winW, winH, currentStates); err != nil {
								fmt.Fprintf(os.Stderr, "redraw up: %v\n", err)
							}
						case keyDown:
							winH -= 30
							if winH < 150 {
								winH = 150
							}
							_ = toplevel.XdgSurface.SetWindowGeometry(0, 0, winW, winH)
							if err := redraw(toplevel, core, winW, winH, currentStates); err != nil {
								fmt.Fprintf(os.Stderr, "redraw down: %v\n", err)
							}
						}
					})
				}
			}
			if ev.Capabilities&wayland.SeatCapabilityPointer != 0 && ptr == nil {
				p, err := seat.GetPointer()
				if err == nil {
					ptr = p
					p.OnMotion(func(ev wayland.PointerMotionEvent) {
						ptrX = ev.SurfaceX.Int()
						ptrY = ev.SurfaceY.Int()
					})
					p.OnButton(func(ev wayland.PointerButtonEvent) {
						if ev.Button != btnLeft || ev.State != wayland.PointerButtonStatePressed {
							return
						}
						edge := edgeFromCoords(ptrX, ptrY, winW, winH)
						seatID := wire.ObjectID(seat.Proxy().ID())
						if edge == xdgshell.ToplevelResizeEdgeNone {
							_ = toplevel.Toplevel.Move(seatID, ev.Serial)
						} else {
							_ = toplevel.Toplevel.Resize(seatID, ev.Serial, edge)
						}
					})
				}
			}
		})
		if err := dpy.Roundtrip(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "seat roundtrip: %v\n", err)
		}
	} else {
		fmt.Println("no wl_seat global: keyboard and pointer interaction disabled")
	}

	fmt.Printf("resizor: %dx%d, keys: m=Max f=Full n=Min Up/Dn=Resize q=Quit, mouse: drag=Move edge=Resize\n", winW, winH)
	fmt.Printf("initial states: ")
	for _, s := range currentStates {
		fmt.Printf("%s ", stateName(s))
	}
	fmt.Println()

	// Print happens before the dispatch goroutine starts, so the snapshot of
	// winW/winH/currentStates above is race-free.
	errCh := shared.DispatchLoop(ctx, dpy)
	for {
		select {
		case <-toplevel.Closed:
			fmt.Println("closed by compositor.")
			return
		case <-quit:
			fmt.Println("quit.")
			return
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			}
			return
		}
	}
}

func edgeFromCoords(x, y, w, h int32) xdgshell.ToplevelResizeEdge {
	const margin int32 = 20
	if x < margin && y < margin {
		return xdgshell.ToplevelResizeEdgeTopLeft
	}
	if x >= w-margin && y < margin {
		return xdgshell.ToplevelResizeEdgeTopRight
	}
	if x < margin && y >= h-margin {
		return xdgshell.ToplevelResizeEdgeBottomLeft
	}
	if x >= w-margin && y >= h-margin {
		return xdgshell.ToplevelResizeEdgeBottomRight
	}
	if y < margin {
		return xdgshell.ToplevelResizeEdgeTop
	}
	if y >= h-margin {
		return xdgshell.ToplevelResizeEdgeBottom
	}
	if x < margin {
		return xdgshell.ToplevelResizeEdgeLeft
	}
	if x >= w-margin {
		return xdgshell.ToplevelResizeEdgeRight
	}
	return xdgshell.ToplevelResizeEdgeNone
}
