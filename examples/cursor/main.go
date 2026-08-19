//go:build linux

// Cursor demo: self-drawn cursor surface vs cursor-shape-v1 protocol.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/staging/cursorshape"
	"github.com/xogas/wayland/wire"
)

const (
	modeCustom = 1
	modeShape  = 2

	key1     = 2
	key2     = 3
	keyLeft  = 105
	keyRight = 106

	cursorSize int32 = 32
	hotspot    int32 = 16
)

var shapeCycle = []cursorshape.CursorShapeDeviceV1Shape{
	cursorshape.CursorShapeDeviceV1ShapeDefault,
	cursorshape.CursorShapeDeviceV1ShapePointer,
	cursorshape.CursorShapeDeviceV1ShapeCrosshair,
	cursorshape.CursorShapeDeviceV1ShapeText,
	cursorshape.CursorShapeDeviceV1ShapeMove,
	cursorshape.CursorShapeDeviceV1ShapeGrab,
}

var shapeNames = []string{
	"default",
	"pointer",
	"crosshair",
	"text",
	"move",
	"grab",
}

type app struct {
	pointer        *wayland.Pointer
	cursorSurface  *wayland.Surface
	csDevice       *cursorshape.CursorShapeDeviceV1
	hasCursorShape bool
	mode           int
	lastSerial     uint32
	shapeIdx       int
}

func (a *app) applyCursor() {
	if a.mode == modeCustom {
		_ = a.pointer.SetCursor(a.lastSerial, wire.ObjectID(a.cursorSurface.Proxy().ID()), hotspot, hotspot)
	} else if a.mode == modeShape && a.csDevice != nil {
		_ = a.csDevice.SetShape(a.lastSerial, shapeCycle[a.shapeIdx])
	}
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
		fmt.Fprintf(os.Stderr, "protocol error: %v\n", pe)
		os.Exit(1)
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

	// Optional cursor-shape-v1: bind once, use it for GetPointer below.
	var csMgr *cursorshape.CursorShapeManagerV1
	var csDevice *cursorshape.CursorShapeDeviceV1
	hasCursorShape := false
	if csMgrG, ok := globals.Find(cursorshape.InterfaceCursorShapeManagerV1); ok {
		csMgr, err = cursorshape.BindCursorShapeManagerV1(reg, csMgrG.Name, min(csMgrG.Version, cursorshape.VersionCursorShapeManagerV1))
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind cursor_shape_manager: %v\n", err)
		} else {
			hasCursorShape = true
			defer csMgr.Destroy() //nolint: errcheck
		}
	}
	if !hasCursorShape {
		fmt.Println("wp_cursor_shape_manager_v1 not available, mode B disabled (custom cursor surface only).")
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Cursor Demo", "cursor-demo", 400, 300, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Static blue window content.
	winID, winData, winCleanup, err := shared.NewBuffer(core.Shm, 400, 300, wayland.ShmFormatXrgb8888)
	if err != nil {
		fmt.Fprintf(os.Stderr, "window buffer: %v\n", err)
		os.Exit(1)
	}
	defer winCleanup()
	shared.FillSolid(winData, 0x80, 0x60, 0x40)
	_ = toplevel.Surface.Attach(winID, 0, 0)
	_ = toplevel.Surface.Damage(0, 0, 400, 300)
	_ = toplevel.Surface.Commit()

	pointer, err := seat.GetPointer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_pointer: %v\n", err)
		os.Exit(1)
	}

	// Self-drawn crosshair cursor surface (ARGB, transparent outside the bars).
	cursorID, cursorData, cursorCleanup, err := shared.NewBuffer(core.Shm, cursorSize, cursorSize, wayland.ShmFormatArgb8888)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cursor buffer: %v\n", err)
		os.Exit(1)
	}
	defer cursorCleanup()
	shared.FillSolid(cursorData, 0, 0, 0)
	for x := range cursorSize {
		off := int(hotspot*int32(4)*cursorSize + x*4)
		cursorData[off+0] = 0xFF
		cursorData[off+1] = 0xFF
		cursorData[off+2] = 0xFF
		cursorData[off+3] = 0xFF
	}
	for y := range cursorSize {
		off := int(y*4*cursorSize + hotspot*4)
		cursorData[off+0] = 0xFF
		cursorData[off+1] = 0xFF
		cursorData[off+2] = 0xFF
		cursorData[off+3] = 0xFF
	}

	cursorSurface, err := core.Compositor.CreateSurface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cursor create_surface: %v\n", err)
		os.Exit(1)
	}
	_ = cursorSurface.Attach(cursorID, 0, 0)
	_ = cursorSurface.Damage(0, 0, cursorSize, cursorSize)
	_ = cursorSurface.Commit()

	if hasCursorShape {
		csDevice, err = csMgr.GetPointer(wire.ObjectID(pointer.Proxy().ID()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "cursor_shape get_pointer: %v\n", err)
			os.Exit(1)
		}
		defer csDevice.Destroy() //nolint: errcheck
	}

	ap := &app{
		pointer:        pointer,
		cursorSurface:  cursorSurface,
		csDevice:       csDevice,
		hasCursorShape: hasCursorShape,
		mode:           modeCustom,
	}

	pointer.OnEnter(func(ev wayland.PointerEnterEvent) {
		ap.lastSerial = ev.Serial
		ap.applyCursor()
	})

	keyboard, err := seat.GetKeyboard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
		os.Exit(1)
	}
	keyboard.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.State != wayland.KeyboardKeyStatePressed {
			return
		}
		switch ev.Key {
		case key1:
			ap.mode = modeCustom
			fmt.Println("mode: custom cursor surface (A)")
			ap.applyCursor()
		case key2:
			if !ap.hasCursorShape {
				fmt.Println("mode B unavailable (cursor-shape-v1 not supported)")
				return
			}
			ap.mode = modeShape
			fmt.Println("mode: cursor-shape-v1 (B) -- shape:", shapeNames[ap.shapeIdx])
			ap.applyCursor()
		case keyLeft:
			if ap.mode != modeShape || !ap.hasCursorShape {
				return
			}
			ap.shapeIdx = (ap.shapeIdx - 1 + len(shapeCycle)) % len(shapeCycle)
			fmt.Println("mode B shape:", shapeNames[ap.shapeIdx])
			ap.applyCursor()
		case keyRight:
			if ap.mode != modeShape || !ap.hasCursorShape {
				return
			}
			ap.shapeIdx = (ap.shapeIdx + 1) % len(shapeCycle)
			fmt.Println("mode B shape:", shapeNames[ap.shapeIdx])
			ap.applyCursor()
		}
	})

	fmt.Println("Cursor Demo -- 400x300 window")
	fmt.Println("Press 1: self-drawn crosshair cursor (mode A)")
	if hasCursorShape {
		fmt.Println("Press 2: cursor-shape-v1 (mode B)")
		fmt.Println("In mode B, use Left/Right arrows to cycle: default, pointer, crosshair, text, move, grab")
	} else {
		fmt.Println("Mode B not available: wp_cursor_shape_manager_v1 not advertised by compositor")
	}

	errCh := shared.DispatchLoop(ctx, dpy)
	for {
		select {
		case <-toplevel.Closed:
			fmt.Println("window closed by compositor.")
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
