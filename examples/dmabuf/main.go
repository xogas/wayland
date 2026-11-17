// Command dmabuf opens an xdg-shell window backed by a linux-dmabuf wl_buffer.
//
// The pixels live in a DRM dumb buffer exported as a dma-buf with the raw DRM
// ioctls and imported by the compositor with zwp_linux_dmabuf_v1. Missing
// prerequisites (a DRM node, the global, an importable GPU) skip gracefully.
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
	"unsafe"

	"github.com/xogas/wayland"
	linuxdmabuf "github.com/xogas/wayland/protocols/stable/linux-dmabuf"
	xdgshell "github.com/xogas/wayland/protocols/stable/xdg-shell"
)

const (
	windowTitle  = "dmabuf"
	bufferWidth  = 320
	bufferHeight = 240
	bufferBPP    = 32
	// DRM_FORMAT_ARGB8888, the little-endian "AR24" fourcc, which is what
	// linux-dmabuf carries instead of a wl_shm format enum.
	drmFormatARGB8888 = 0x34325241

	// DRM ioctl request numbers encode the direction and argument size, so they
	// must match the structs below; the kernel rejects a mismatch with EINVAL.
	drmIoctlModeCreateDumb  = 0xc02064b2 // _IOWR('d', 0xb2), drm_mode_create_dumb, 32 bytes
	drmIoctlModeMapDumb     = 0xc01064b3 // _IOWR('d', 0xb3), drm_mode_map_dumb, 16 bytes
	drmIoctlModeDestroyDumb = 0xc00464b4 // _IOW('d', 0xb4), drm_mode_destroy_dumb, 4 bytes
	drmIoctlPrimeHandleToFD = 0xc00c642d // _IOWR('d', 0x2d), drm_prime_handle, 12 bytes
)

// The field order and alignment mirror struct drm_mode_create_dumb from
// drm_mode.h; the kernel rejects a mismatch with EINVAL.
type drmModeCreateDumb struct {
	Height, Width, Bpp, Flags, Handle, Pitch uint32
	Size                                     uint64
}

// Pad keeps Offset 8-byte aligned, as struct drm_mode_map_dumb requires.
type drmModeMapDumb struct {
	Handle, Pad uint32
	Offset      uint64
}

type drmModeDestroyDumb struct{ Handle uint32 }

// Flags 0 means no DRM_CLOEXEC and no DRM_RDWR; Fd is a new client-owned
// dma-buf descriptor.
type drmPrimeHandle struct {
	Handle, Flags uint32
	Fd            int32
}

var (
	errNoDRMDevice = errors.New("no DRM device under /dev/dri; this example needs a DRM node")
	// A render node can forbid dumb buffers that a card node accepts; the wrap
	// keeps the errno of the last attempt.
	errNoDumbBuffer = errors.New("no DRM device under /dev/dri accepted a dumb buffer; this example needs a DRM node that supports dumb buffers")
)

// skip reports an environment limitation on stderr and returns nil, so main
// exits 0 on a machine that cannot run this example.
func skip(err error) error {
	fmt.Fprintln(os.Stderr, "dmabuf:", err)
	return nil
}

// drmDevice is an open DRM node plus the dumb buffer and dma-buf exported from
// it; close releases whatever was allocated, so it is safe to defer.
type drmDevice struct {
	path    string
	file    *os.File
	fd      int
	handle  uint32
	pitch   uint32
	size    uint64
	mapped  []byte
	primeFD int
}

// openDRMDevice returns a node that can allocate a dumb buffer, trying render
// nodes (no DRM master needed) then card nodes; CREATE_DUMB failures are skipped.
func openDRMDevice() (*drmDevice, error) {
	paths := []string{"/dev/dri/renderD128", "/dev/dri/renderD129", "/dev/dri/renderD130"}
	for i := 0; i < 10; i++ {
		paths = append(paths, fmt.Sprintf("/dev/dri/card%d", i))
	}
	var lastDumbErr error
	opened := false
	for _, path := range paths {
		f, err := os.OpenFile(path, os.O_RDWR|syscall.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		opened = true
		dev := &drmDevice{path: path, file: f, fd: int(f.Fd()), primeFD: -1}
		if err := dev.createDumb(); err != nil {
			lastDumbErr = err
			_ = f.Close()
			continue
		}
		return dev, nil
	}
	if opened {
		return nil, fmt.Errorf("%w (last error: %w)", errNoDumbBuffer, lastDumbErr)
	}
	return nil, errNoDRMDevice
}

// drmIoctl issues one ioctl(2). The argument is valid only during the call: the
// kernel copies the fixed struct in and out, so callers pass unsafe.Pointer.
func drmIoctl(fd int, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// createDumb allocates the buffer fillPattern paints directly: dumb buffers are
// CPU-mappable, so no GPU command stream is needed.
func (d *drmDevice) createDumb() error {
	arg := drmModeCreateDumb{Width: bufferWidth, Height: bufferHeight, Bpp: bufferBPP}
	if err := drmIoctl(d.fd, drmIoctlModeCreateDumb, unsafe.Pointer(&arg)); err != nil {
		return fmt.Errorf("DRM_IOCTL_MODE_CREATE_DUMB: %w", err)
	}
	// The pitch is driver-chosen and may exceed width*4; the buffer must cover
	// pitch*height, because the compositor validates offset+stride*height.
	if uint64(arg.Pitch)*uint64(bufferHeight) > arg.Size {
		return fmt.Errorf("DRM_IOCTL_MODE_CREATE_DUMB: pitch %d x height %d exceeds size %d", arg.Pitch, bufferHeight, arg.Size)
	}
	d.handle = arg.Handle
	d.pitch = arg.Pitch
	d.size = arg.Size
	fmt.Printf("dmabuf: %s: %dx%d bpp %d dumb buffer, pitch=%d size=%d\n", d.path, bufferWidth, bufferHeight, bufferBPP, arg.Pitch, arg.Size)
	return nil
}

func (d *drmDevice) mapDumb() error {
	arg := drmModeMapDumb{Handle: d.handle}
	if err := drmIoctl(d.fd, drmIoctlModeMapDumb, unsafe.Pointer(&arg)); err != nil {
		return fmt.Errorf("DRM_IOCTL_MODE_MAP_DUMB: %w", err)
	}
	// MAP_SHARED is required: a private mapping would copy the pages and the
	// dma-buf would never observe the pixels written to it.
	m, err := syscall.Mmap(d.fd, int64(arg.Offset), int(d.size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap dumb buffer: %w", err)
	}
	d.mapped = m
	return nil
}

// fillPattern writes an opaque ARGB8888 pattern; alpha 0xff means premultiplied
// values are the straight ones.
func fillPattern(pixels []byte, width int, height int, pitch int) {
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g := uint32(x)*255/uint32(width-1), uint32(y)*255/uint32(height-1)
			b := uint32(0x30)
			if (x+y)/16%2 == 0 { // diagonal stripes
				r, g, b = min(r+0x40, 0xff), min(g+0x20, 0xff), 0xa0
			}
			if x < 8 || y < 8 || x >= width-8 || y >= height-8 { // border
				r, g, b = 0xff, 0xff, 0xff
			}
			// Little-endian uint32 0xAARRGGBB, hence r<<16.
			binary.LittleEndian.PutUint32(pixels[y*pitch+x*4:], 0xff000000|r<<16|g<<8|b)
		}
	}
}

// exportPrime turns the dumb-buffer handle into a dma-buf descriptor. The
// descriptor is new and client-owned; the handle lives until MODE_DESTROY_DUMB.
func (d *drmDevice) exportPrime() error {
	arg := drmPrimeHandle{Handle: d.handle}
	if err := drmIoctl(d.fd, drmIoctlPrimeHandleToFD, unsafe.Pointer(&arg)); err != nil {
		return fmt.Errorf("DRM_IOCTL_PRIME_HANDLE_TO_FD: %w", err)
	}
	if arg.Fd < 0 {
		return fmt.Errorf("DRM_IOCTL_PRIME_HANDLE_TO_FD: invalid fd %d", arg.Fd)
	}
	d.primeFD = int(arg.Fd)
	// flags 0 means the kernel did not set FD_CLOEXEC; set it here so the
	// descriptor cannot leak into a child process. Best effort, this never execs.
	_, _, _ = syscall.Syscall(syscall.SYS_FCNTL, uintptr(d.primeFD), syscall.F_SETFD, syscall.FD_CLOEXEC)
	return nil
}

func (d *drmDevice) close() {
	if d.mapped != nil {
		_ = syscall.Munmap(d.mapped)
	}
	if d.handle != 0 {
		arg := drmModeDestroyDumb{Handle: d.handle}
		// A failure only leaks buffer memory until the DRM fd closes; ignore it.
		_ = drmIoctl(d.fd, drmIoctlModeDestroyDumb, unsafe.Pointer(&arg))
	}
	if d.primeFD >= 0 {
		_ = syscall.Close(d.primeFD)
	}
	if d.file != nil {
		_ = d.file.Close()
	}
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

// shmFormatHandler drops wl_shm.format events: no shm pool is created, wl_shm is
// only kept bound as the fallback a full client would use.
type shmFormatHandler struct{}

func (shmFormatHandler) Format(uint32) {}

// dmabufHandler drops the advertised format/modifier pairs, which only become
// meaningful with the v4 feedback tranches; the first commit reveals mismatches.
type dmabufHandler struct{}

func (dmabufHandler) Format(uint32)                   {}
func (dmabufHandler) Modifier(uint32, uint32, uint32) {}

// seatHandler ignores wl_seat.name and capabilities; only the keyboard is used.
type seatHandler struct{}

func (seatHandler) Capabilities(uint32) {}
func (seatHandler) Name(string)         {}

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

// Configure acks every serial; only the first one maps the window.
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
	if err := a.surface.Damage(0, 0, bufferWidth, bufferHeight); err != nil {
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

func (h *toplevelHandler) Configure(width int32, height int32, states []byte) {
	fmt.Printf("dmabuf: xdg_toplevel.configure width=%d height=%d states=%d\n", width, height, len(states))
}
func (h *toplevelHandler) Close()                       { h.app.stopping.Store(true) }
func (h *toplevelHandler) ConfigureBounds(int32, int32) {}
func (h *toplevelHandler) WmCapabilities([]byte)        {}

// bufferParamsHandler satisfies the non-nil handler requirement of
// create_params. create_immed reports import failures as a fatal protocol error
// (handled by run) or as an invalid wl_buffer plus 'failed' (ignored here).
type bufferParamsHandler struct{}

func (bufferParamsHandler) Created(*wayland.Proxy) {}
func (bufferParamsHandler) Failed()                {}

// dmabufBufferHandler ignores wl_buffer.release: the buffer is never reused.
type dmabufBufferHandler struct{}

func (dmabufBufferHandler) Release() {}

type keyboardHandler struct {
	app *app
}

// Keymap closes the keymap fd: this example does not build a keymap, and closing
// our copy does not affect the keymap object.
func (h *keyboardHandler) Keymap(_ uint32, fd int, _ uint32) {
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

type app struct {
	dmabuf      *linuxdmabuf.DmabufV1
	wmBase      *xdgshell.WmBase
	surface     *wayland.Surface
	xdgSurface  *xdgshell.Surface
	toplevel    *xdgshell.Toplevel
	keyboard    *wayland.Keyboard
	buffer      *wayland.Buffer
	params      *linuxdmabuf.BufferParamsV1
	mapped      bool
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
	if a.params != nil {
		_ = a.params.Destroy()
	}
	if a.dmabuf != nil {
		_ = a.dmabuf.Destroy()
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

// createDmabufBuffer imports the exported dma-buf and creates the wl_buffer.
func (a *app) createDmabufBuffer(dev *drmDevice) error {
	params, err := a.dmabuf.CreateParams(bufferParamsHandler{})
	if err != nil {
		return fmt.Errorf("zwp_linux_dmabuf_v1.create_params: %w", err)
	}
	a.params = params
	// Plane 0 at offset 0 with DRM_FORMAT_MOD_LINEAR (both modifier halves zero).
	// The message is written at once, so the kernel duplicates the descriptor and
	// the client could close its copy here; it keeps it until teardown instead.
	if err := params.Add(dev.primeFD, 0, 0, dev.pitch, 0, 0); err != nil {
		return fmt.Errorf("zwp_linux_buffer_params_v1.add: %w", err)
	}
	buffer, err := params.CreateImmed(bufferWidth, bufferHeight, drmFormatARGB8888, 0, dmabufBufferHandler{})
	if err != nil {
		return fmt.Errorf("zwp_linux_buffer_params_v1.create_immed: %w", err)
	}
	a.buffer = buffer
	return nil
}

// waitForConfigure dispatches until the first xdg_surface.configure event, which
// the compositor may deliver after the roundtrip's sync callback.
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

// bufferRejected turns the fatal protocol error caused by the buffer import into
// the documented runtime limitation: a compositor can refuse a linear ARGB8888
// dma-buf allocated on a different DRM device. A full implementation would query
// zwp_linux_dmabuf_v1.get_default_feedback and allocate for the main device.
func bufferRejected(err error) error {
	return skip(fmt.Errorf("compositor rejected the dma-buf buffer (%w); a full client would allocate on the compositor's main device from zwp_linux_dmabuf_v1 feedback", err))
}

func run() error {
	dev, err := openDRMDevice()
	if errors.Is(err, errNoDRMDevice) || errors.Is(err, errNoDumbBuffer) {
		return skip(err)
	}
	if err != nil {
		return err
	}
	defer dev.close()

	if err := dev.mapDumb(); err != nil {
		return err
	}
	fillPattern(dev.mapped, bufferWidth, bufferHeight, int(dev.pitch))
	if err := dev.exportPrime(); err != nil {
		return err
	}

	d, err := connect()
	if err != nil {
		return err
	}
	a := &app{}
	defer func() {
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "dmabuf: close:", cerr)
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
	// Globals the window needs; zwp_linux_dmabuf_v1 is a skip, not an error.
	for _, name := range []string{"wl_compositor", "wl_shm", "xdg_wm_base", "wl_seat"} {
		if _, ok := findGlobal(globals, name); !ok {
			return fmt.Errorf("missing required global %s", name)
		}
	}
	compositorGlobal, _ := findGlobal(globals, "wl_compositor")
	shmGlobal, _ := findGlobal(globals, "wl_shm")
	wmBaseGlobal, _ := findGlobal(globals, "xdg_wm_base")
	seatGlobal, _ := findGlobal(globals, "wl_seat")
	dmabufGlobal, ok := findGlobal(globals, "zwp_linux_dmabuf_v1")
	if !ok {
		return skip(errors.New("compositor does not advertise zwp_linux_dmabuf_v1; skipping"))
	}
	// create_immed is since version 2; a v1 compositor cannot support this.
	if dmabufGlobal.Version < 2 {
		return skip(fmt.Errorf("zwp_linux_dmabuf_v1 version %d is too old for create_immed (needs >= 2); skipping", dmabufGlobal.Version))
	}

	compositor, err := wayland.BindCompositor(reg, compositorGlobal)
	if err != nil {
		return fmt.Errorf("bind wl_compositor: %w", err)
	}
	// wl_shm would be the fallback a full client uses; no shm buffer is created.
	if _, err := wayland.BindShm(reg, shmGlobal, shmFormatHandler{}); err != nil {
		return fmt.Errorf("bind wl_shm: %w", err)
	}
	wmBase, err := xdgshell.BindWmBase(reg, wmBaseGlobal, &wmBaseHandler{app: a})
	if err != nil {
		return fmt.Errorf("bind xdg_wm_base: %w", err)
	}
	a.wmBase = wmBase
	seat, err := wayland.BindSeat(reg, seatGlobal, seatHandler{})
	if err != nil {
		return fmt.Errorf("bind wl_seat: %w", err)
	}
	// At most version 3: version 2 is the minimum for create_immed and version 3
	// adds modifiers; a full client would bind v4+ for the feedback protocol.
	dmabufVersion := min(dmabufGlobal.Version, 3)
	dmabufProxy, err := reg.Bind(dmabufGlobal, dmabufVersion)
	if err != nil {
		return fmt.Errorf("bind zwp_linux_dmabuf_v1 version %d: %w", dmabufVersion, err)
	}
	a.dmabuf = linuxdmabuf.NewDmabufV1(dmabufProxy, dmabufHandler{})

	if err := roundtrip(d, "seat and dmabuf roundtrip"); err != nil {
		return err
	}
	keyboard, err := seat.GetKeyboard(&keyboardHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_seat.get_keyboard: %w", err)
	}
	a.keyboard = keyboard

	if err := a.createDmabufBuffer(dev); err != nil {
		return err
	}
	// create_immed imports the buffer server-side, so a rejection is already
	// pending; a roundtrip surfaces it before the window machinery runs.
	if err := roundtrip(d, "dmabuf buffer import"); err != nil {
		if errors.Is(err, wayland.ErrProtocolViolation) {
			return bufferRejected(err)
		}
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
	// A fixed-size window: min and max size pin the compositor to the buffer size.
	if err := toplevel.SetMinSize(bufferWidth, bufferHeight); err != nil {
		return fmt.Errorf("xdg_toplevel.set_min_size: %w", err)
	}
	if err := toplevel.SetMaxSize(bufferWidth, bufferHeight); err != nil {
		return fmt.Errorf("xdg_toplevel.set_max_size: %w", err)
	}
	if err := xdgSurface.SetWindowGeometry(0, 0, bufferWidth, bufferHeight); err != nil {
		return fmt.Errorf("xdg_surface.set_window_geometry: %w", err)
	}
	if err := surface.Commit(); err != nil {
		return fmt.Errorf("initial wl_surface.commit: %w", err)
	}
	if err := waitForConfigure(d, a); err != nil {
		if errors.Is(err, wayland.ErrProtocolViolation) {
			return bufferRejected(err)
		}
		return err
	}
	if !a.mapped {
		return fmt.Errorf("xdg_surface did not send configure")
	}

	fmt.Printf("dmabuf: %dx%d linear ARGB8888 buffer from %s; close the window or press Esc to exit\n", bufferWidth, bufferHeight, dev.path)
	for !a.stopping.Load() {
		if err := d.Dispatch(); err != nil {
			if a.stopping.Load() && errors.Is(err, wayland.ErrClosed) {
				break
			}
			// The commit can fail when the compositor imports the dma-buf on a
			// GPU it cannot share with; that is an environment limitation.
			if errors.Is(err, wayland.ErrProtocolViolation) {
				return bufferRejected(err)
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
		fmt.Fprintln(os.Stderr, "dmabuf:", err)
		os.Exit(1)
	}
}
