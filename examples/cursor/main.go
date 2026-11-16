// Command cursor changes the pointer cursor via cursor-shape-v1, or via a
// custom shared-memory cursor surface when the compositor lacks that protocol.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	xdgshell "github.com/xogas/wayland/protocols/stable/xdg-shell"
	cursorshape "github.com/xogas/wayland/protocols/staging/cursor-shape"
)

const (
	windowWidth  = 320
	windowHeight = 200
	windowTitle  = "cursor"

	cursorSize     = 24
	cursorHotspotX = 0
	cursorHotspotY = 0

	leftButton = 0x110

	// Opaque ARGB8888 background color.
	solidColor = 0xff18323d
)

// cursorShapes is cycled by clicks and re-applied on enter (the compositor
// resets the cursor there).
var cursorShapes = []struct {
	name  string
	shape cursorshape.DeviceV1Shape
}{
	{"default", cursorshape.DeviceV1ShapeDefault},
	{"pointer", cursorshape.DeviceV1ShapePointer},
	{"text", cursorshape.DeviceV1ShapeText},
	{"crosshair", cursorshape.DeviceV1ShapeCrosshair},
	{"wait", cursorshape.DeviceV1ShapeWait},
	{"cell", cursorshape.DeviceV1ShapeCell},
	{"help", cursorshape.DeviceV1ShapeHelp},
	{"progress", cursorshape.DeviceV1ShapeProgress},
}

type registryHandler struct {
	globals *[]wayland.Global
}

func (h registryHandler) Global(g wayland.Global) {
	*h.globals = append(*h.globals, g)
}
func (h registryHandler) GlobalRemove(name uint32) {
	gs := *h.globals
	for i := range gs {
		if gs[i].Name == name {
			gs = append(gs[:i], gs[i+1:]...)
			*h.globals = gs
			return
		}
	}
}

type shmHandler struct {
	formats map[uint32]bool
}

func (h *shmHandler) Format(format uint32) { h.formats[format] = true }

type wlSurfaceHandler struct{}

func (wlSurfaceHandler) Enter(_ *wayland.Proxy)          {}
func (wlSurfaceHandler) Leave(_ *wayland.Proxy)          {}
func (wlSurfaceHandler) PreferredBufferScale(_ int32)    {}
func (wlSurfaceHandler) PreferredBufferTransform(uint32) {}

type wmBaseHandler struct {
	app *app
}

func (h *wmBaseHandler) Ping(serial uint32) {
	if err := h.app.wmBase.Pong(serial); err != nil {
		h.app.setErr(fmt.Errorf("xdg_wm_base.pong: %w", err))
	}
}

type xdgSurfaceHandler struct {
	app *app
}

func (h *xdgSurfaceHandler) Configure(serial uint32) {
	a := h.app
	if err := a.xdgSurface.AckConfigure(serial); err != nil {
		a.setErr(fmt.Errorf("xdg_surface.ack_configure: %w", err))
		return
	}
	if a.mapped {
		return
	}
	if err := a.surface.Attach(a.windowImage.buffer, 0, 0); err != nil {
		a.setErr(fmt.Errorf("wl_surface.attach: %w", err))
		return
	}
	if err := a.surface.Damage(0, 0, windowWidth, windowHeight); err != nil {
		a.setErr(fmt.Errorf("wl_surface.damage: %w", err))
		return
	}
	if err := a.surface.Commit(); err != nil {
		a.setErr(fmt.Errorf("wl_surface.commit: %w", err))
		return
	}
	a.mapped = true
}

type toplevelHandler struct {
	app *app
}

func (h *toplevelHandler) Configure(int32, int32, []byte) {}
func (h *toplevelHandler) Close()                         { h.app.stopping.Store(true) }
func (h *toplevelHandler) ConfigureBounds(int32, int32)   {}
func (h *toplevelHandler) WmCapabilities([]byte)          {}

// bufferHandler ignores wl_buffer.release; both images are written once.
type bufferHandler struct{}

func (bufferHandler) Release() {}

type seatHandler struct {
	capabilities uint32
}

func (h *seatHandler) Capabilities(capabilities uint32) { h.capabilities = capabilities }
func (*seatHandler) Name(string)                        {}

type keyboardHandler struct {
	app *app
}

func (h *keyboardHandler) Keymap(_ uint32, fd int, _ uint32) {
	// The caller owns the keymap fd; this example does not build a keymap.
	if fd >= 0 {
		_ = syscall.Close(fd)
	}
}
func (h *keyboardHandler) Enter(_ uint32, _ *wayland.Proxy, _ []byte) {}
func (h *keyboardHandler) Leave(_ uint32, _ *wayland.Proxy)           {}
func (h *keyboardHandler) Key(_ uint32, _ uint32, key uint32, state uint32) {
	if key == 1 && (state == uint32(wayland.KeyboardKeyStatePressed) || state == uint32(wayland.KeyboardKeyStateRepeated)) {
		h.app.stopping.Store(true)
	}
}
func (h *keyboardHandler) Modifiers(_, _, _, _, _ uint32) {}
func (h *keyboardHandler) RepeatInfo(_, _ int32)          {}

type pointerHandler struct {
	app *app
}

// Enter stores the enter serial. wl_pointer.set_cursor and
// wp_cursor_shape_device_v1.set_shape both require the serial of the latest
// wl_pointer.enter event, not the serial of a button press, so clicks reuse it
// until the next enter replaces it. The compositor resets the cursor on enter,
// so the current cursor is re-applied here.
func (h *pointerHandler) Enter(serial uint32, _ *wayland.Proxy, _, _ float64) {
	a := h.app
	a.enterSerial = serial
	if err := a.applyCursor(serial); err != nil {
		a.setErr(err)
	}
}
func (h *pointerHandler) Leave(uint32, *wayland.Proxy)         {}
func (h *pointerHandler) Motion(uint32, float64, float64)      {}
func (h *pointerHandler) Axis(uint32, uint32, float64)         {}
func (h *pointerHandler) Frame()                               {}
func (h *pointerHandler) AxisSource(uint32)                    {}
func (h *pointerHandler) AxisStop(uint32, uint32)              {}
func (h *pointerHandler) AxisDiscrete(uint32, int32)           {}
func (h *pointerHandler) AxisValue120(uint32, int32)           {}
func (h *pointerHandler) AxisRelativeDirection(uint32, uint32) {}
func (h *pointerHandler) Warp(float64, float64)                {}
func (h *pointerHandler) Button(_ uint32, _ uint32, button uint32, state uint32) {
	a := h.app
	if button != leftButton || state != uint32(wayland.PointerButtonStatePressed) {
		return
	}
	if a.cursorDevice == nil {
		// The fallback image cannot change; it stays the custom arrow.
		return
	}
	a.shapeIndex = (a.shapeIndex + 1) % len(cursorShapes)
	if err := a.applyCursor(a.enterSerial); err != nil {
		a.setErr(err)
		return
	}
	fmt.Printf("cursor: shape %s\n", cursorShapes[a.shapeIndex].name)
}

// shmImage is one mapped ARGB8888 image plus the pool and buffer sharing it.
type shmImage struct {
	pool   *wayland.ShmPool
	buffer *wayland.Buffer
	mmap   []byte
}

type app struct {
	compositor *wayland.Compositor
	shm        *wayland.Shm
	wmBase     *xdgshell.WmBase
	surface    *wayland.Surface
	xdgSurface *xdgshell.Surface
	toplevel   *xdgshell.Toplevel
	seat       *wayland.Seat
	keyboard   *wayland.Keyboard
	pointer    *wayland.Pointer

	cursorDevice  *cursorshape.DeviceV1
	cursorSurface *wayland.Surface
	cursorImage   *shmImage
	windowImage   *shmImage

	shapeIndex  int
	enterSerial uint32

	mapped      bool
	stopping    atomic.Bool
	callbackErr error
}

func (a *app) setErr(err error) {
	if err != nil && a.callbackErr == nil {
		a.callbackErr = err
	}
}

// applyCursor sends the current cursor for an enter serial.
func (a *app) applyCursor(serial uint32) error {
	if a.cursorDevice != nil {
		if err := a.cursorDevice.SetShape(serial, cursorShapes[a.shapeIndex].shape); err != nil {
			return fmt.Errorf("wp_cursor_shape_device_v1.set_shape: %w", err)
		}
		return nil
	}
	if a.cursorSurface == nil {
		return nil
	}
	if err := a.pointer.SetCursor(serial, a.cursorSurface, cursorHotspotX, cursorHotspotY); err != nil {
		return fmt.Errorf("wl_pointer.set_cursor: %w", err)
	}
	return nil
}
func (a *app) destroy() {
	if a.cursorDevice != nil {
		_ = a.cursorDevice.Destroy()
	}
	if a.cursorSurface != nil {
		_ = a.cursorSurface.Destroy()
	}
	// wl_pointer/wl_keyboard release since v3, wl_seat.release since v5.
	if a.seat != nil && a.seat.Proxy().Version() >= 5 {
		if a.pointer != nil {
			_ = a.pointer.Release()
		}
		if a.keyboard != nil {
			_ = a.keyboard.Release()
		}
		_ = a.seat.Release()
	}
	if a.toplevel != nil {
		_ = a.toplevel.Destroy()
	}
	if a.xdgSurface != nil {
		_ = a.xdgSurface.Destroy()
	}
	if a.surface != nil {
		_ = a.surface.Destroy()
	}
	for _, img := range []*shmImage{a.cursorImage, a.windowImage} {
		if img != nil {
			_ = img.buffer.Destroy()
			_ = img.pool.Destroy()
			_ = syscall.Munmap(img.mmap)
		}
	}
	if a.wmBase != nil {
		_ = a.wmBase.Destroy()
	}
}
func connect() (*wayland.Display, error) {
	d, err := wayland.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect: set WAYLAND_DISPLAY (with XDG_RUNTIME_DIR) or WAYLAND_SOCKET: %w", err)
	}
	return d, nil
}
func roundtrip(d *wayland.Display, what string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}
func findGlobal(globals []wayland.Global, name string) (wayland.Global, bool) {
	for _, g := range globals {
		if g.Interface == name {
			return g, true
		}
	}
	return wayland.Global{}, false
}

// createShmImage maps a new unlinked shm file and sends its pool and buffer.
// The client descriptor is closed afterwards; pixels are zeroed for the caller.
func (a *app) createShmImage(prefix string, width int32, height int32) (*shmImage, error) {
	size := int(width) * int(height) * 4
	f, err := os.CreateTemp("", prefix)
	if err != nil {
		return nil, fmt.Errorf("create shm file: %w", err)
	}
	_ = os.Remove(f.Name()) // unlink immediately so the file dies with the mapping
	if err := f.Truncate(int64(size)); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("truncate shm file: %w", err)
	}
	mmap, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("mmap shm file: %w", err)
	}
	pool, err := a.shm.CreatePool(int(f.Fd()), int32(size))
	if err != nil {
		return nil, fmt.Errorf("wl_shm.create_pool: %w", err)
	}
	// The kernel copied the descriptor into the socket; close the client's copy.
	// The library does not close descriptors passed to requests.
	_ = f.Close()
	buffer, err := pool.CreateBuffer(0, width, height, width*4, wayland.ShmFormatArgb8888, bufferHandler{})
	if err != nil {
		return nil, fmt.Errorf("wl_shm_pool.create_buffer: %w", err)
	}
	return &shmImage{pool: pool, buffer: buffer, mmap: mmap}, nil
}

// createCustomCursor rasterizes the fallback arrow on its own wl_surface.
func (a *app) createCustomCursor() error {
	image, err := a.createShmImage("wayland-go-cursor-image-*", cursorSize, cursorSize)
	if err != nil {
		return err
	}
	a.cursorImage = image
	drawCursorArrow(image.mmap)
	surface, err := a.compositor.CreateSurface(wlSurfaceHandler{})
	if err != nil {
		return fmt.Errorf("cursor wl_compositor.create_surface: %w", err)
	}
	a.cursorSurface = surface
	if err := surface.Attach(image.buffer, 0, 0); err != nil {
		return fmt.Errorf("cursor wl_surface.attach: %w", err)
	}
	if err := surface.Damage(0, 0, cursorSize, cursorSize); err != nil {
		return fmt.Errorf("cursor wl_surface.damage: %w", err)
	}
	if err := surface.Commit(); err != nil {
		return fmt.Errorf("cursor wl_surface.commit: %w", err)
	}
	return nil
}

// drawCursorArrow draws a white arrow triangle with a black outline on a
// transparent background; the tip is the hotspot. The arrow pixels are opaque;
// every pixel the loop does not touch stays transparent.
func drawCursorArrow(pixels []byte) {
	for y := 0; y < cursorSize; y++ {
		width := 1 + y*3/4
		for x := 0; x < width; x++ {
			color := uint32(0xffffffff)
			if x == 0 || x == width-1 || y == cursorSize-1 {
				color = 0xff000000
			}
			off := (y*cursorSize + x) * 4
			binary.LittleEndian.PutUint32(pixels[off:off+4], color)
		}
	}
}
func run() error {
	d, err := connect()
	if err != nil {
		return err
	}
	a := &app{}
	// Destroy created objects first, then close and flush the connection.
	defer func() {
		a.destroy()
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "cursor: close:", cerr)
		}
	}()

	var globals []wayland.Global
	reg, err := d.GetRegistry(registryHandler{globals: &globals})
	if err != nil {
		return fmt.Errorf("get_registry: %w", err)
	}
	if err := roundtrip(d, "initial roundtrip"); err != nil {
		return err
	}
	compositorGlobal, ok := findGlobal(globals, "wl_compositor")
	if !ok {
		return fmt.Errorf("missing required global wl_compositor")
	}
	shmGlobal, ok := findGlobal(globals, "wl_shm")
	if !ok {
		return fmt.Errorf("missing required global wl_shm")
	}
	wmBaseGlobal, ok := findGlobal(globals, "xdg_wm_base")
	if !ok {
		return fmt.Errorf("missing required global xdg_wm_base")
	}
	seatGlobal, ok := findGlobal(globals, "wl_seat")
	if !ok {
		return fmt.Errorf("missing required global wl_seat")
	}

	a.compositor, err = wayland.BindCompositor(reg, compositorGlobal)
	if err != nil {
		return fmt.Errorf("bind wl_compositor: %w", err)
	}
	shmFormats := &shmHandler{formats: make(map[uint32]bool)}
	a.shm, err = wayland.BindShm(reg, shmGlobal, shmFormats)
	if err != nil {
		return fmt.Errorf("bind wl_shm: %w", err)
	}
	a.wmBase, err = xdgshell.BindWmBase(reg, wmBaseGlobal, &wmBaseHandler{app: a})
	if err != nil {
		return fmt.Errorf("bind xdg_wm_base: %w", err)
	}
	seatState := &seatHandler{}
	a.seat, err = wayland.BindSeat(reg, seatGlobal, seatState)
	if err != nil {
		return fmt.Errorf("bind wl_seat: %w", err)
	}

	if err := roundtrip(d, "seat and shm roundtrip"); err != nil {
		return err
	}
	if !shmFormats.formats[uint32(wayland.ShmFormatArgb8888)] {
		return fmt.Errorf("wl_shm did not advertise ARGB8888")
	}
	if seatState.capabilities&uint32(wayland.SeatCapabilityPointer) == 0 {
		return fmt.Errorf("wl_seat has no pointer capability")
	}

	a.keyboard, err = a.seat.GetKeyboard(&keyboardHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_seat.get_keyboard: %w", err)
	}
	a.pointer, err = a.seat.GetPointer(&pointerHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_seat.get_pointer: %w", err)
	}

	if shapeGlobal, ok := findGlobal(globals, "wp_cursor_shape_manager_v1"); ok {
		manager, err := cursorshape.BindManagerV1(reg, shapeGlobal)
		if err != nil {
			return fmt.Errorf("bind wp_cursor_shape_manager_v1: %w", err)
		}
		a.cursorDevice, err = manager.GetPointer(a.pointer)
		if err != nil {
			return fmt.Errorf("wp_cursor_shape_manager_v1.get_pointer: %w", err)
		}
	} else {
		if err := a.createCustomCursor(); err != nil {
			return err
		}
		fmt.Println("cursor: wp_cursor_shape_manager_v1 unavailable; using a fallback custom shm cursor")
	}

	a.windowImage, err = a.createShmImage("wayland-go-cursor-*", windowWidth, windowHeight)
	if err != nil {
		return err
	}
	for off := 0; off+4 <= len(a.windowImage.mmap); off += 4 {
		binary.LittleEndian.PutUint32(a.windowImage.mmap[off:off+4], solidColor)
	}

	a.surface, err = a.compositor.CreateSurface(wlSurfaceHandler{})
	if err != nil {
		return fmt.Errorf("wl_compositor.create_surface: %w", err)
	}
	a.xdgSurface, err = a.wmBase.GetXdgSurface(a.surface, &xdgSurfaceHandler{app: a})
	if err != nil {
		return fmt.Errorf("xdg_wm_base.get_xdg_surface: %w", err)
	}
	a.toplevel, err = a.xdgSurface.GetToplevel(&toplevelHandler{app: a})
	if err != nil {
		return fmt.Errorf("xdg_surface.get_toplevel: %w", err)
	}
	if err := a.toplevel.SetTitle(windowTitle); err != nil {
		return fmt.Errorf("xdg_toplevel.set_title: %w", err)
	}
	if err := a.xdgSurface.SetWindowGeometry(0, 0, windowWidth, windowHeight); err != nil {
		return fmt.Errorf("xdg_surface.set_window_geometry: %w", err)
	}
	if err := a.surface.Commit(); err != nil {
		return fmt.Errorf("initial wl_surface.commit: %w", err)
	}
	// The compositor may deliver configure after the roundtrip's sync callback.
	for attempt := 0; attempt < 3 && !a.mapped; attempt++ {
		if err := roundtrip(d, "wait for xdg configure"); err != nil {
			return err
		}
	}
	if a.callbackErr != nil {
		return a.callbackErr
	}
	if !a.mapped {
		return fmt.Errorf("xdg_surface did not send configure")
	}

	if a.cursorDevice != nil {
		fmt.Println("cursor: move the pointer over the window and click to cycle cursor shapes; close the window or press Esc to exit")
	} else {
		fmt.Println("cursor: move the pointer over the window with the custom shm cursor; close the window or press Esc to exit")
	}
	for !a.stopping.Load() {
		if err := d.Dispatch(); err != nil {
			if a.stopping.Load() && errors.Is(err, wayland.ErrClosed) {
				break
			}
			return fmt.Errorf("dispatch: %w", err)
		}
		if a.callbackErr != nil {
			return a.callbackErr
		}
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cursor:", err)
		os.Exit(1)
	}
}
