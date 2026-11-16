// Command clipboard copies and pastes text through wl_data_device.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	xdgshell "github.com/xogas/wayland/protocols/stable/xdg-shell"
)

const (
	windowWidth  = 240
	windowHeight = 140
	windowTitle  = "clipboard"
	// The window only exists to receive keyboard focus: input events supply the
	// serial that wl_data_device.set_selection requires.
	solidColor    = 0xff1b1b1b
	clipboardText = "hello from the wayland-go clipboard example\n"
	// The same bytes are served under both types; a paste prefers the UTF-8 one.
	mimeUTF8     = "text/plain;charset=utf-8"
	mimePlain    = "text/plain"
	pasteTimeout = 2 * time.Second // a stalled source must not hang the app
	quoteMax     = 120             // cap what one paste prints
)

// Linux input event codes; no keymap is built, so raw keycodes are used.
const (
	keyEsc   = 1  // KEY_ESC
	keyCopy  = 46 // KEY_C
	keyPaste = 47 // KEY_V
)

type registryHandler struct {
	globals *[]wayland.Global
}

func (h registryHandler) Global(g wayland.Global) {
	*h.globals = append(*h.globals, g)
}

func (h registryHandler) GlobalRemove(name uint32) {
	*h.globals = slices.DeleteFunc(*h.globals, func(g wayland.Global) bool { return g.Name == name })
}

type shmHandler struct {
	formats map[uint32]bool
}

func (h *shmHandler) Format(format uint32) {
	h.formats[format] = true
}

type wlSurfaceHandler struct{}

func (wlSurfaceHandler) Enter(_ *wayland.Proxy)            {}
func (wlSurfaceHandler) Leave(_ *wayland.Proxy)            {}
func (wlSurfaceHandler) PreferredBufferScale(_ int32)      {}
func (wlSurfaceHandler) PreferredBufferTransform(_ uint32) {}

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
}

type toplevelHandler struct {
	app *app
}

func (h *toplevelHandler) Configure(int32, int32, []byte) {}
func (h *toplevelHandler) Close()                         { h.app.stopping.Store(true) }
func (h *toplevelHandler) ConfigureBounds(int32, int32)   {}
func (h *toplevelHandler) WmCapabilities([]byte)          {}

type bufferHandler struct{}

func (bufferHandler) Release() {}

type seatHandler struct{}

func (seatHandler) Capabilities(uint32) {}
func (seatHandler) Name(string)         {}

type keyboardHandler struct {
	app *app
}

func (h *keyboardHandler) Keymap(_ uint32, fd int, _ uint32) {
	// The caller owns the keymap descriptor; this example builds no keymap.
	if fd >= 0 {
		_ = syscall.Close(fd)
	}
}

func (h *keyboardHandler) Enter(serial uint32, _ *wayland.Proxy, _ []byte) {
	h.app.setSerial(serial)
}
func (h *keyboardHandler) Leave(_ uint32, _ *wayland.Proxy) {}
func (h *keyboardHandler) Key(serial uint32, _ uint32, key uint32, state uint32) {
	h.app.setSerial(serial)
	if state != uint32(wayland.KeyboardKeyStatePressed) && state != uint32(wayland.KeyboardKeyStateRepeated) {
		return
	}
	switch key {
	case keyEsc:
		h.app.stopping.Store(true)
	case keyCopy:
		h.app.setErr(h.app.copySelection())
	case keyPaste:
		h.app.setErr(h.app.paste())
	}
}
func (h *keyboardHandler) Modifiers(_, _, _, _, _ uint32) {}
func (h *keyboardHandler) RepeatInfo(_, _ int32)          {}

// dataSourceHandler serves wl_data_source events while this client owns the
// selection; the compositor asks for the bytes through Send when a target
// pastes.
type dataSourceHandler struct {
	app *app
}

// Target is drag-and-drop feedback and is unused for selections.
func (h *dataSourceHandler) Target(_ *string) {}

// Send writes the clipboard text into the descriptor carried by the event. The
// client owns that descriptor and must close it; the library only closes
// descriptors belonging to discarded events.
func (h *dataSourceHandler) Send(mimeType string, fd int) {
	f := os.NewFile(uintptr(fd), "wl_data_source.send")
	_, writeErr := io.WriteString(f, clipboardText)
	if closeErr := f.Close(); closeErr != nil && writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		// A target that closes its read end is not our error; keep running.
		fmt.Fprintln(os.Stderr, "clipboard: write selection data:", writeErr)
		return
	}
	fmt.Printf("clipboard: source served %s\n", mimeType)
}

// Cancelled drops the source; a wl_data_source is single-use, so the next copy
// creates a fresh one.
func (h *dataSourceHandler) Cancelled() {
	if h.app.dataSource == nil {
		return
	}
	_ = h.app.dataSource.Destroy()
	h.app.dataSource = nil
	fmt.Println("clipboard: selection cancelled, source released")
}

func (h *dataSourceHandler) DndDropPerformed() {}
func (h *dataSourceHandler) DndFinished()      {}
func (h *dataSourceHandler) Action(_ uint32)   {}

// dataOfferHandler records the MIME types of one wl_data_offer. The offer is
// introduced by wl_data_device.data_offer and its types arrive in the
// immediately following wl_data_offer.offer events.
type dataOfferHandler struct {
	app *app
	id  uint32
}

func (h *dataOfferHandler) Offer(mimeType string) {
	h.app.offerMIME[h.id] = append(h.app.offerMIME[h.id], mimeType)
}

// SourceActions and Action are drag-and-drop negotiation.
func (h *dataOfferHandler) SourceActions(_ uint32) {}
func (h *dataOfferHandler) Action(_ uint32)        {}

// dataDeviceHandler tracks the offers the compositor introduces and which one
// carries the selection.
type dataDeviceHandler struct {
	app *app
}

// DataOffer introduces a server-created wl_data_offer. The object arrives
// without an event sink, so wrap it now: the offer's MIME type events follow
// immediately and would fail without a handler installed.
func (h *dataDeviceHandler) DataOffer(id *wayland.Proxy) {
	if id == nil {
		return
	}
	if _, exists := h.app.offers[id.ID()]; exists {
		return
	}
	h.app.wrapOffer(id)
}

// Enter, Leave, Motion, and Drop describe drag-and-drop positions.
func (h *dataDeviceHandler) Enter(uint32, *wayland.Proxy, float64, float64, *wayland.Proxy) {
}
func (h *dataDeviceHandler) Leave()                          {}
func (h *dataDeviceHandler) Motion(uint32, float64, float64) {}
func (h *dataDeviceHandler) Drop()                           {}

// Selection announces the offer that carries the clipboard. The compositor also
// sends it when keyboard focus changes, so the offer may describe another
// client's data or a compositor-provided selection.
func (h *dataDeviceHandler) Selection(id *wayland.Proxy) {
	a := h.app
	// The protocol requires the destination to destroy the previous selection
	// offer when a new selection event arrives.
	if a.currentOfferID != 0 {
		a.destroyOffer(a.currentOfferID)
	}
	if id == nil {
		fmt.Println("clipboard: selection cleared")
		return
	}
	oid := id.ID()
	if _, exists := a.offers[oid]; !exists {
		// Normally introduced by data_offer; wrap a stray one defensively.
		a.wrapOffer(id)
	}
	a.currentOfferID = oid
	fmt.Printf("clipboard: selection offer %d advertises mime types %q\n", oid, a.offerMIME[oid])
}

type app struct {
	shm        *wayland.Shm
	wmBase     *xdgshell.WmBase
	manager    *wayland.DataDeviceManager
	seat       *wayland.Seat
	keyboard   *wayland.Keyboard
	dataDevice *wayland.DataDevice
	dataSource *wayland.DataSource
	// offers owns every live wl_data_offer by wire id; currentOfferID marks the
	// one that carries the selection, if any.
	offers         map[uint32]*wayland.DataOffer
	offerMIME      map[uint32][]string
	currentOfferID uint32
	surface        *wayland.Surface
	xdgSurface     *xdgshell.Surface
	toplevel       *xdgshell.Toplevel
	buffer         *wayland.Buffer
	pool           *wayland.ShmPool
	mmap           []byte
	mapped         bool
	serial         uint32
	hasSerial      bool
	stopping       atomic.Bool
	callbackErr    error
}

func (a *app) setErr(err error) {
	if err != nil && a.callbackErr == nil {
		a.callbackErr = err
	}
}

// setSerial records the newest input serial; wl_data_device.set_selection only
// accepts a serial from a real input event. Both callers run on the dispatch
// goroutine, so no lock is needed.
func (a *app) setSerial(serial uint32) {
	a.serial = serial
	a.hasSerial = true
}

// wrapOffer installs the event sink of a server-created wl_data_offer; the
// compositor allocates offers with data_device.data_offer, never the registry.
func (a *app) wrapOffer(id *wayland.Proxy) *wayland.DataOffer {
	oid := id.ID()
	offer := wayland.NewDataOffer(id, &dataOfferHandler{app: a, id: oid})
	a.offers[oid] = offer
	return offer
}

// destroyOffer sends wl_data_offer.destroy and forgets the offer's MIME types.
func (a *app) destroyOffer(id uint32) {
	if offer, ok := a.offers[id]; ok {
		_ = offer.Destroy()
		delete(a.offers, id)
	}
	delete(a.offerMIME, id)
	if a.currentOfferID == id {
		a.currentOfferID = 0
	}
}

// destroy releases every protocol object, the selection side first.
func (a *app) destroy() {
	if a.dataSource != nil {
		_ = a.dataSource.Destroy()
	}
	for id := range a.offers {
		a.destroyOffer(id)
	}
	// release requests are version-gated: wl_data_device.release and
	// wl_keyboard.release since v3, wl_seat.release since v5. Sending one on a
	// lower-version object would be a protocol error.
	if a.dataDevice != nil && a.dataDevice.Proxy().Version() >= 3 {
		_ = a.dataDevice.Release()
	}
	if a.keyboard != nil && a.keyboard.Proxy().Version() >= 3 {
		_ = a.keyboard.Release()
	}
	if a.seat != nil && a.seat.Proxy().Version() >= 5 {
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

// createShmBuffer creates the single ARGB8888 buffer that fills the window.
func (a *app) createShmBuffer(width int32, height int32, color uint32) error {
	size := int(width) * int(height) * 4
	f, err := os.CreateTemp("", "wayland-go-shm-*")
	if err != nil {
		return fmt.Errorf("create shm file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := os.Remove(f.Name()); err != nil {
		return fmt.Errorf("unlink shm file: %w", err)
	}
	if err := f.Truncate(int64(size)); err != nil {
		return fmt.Errorf("truncate shm file: %w", err)
	}
	mmap, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap shm file: %w", err)
	}
	for off := 0; off+4 <= len(mmap); off += 4 {
		binary.LittleEndian.PutUint32(mmap[off:off+4], color)
	}
	a.mmap = mmap
	pool, err := a.shm.CreatePool(int(f.Fd()), int32(size))
	if err != nil {
		return fmt.Errorf("wl_shm.create_pool: %w", err)
	}
	// The kernel copied the descriptor into the socket; the deferred Close
	// drops the client's copy after the request is written.
	a.pool = pool
	buffer, err := pool.CreateBuffer(0, width, height, width*4, wayland.ShmFormatArgb8888, bufferHandler{})
	if err != nil {
		return fmt.Errorf("wl_shm_pool.create_buffer: %w", err)
	}
	a.buffer = buffer
	return nil
}

// copySelection offers clipboardText as the selection. A wl_data_source is
// single-use, so each copy creates a fresh source and drops the previous one.
func (a *app) copySelection() error {
	if !a.hasSerial {
		// Before the window has keyboard focus no serial would be accepted.
		fmt.Println("clipboard: no input serial yet, click the window and press c again")
		return nil
	}
	if a.dataSource != nil {
		_ = a.dataSource.Destroy()
		a.dataSource = nil
	}
	source, err := a.manager.CreateDataSource(&dataSourceHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_data_device_manager.create_data_source: %w", err)
	}
	a.dataSource = source
	for _, mimeType := range []string{mimeUTF8, mimePlain} {
		if err := source.Offer(mimeType); err != nil {
			return fmt.Errorf("wl_data_source.offer %q: %w", mimeType, err)
		}
	}
	if err := a.dataDevice.SetSelection(source, a.serial); err != nil {
		return fmt.Errorf("wl_data_device.set_selection: %w", err)
	}
	fmt.Printf("clipboard: copied %q with serial %d\n", clipboardText, a.serial)
	return nil
}

// paste asks the current selection for text and prints what arrives. The
// receive request is written immediately, so the server can start the transfer
// without further dispatch, and reading the pipe is a plain descriptor read
// that needs no event loop either. The read still runs asynchronously because
// the source may be this client, whose send event must be dispatched before it
// can close the write end.
func (a *app) paste() error {
	if a.currentOfferID == 0 {
		fmt.Println("clipboard: nothing to paste, no selection announced yet")
		return nil
	}
	offer, ok := a.offers[a.currentOfferID]
	if !ok {
		return fmt.Errorf("selection offer %d is no longer tracked", a.currentOfferID)
	}
	mimeType, ok := preferredMIME(a.offerMIME[a.currentOfferID])
	if !ok {
		fmt.Printf("clipboard: selection offers no text type; advertised %q\n", a.offerMIME[a.currentOfferID])
		return nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create paste pipe: %w", err)
	}
	// The library does not close descriptors passed to requests, so close the
	// client's copy of w once the kernel has duplicated it into the socket.
	if err := offer.Receive(mimeType, int(w.Fd())); err != nil {
		_ = r.Close()
		_ = w.Close()
		return fmt.Errorf("wl_data_offer.receive: %w", err)
	}
	if err := w.Close(); err != nil {
		_ = r.Close()
		return fmt.Errorf("close paste pipe: %w", err)
	}
	go readPaste(r)
	return nil
}

// preferredMIME returns the text type to request, preferring UTF-8 plain text;
// false means the offer advertises no text.
func preferredMIME(mimes []string) (string, bool) {
	for _, wanted := range []string{mimeUTF8, mimePlain} {
		if slices.Contains(mimes, wanted) {
			return wanted, true
		}
	}
	return "", false
}

// quoteText formats received data for the terminal and truncates long payloads
// so a single paste cannot flood it. Truncation may split a UTF-8 sequence; %q
// renders the invalid bytes as escapes.
func quoteText(s string) string {
	if len(s) <= quoteMax {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%q... (%d bytes)", s[:quoteMax], len(s))
}

// readPaste drains the read end of a paste pipe with a bounded wait.
func readPaste(r *os.File) {
	defer func() { _ = r.Close() }()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1) // buffered: a late read must not block
	go func() {
		data, err := io.ReadAll(r)
		done <- result{data: data, err: err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			fmt.Fprintln(os.Stderr, "clipboard: read paste data:", res.err)
			return
		}
		fmt.Printf("clipboard: pasted %s\n", quoteText(string(res.data)))
	case <-time.After(pasteTimeout):
		fmt.Printf("clipboard: paste timed out after %s\n", pasteTimeout)
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

func run() error {
	d, err := connect()
	if err != nil {
		return err
	}
	a := &app{
		offers:    make(map[uint32]*wayland.DataOffer),
		offerMIME: make(map[uint32][]string),
	}
	defer func() {
		if a.mmap != nil {
			_ = syscall.Munmap(a.mmap)
		}
	}()
	defer func() {
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "clipboard: close:", cerr)
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
	for _, name := range []string{"wl_compositor", "wl_shm", "xdg_wm_base", "wl_seat", "wl_data_device_manager"} {
		if _, ok := findGlobal(globals, name); !ok {
			return fmt.Errorf("missing required global %s", name)
		}
	}
	compositorGlobal, _ := findGlobal(globals, "wl_compositor")
	shmGlobal, _ := findGlobal(globals, "wl_shm")
	wmBaseGlobal, _ := findGlobal(globals, "xdg_wm_base")
	seatGlobal, _ := findGlobal(globals, "wl_seat")
	dataDeviceGlobal, _ := findGlobal(globals, "wl_data_device_manager")

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
	wmBase, err := xdgshell.BindWmBase(reg, wmBaseGlobal, &wmBaseHandler{app: a})
	if err != nil {
		return fmt.Errorf("bind xdg_wm_base: %w", err)
	}
	a.wmBase = wmBase
	seat, err := wayland.BindSeat(reg, seatGlobal, seatHandler{})
	if err != nil {
		return fmt.Errorf("bind wl_seat: %w", err)
	}
	a.seat = seat
	manager, err := wayland.BindDataDeviceManager(reg, dataDeviceGlobal)
	if err != nil {
		return fmt.Errorf("bind wl_data_device_manager: %w", err)
	}
	a.manager = manager

	if err := roundtrip(d, "seat and shm roundtrip"); err != nil {
		return err
	}
	if !fmtFormats.formats[uint32(wayland.ShmFormatArgb8888)] {
		return fmt.Errorf("wl_shm did not advertise ARGB8888")
	}

	keyboard, err := seat.GetKeyboard(&keyboardHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_seat.get_keyboard: %w", err)
	}
	a.keyboard = keyboard
	// The data device is per seat and usable once the seat reported its
	// capabilities; the compositor then routes selection events here.
	dataDevice, err := manager.GetDataDevice(seat, &dataDeviceHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_data_device_manager.get_data_device: %w", err)
	}
	a.dataDevice = dataDevice
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
	// Usually the configure arrives before this roundtrip returns; a delayed one
	// is still handled by the dispatch loop.
	if err := roundtrip(d, "initial commit roundtrip"); err != nil {
		return err
	}

	// Clipboard content can also be set by the compositor when keyboard focus
	// changes, for example a selection copied in another window; whatever the
	// compositor announces is printed by the Selection handler.
	fmt.Println("clipboard: press c to copy, v to paste; close the window or press Esc to exit")
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
		fmt.Fprintln(os.Stderr, "clipboard:", err)
		os.Exit(1)
	}
}
