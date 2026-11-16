// Command subsurface moves an animated wl_subsurface child inside an
// xdg-shell parent window.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	xdgshell "github.com/xogas/wayland/protocols/stable/xdg-shell"
)

const (
	parentWidth  = 480
	parentHeight = 320
	childSize    = 120
	windowTitle  = "subsurface"

	// The parent background is a checkerboard of two dark ARGB8888 colors.
	checkerCell         = 32
	checkerDark  uint32 = 0xff10161e
	checkerLight uint32 = 0xff1c2530

	// The child is an opaque orange square with a cyan border and a white
	// crosshair, so its motion is obvious frame to frame.
	childFill     uint32 = 0xffc86428
	childBorder   uint32 = 0xff20e0ff
	childMarker   uint32 = 0xffffffff
	childBorderPx        = 4
	markerArmPx          = 20
	markerThickPx        = 2

	orbitPeriodMS = 6000
)

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

func (h *shmHandler) Format(format uint32) {
	h.formats[format] = true
}

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
	// Acknowledge every configure, including the ones that arrive after the
	// first map: the parent keeps its fixed size, so only the ack matters.
	if err := a.xdgSurface.AckConfigure(serial); err != nil {
		a.setErr(fmt.Errorf("xdg_surface.ack_configure: %w", err))
		return
	}
	if a.mapped {
		return
	}
	// Initial map. The child is synchronized, so its position and buffer are
	// committed first and cached; the parent commit below applies them. That
	// keeps the first presented frame consistent instead of showing the child
	// at a default position for one frame.
	if err := a.subsurface.SetPosition((parentWidth-childSize)/2, (parentHeight-childSize)/2); err != nil {
		a.setErr(fmt.Errorf("wl_subsurface.set_position: %w", err))
		return
	}
	if err := a.childSurface.Attach(a.childBuf.buffer, 0, 0); err != nil {
		a.setErr(fmt.Errorf("child wl_surface.attach: %w", err))
		return
	}
	if err := a.childSurface.Commit(); err != nil {
		a.setErr(fmt.Errorf("child wl_surface.commit: %w", err))
		return
	}
	// A wl_surface.frame request takes effect on the next commit, so queue the
	// first callback before the commit that maps the parent.
	if err := a.requestFrame(); err != nil {
		a.setErr(err)
		return
	}
	if err := a.parentSurface.Attach(a.parentBuf.buffer, 0, 0); err != nil {
		a.setErr(fmt.Errorf("parent wl_surface.attach: %w", err))
		return
	}
	if err := a.parentSurface.Damage(0, 0, parentWidth, parentHeight); err != nil {
		a.setErr(fmt.Errorf("parent wl_surface.damage: %w", err))
		return
	}
	if err := a.parentSurface.Commit(); err != nil {
		a.setErr(fmt.Errorf("parent wl_surface.commit: %w", err))
		return
	}
	a.mapped = true
}

type toplevelHandler struct {
	app *app
}

// Configure ignores the configured size: the parent is fixed at 480x320 and
// the compositor is expected to letterbox it rather than resize our buffers.
func (h *toplevelHandler) Configure(_ int32, _ int32, _ []byte) {}
func (h *toplevelHandler) Close()                               { h.app.stopping.Store(true) }
func (h *toplevelHandler) ConfigureBounds(int32, int32)         {}
func (h *toplevelHandler) WmCapabilities([]byte)                {}

type bufferHandler struct{}

// Release reports that the compositor no longer reads the buffer. Each surface
// keeps one buffer attached for its whole lifetime, so release events need no
// bookkeeping here.
func (bufferHandler) Release() {}

// frameHandler advances the animation once per presented parent frame.
type frameHandler struct {
	app *app
}

func (h *frameHandler) Done(_ uint32) {
	a := h.app
	if a.stopping.Load() {
		return
	}
	x, y := orbitPosition(time.Now().UnixMilli())
	// The child is synchronized, so its new state is cached and only applied on
	// the next parent commit: update the position and commit the child before
	// the parent, never after.
	if err := a.subsurface.SetPosition(x, y); err != nil {
		a.setErr(fmt.Errorf("wl_subsurface.set_position: %w", err))
		return
	}
	if err := a.childSurface.Commit(); err != nil {
		a.setErr(fmt.Errorf("child wl_surface.commit: %w", err))
		return
	}
	if err := a.parentSurface.Damage(0, 0, parentWidth, parentHeight); err != nil {
		a.setErr(fmt.Errorf("parent wl_surface.damage: %w", err))
		return
	}
	// Queue the next callback before this commit: wl_surface.frame is only
	// armed by a commit, and requesting it here keeps exactly one outstanding.
	if err := a.requestFrame(); err != nil {
		a.setErr(err)
		return
	}
	// Full-surface damage is required because the child's previous position must
	// be repainted; the compositor also tracks the moved subsurface across this
	// commit, so partial damage would leave stale child pixels behind.
	if err := a.parentSurface.Commit(); err != nil {
		a.setErr(fmt.Errorf("parent wl_surface.commit: %w", err))
		return
	}
}

type seatHandler struct {
	name         string
	hasName      bool
	capabilities uint32
}

func (h *seatHandler) Capabilities(capabilities uint32) { h.capabilities = capabilities }
func (h *seatHandler) Name(name string)                 { h.name = name; h.hasName = true }

type keyboardHandler struct {
	app *app
}

func (h *keyboardHandler) Keymap(_ uint32, fd int, _ uint32) {
	// The caller owns the keymap descriptor; this example does not build a
	// keymap, so close it immediately.
	if fd >= 0 {
		_ = syscall.Close(fd)
	}
}

func (h *keyboardHandler) Enter(_ uint32, _ *wayland.Proxy, _ []byte) {}
func (h *keyboardHandler) Leave(_ uint32, _ *wayland.Proxy)           {}
func (h *keyboardHandler) Key(_ uint32, _ uint32, key uint32, state uint32) {
	fmt.Printf("key keycode=%d state=%d\n", key, state)
	if key == 1 && (state == uint32(wayland.KeyboardKeyStatePressed) || state == uint32(wayland.KeyboardKeyStateRepeated)) {
		h.app.stopping.Store(true)
	}
}
func (h *keyboardHandler) Modifiers(_, _, _, _, _ uint32) {
}
func (h *keyboardHandler) RepeatInfo(_, _ int32) {}

// shmBuffer bundles one mapped, unlinked shared memory file with the pool and
// wl_buffer created from it. destroy releases everything it owns.
type shmBuffer struct {
	pool   *wayland.ShmPool
	buffer *wayland.Buffer
	mmap   []byte
}

func (b *shmBuffer) destroy() {
	_ = b.buffer.Destroy()
	_ = b.pool.Destroy()
	_ = syscall.Munmap(b.mmap)
}

type app struct {
	shm           *wayland.Shm
	wmBase        *xdgshell.WmBase
	subcompositor *wayland.Subcompositor
	subsurface    *wayland.Subsurface
	parentSurface *wayland.Surface
	childSurface  *wayland.Surface
	xdgSurface    *xdgshell.Surface
	toplevel      *xdgshell.Toplevel
	parentBuf     *shmBuffer
	childBuf      *shmBuffer
	mapped        bool
	stopping      atomic.Bool
	callbackErr   error
}

func (a *app) setErr(err error) {
	if err != nil && a.callbackErr == nil {
		a.callbackErr = err
	}
}

func (a *app) destroy() {
	// The subsurface must go before the surfaces it refers to.
	if a.subsurface != nil {
		_ = a.subsurface.Destroy()
	}
	if a.toplevel != nil {
		_ = a.toplevel.Destroy()
	}
	if a.xdgSurface != nil {
		_ = a.xdgSurface.Destroy()
	}
	if a.childSurface != nil {
		_ = a.childSurface.Destroy()
	}
	if a.parentSurface != nil {
		_ = a.parentSurface.Destroy()
	}
	// Buffer, pool and mapping stay alive until the connection closes.
	if a.childBuf != nil {
		a.childBuf.destroy()
	}
	if a.parentBuf != nil {
		a.parentBuf.destroy()
	}
	if a.wmBase != nil {
		_ = a.wmBase.Destroy()
	}
	if a.subcompositor != nil {
		_ = a.subcompositor.Destroy()
	}
}

// requestFrame queues one wl_surface.frame callback on the parent. The request
// must be followed by a parent commit to be armed.
func (a *app) requestFrame() error {
	if _, err := a.parentSurface.Frame(&frameHandler{app: a}); err != nil {
		return fmt.Errorf("wl_surface.frame: %w", err)
	}
	return nil
}

// createShmBuffer creates an unlinked shared memory file, maps it, fills it via
// fill and sends the pool descriptor. The client descriptor is closed after the
// request is written; the mapping and buffer stay alive until destroy.
func (a *app) createShmBuffer(width int32, height int32, fill func([]byte)) (*shmBuffer, error) {
	size := int(width) * int(height) * 4
	f, err := os.CreateTemp("", "wayland-go-shm-*")
	if err != nil {
		return nil, fmt.Errorf("create shm file: %w", err)
	}
	if err := os.Remove(f.Name()); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("unlink shm file: %w", err)
	}
	if err := f.Truncate(int64(size)); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("truncate shm file: %w", err)
	}
	mmap, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("mmap shm file: %w", err)
	}
	fill(mmap)
	pool, err := a.shm.CreatePool(int(f.Fd()), int32(size))
	if err != nil {
		_ = f.Close()
		_ = syscall.Munmap(mmap)
		return nil, fmt.Errorf("wl_shm.create_pool: %w", err)
	}
	// The kernel copied the descriptor into the socket; close the client's
	// copy. The library does not close descriptors passed to requests.
	_ = f.Close()
	buffer, err := pool.CreateBuffer(0, width, height, width*4, wayland.ShmFormatArgb8888, bufferHandler{})
	if err != nil {
		_ = pool.Destroy()
		_ = syscall.Munmap(mmap)
		return nil, fmt.Errorf("wl_shm_pool.create_buffer: %w", err)
	}
	return &shmBuffer{pool: pool, buffer: buffer, mmap: mmap}, nil
}

// fillCheckerboard paints the parent background with two dark tones.
func fillCheckerboard(buf []byte) {
	for y := 0; y < parentHeight; y++ {
		row := y * parentWidth * 4
		for x := 0; x < parentWidth; x++ {
			color := checkerDark
			if (x/checkerCell+y/checkerCell)%2 == 0 {
				color = checkerLight
			}
			binary.LittleEndian.PutUint32(buf[row+x*4:], color)
		}
	}
}

// fillChild paints the child as an opaque square with a contrasting border and
// a centered crosshair that makes its translation visible.
func fillChild(buf []byte) {
	centerX, centerY := childSize/2, childSize/2
	for y := 0; y < childSize; y++ {
		row := y * childSize * 4
		for x := 0; x < childSize; x++ {
			color := childFill
			switch {
			case x < childBorderPx || y < childBorderPx ||
				x >= childSize-childBorderPx || y >= childSize-childBorderPx:
				color = childBorder
			case x >= centerX-markerThickPx && x < centerX+markerThickPx &&
				y >= centerY-markerArmPx && y < centerY+markerArmPx:
				color = childMarker
			case y >= centerY-markerThickPx && y < centerY+markerThickPx &&
				x >= centerX-markerArmPx && x < centerX+markerArmPx:
				color = childMarker
			}
			binary.LittleEndian.PutUint32(buf[row+x*4:], color)
		}
	}
}

// orbitPosition places the child on an ellipse around the parent center. The
// radii keep the 120x120 child inside the 480x320 parent on both axes.
func orbitPosition(ms int64) (int32, int32) {
	const (
		radiusX = (parentWidth-childSize)/2 - 20
		radiusY = (parentHeight-childSize)/2 - 20
	)
	angle := 2 * math.Pi * float64(ms%orbitPeriodMS) / float64(orbitPeriodMS)
	centerX := float64(parentWidth-childSize) / 2
	centerY := float64(parentHeight-childSize) / 2
	return int32(math.Round(centerX + radiusX*math.Cos(angle))),
		int32(math.Round(centerY + radiusY*math.Sin(angle)))
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

// waitForConfigure dispatches until the first xdg_surface.configure event has
// been handled. A compositor may deliver it after the sync callback that ends
// the first roundtrip, so retry a bounded number of roundtrips instead of
// assuming one is enough.
func waitForConfigure(d *wayland.Display, a *app) error {
	for attempt := 0; attempt < 3 && !a.mapped; attempt++ {
		if a.callbackErr != nil {
			return a.callbackErr
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := d.Roundtrip(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("wait for xdg configure: %w", err)
		}
	}
	if a.callbackErr != nil {
		return a.callbackErr
	}
	return nil
}

func run() error {
	d, err := connect()
	if err != nil {
		return err
	}
	a := &app{}
	defer func() {
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "subsurface: close:", cerr)
		}
	}()
	defer a.destroy()

	var globals []wayland.Global
	reg, err := d.GetRegistry(registryHandler{globals: &globals})
	if err != nil {
		return fmt.Errorf("get_registry: %w", err)
	}
	if err := roundtrip(d, "initial roundtrip"); err != nil {
		return err
	}
	if len(globals) == 0 {
		return fmt.Errorf("no globals advertised")
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
	subcompositorGlobal, ok := findGlobal(globals, "wl_subcompositor")
	if !ok {
		return fmt.Errorf("missing required global wl_subcompositor")
	}

	compositor, err := wayland.BindCompositor(reg, compositorGlobal)
	if err != nil {
		return fmt.Errorf("bind wl_compositor: %w", err)
	}
	fmtFormats := &shmHandler{formats: make(map[uint32]bool)}
	shm, err := wayland.BindShm(reg, shmGlobal, fmtFormats)
	if err != nil {
		return fmt.Errorf("bind wl_shm: %w", err)
	}
	a.shm = shm
	wmBaseWatcher := &wmBaseHandler{app: a}
	wmBase, err := xdgshell.BindWmBase(reg, wmBaseGlobal, wmBaseWatcher)
	if err != nil {
		return fmt.Errorf("bind xdg_wm_base: %w", err)
	}
	a.wmBase = wmBase
	seatState := &seatHandler{}
	seat, err := wayland.BindSeat(reg, seatGlobal, seatState)
	if err != nil {
		return fmt.Errorf("bind wl_seat: %w", err)
	}
	subcompositor, err := wayland.BindSubcompositor(reg, subcompositorGlobal)
	if err != nil {
		return fmt.Errorf("bind wl_subcompositor: %w", err)
	}
	a.subcompositor = subcompositor

	if err := roundtrip(d, "seat and shm roundtrip"); err != nil {
		return err
	}
	if !fmtFormats.formats[uint32(wayland.ShmFormatArgb8888)] {
		return fmt.Errorf("wl_shm did not advertise ARGB8888")
	}

	if _, err := seat.GetKeyboard(&keyboardHandler{app: a}); err != nil {
		return fmt.Errorf("wl_seat.get_keyboard: %w", err)
	}

	parentBuf, err := a.createShmBuffer(parentWidth, parentHeight, fillCheckerboard)
	if err != nil {
		return err
	}
	a.parentBuf = parentBuf
	childBuf, err := a.createShmBuffer(childSize, childSize, fillChild)
	if err != nil {
		return err
	}
	a.childBuf = childBuf

	parentSurface, err := compositor.CreateSurface(wlSurfaceHandler{})
	if err != nil {
		return fmt.Errorf("wl_compositor.create_surface: %w", err)
	}
	a.parentSurface = parentSurface
	xdgSurface, err := wmBase.GetXdgSurface(parentSurface, &xdgSurfaceHandler{app: a})
	if err != nil {
		return fmt.Errorf("xdg_wm_base.get_xdg_surface: %w", err)
	}
	a.xdgSurface = xdgSurface
	toplevel, err := xdgSurface.GetToplevel(&toplevelHandler{app: a})
	if err != nil {
		return fmt.Errorf("xdg_surface.get_toplevel: %w", err)
	}
	a.toplevel = toplevel
	if err := toplevel.SetTitle(windowTitle); err != nil {
		return fmt.Errorf("xdg_toplevel.set_title: %w", err)
	}
	// Fixed-size parent: set the window geometry once and never resize.
	if err := xdgSurface.SetWindowGeometry(0, 0, parentWidth, parentHeight); err != nil {
		return fmt.Errorf("xdg_surface.set_window_geometry: %w", err)
	}

	// The child surface has no role of its own; the subsurface role is assigned
	// by wl_subcompositor and places it inside the parent surface.
	childSurface, err := compositor.CreateSurface(wlSurfaceHandler{})
	if err != nil {
		return fmt.Errorf("wl_compositor.create_surface (child): %w", err)
	}
	a.childSurface = childSurface
	subsurface, err := subcompositor.GetSubsurface(childSurface, parentSurface)
	if err != nil {
		return fmt.Errorf("wl_subcompositor.get_subsurface: %w", err)
	}
	a.subsurface = subsurface
	// Synchronized mode is the default for new subsurfaces, but request it
	// explicitly: the child state is cached and applied atomically on the
	// parent's commit, which keeps position and repaint in lockstep.
	if err := subsurface.SetSync(); err != nil {
		return fmt.Errorf("wl_subsurface.set_sync: %w", err)
	}

	if err := parentSurface.Commit(); err != nil {
		return fmt.Errorf("initial wl_surface.commit: %w", err)
	}
	if err := waitForConfigure(d, a); err != nil {
		return err
	}
	if !a.mapped {
		return fmt.Errorf("xdg_surface did not send configure")
	}

	fmt.Println("subsurface: 480x320 window with an animated 120x120 synchronized child; close the window or press Esc to exit")

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
		fmt.Fprintln(os.Stderr, "subsurface:", err)
		os.Exit(1)
	}
}
