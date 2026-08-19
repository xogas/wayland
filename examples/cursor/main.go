//go:build linux

// Cursor demo: switches between a self-drawn cursor surface (mode A) and the
// compositor's built-in cursors via cursor-shape-v1 (mode B).
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
	key1     = 2
	key2     = 3
	keyLeft  = 105
	keyRight = 106
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

	// Optional cursor-shape-v1: mode B is only available when the compositor
	// advertises wp_cursor_shape_manager_v1.
	var csMgr *cursorshape.CursorShapeManagerV1
	hasCursorShape := false
	if csMgrG, ok := globals.Find(cursorshape.InterfaceCursorShapeManagerV1); ok {
		csMgr, err = cursorshape.BindCursorShapeManagerV1(reg, csMgrG.Name, min(csMgrG.Version, cursorshape.VersionCursorShapeManagerV1))
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind cursor_shape_manager: %v\n", err)
		} else {
			hasCursorShape = true
			defer func() { _ = csMgr.Destroy() }()
		}
	}
	if !hasCursorShape {
		fmt.Println("wp_cursor_shape_manager_v1 not available, mode B disabled (custom cursor surface only).")
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Cursor Demo", "cursor-demo", 400, 300, nil)
	if err != nil {
		return err
	}

	// Static blue window content.
	winCleanup, err := shared.StaticBuffer(toplevel.Surface, core.Shm, 400, 300,
		func(pixels []byte, stride int32) { shared.FillSolid(pixels, 0x80, 0x60, 0x40) })
	if err != nil {
		return err
	}
	defer winCleanup()

	pointer, err := seat.GetPointer()
	if err != nil {
		return fmt.Errorf("get pointer: %w", err)
	}

	// Self-drawn crosshair cursor surface (ARGB, transparent outside the bars).
	cursorSurface, err := core.Compositor.CreateSurface()
	if err != nil {
		return fmt.Errorf("create cursor surface: %w", err)
	}
	cursorCleanup, err := shared.StaticBuffer(cursorSurface, core.Shm, cursorSize, cursorSize,
		func(pixels []byte, stride int32) { drawCrosshair(pixels, stride) })
	if err != nil {
		return err
	}
	defer cursorCleanup()

	ap := &app{
		pointer:        pointer,
		cursorSurface:  cursorSurface,
		hasCursorShape: hasCursorShape,
		mode:           modeCustom,
	}

	if hasCursorShape {
		ap.csDevice, err = csMgr.GetPointer(wire.ObjectID(pointer.Proxy().ID()))
		if err != nil {
			return fmt.Errorf("cursor_shape get_pointer: %w", err)
		}
		defer func() { _ = ap.csDevice.Destroy() }()
	}

	pointer.OnEnter(func(ev wayland.PointerEnterEvent) {
		ap.lastSerial = ev.Serial
		ap.applyCursor()
	})

	keyboard, err := seat.GetKeyboard()
	if err != nil {
		return fmt.Errorf("get keyboard: %w", err)
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
			return nil
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return nil
		case err := <-errCh:
			return err
		}
	}
}
