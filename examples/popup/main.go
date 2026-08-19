//go:build linux

// An xdg_popup + xdg_positioner right-click context menu demonstration.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const (
	btnRight = 273
	btnLeft  = 272
)

type popupState struct {
	active       bool
	grab         bool
	surface      *wayland.Surface
	surfaceID    wire.ObjectID
	xdgSurface   *xdgshell.Surface
	popupObj     *xdgshell.Popup
	haveXdgCfg   bool
	havePopupCfg bool
	rendered     bool
	cleanup      func()
}

func (ps *popupState) reset() {
	*ps = popupState{}
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
		fmt.Fprintf(os.Stderr, "protocol error: obj=%d code=%d msg=%s\n", pe.ObjectID, pe.Code, pe.Message)
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

	var caps wayland.SeatCapability
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip caps: %v\n", err)
		os.Exit(1)
	}
	if caps&wayland.SeatCapabilityPointer == 0 {
		fmt.Fprintln(os.Stderr, "seat has no pointer capability")
		os.Exit(1)
	}

	const (
		winW = 400
		winH = 300
	)

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Popup Demo", "wayland-popup-demo", winW, winH, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	winID, winData, winCleanup, err := shared.NewBuffer(core.Shm, winW, winH, wayland.ShmFormatXrgb8888)
	if err != nil {
		fmt.Fprintf(os.Stderr, "window buffer: %v\n", err)
		os.Exit(1)
	}
	defer winCleanup()
	shared.FillSolid(winData, 0x80, 0x80, 0x80)
	shared.DrawText(winData, int(winW)*4, winW, winH, "Right click for menu", 80, 130, 1, 0x000000)
	_ = toplevel.Surface.Attach(winID, 0, 0)
	_ = toplevel.Surface.Damage(0, 0, winW, winH)
	_ = toplevel.Surface.Commit()

	pointer, err := seat.GetPointer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_pointer: %v\n", err)
		os.Exit(1)
	}

	var cursorX, cursorY int32
	var ptrOnPopup bool
	var popupCursorY int32
	var ps popupState

	var rightClickPending bool
	var rightClickSerial uint32
	var rightClickX, rightClickY int32

	var popupClickPending bool
	var popupClickItemY int32

	pointer.OnEnter(func(ev wayland.PointerEnterEvent) {
		cursorX = ev.SurfaceX.Int()
		cursorY = ev.SurfaceY.Int()
		if ps.active && ev.Surface == ps.surfaceID {
			ptrOnPopup = true
			popupCursorY = cursorY
		}
	})
	pointer.OnLeave(func(ev wayland.PointerLeaveEvent) {
		if ev.Surface == ps.surfaceID {
			ptrOnPopup = false
		}
	})
	pointer.OnMotion(func(ev wayland.PointerMotionEvent) {
		cursorX = ev.SurfaceX.Int()
		cursorY = ev.SurfaceY.Int()
		if ps.active && ptrOnPopup {
			popupCursorY = cursorY
		}
	})
	pointer.OnButton(func(ev wayland.PointerButtonEvent) {
		if ev.Button == btnRight && ev.State == wayland.PointerButtonStatePressed && !ps.active {
			rightClickPending = true
			rightClickSerial = ev.Serial
			rightClickX = cursorX
			rightClickY = cursorY
		}
		if ev.Button == btnLeft && ev.State == wayland.PointerButtonStatePressed && ps.active && ptrOnPopup {
			popupClickPending = true
			popupClickItemY = popupCursorY
		}
	})

	autoPopupTimer := time.After(2 * time.Second)
	var autoPopupCreated bool
	var autoDestroyCh <-chan time.Time

	fmt.Println("popup demo: main window 400x300, right-click for context menu, auto-popup in 2s")

	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-toplevel.Closed:
			fmt.Println("window closed by compositor")
			return
		case <-ctx.Done():
			fmt.Println("timeout reached")
			return
		case <-autoPopupTimer:
			if !autoPopupCreated && !ps.active {
				fmt.Println("auto-popup: creating at (100, 100)")
				createPopup(core, seat, toplevel.XdgSurface, &ps, 100, 100, 0, false)
				autoPopupCreated = true
				autoDestroyCh = time.After(3 * time.Second)
			}
		case <-autoDestroyCh:
			if autoPopupCreated && ps.active && !ps.grab {
				fmt.Println("auto-popup: timed destroy")
				destroyPopup(&ps)
			}
			autoDestroyCh = nil
		case <-ticker.C:
		}

		if rightClickPending {
			rightClickPending = false
			if !ps.active {
				fmt.Printf("right-click popup: creating at (%d, %d)\n", rightClickX, rightClickY)
				createPopup(core, seat, toplevel.XdgSurface, &ps, rightClickX, rightClickY, rightClickSerial, true)
			}
		}

		if popupClickPending {
			popupClickPending = false
			item := popupClickItemY / 33
			if item < 0 {
				item = 0
			}
			if item > 2 {
				item = 2
			}
			fmt.Printf("popup: item %d selected\n", item+1)
			destroyPopup(&ps)
		}

		if ps.active && ps.haveXdgCfg && ps.havePopupCfg && !ps.rendered {
			renderPopup(core.Shm, &ps)
		}

		if err := dpy.DispatchPending(); err != nil {
			if ctx.Err() != nil {
				fmt.Println("timeout reached")
				return
			}
			fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			return
		}
	}
}

func createPopup(core *shared.Core, seat *wayland.Seat, parentXdg *xdgshell.Surface,
	ps *popupState, x, y int32, serial uint32, grab bool) {

	positioner, err := core.WmBase.CreatePositioner()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_positioner: %v\n", err)
		return
	}

	_ = positioner.SetSize(160, 100)
	_ = positioner.SetAnchorRect(x, y, 1, 1)
	_ = positioner.SetAnchor(xdgshell.PositionerAnchorBottomRight)
	_ = positioner.SetGravity(xdgshell.PositionerGravityBottomRight)
	_ = positioner.SetConstraintAdjustment(xdgshell.PositionerConstraintAdjustmentSlideX | xdgshell.PositionerConstraintAdjustmentSlideY)

	popupSurface, err := core.Compositor.CreateSurface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create popup surface: %v\n", err)
		_ = positioner.Destroy()
		return
	}

	popupXdgSurface, err := core.WmBase.GetXdgSurface(wire.ObjectID(popupSurface.Proxy().ID()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_xdg_surface popup: %v\n", err)
		_ = popupSurface.Destroy()
		_ = positioner.Destroy()
		return
	}

	ps.surface = popupSurface
	ps.surfaceID = wire.ObjectID(popupSurface.Proxy().ID())
	ps.xdgSurface = popupXdgSurface
	ps.active = true
	ps.grab = grab
	ps.haveXdgCfg = false
	ps.havePopupCfg = false
	ps.rendered = false

	popupXdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		ps.haveXdgCfg = true
		// Ack immediately so the popup commit below is protocol-safe.
		_ = popupXdgSurface.AckConfigure(ev.Serial)
	})

	popupObj, err := popupXdgSurface.GetPopup(wire.ObjectID(parentXdg.Proxy().ID()), wire.ObjectID(positioner.Proxy().ID()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_popup: %v\n", err)
		destroyPopup(ps)
		_ = positioner.Destroy()
		return
	}
	ps.popupObj = popupObj

	popupObj.OnConfigure(func(ev xdgshell.PopupConfigureEvent) {
		ps.havePopupCfg = true
	})
	popupObj.OnPopupDone(func(ev xdgshell.PopupPopupDoneEvent) {
		fmt.Println("popup_done received")
		destroyPopup(ps)
	})

	_ = positioner.Destroy()

	if grab {
		_ = popupObj.Grab(wire.ObjectID(seat.Proxy().ID()), serial)
	}

	_ = popupSurface.Commit()
}

func renderPopup(shm *wayland.Shm, ps *popupState) {
	const (
		pw = 160
		ph = 100
	)

	bufID, data, cleanup, err := shared.NewBuffer(shm, pw, ph, wayland.ShmFormatXrgb8888)
	if err != nil {
		fmt.Fprintf(os.Stderr, "popup buffer: %v\n", err)
		return
	}
	ps.cleanup = cleanup

	shared.FillSolid(data, 0xF0, 0xF0, 0xF0)
	colors := [][3]byte{
		{0xC0, 0x40, 0x40},
		{0x40, 0xA0, 0x40},
		{0x40, 0x40, 0xC0},
	}
	itemH := ph / 3
	stride := pw * 4
	for i := 0; i < 3; i++ {
		top := i * itemH
		shared.FillRect(data, stride, pw, ph, 0, top, pw, itemH, colors[i][0], colors[i][1], colors[i][2])
		if i < 2 {
			shared.FillRect(data, stride, pw, ph, 0, top+itemH-1, pw, 1, 0, 0, 0)
		}
	}

	labels := []string{"Item 1", "Item 2", "Item 3"}
	for i, label := range labels {
		labelW := shared.TextWidth(label, 1)
		tx := (pw - labelW) / 2
		ty := i*itemH + (itemH-shared.TextHeight(1))/2
		shared.DrawText(data, stride, pw, ph, label, tx, ty, 1, 0x000000)
	}

	_ = ps.surface.Attach(bufID, 0, 0)
	_ = ps.surface.Damage(0, 0, pw, ph)
	_ = ps.surface.Commit()
	ps.rendered = true
}

func destroyPopup(ps *popupState) {
	if !ps.active {
		return
	}
	ps.active = false
	if ps.popupObj != nil {
		_ = ps.popupObj.Destroy()
	}
	if ps.xdgSurface != nil {
		_ = ps.xdgSurface.Destroy()
	}
	if ps.surface != nil {
		_ = ps.surface.Destroy()
	}
	if ps.cleanup != nil {
		ps.cleanup()
	}
	ps.reset()
}
