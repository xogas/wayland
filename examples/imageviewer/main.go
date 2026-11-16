// Command imageviewer displays a PNG or JPEG image in an xdg-shell window.
//
// Usage: go run ./examples/imageviewer [path]
//
// Without an argument it draws a procedural test image.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	xdgshell "github.com/xogas/wayland/protocols/stable/xdg-shell"
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
	// Every configure must be acknowledged, including repeated ones.
	if err := a.xdgSurface.AckConfigure(serial); err != nil {
		a.setErr(fmt.Errorf("xdg_surface.ack_configure: %w", err))
		return
	}
	if a.mapped {
		return
	}
	// The image is ready before the first commit, so map it on the first
	// configure and keep the same buffer attached for the window lifetime.
	if err := a.surface.Attach(a.buffer, 0, 0); err != nil {
		a.setErr(fmt.Errorf("wl_surface.attach: %w", err))
		return
	}
	if err := a.surface.Damage(0, 0, a.width, a.height); err != nil {
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

// Configure ignores the suggested size: this is a fixed-size demo, and window
// and buffer are pinned to the image size.
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
	// The descriptor belongs to the client; this example does not build a
	// keymap, so close it immediately.
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
	shm           *wayland.Shm
	wmBase        *xdgshell.WmBase
	surface       *wayland.Surface
	xdgSurface    *xdgshell.Surface
	toplevel      *xdgshell.Toplevel
	buffer        *wayland.Buffer
	pool          *wayland.ShmPool
	mmap          []byte
	width, height int32
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
	// Children before parents; the requests must stay valid on the wire.
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

// loadImage decodes the file at path, or returns the generated pattern when
// path is empty.
func loadImage(path string) (image.Image, error) {
	if path == "" {
		return generatedPattern(), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return img, nil
}

// generatedPattern builds the fallback image: a diagonal ramp, a checker
// overlay and a light border that makes a wrong window or buffer size visible.
func generatedPattern() *image.RGBA {
	const w, h, border, cell = 480, 320, 4, 32
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{
				R: uint8(x * 255 / (w - 1)),
				G: uint8(y * 255 / (h - 1)),
				B: uint8(255 - (x+y)*255/(w+h-2)),
				A: 255,
			}
			if (x/cell+y/cell)%2 == 0 {
				c.R = uint8(min(int(c.R)+48, 255))
				c.G = uint8(min(int(c.G)+48, 255))
				c.B = uint8(min(int(c.B)+48, 255))
			}
			if x < border || y < border || x >= w-border || y >= h-border {
				c = color.RGBA{R: 0xf5, G: 0xf5, B: 0xf5, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// fitSize caps an image at 1024x768 while preserving its aspect ratio. It
// never upscales: images that already fit keep their native size.
func fitSize(w, h int) (int, int) {
	const maxWidth, maxHeight = 1024, 768
	if w <= maxWidth && h <= maxHeight {
		return w, h
	}
	// Cross multiplication picks the limiting dimension without floating point.
	if w*maxHeight >= h*maxWidth {
		return maxWidth, max(1, h*maxWidth/w)
	}
	return max(1, w*maxHeight/h), maxHeight
}

// blitARGB8888 writes img into dst, an ARGB8888 wl_shm mapping of dstW x dstH
// pixels. Sampling is nearest-neighbor: destination pixel i takes source pixel
// (2*i+1)*src/(2*dst), the source pixel nearest its center.
func blitARGB8888(dst []byte, img image.Image, dstW, dstH int) {
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	rgba, isRGBA := img.(*image.RGBA)
	for y := 0; y < dstH; y++ {
		sy := b.Min.Y + (2*y+1)*srcH/(2*dstH)
		row := dst[y*dstW*4 : (y+1)*dstW*4]
		for x := 0; x < dstW; x++ {
			sx := b.Min.X + (2*x+1)*srcW/(2*dstW)
			var v uint32
			if isRGBA {
				// image.RGBA stores alpha-premultiplied bytes in the wl_shm
				// order; PixOffset accounts for the bounds of a sub-image.
				o := rgba.PixOffset(sx, sy)
				v = uint32(rgba.Pix[o+3])<<24 | uint32(rgba.Pix[o])<<16 |
					uint32(rgba.Pix[o+1])<<8 | uint32(rgba.Pix[o+2])
			} else {
				// wl_shm wants alpha-premultiplied bytes. Normalize through
				// color.NRGBAModel so non-premultiplied sources (image.NRGBA
				// is what the PNG decoder returns for images with alpha) give
				// unpremultiplied 8-bit components, then premultiply here.
				c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
				a := uint32(c.A)
				v = a<<24 | uint32(c.R)*a/255<<16 | uint32(c.G)*a/255<<8 | uint32(c.B)*a/255
			}
			binary.LittleEndian.PutUint32(row[x*4:], v)
		}
	}
}

// createShmBuffer creates the single wl_shm buffer backing the window and fills
// it with img. The mapping is written in place before the buffer is advertised,
// so the compositor never sees a partially rendered frame.
func (a *app) createShmBuffer(width, height int, img image.Image) error {
	size := width * height * 4
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
	a.mmap = mmap
	blitARGB8888(mmap, img, width, height)
	pool, err := a.shm.CreatePool(int(f.Fd()), int32(size))
	if err != nil {
		return fmt.Errorf("wl_shm.create_pool: %w", err)
	}
	// The kernel copied the descriptor into the socket; the deferred Close
	// drops the client's copy after the requests are written.
	a.pool = pool
	buffer, err := pool.CreateBuffer(0, int32(width), int32(height), int32(width*4), wayland.ShmFormatArgb8888, bufferHandler{})
	if err != nil {
		return fmt.Errorf("wl_shm_pool.create_buffer: %w", err)
	}
	a.buffer = buffer
	return nil
}

func run() error {
	path := ""
	if len(os.Args) > 2 {
		return errors.New("usage: imageviewer [path]")
	}
	if len(os.Args) == 2 {
		path = os.Args[1]
	}
	img, err := loadImage(path)
	if err != nil {
		return err
	}
	bounds := img.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	dstW, dstH := fitSize(srcW, srcH)
	label, title := "generated pattern", "imageviewer: generated pattern"
	if path != "" {
		label, title = path, "imageviewer: "+filepath.Base(path)
	}
	fmt.Printf("imageviewer: %s %dx%d -> %dx%d ARGB8888\n", label, srcW, srcH, dstW, dstH)

	d, err := connect()
	if err != nil {
		return err
	}
	a := &app{width: int32(dstW), height: int32(dstH)}
	defer func() {
		if a.mmap != nil {
			_ = syscall.Munmap(a.mmap)
		}
	}()
	defer func() {
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "imageviewer: close:", cerr)
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
	for _, name := range []string{"wl_compositor", "wl_shm", "xdg_wm_base", "wl_seat"} {
		if _, ok := findGlobal(globals, name); !ok {
			return fmt.Errorf("missing required global %s", name)
		}
	}
	compositorGlobal, _ := findGlobal(globals, "wl_compositor")
	shmGlobal, _ := findGlobal(globals, "wl_shm")
	wmBaseGlobal, _ := findGlobal(globals, "xdg_wm_base")
	seatGlobal, _ := findGlobal(globals, "wl_seat")

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

	if err := roundtrip(d, "seat and shm roundtrip"); err != nil {
		return err
	}
	if !fmtFormats.formats[uint32(wayland.ShmFormatArgb8888)] {
		return fmt.Errorf("wl_shm did not advertise ARGB8888")
	}
	if _, err := seat.GetKeyboard(&keyboardHandler{app: a}); err != nil {
		return fmt.Errorf("wl_seat.get_keyboard: %w", err)
	}
	if err := a.createShmBuffer(dstW, dstH, img); err != nil {
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
	if err := toplevel.SetTitle(title); err != nil {
		return fmt.Errorf("xdg_toplevel.set_title: %w", err)
	}
	if err := xdgSurface.SetWindowGeometry(0, 0, a.width, a.height); err != nil {
		return fmt.Errorf("xdg_surface.set_window_geometry: %w", err)
	}
	if err := surface.Commit(); err != nil {
		return fmt.Errorf("initial wl_surface.commit: %w", err)
	}
	// Usually the configure arrives before this roundtrip returns; a delayed one
	// is still handled by the dispatch loop below.
	if err := roundtrip(d, "initial commit roundtrip"); err != nil {
		return err
	}
	if a.callbackErr != nil {
		return a.callbackErr
	}

	fmt.Println("imageviewer: press Esc or close the window to exit")
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
		fmt.Fprintln(os.Stderr, "imageviewer:", err)
		os.Exit(1)
	}
}
