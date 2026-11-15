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
)

const (
	windowWidth  = 400
	windowHeight = 300
	solidColor   = 0xff228844
	windowTitle  = "wayland-go demo"
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
			*h.globals = append(gs[:i], gs[i+1:]...)
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

func (wlSurfaceHandler) Enter(output *wayland.Proxy)       {}
func (wlSurfaceHandler) Leave(output *wayland.Proxy)       {}
func (wlSurfaceHandler) PreferredBufferScale(factor int32) {}
func (wlSurfaceHandler) PreferredBufferTransform(uint32)   {}

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
	if err := a.surface.Attach(a.buffer, 0, 0); err != nil {
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
	fmt.Printf("xdg-shell: mapped %dx%d window, title %q\n", windowWidth, windowHeight, windowTitle)
}

type toplevelHandler struct {
	app *app
}

func (h *toplevelHandler) Configure(width int32, height int32, states []byte) {
	fmt.Printf("xdg_toplevel.configure width=%d height=%d states=%d\n", width, height, len(states))
}
func (h *toplevelHandler) Close()                       { h.app.stopping.Store(true) }
func (h *toplevelHandler) ConfigureBounds(int32, int32) {}
func (h *toplevelHandler) WmCapabilities([]byte)        {}

type bufferHandler struct {
	app *app
}

func (h *bufferHandler) Release() {
	h.app.released = true
}

type seatHandler struct {
	name         string
	hasName      bool
	capabilities uint32
}

func (h *seatHandler) Capabilities(capabilities uint32) {
	h.capabilities = capabilities
}

func (h *seatHandler) Name(name string) {
	h.name = name
	h.hasName = true
}

type keyboardHandler struct {
	app *app
}

func (h *keyboardHandler) Keymap(format uint32, fd int, size uint32) {
	// The caller owns the keymap descriptor; this example does not build a
	// keymap, so close it immediately.
	if fd >= 0 {
		_ = syscall.Close(fd)
	}
}

func (h *keyboardHandler) Enter(serial uint32, surface *wayland.Proxy, keys []byte) {}
func (h *keyboardHandler) Leave(serial uint32, surface *wayland.Proxy)              {}
func (h *keyboardHandler) Key(serial uint32, time uint32, key uint32, state uint32) {
	fmt.Printf("key keycode=%d state=%d\n", key, state)
	if key == 1 && (state == uint32(wayland.KeyboardKeyStatePressed) || state == uint32(wayland.KeyboardKeyStateRepeated)) {
		h.app.stopping.Store(true)
	}
}
func (h *keyboardHandler) Modifiers(serial uint32, modsDepressed uint32, modsLatched uint32, modsLocked uint32, group uint32) {
}
func (h *keyboardHandler) RepeatInfo(rate int32, delay int32) {}

type app struct {
	display     *wayland.Display
	shm         *wayland.Shm
	wmBase      *xdgshell.WmBase
	surface     *wayland.Surface
	xdgSurface  *xdgshell.Surface
	toplevel    *xdgshell.Toplevel
	keyboard    *wayland.Keyboard
	buffer      *wayland.Buffer
	pool        *wayland.ShmPool
	mmap        []byte
	mapped      bool
	released    bool
	stopping    atomic.Bool
	callbackErr error
}

func (a *app) setErr(err error) {
	if err != nil && a.callbackErr == nil {
		a.callbackErr = err
	}
}

func (a *app) destroy() {
	if a.toplevel != nil {
		_ = a.toplevel.Destroy()
	}
	if a.xdgSurface != nil {
		_ = a.xdgSurface.Destroy()
	}
	if a.surface != nil {
		_ = a.surface.Destroy()
	}
	if a.buffer != nil {
		_ = a.buffer.Destroy()
	}
	if a.pool != nil {
		_ = a.pool.Destroy()
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

// createShmBuffer creates an unlinked shared memory file, fills it with one
// ARGB8888 color and sends the pool descriptor. The client descriptor is
// closed after the request is written; the mapping and buffer stay alive until
// the connection closes.
func (a *app) createShmBuffer(width int32, height int32, color uint32) error {
	size := int(width) * int(height) * 4
	f, err := os.CreateTemp("", "wayland-go-shm-*")
	if err != nil {
		return fmt.Errorf("create shm file: %w", err)
	}
	if err := os.Remove(f.Name()); err != nil {
		_ = f.Close()
		return fmt.Errorf("unlink shm file: %w", err)
	}
	if err := f.Truncate(int64(size)); err != nil {
		_ = f.Close()
		return fmt.Errorf("truncate shm file: %w", err)
	}
	mmap, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("mmap shm file: %w", err)
	}
	for off := 0; off+4 <= len(mmap); off += 4 {
		binary.LittleEndian.PutUint32(mmap[off:off+4], color)
	}
	a.mmap = mmap
	pool, err := a.shm.CreatePool(int(f.Fd()), int32(size))
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("wl_shm.create_pool: %w", err)
	}
	// The kernel copied the descriptor into the socket; close the client's
	// copy. The library does not close descriptors passed to requests.
	_ = f.Close()
	a.pool = pool
	buffer, err := pool.CreateBuffer(0, width, height, width*4, wayland.ShmFormatArgb8888, &bufferHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_shm_pool.create_buffer: %w", err)
	}
	a.buffer = buffer
	return nil
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
	a := &app{display: d}
	defer func() {
		if a.mmap != nil {
			_ = syscall.Munmap(a.mmap)
		}
	}()
	defer func() {
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "xdg-shell: close:", cerr)
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

	if err := roundtrip(d, "seat and shm roundtrip"); err != nil {
		return err
	}
	if !fmtFormats.formats[uint32(wayland.ShmFormatArgb8888)] {
		return fmt.Errorf("wl_shm did not advertise ARGB8888")
	}
	seatLabel := "unknown"
	if seatState.hasName {
		seatLabel = seatState.name
	}
	fmt.Printf("seat name=%q capabilities=0x%x\n", seatLabel, seatState.capabilities)

	keyboard, err := seat.GetKeyboard(&keyboardHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_seat.get_keyboard: %w", err)
	}
	a.keyboard = keyboard

	if err := a.createShmBuffer(windowWidth, windowHeight, solidColor); err != nil {
		return err
	}

	surface, err := compositor.CreateSurface(wlSurfaceHandler{})
	if err != nil {
		return fmt.Errorf("wl_compositor.create_surface: %w", err)
	}
	a.surface = surface
	xdgSurface, err := wmBase.GetXdgSurface(surface, &xdgSurfaceHandler{app: a})
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
	if err := xdgSurface.SetWindowGeometry(0, 0, windowWidth, windowHeight); err != nil {
		return fmt.Errorf("xdg_surface.set_window_geometry: %w", err)
	}
	if err := surface.Commit(); err != nil {
		return fmt.Errorf("initial wl_surface.commit: %w", err)
	}
	if err := waitForConfigure(d, a); err != nil {
		return err
	}
	if !a.mapped {
		return fmt.Errorf("xdg_surface did not send configure")
	}

	fmt.Println("xdg-shell: press Esc or close the window to exit")
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
		fmt.Fprintln(os.Stderr, "xdg-shell:", err)
		os.Exit(1)
	}
}
