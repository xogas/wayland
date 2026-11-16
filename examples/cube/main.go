// Command cube animates a software-rendered 3D cube in a fixed-size
// xdg-shell window.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	xdgshell "github.com/xogas/wayland/protocols/stable/xdg-shell"
)

const (
	windowWidth  = 320
	windowHeight = 320
	windowTitle  = "cube"

	// backgroundColor is opaque ARGB8888, like every pixel this example writes.
	backgroundColor = 0xff101018

	// Model-to-pixels setup: the camera sits at the view-space origin looking
	// down +z, the cube is pushed cameraDistance in front of it, and
	// focalLength is the pinhole-to-projection-plane distance in pixels.
	cameraDistance = 3.5
	focalLength    = 300.0

	centerX = float64(windowWidth) / 2
	centerY = float64(windowHeight) / 2

	// frameClockScale doubles the compositor's millisecond frame timestamp so
	// one full turn takes a couple of seconds. That timestamp is the only clock
	// the animation uses; no wall-clock read is needed.
	frameClockScale = 2.0
	yawPerMs        = 0.0011
	pitchPerMs      = 0.0007

	cubeVertexCount = 8
	cubeFaceCount   = 6
)

// screenPoint is a projected cube vertex in surface pixels.
type screenPoint struct {
	x, y int32
}

// vec3 is a point in model space. The cube is tiny, so float64 math is plenty.
type vec3 struct {
	x, y, z float64
}

// projection holds the perspective projection of all eight cube vertices plus
// the view-space depth of each, which the painter's sort consumes.
type projection struct {
	screen [cubeVertexCount]screenPoint
	depth  [cubeVertexCount]float64
}

// cubeVertices are the eight corners of the cube centered on the origin.
var cubeVertices = [cubeVertexCount]vec3{
	{-1, -1, -1},
	{+1, -1, -1},
	{+1, +1, -1},
	{-1, +1, -1},
	{-1, -1, +1},
	{+1, -1, +1},
	{+1, +1, +1},
	{-1, +1, +1},
}

// cubeFace is one quad: four cubeVertices indices in cyclic order around the
// face plus one opaque fill color. Opaque colors are what let a painter's
// algorithm overlap faces without leaking what is behind them.
type cubeFace struct {
	color   uint32
	corners [4]int
}

// cubeFaces lists all six sides with distinct colors.
var cubeFaces = [cubeFaceCount]cubeFace{
	{color: 0xff3a6ea5, corners: [4]int{0, 1, 2, 3}}, // z = -1
	{color: 0xffe05a47, corners: [4]int{4, 5, 6, 7}}, // z = +1
	{color: 0xff4caf7d, corners: [4]int{0, 3, 7, 4}}, // x = -1
	{color: 0xffd8a13a, corners: [4]int{1, 2, 6, 5}}, // x = +1
	{color: 0xff8e5bb5, corners: [4]int{0, 4, 5, 1}}, // y = -1
	{color: 0xff5ac8d8, corners: [4]int{3, 2, 6, 7}}, // y = +1
}

// faceDraw pairs a face with its average view depth so the six faces can be
// drawn far to near each frame.
type faceDraw struct {
	face  cubeFace
	depth float64
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

// Configure acks every configure, as the protocol requires. The window is
// fixed-size, so only the first configure is used: it starts the animation.
func (h *xdgSurfaceHandler) Configure(serial uint32) {
	a := h.app
	if err := a.xdgSurface.AckConfigure(serial); err != nil {
		a.setErr(fmt.Errorf("xdg_surface.ack_configure: %w", err))
		return
	}
	if a.started {
		// Later configures still got their ack; a fixed-size demo ignores the
		// configured size it cannot honor.
		return
	}
	// Clock 0 draws the first frame; the first wl_callback.done that follows
	// supplies the compositor timestamp every later frame is derived from.
	a.render(0)
	// wl_surface.frame only takes effect on the following commit, so the
	// callback for the next frame is requested before presenting this one.
	if err := a.requestFrame(); err != nil {
		a.setErr(err)
		return
	}
	if err := a.present(); err != nil {
		a.setErr(err)
		return
	}
	a.started = true
}

type toplevelHandler struct {
	app *app
}

// Configure ignores the compositor's suggested size: this is a fixed-size demo
// that always keeps its declared 320x320 geometry.
func (h *toplevelHandler) Configure(int32, int32, []byte) {}
func (h *toplevelHandler) Close()                         { h.app.stopping.Store(true) }
func (h *toplevelHandler) ConfigureBounds(int32, int32)   {}
func (h *toplevelHandler) WmCapabilities([]byte)          {}

// bufferHandler has nothing to track: the single shm buffer is only redrawn
// after the previous frame callback returned it to the client.
type bufferHandler struct{}

func (bufferHandler) Release() {}

// seatHandler ignores seat metadata; the keyboard is requested unconditionally
// right after the seat is bound, as in examples/xdg-shell.
type seatHandler struct{}

func (seatHandler) Capabilities(uint32) {}
func (seatHandler) Name(string)         {}

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
	if key == 1 && (state == uint32(wayland.KeyboardKeyStatePressed) || state == uint32(wayland.KeyboardKeyStateRepeated)) {
		h.app.stopping.Store(true)
	}
}

func (h *keyboardHandler) Modifiers(_, _, _, _, _ uint32) {
}

func (h *keyboardHandler) RepeatInfo(_, _ int32) {}

// frameHandler draws the next frame from the wl_callback.done timestamp and
// re-arms the frame callback, so exactly one is outstanding at a time.
type frameHandler struct {
	app *app
}

func (h *frameHandler) Done(callbackData uint32) {
	h.app.onFrame(callbackData)
}

type app struct {
	display    *wayland.Display
	shm        *wayland.Shm
	wmBase     *xdgshell.WmBase
	surface    *wayland.Surface
	xdgSurface *xdgshell.Surface
	toplevel   *xdgshell.Toplevel
	keyboard   *wayland.Keyboard
	buffer     *wayland.Buffer
	pool       *wayland.ShmPool
	pixels     []byte

	started      bool
	framePending bool
	stopping     atomic.Bool
	callbackErr  error
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

// createShmBuffer creates an unlinked shared memory file, maps it, and wraps it
// in a wl_shm pool plus one ARGB8888 buffer. The client descriptor is closed
// after the request is written; the mapping and the buffer stay alive until
// cleanup. The single buffer is safe because the next frame is only drawn after
// the previous frame's wl_callback.done, by which time the compositor released
// the old contents.
func (a *app) createShmBuffer(width, height int32) error {
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
	a.pixels = mmap
	pool, err := a.shm.CreatePool(int(f.Fd()), int32(size))
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("wl_shm.create_pool: %w", err)
	}
	// The kernel copied the descriptor into the socket; close the client's
	// copy. The library does not close descriptors passed to requests.
	_ = f.Close()
	a.pool = pool
	buffer, err := pool.CreateBuffer(0, width, height, width*4, wayland.ShmFormatArgb8888, bufferHandler{})
	if err != nil {
		return fmt.Errorf("wl_shm_pool.create_buffer: %w", err)
	}
	a.buffer = buffer
	return nil
}

// requestFrame arms exactly one wl_callback for the next presented frame. A
// frame request only takes effect on the next commit, so callers must commit
// right after this returns. The callback object is server-owned: the compositor
// reaps it after done, so the returned pointer is deliberately dropped and no
// method is ever called on it.
func (a *app) requestFrame() error {
	if a.framePending {
		return fmt.Errorf("wl_surface.frame: frame callback already outstanding")
	}
	if _, err := a.surface.Frame(&frameHandler{app: a}); err != nil {
		return fmt.Errorf("wl_surface.frame: %w", err)
	}
	a.framePending = true
	return nil
}

// present attaches the single buffer, damages the whole surface and commits.
// The commit also arms the frame callback requested just before it.
func (a *app) present() error {
	if err := a.surface.Attach(a.buffer, 0, 0); err != nil {
		return fmt.Errorf("wl_surface.attach: %w", err)
	}
	if err := a.surface.Damage(0, 0, windowWidth, windowHeight); err != nil {
		return fmt.Errorf("wl_surface.damage: %w", err)
	}
	if err := a.surface.Commit(); err != nil {
		return fmt.Errorf("wl_surface.commit: %w", err)
	}
	return nil
}

// onFrame runs from wl_callback.done: draw the next frame, arm the following
// frame callback and present. callbackData is the compositor frame clock in
// milliseconds; it is the animation's only time source.
func (a *app) onFrame(callbackData uint32) {
	if a.stopping.Load() {
		return
	}
	a.framePending = false
	a.render(float64(callbackData) * frameClockScale)
	if err := a.requestFrame(); err != nil {
		a.setErr(err)
		return
	}
	if err := a.present(); err != nil {
		a.setErr(err)
	}
}

// render clears the buffer and paints the six cube faces for the given clock
// value in (doubled) milliseconds.
func (a *app) render(clockMs float64) {
	for offset := 0; offset+4 <= len(a.pixels); offset += 4 {
		binary.LittleEndian.PutUint32(a.pixels[offset:offset+4], backgroundColor)
	}

	proj := projectCube(clockMs*yawPerMs, clockMs*pitchPerMs)

	var draws [cubeFaceCount]faceDraw
	for i, face := range cubeFaces {
		sum := 0.0
		for _, corner := range face.corners {
			sum += proj.depth[corner]
		}
		draws[i] = faceDraw{face: face, depth: sum / float64(len(face.corners))}
	}
	// Painter's algorithm: draw the farthest quad first so nearer opaque fills
	// cover it. A convex cube needs no z-buffer.
	sort.Slice(draws[:], func(i, j int) bool {
		return draws[i].depth > draws[j].depth
	})

	for _, draw := range draws {
		c := draw.face
		p0 := proj.screen[c.corners[0]]
		p1 := proj.screen[c.corners[1]]
		p2 := proj.screen[c.corners[2]]
		p3 := proj.screen[c.corners[3]]
		fillTriangle(a.pixels, windowWidth, windowHeight, p0, p1, p2, c.color)
		fillTriangle(a.pixels, windowWidth, windowHeight, p0, p2, p3, c.color)
	}
}

// projectCube rotates every vertex around Y then X and perspective-projects it.
// The fixed rotation order makes the cube tumble as it spins.
func projectCube(yaw, pitch float64) projection {
	var proj projection
	for i, v := range cubeVertices {
		r := rotateY(v, yaw)
		r = rotateX(r, pitch)
		// Translate in front of the camera before the perspective divide; every
		// vertex keeps a positive depth for the rotations shown here.
		depth := r.z + cameraDistance
		scale := focalLength / depth
		proj.depth[i] = depth
		proj.screen[i] = screenPoint{
			x: int32(math.Round(centerX + r.x*scale)),
			y: int32(math.Round(centerY - r.y*scale)),
		}
	}
	return proj
}

// rotateX applies a right-handed rotation around the X axis.
func rotateX(v vec3, angle float64) vec3 {
	s, c := math.Sincos(angle)
	return vec3{x: v.x, y: v.y*c - v.z*s, z: v.y*s + v.z*c}
}

// rotateY applies a right-handed rotation around the Y axis.
func rotateY(v vec3, angle float64) vec3 {
	s, c := math.Sincos(angle)
	return vec3{x: v.x*c + v.z*s, y: v.y, z: -v.x*s + v.z*c}
}

// edge returns twice the signed area of triangle (a, b, c); its sign tells which
// side of the directed line a->b the point c lies on.
func edge(a, b, c screenPoint) int64 {
	return int64(b.x-a.x)*int64(c.y-a.y) - int64(b.y-a.y)*int64(c.x-a.x)
}

// fillTriangle paints one triangle with a flat opaque color by testing every
// pixel center inside its bounding box against the three edge functions.
func fillTriangle(pixels []byte, width, height int, p0, p1, p2 screenPoint, color uint32) {
	area := edge(p0, p1, p2)
	if area == 0 {
		return // Degenerate: the face is exactly edge-on to the camera.
	}
	if area < 0 {
		// Normalize the winding so the inside test can require all edge
		// functions to be non-negative.
		p1, p2 = p2, p1
	}

	minX := max(int32(0), min(p0.x, p1.x, p2.x))
	maxX := min(int32(width-1), max(p0.x, p1.x, p2.x))
	minY := max(int32(0), min(p0.y, p1.y, p2.y))
	maxY := min(int32(height-1), max(p0.y, p1.y, p2.y))
	if minX > maxX || minY > maxY {
		return
	}

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			p := screenPoint{x: x, y: y}
			if edge(p0, p1, p) < 0 || edge(p1, p2, p) < 0 || edge(p2, p0, p) < 0 {
				continue
			}
			offset := (int(y)*width + int(x)) * 4
			binary.LittleEndian.PutUint32(pixels[offset:offset+4], color)
		}
	}
}

// waitForConfigure dispatches until the first xdg_surface.configure event has
// been handled. A compositor may deliver it after the sync callback that ends
// the first roundtrip, so retry a bounded number of roundtrips instead of
// assuming one is enough.
func waitForConfigure(d *wayland.Display, a *app) error {
	for attempt := 0; attempt < 3 && !a.started; attempt++ {
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
		if a.pixels != nil {
			_ = syscall.Munmap(a.pixels)
		}
	}()
	defer func() {
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "cube: close:", cerr)
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

	keyboard, err := seat.GetKeyboard(&keyboardHandler{app: a})
	if err != nil {
		return fmt.Errorf("wl_seat.get_keyboard: %w", err)
	}
	a.keyboard = keyboard

	if err := a.createShmBuffer(windowWidth, windowHeight); err != nil {
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
	// The geometry is declared once; this demo never honors a configured size.
	if err := xdgSurface.SetWindowGeometry(0, 0, windowWidth, windowHeight); err != nil {
		return fmt.Errorf("xdg_surface.set_window_geometry: %w", err)
	}
	if err := surface.Commit(); err != nil {
		return fmt.Errorf("initial wl_surface.commit: %w", err)
	}
	if err := waitForConfigure(d, a); err != nil {
		return err
	}
	if !a.started {
		return fmt.Errorf("xdg_surface did not send configure")
	}

	fmt.Printf("cube: %dx%d rotating cube; close the window or press Esc to exit\n", windowWidth, windowHeight)
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
		fmt.Fprintln(os.Stderr, "cube:", err)
		os.Exit(1)
	}
}
