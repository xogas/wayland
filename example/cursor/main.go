//go:build linux

// Cursor demo: self-drawn cursor surface vs cursor-shape-v1 protocol.
package main

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
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

const cursorSize int32 = 32
const hotspot int32 = 16

func (a *app) applyCursor() {
	if a.mode == modeCustom {
		_ = a.pointer.SetCursor(a.lastSerial, wire.ObjectID(a.cursorSurface.Proxy().ID()), hotspot, hotspot)
	} else if a.mode == modeShape && a.csDevice != nil {
		_ = a.csDevice.SetShape(a.lastSerial, uint32(shapeCycle[a.shapeIdx]))
	}
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
	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: %v\n", pe)
		os.Exit(1)
	})

	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}

	var compG, shmG, seatG, wmG, cursorShapeG wayland.RegistryGlobalEvent
	for _, g := range globals {
		switch g.Interface {
		case wayland.InterfaceCompositor:
			compG = g
		case wayland.InterfaceShm:
			shmG = g
		case wayland.InterfaceSeat:
			seatG = g
		case xdgshell.InterfaceWmBase:
			wmG = g
		case cursorshape.InterfaceCursorShapeManagerV1:
			cursorShapeG = g
		}
	}

	hasCursorShape := cursorShapeG.Interface != ""
	if !hasCursorShape {
		fmt.Println("wp_cursor_shape_manager_v1 not available, mode B disabled (custom cursor surface only).")
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
	seat, err := wayland.BindSeat(reg, seatG.Name, seatG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind seat: %v\n", err)
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

	const winW, winH = 400, 300

	var cfgSerial uint32
	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})

	shutdown := make(chan struct{})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) { close(shutdown) })

	_ = toplevel.SetTitle("Cursor Demo")
	_ = toplevel.SetAppID("cursor-demo")
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
	_ = xdgSurface.AckConfigure(cfgSerial)

	stride := int32(winW * 4)
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

	for y := range int32(winH) {
		for x := range int32(winW) {
			off := int(y*stride + x*4)
			data[off+0] = 0x40
			data[off+1] = 0x60
			data[off+2] = 0x80
			data[off+3] = 0xFF
		}
	}

	pool, err := shm.CreatePool(fd, int32(bufSize))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Destroy() //nolint: errcheck

	winBuf, err := pool.CreateBuffer(0, winW, winH, stride, uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
		os.Exit(1)
	}
	defer winBuf.Destroy() //nolint: errcheck

	_ = surface.Attach(wire.ObjectID(winBuf.Proxy().ID()), 0, 0)
	_ = surface.Damage(0, 0, winW, winH)
	_ = surface.Commit()

	pointer, err := seat.GetPointer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_pointer: %v\n", err)
		os.Exit(1)
	}

	cursorFd, cursorCloseFd, err := shmFile(int64(cursorSize) * int64(cursorSize) * 4)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shm cursor: %v\n", err)
		os.Exit(1)
	}
	defer cursorCloseFd()

	cursorData, err := syscall.Mmap(cursorFd, 0, int(cursorSize*cursorSize*4), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mmap cursor: %v\n", err)
		os.Exit(1)
	}
	defer syscall.Munmap(cursorData) //nolint: errcheck

	cursorStride := cursorSize * 4
	for i := 0; i < len(cursorData); i += 4 {
		cursorData[i+0] = 0x00
		cursorData[i+1] = 0x00
		cursorData[i+2] = 0x00
		cursorData[i+3] = 0x00
	}
	for x := range cursorSize {
		off := hotspot*int32(cursorStride) + x*4
		cursorData[off+0] = 0xFF
		cursorData[off+1] = 0xFF
		cursorData[off+2] = 0xFF
		cursorData[off+3] = 0xFF
	}
	for y := range cursorSize {
		off := y*cursorStride + hotspot*4
		cursorData[off+0] = 0xFF
		cursorData[off+1] = 0xFF
		cursorData[off+2] = 0xFF
		cursorData[off+3] = 0xFF
	}

	cursorPool, err := shm.CreatePool(cursorFd, int32(len(cursorData)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cursor create_pool: %v\n", err)
		os.Exit(1)
	}
	defer cursorPool.Destroy() //nolint: errcheck

	cursorBuf, err := cursorPool.CreateBuffer(0, cursorSize, cursorSize, cursorStride, uint32(wayland.ShmFormatArgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cursor create_buffer: %v\n", err)
		os.Exit(1)
	}
	defer cursorBuf.Destroy() //nolint: errcheck

	cursorSurface, err := comp.CreateSurface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cursor create_surface: %v\n", err)
		os.Exit(1)
	}
	_ = cursorSurface.Attach(wire.ObjectID(cursorBuf.Proxy().ID()), 0, 0)
	_ = cursorSurface.Damage(0, 0, cursorSize, cursorSize)
	_ = cursorSurface.Commit()

	var csDevice *cursorshape.CursorShapeDeviceV1
	if hasCursorShape {
		csMgr, err := cursorshape.BindCursorShapeManagerV1(reg, cursorShapeG.Name, cursorShapeG.Version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind cursor_shape_manager: %v\n", err)
			os.Exit(1)
		}
		defer csMgr.Destroy() //nolint: errcheck
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
		if ev.State != uint32(wayland.KeyboardKeyStatePressed) {
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

	for {
		select {
		case <-shutdown:
			fmt.Println("window closed by compositor.")
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
			if err == wayland.ErrConnClosed {
				return
			}
			fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			break
		}
	}
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
