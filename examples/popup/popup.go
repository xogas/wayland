package main

import (
	"fmt"
	"os"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

// popupState tracks the lifetime of the currently open popup, if any.
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

// createPopup builds a 160x100 popup anchored to (x, y) and commits it. The
// buffer is attached only after both configure events arrive (renderPopup).
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
		fmt.Fprintf(os.Stderr, "get xdg_surface popup: %v\n", err)
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

	// Ack immediately so the popup commit below is protocol-safe.
	popupXdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		ps.haveXdgCfg = true
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

// renderPopup draws the three menu items into a fresh buffer and commits.
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
	for i := range 3 {
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

// destroyPopup tears the popup down and releases its resources.
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
