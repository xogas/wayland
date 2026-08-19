//go:build linux

// Clipboard and drag-and-drop demo: four draggable color boxes (the dragged
// color is sent as application/x-color) plus keyboard copy/paste of the
// clipboard.
package main

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/wire"
)

const (
	keyC = 46
	keyV = 47

	btnLeft = 272

	winW = 500
	winH = 300
)

// colorBox is one draggable box in the window.
type colorBox struct {
	x, y, w, h int32
	r, g, b    byte
	colorHex   string
}

// boxes is the static window content: four colored boxes.
var boxes = []colorBox{
	{20, 20, 220, 120, 0xFF, 0x00, 0x00, "#FF0000"},
	{260, 20, 220, 120, 0x00, 0xFF, 0x00, "#00FF00"},
	{20, 160, 220, 120, 0x00, 0x00, 0xFF, "#0000FF"},
	{260, 160, 220, 120, 0xFF, 0xFF, 0x00, "#FFFF00"},
}

// boxAt returns the box containing (x, y), or nil.
func boxAt(x, y int32) *colorBox {
	for i := range boxes {
		b := &boxes[i]
		if x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h {
			return b
		}
	}
	return nil
}

// app carries the data-device state shared by the input handlers and the
// main loop.
type app struct {
	dpy        *wayland.Display
	ddm        *wayland.DataDeviceManager
	dd         *wayland.DataDevice
	toplevel   *shared.Toplevel
	ddmVersion uint32

	// offers maps object ids to the data offers announced by the compositor.
	offers     map[uint32]*wayland.DataOffer
	offerMimes map[uint32][]string

	activeOfferID   uint32
	selectionOffer  *wayland.DataOffer
	clipboardSource *wayland.DataSource
	kbSerial        uint32
	transfers       chan *transferReq
}

// queueTransfer enqueues a receive for the main loop, or closes the pipe
// ends if the queue is full.
func (a *app) queueTransfer(req *transferReq) {
	select {
	case a.transfers <- req:
	default:
		_ = syscall.Close(req.rfd)
		_ = syscall.Close(req.wfd)
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

	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		return err
	}

	ddmG, ok := globals.Find(wayland.InterfaceDataDeviceManager)
	if !ok {
		return fmt.Errorf("no wl_data_device_manager global")
	}
	ddm, err := wayland.BindDataDeviceManager(reg, ddmG.Name, min(ddmG.Version, wayland.VersionDataDeviceManager))
	if err != nil {
		return fmt.Errorf("bind data_device_manager: %w", err)
	}

	// The example needs keyboard and pointer capabilities.
	var caps wayland.SeatCapability
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		return err
	}
	if caps&wayland.SeatCapabilityKeyboard == 0 || caps&wayland.SeatCapabilityPointer == 0 {
		return fmt.Errorf("seat needs keyboard and pointer capabilities")
	}

	kb, err := seat.GetKeyboard()
	if err != nil {
		return fmt.Errorf("get keyboard: %w", err)
	}
	kb.OnKeymap(func(ev wayland.KeyboardKeymapEvent) {
		_ = syscall.Close(ev.Fd)
	})
	ptr, err := seat.GetPointer()
	if err != nil {
		return fmt.Errorf("get pointer: %w", err)
	}
	dd, err := ddm.GetDataDevice(wire.ObjectID(seat.Proxy().ID()))
	if err != nil {
		return fmt.Errorf("get data_device: %w", err)
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "wayland-dnd", "wayland-dnd", winW, winH, nil)
	if err != nil {
		return err
	}

	// Static window content: the four color boxes.
	winCleanup, err := shared.StaticBuffer(toplevel.Surface, core.Shm, winW, winH,
		func(pixels []byte, stride int32) {
			shared.FillSolid(pixels, 0xFF, 0xFF, 0xFF)
			for _, b := range boxes {
				shared.FillRect(pixels, int(stride), winW, winH, int(b.x), int(b.y), int(b.w), int(b.h), b.r, b.g, b.b)
			}
		})
	if err != nil {
		return err
	}
	defer winCleanup()

	a := &app{
		dpy:        dpy,
		ddm:        ddm,
		dd:         dd,
		toplevel:   toplevel,
		ddmVersion: ddmG.Version,
		offers:     map[uint32]*wayland.DataOffer{},
		offerMimes: map[uint32][]string{},
		transfers:  make(chan *transferReq, 4),
	}

	var ptrX, ptrY int32
	kb.OnEnter(func(ev wayland.KeyboardEnterEvent) {
		a.kbSerial = ev.Serial
		fmt.Printf("keyboard: enter serial=%d\n", ev.Serial)
	})
	kb.OnLeave(func(ev wayland.KeyboardLeaveEvent) {
		fmt.Printf("keyboard: leave serial=%d\n", ev.Serial)
	})
	kb.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.State != wayland.KeyboardKeyStatePressed {
			return
		}
		switch ev.Key {
		case keyC:
			a.copy()
		case keyV:
			a.paste()
		}
	})

	ptr.OnEnter(func(ev wayland.PointerEnterEvent) {
		ptrX = ev.SurfaceX.Int()
		ptrY = ev.SurfaceY.Int()
		fmt.Printf("pointer: enter serial=%d x=%d y=%d\n", ev.Serial, ptrX, ptrY)
	})
	ptr.OnLeave(func(ev wayland.PointerLeaveEvent) {
		fmt.Printf("pointer: leave serial=%d\n", ev.Serial)
	})
	ptr.OnMotion(func(ev wayland.PointerMotionEvent) {
		ptrX = ev.SurfaceX.Int()
		ptrY = ev.SurfaceY.Int()
	})
	ptr.OnButton(func(ev wayland.PointerButtonEvent) {
		st := "release"
		if ev.State == wayland.PointerButtonStatePressed {
			st = "press"
		}
		fmt.Printf("pointer: button=%d state=%s serial=%d\n", ev.Button, st, ev.Serial)
		if ev.State == wayland.PointerButtonStatePressed && ev.Button == btnLeft {
			a.startDrag(boxAt(ptrX, ptrY), ev.Serial)
		}
	})

	dd.OnDataOffer(func(ev wayland.DataDeviceDataOfferEvent) {
		a.addOffer(ev.ID)
	})
	dd.OnEnter(func(ev wayland.DataDeviceEnterEvent) {
		a.onDragEnter(ev)
	})
	dd.OnMotion(func(ev wayland.DataDeviceMotionEvent) {
		fmt.Printf("data_device: motion time=%d x=%.2f y=%.2f\n", ev.Time, ev.X.Float64(), ev.Y.Float64())
	})
	dd.OnDrop(func(ev wayland.DataDeviceDropEvent) {
		a.onDrop()
	})
	dd.OnLeave(func(ev wayland.DataDeviceLeaveEvent) {
		fmt.Println("data_device: leave")
		a.activeOfferID = 0
	})
	dd.OnSelection(func(ev wayland.DataDeviceSelectionEvent) {
		a.onSelection(ev)
	})

	fmt.Printf("wayland-dnd: window %dx%d, 120s timeout. c=copy v=paste, drag boxes with left mouse.\n", winW, winH)

	// Main loop: dispatch, then run queued transfers (never inside a handler).
	for {
		select {
		case <-toplevel.Closed:
			fmt.Println("window closed by compositor.")
			return nil
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return nil
		default:
		}
		if err := dpy.Dispatch(ctx); err != nil {
			if ctx.Err() != nil {
				fmt.Println("timeout reached.")
				return nil
			}
			return err
		}
		for len(a.transfers) > 0 {
			a.doTransfer(ctx, <-a.transfers)
		}
	}
}
