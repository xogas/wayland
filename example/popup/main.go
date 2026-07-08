//go:build linux

// An xdg_popup + xdg_positioner right-click context menu demonstration.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

func fillRect(data []byte, stride int, x, y, w, h int, r, g, b byte) {
	for row := y; row < y+h; row++ {
		off := row*stride + x*4
		for col := 0; col < w; col++ {
			o := off + col*4
			data[o+0] = b
			data[o+1] = g
			data[o+2] = r
			data[o+3] = 0xFF
		}
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

type popupState struct {
	active       bool
	grab         bool
	surface      *wayland.Surface
	surfaceID    wire.ObjectID
	xdgSurface   *xdgshell.Surface
	popupObj     *xdgshell.Popup
	xdgSerial    uint32
	popupCfg     xdgshell.PopupConfigureEvent
	haveXdgCfg   bool
	havePopupCfg bool
	rendered     bool
	pool         *wayland.ShmPool
	buf          *wayland.Buffer
	closeFd      func()
	munmap       func()
}

func (ps *popupState) reset() {
	ps.active = false
	ps.grab = false
	ps.surface = nil
	ps.xdgSurface = nil
	ps.popupObj = nil
	ps.xdgSerial = 0
	ps.haveXdgCfg = false
	ps.havePopupCfg = false
	ps.rendered = false
	ps.pool = nil
	ps.buf = nil
	ps.closeFd = nil
	ps.munmap = nil
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

	reg, err := dpy.GetRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_registry: %v\n", err)
		os.Exit(1)
	}
	var compG, shmG, wmG, seatG wayland.RegistryGlobalEvent
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
		case wayland.InterfaceSeat:
			seatG = g
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

	var caps uint32
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip caps: %v\n", err)
		os.Exit(1)
	}
	if caps&uint32(wayland.SeatCapabilityPointer) == 0 {
		fmt.Fprintln(os.Stderr, "seat has no pointer capability")
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

	const (
		winW = 400
		winH = 300
	)

	var cfgSerial uint32
	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})

	shutdown := make(chan struct{})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) { close(shutdown) })

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: obj=%d code=%d msg=%s\n", pe.ObjectID, pe.Code, pe.Message)
	})

	_ = toplevel.SetTitle("Popup Demo")
	_ = toplevel.SetAppID("wayland-popup-demo")
	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for cfgSerial == 0 {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure")
				os.Exit(1)
			}
			break
		}
	}
	if cfgSerial == 0 {
		fmt.Fprintln(os.Stderr, "no configure event received")
		os.Exit(1)
	}
	_ = xdgSurface.AckConfigure(cfgSerial)

	stride := winW * 4
	bufSize := int64(winH) * int64(stride)

	fd, closeFd, err := shmFile(bufSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shm: %v\n", err)
		os.Exit(1)
	}
	defer closeFd()

	data, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mmap: %v\n", err)
		os.Exit(1)
	}
	defer syscall.Munmap(data) //nolint: errcheck

	for i := 0; i < len(data); i += 4 {
		data[i+0] = 0x80
		data[i+1] = 0x80
		data[i+2] = 0x80
		data[i+3] = 0xFF
	}

	drawText(data, stride, winW, winH, "Right click for menu", 80, 130, 1, 0x000000)

	pool, err := shm.CreatePool(fd, int32(bufSize))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Destroy() //nolint: errcheck

	buf, err := pool.CreateBuffer(0, int32(winW), int32(winH), int32(stride), uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
		os.Exit(1)
	}
	defer buf.Destroy() //nolint: errcheck

	_ = surface.Attach(wire.ObjectID(buf.Proxy().ID()), 0, 0)
	_ = surface.Damage(0, 0, int32(winW), int32(winH))
	_ = surface.Commit()

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
		cursorX = int32(ev.SurfaceX) / 256
		cursorY = int32(ev.SurfaceY) / 256
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
		cursorX = int32(ev.SurfaceX) / 256
		cursorY = int32(ev.SurfaceY) / 256
		if ps.active && ptrOnPopup {
			popupCursorY = cursorY
		}
	})
	pointer.OnButton(func(ev wayland.PointerButtonEvent) {
		if ev.Button == 273 && ev.State == uint32(wayland.PointerButtonStatePressed) && !ps.active {
			rightClickPending = true
			rightClickSerial = ev.Serial
			rightClickX = cursorX
			rightClickY = cursorY
		}
		if ev.Button == 272 && ev.State == uint32(wayland.PointerButtonStatePressed) && ps.active && ptrOnPopup {
			popupClickPending = true
			popupClickItemY = popupCursorY
		}
	})

	autoPopupTimer := time.After(2 * time.Second)
	var autoPopupCreated bool
	var autoDestroyCh <-chan time.Time

	fmt.Println("popup demo: main window 400x300, right-click for context menu, auto-popup in 2s")

	for {
		select {
		case <-shutdown:
			fmt.Println("window closed by compositor")
			return
		case <-ctx.Done():
			fmt.Println("timeout reached")
			return
		case <-autoPopupTimer:
			if !autoPopupCreated && !ps.active {
				fmt.Println("auto-popup: creating at (100, 100)")
				createPopup(comp, wmBase, seat, xdgSurface, &ps, 100, 100, 0, false)
				autoPopupCreated = true
				autoDestroyCh = time.After(3 * time.Second)
			}
		case <-autoDestroyCh:
			if autoPopupCreated && ps.active && !ps.grab {
				fmt.Println("auto-popup: timed destroy")
				destroyPopup(&ps)
			}
			autoDestroyCh = nil
		default:
		}

		if rightClickPending {
			rightClickPending = false
			if !ps.active {
				fmt.Printf("right-click popup: creating at (%d, %d)\n", rightClickX, rightClickY)
				createPopup(comp, wmBase, seat, xdgSurface, &ps, rightClickX, rightClickY, rightClickSerial, true)
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
			renderPopup(shm, &ps)
		}

		dispatchCtx, dcancel := context.WithTimeout(ctx, 200*time.Millisecond)
		err := dpy.Dispatch(dispatchCtx)
		dcancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			if ctx.Err() != nil {
				fmt.Println("timeout reached")
				return
			}
			fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			break
		}
	}
}

func createPopup(comp *wayland.Compositor, wmBase *xdgshell.WmBase,
	seat *wayland.Seat, parentXdg *xdgshell.Surface,
	ps *popupState, x, y int32, serial uint32, grab bool) {

	positioner, err := wmBase.CreatePositioner()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_positioner: %v\n", err)
		return
	}

	_ = positioner.SetSize(160, 100)
	_ = positioner.SetAnchorRect(x, y, 1, 1)
	_ = positioner.SetAnchor(uint32(xdgshell.PositionerAnchorBottomRight))
	_ = positioner.SetGravity(uint32(xdgshell.PositionerGravityBottomRight))
	_ = positioner.SetConstraintAdjustment(uint32(xdgshell.PositionerConstraintAdjustmentSlideX | xdgshell.PositionerConstraintAdjustmentSlideY))

	popupSurface, err := comp.CreateSurface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create popup surface: %v\n", err)
		_ = positioner.Destroy()
		return
	}

	popupXdgSurface, err := wmBase.GetXdgSurface(wire.ObjectID(popupSurface.Proxy().ID()))
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
		ps.xdgSerial = ev.Serial
		ps.haveXdgCfg = true
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
		ps.popupCfg = ev
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

	_ = ps.xdgSurface.AckConfigure(ps.xdgSerial)

	stride := pw * 4
	bufSize := int64(ph) * int64(stride)

	fd, closeFd, err := shmFile(bufSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "popup shm: %v\n", err)
		return
	}

	bufData, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "popup mmap: %v\n", err)
		closeFd()
		return
	}

	for i := 0; i < len(bufData); i += 4 {
		bufData[i+0] = 0xF0
		bufData[i+1] = 0xF0
		bufData[i+2] = 0xF0
		bufData[i+3] = 0xFF
	}

	colors := [][3]byte{
		{0xC0, 0x40, 0x40},
		{0x40, 0xA0, 0x40},
		{0x40, 0x40, 0xC0},
	}
	itemH := ph / 3

	for i := 0; i < 3; i++ {
		top := i * itemH
		fillRect(bufData, stride, 0, top, pw, itemH, colors[i][0], colors[i][1], colors[i][2])
		if i < 2 {
			fillRect(bufData, stride, 0, top+itemH-1, pw, 1, 0x00, 0x00, 0x00)
		}
	}

	labels := []string{"Item 1", "Item 2", "Item 3"}
	for i, label := range labels {
		labelW := textWidth(label, 1)
		tx := (pw - labelW) / 2
		ty := i*itemH + (itemH-textHeight(1))/2
		drawText(bufData, stride, pw, ph, label, tx, ty, 1, 0x000000)
	}

	pool, err := shm.CreatePool(fd, int32(bufSize))
	if err != nil {
		fmt.Fprintf(os.Stderr, "popup create_pool: %v\n", err)
		_ = syscall.Munmap(bufData)
		closeFd()
		return
	}

	buf, err := pool.CreateBuffer(0, pw, ph, int32(stride), uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "popup create_buffer: %v\n", err)
		_ = pool.Destroy()
		_ = syscall.Munmap(bufData)
		closeFd()
		return
	}

	_ = ps.surface.Attach(wire.ObjectID(buf.Proxy().ID()), 0, 0)
	_ = ps.surface.Damage(0, 0, pw, ph)
	_ = ps.surface.Commit()
	ps.rendered = true

	ps.pool = pool
	ps.buf = buf
	ps.closeFd = closeFd
	ps.munmap = func() { _ = syscall.Munmap(bufData) }
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
	if ps.munmap != nil {
		ps.munmap()
	}
	if ps.closeFd != nil {
		ps.closeFd()
	}
	if ps.surface != nil {
		_ = ps.surface.Destroy()
	}
	ps.reset()
}
