//go:build linux

// An xdg_popup + xdg_positioner right-click context menu demonstration:
// right-click opens a grabbed menu, a popup is also auto-opened after 2
// seconds and auto-closed 3 seconds later.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
)

const (
	btnRight = 273
	btnLeft  = 272

	winW = 400
	winH = 300
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

	// The example needs a pointer.
	var caps wayland.SeatCapability
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		return fmt.Errorf("roundtrip caps: %w", err)
	}
	if caps&wayland.SeatCapabilityPointer == 0 {
		return fmt.Errorf("seat has no pointer capability")
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Popup Demo", "wayland-popup-demo", winW, winH, nil)
	if err != nil {
		return err
	}

	winCleanup, err := shared.StaticBuffer(toplevel.Surface, core.Shm, winW, winH,
		func(pixels []byte, stride int32) {
			shared.FillSolid(pixels, 0x80, 0x80, 0x80)
			shared.DrawText(pixels, int(stride), winW, winH, "Right click for menu", 80, 130, 1, 0x000000)
		})
	if err != nil {
		return err
	}
	defer winCleanup()

	pointer, err := seat.GetPointer()
	if err != nil {
		return fmt.Errorf("get pointer: %w", err)
	}

	// Pointer state. Events that must act on the popup (open, select item)
	// are recorded as pending flags and handled by the main loop, never
	// inside the handler.
	var (
		cursorX, cursorY  int32
		ptrOnPopup        bool
		popupCursorY      int32
		rightClickPending bool
		rightClickSerial  uint32
		rightClickX       int32
		rightClickY       int32
		popupClickPending bool
		popupClickItemY   int32
	)
	var ps popupState

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

	// Auto-demo: open a popup after 2s, close it 3s later.
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
			return nil
		case <-ctx.Done():
			fmt.Println("timeout reached")
			return nil
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
			item := min(max(popupClickItemY/33, 0), 2)
			fmt.Printf("popup: item %d selected\n", item+1)
			destroyPopup(&ps)
		}

		if ps.active && ps.haveXdgCfg && ps.havePopupCfg && !ps.rendered {
			renderPopup(core.Shm, &ps)
		}

		if err := dpy.DispatchPending(); err != nil {
			if ctx.Err() != nil {
				fmt.Println("timeout reached")
				return nil
			}
			return err
		}
	}
}
