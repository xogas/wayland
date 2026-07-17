//go:build linux

// Interactive window management demo: pointer move/resize, keyboard state toggles, configure-driven buffer resizing.
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/protocol/unstable/xdgdecorationunstable"
	"github.com/xogas/wayland/wire"
)

func parseStates(data []byte) []xdgshell.ToplevelState {
	if len(data) < 4 {
		return nil
	}
	n := len(data) / 4
	out := make([]xdgshell.ToplevelState, 0, n)
	for i := 0; i < n; i++ {
		v := binary.LittleEndian.Uint32(data[i*4 : (i+1)*4])
		out = append(out, xdgshell.ToplevelState(v))
	}
	return out
}

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

func fillRect(data []byte, stride int, rx, ry, rw, rh int, b, g, r, a byte) {
	for y := ry; y < ry+rh; y++ {
		off := y*stride + rx*4
		for x := 0; x < rw; x++ {
			o := off + x*4
			data[o+0] = b
			data[o+1] = g
			data[o+2] = r
			data[o+3] = a
		}
	}
}

func redraw(surface *wayland.Surface, shm *wayland.Shm, w, h int32, states []xdgshell.ToplevelState) error {
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

	fillRect(data, int(stride), 0, 0, int(w), int(h), 0xCC, 0xCC, 0xDD, 0xFF)
	const border = 4
	borderColor := [4]byte{0x88, 0x88, 0x99, 0xFF}
	if hasState(states, xdgshell.ToplevelStateActivated) {
		borderColor = [4]byte{0x44, 0x88, 0xCC, 0xFF}
	}
	if hasState(states, xdgshell.ToplevelStateResizing) {
		borderColor = [4]byte{0xCC, 0x88, 0x44, 0xFF}
	}
	fillRect(data, int(stride), 0, 0, int(w), border, borderColor[0], borderColor[1], borderColor[2], borderColor[3])
	fillRect(data, int(stride), 0, int(h)-border, int(w), border, borderColor[0], borderColor[1], borderColor[2], borderColor[3])
	fillRect(data, int(stride), 0, 0, border, int(h), borderColor[0], borderColor[1], borderColor[2], borderColor[3])
	fillRect(data, int(stride), int(w)-border, 0, border, int(h), borderColor[0], borderColor[1], borderColor[2], borderColor[3])

	const scale = 3
	textSize := fmt.Sprintf("%dx%d", w, h)
	textW := textWidth(textSize, scale)
	textH := textHeight(scale)
	centerX := (int(w) - textW) / 2
	centerY := (int(h) - textH) / 2
	drawText(data, int(stride), int(w), int(h), textSize, centerX, centerY, scale, 0x000000)

	stY := centerY + textH + 2*scale
	lineH := textH + scale
	for i, s := range states {
		label := stateName(s)
		if label == "" {
			continue
		}
		lw := textWidth(label, scale)
		lx := (int(w) - lw) / 2
		ly := stY + i*lineH
		drawText(data, int(stride), int(w), int(h), label, lx, ly, scale, 0x000000)
	}

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

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	var compG, shmG, wmG, seatG, decoG wayland.RegistryGlobalEvent
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
		case xdgdecorationunstable.InterfaceDecorationManagerV1:
			decoG = g
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

	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) { _ = wmBase.Pong(ev.Serial) })

	surface, err := comp.CreateSurface()
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

	winW := int32(400)
	winH := int32(300)
	var currentStates []xdgshell.ToplevelState
	var cfgSerial uint32
	cfgAcked := false

	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {
		prevStates := currentStates
		currentStates = parseStates(ev.States)
		added, removed := diffStates(prevStates, currentStates)
		for _, s := range removed {
			fmt.Printf("  -%s\n", stateName(s))
		}
		for _, s := range added {
			fmt.Printf("  +%s\n", stateName(s))
		}
		if ev.Width > 0 && ev.Height > 0 {
			if ev.Width != winW || ev.Height != winH {
				fmt.Printf("configure: %dx%d -> %dx%d\n", winW, winH, ev.Width, ev.Height)
				winW = ev.Width
				winH = ev.Height
			}
		}
		if cfgSerial != 0 {
			_ = xdgSurface.AckConfigure(cfgSerial)
			cfgSerial = 0
			if err := redraw(surface, shm, winW, winH, currentStates); err != nil {
				fmt.Fprintf(os.Stderr, "redraw: %v\n", err)
			}
		}
	})

	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})

	shutdown := make(chan struct{})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) { close(shutdown) })

	_ = toplevel.SetTitle("resizor")
	_ = toplevel.SetAppID("go-wayland-resizor")
	_ = toplevel.SetMinSize(200, 150)
	_ = toplevel.SetMaxSize(1280, 1024)

	if decoG.Interface != "" {
		decoMan, err := xdgdecorationunstable.BindDecorationManagerV1(reg, decoG.Name, decoG.Version)
		if err == nil {
			td, err := decoMan.GetToplevelDecoration(wire.ObjectID(toplevel.Proxy().ID()))
			if err == nil {
				_ = td.SetMode(uint32(xdgdecorationunstable.ToplevelDecorationV1ModeServerSide))
				fmt.Println("requested server-side decoration")
			}
		}
	} else {
		fmt.Println("zxdg_decoration_manager_v1 not available, using client-side decoration")
	}

	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for !cfgAcked {
		if cfgSerial != 0 {
			_ = xdgSurface.AckConfigure(cfgSerial)
			cfgSerial = 0
			cfgAcked = true
		} else {
			if err := dpy.Dispatch(waitCtx); err != nil {
				if waitCtx.Err() != nil {
					fmt.Fprintln(os.Stderr, "timeout waiting for configure")
					os.Exit(1)
				}
				break
			}
		}
	}
	if !cfgAcked {
		fmt.Fprintln(os.Stderr, "no configure event received")
		os.Exit(1)
	}

	if err := redraw(surface, shm, winW, winH, currentStates); err != nil {
		fmt.Fprintf(os.Stderr, "initial redraw: %v\n", err)
		os.Exit(1)
	}

	var seat *wayland.Seat
	var kb *wayland.Keyboard
	var ptr *wayland.Pointer
	var ptrX, ptrY int32

	if seatG.Interface != "" {
		seat, err = wayland.BindSeat(reg, seatG.Name, seatG.Version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind seat: %v\n", err)
		} else {
			seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
				if ev.Capabilities&uint32(wayland.SeatCapabilityKeyboard) != 0 && kb == nil {
					k, err := seat.GetKeyboard()
					if err == nil {
						kb = k
						k.OnKey(func(ev wayland.KeyboardKeyEvent) {
							if ev.State != 1 {
								return
							}
							switch ev.Key {
							case 16:
								close(shutdown)
							case 50:
								if hasState(currentStates, xdgshell.ToplevelStateMaximized) {
									_ = toplevel.UnsetMaximized()
								} else {
									_ = toplevel.SetMaximized()
								}
							case 33:
								if hasState(currentStates, xdgshell.ToplevelStateFullscreen) {
									_ = toplevel.UnsetFullscreen()
								} else {
									_ = toplevel.SetFullscreen(0)
								}
							case 49:
								_ = toplevel.SetMinimized()
							case 103:
								winH += 30
								if winH > 1280 {
									winH = 1280
								}
								_ = xdgSurface.SetWindowGeometry(0, 0, winW, winH)
								if err := redraw(surface, shm, winW, winH, currentStates); err != nil {
									fmt.Fprintf(os.Stderr, "redraw up: %v\n", err)
								}
							case 108:
								winH -= 30
								if winH < 150 {
									winH = 150
								}
								_ = xdgSurface.SetWindowGeometry(0, 0, winW, winH)
								if err := redraw(surface, shm, winW, winH, currentStates); err != nil {
									fmt.Fprintf(os.Stderr, "redraw down: %v\n", err)
								}
							}
						})
					}
				}
				if ev.Capabilities&uint32(wayland.SeatCapabilityPointer) != 0 && ptr == nil {
					p, err := seat.GetPointer()
					if err == nil {
						ptr = p
						p.OnMotion(func(ev wayland.PointerMotionEvent) {
							ptrX = int32(ev.SurfaceX.Float64())
							ptrY = int32(ev.SurfaceY.Float64())
						})
						p.OnButton(func(ev wayland.PointerButtonEvent) {
							if ev.Button != 272 || ev.State != 1 {
								return
							}
							edge := edgeFromCoords(ptrX, ptrY, winW, winH)
							seatID := wire.ObjectID(seat.Proxy().ID())
							if edge == xdgshell.ToplevelResizeEdgeNone {
								_ = toplevel.Move(seatID, ev.Serial)
							} else {
								_ = toplevel.Resize(seatID, ev.Serial, uint32(edge))
							}
						})
					}
				}
			})
		}
	}

	if seat != nil {
		if err := dpy.Roundtrip(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "seat roundtrip: %v\n", err)
		}
	}

	fmt.Printf("resizor: %dx%d, keys: m=Max f=Full n=Min Up/Dn=Resize q=Quit, mouse: drag=Move edge=Resize\n", winW, winH)
	fmt.Printf("initial states: ")
	for _, s := range currentStates {
		fmt.Printf("%s ", stateName(s))
	}
	fmt.Println()

	for {
		select {
		case <-shutdown:
			fmt.Println("closed by compositor.")
			return
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return
		default:
		}
		if err := dpy.Dispatch(ctx); err != nil {
			if ctx.Err() != nil {
				fmt.Println("timeout reached.")
				return
			}
			fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			break
		}
	}
}

func edgeFromCoords(x, y, w, h int32) xdgshell.ToplevelResizeEdge {
	const margin int32 = 20
	top := y < margin
	bottom := y >= h-margin
	left := x < margin
	right := x >= w-margin
	if top && left {
		return xdgshell.ToplevelResizeEdgeTopLeft
	}
	if top && right {
		return xdgshell.ToplevelResizeEdgeTopRight
	}
	if bottom && left {
		return xdgshell.ToplevelResizeEdgeBottomLeft
	}
	if bottom && right {
		return xdgshell.ToplevelResizeEdgeBottomRight
	}
	if top {
		return xdgshell.ToplevelResizeEdgeTop
	}
	if bottom {
		return xdgshell.ToplevelResizeEdgeBottom
	}
	if left {
		return xdgshell.ToplevelResizeEdgeLeft
	}
	if right {
		return xdgshell.ToplevelResizeEdgeRight
	}
	return xdgshell.ToplevelResizeEdgeNone
}
