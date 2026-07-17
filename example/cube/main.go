//go:build linux

// Software-rendered rotating 3D cube via frame-callback-driven animation with double-buffered shm.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const (
	winW      = 480
	winH      = 480
	stride    = winW * 4
	bufBytes  = stride * winH
	poolBytes = 2 * bufBytes
	focal     = 300.0
	cubeDist  = 4.0
	speedY    = 0.9
	speedX    = 0.6
)

type vec3 [3]float64

var cubeVerts = [8]vec3{
	{-1, -1, -1}, {1, -1, -1}, {1, 1, -1}, {-1, 1, -1},
	{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1},
}

type faceDef struct {
	idx   [4]int
	color [4]byte
}

var cubeFaces = [6]faceDef{
	{[4]int{1, 0, 3, 2}, [4]byte{0x00, 0x00, 0xFF, 0xFF}}, // front, red
	{[4]int{4, 5, 6, 7}, [4]byte{0x00, 0xFF, 0x00, 0xFF}}, // back, green
	{[4]int{3, 0, 4, 7}, [4]byte{0xFF, 0x00, 0x00, 0xFF}}, // left, blue
	{[4]int{5, 1, 2, 6}, [4]byte{0xFF, 0xFF, 0x00, 0xFF}}, // right, yellow
	{[4]int{7, 6, 2, 3}, [4]byte{0x00, 0xFF, 0xFF, 0xFF}}, // top, cyan
	{[4]int{0, 1, 5, 4}, [4]byte{0xFF, 0x00, 0xFF, 0xFF}}, // bottom, magenta
}

func rotY(v vec3, a float64) vec3 {
	s, c := math.Sincos(a)
	return vec3{v[0]*c + v[2]*s, v[1], -v[0]*s + v[2]*c}
}

func rotX(v vec3, a float64) vec3 {
	s, c := math.Sincos(a)
	return vec3{v[0], v[1]*c - v[2]*s, v[1]*s + v[2]*c}
}

func sub(a, b vec3) vec3 { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

func cross(a, b vec3) vec3 {
	return vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func edge(ax, ay, bx, by, px, py float64) float64 {
	return (px-ax)*(by-ay) - (py-ay)*(bx-ax)
}

func rasterTriangle(data []byte, s int, x0, y0, x1, y1, x2, y2 float64, c [4]byte) {
	minX := int(min(x0, min(x1, x2)))
	maxX := int(max(x0, max(x1, x2)))
	minY := int(min(y0, min(y1, y2)))
	maxY := int(max(y0, max(y1, y2)))
	if minX < 0 {
		minX = 0
	}
	if maxX >= winW {
		maxX = winW - 1
	}
	if minY < 0 {
		minY = 0
	}
	if maxY >= winH {
		maxY = winH - 1
	}
	for py := minY; py <= maxY; py++ {
		off := py * s
		for px := minX; px <= maxX; px++ {
			e0 := edge(x0, y0, x1, y1, float64(px), float64(py))
			e1 := edge(x1, y1, x2, y2, float64(px), float64(py))
			e2 := edge(x2, y2, x0, y0, float64(px), float64(py))
			if (e0 >= 0 && e1 >= 0 && e2 >= 0) || (e0 <= 0 && e1 <= 0 && e2 <= 0) {
				o := off + px*4
				data[o+0] = c[0]
				data[o+1] = c[1]
				data[o+2] = c[2]
				data[o+3] = c[3]
			}
		}
	}
}

func clearBlack(data []byte) {
	for i := range len(data) {
		data[i] = 0
	}
}

func renderCube(data []byte, ay, ax float64) {
	var rv [8]vec3
	for i, v := range cubeVerts {
		p := rotX(rotY(v, ay), ax)
		p[2] -= cubeDist
		rv[i] = p
	}

	type faceInfo struct {
		col     [4]byte
		depth   float64
		screenV [4][2]float64
	}
	var visible []faceInfo

	for _, f := range cubeFaces {
		v0, v1, v2 := rv[f.idx[0]], rv[f.idx[1]], rv[f.idx[2]]
		n := cross(sub(v1, v0), sub(v2, v0))
		if n[2] <= 0 {
			continue
		}

		depth := (rv[f.idx[0]][2] + rv[f.idx[1]][2] + rv[f.idx[2]][2] + rv[f.idx[3]][2]) / 4

		fi := faceInfo{col: f.color, depth: depth}
		for j, vi := range f.idx {
			p := rv[vi]
			nz := -p[2]
			sx := focal*p[0]/nz + winW/2
			sy := winH/2 - focal*p[1]/nz
			fi.screenV[j] = [2]float64{sx, sy}
		}
		visible = append(visible, fi)
	}

	for i := 0; i < len(visible); i++ {
		for j := i + 1; j < len(visible); j++ {
			if visible[i].depth > visible[j].depth {
				visible[i], visible[j] = visible[j], visible[i]
			}
		}
	}

	for _, f := range visible {
		sv := f.screenV
		rasterTriangle(data, stride, sv[0][0], sv[0][1], sv[1][0], sv[1][1], sv[2][0], sv[2][1], f.col)
		rasterTriangle(data, stride, sv[0][0], sv[0][1], sv[2][0], sv[2][1], sv[3][0], sv[3][1], f.col)
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

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dpy, err := wayland.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: object=%d code=%d message=%q\n", pe.ObjectID, pe.Code, pe.Message)
	})

	reg, err := dpy.GetRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_registry: %v\n", err)
		os.Exit(1)
	}
	var compG, shmG, wmG wayland.RegistryGlobalEvent
	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})

	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}

	for _, g := range globals {
		switch g.Interface {
		case wayland.InterfaceCompositor:
			compG = g
		case wayland.InterfaceShm:
			shmG = g
		case xdgshell.InterfaceWmBase:
			wmG = g
		}
	}
	if compG.Interface == "" || shmG.Interface == "" || wmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "missing required globals")
		os.Exit(1)
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

	shutdown := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
		case <-shutdown:
		}
		cancel()
	}()

	var cfgSerial uint32
	var cfgDone bool

	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) { cfgSerial = ev.Serial })
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) { cfgDone = true })
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) { close(shutdown) })

	_ = toplevel.SetTitle("Rotating Cube")
	_ = toplevel.SetAppID("go-wayland-cube")
	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for cfgSerial == 0 || !cfgDone {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure")
				return
			}
			break
		}
	}
	_ = xdgSurface.AckConfigure(cfgSerial)

	fd, closeFd, err := shmFile(poolBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shm: %v\n", err)
		os.Exit(1)
	}
	defer closeFd()

	data, err := syscall.Mmap(fd, 0, int(poolBytes), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mmap: %v\n", err)
		os.Exit(1)
	}
	defer syscall.Munmap(data) //nolint: errcheck

	pool, err := shm.CreatePool(fd, int32(poolBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Destroy() //nolint: errcheck

	buf0, err := pool.CreateBuffer(0, winW, winH, stride, uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
		os.Exit(1)
	}
	buf1, err := pool.CreateBuffer(int32(bufBytes), winW, winH, stride, uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
		os.Exit(1)
	}
	defer buf0.Destroy() //nolint: errcheck
	defer buf1.Destroy() //nolint: errcheck

	freeBufs := make(chan int, 2)
	freeBufs <- 0
	freeBufs <- 1
	buf0.OnRelease(func(ev wayland.BufferReleaseEvent) { freeBufs <- 0 })
	buf1.OnRelease(func(ev wayland.BufferReleaseEvent) { freeBufs <- 1 })

	bufObj := [2]wire.ObjectID{wire.ObjectID(buf0.Proxy().ID()), wire.ObjectID(buf1.Proxy().ID())}
	bufData := [2][]byte{data[0:bufBytes], data[bufBytes:poolBytes]}

	// dedicated event dispatch goroutine
	go func() {
		for {
			if err := dpy.Dispatch(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				return
			}
		}
	}()

	frameReady := make(chan struct{}, 1)

	// first frame
	bi := <-freeBufs
	clearBlack(bufData[bi])
	renderCube(bufData[bi], 0, 0)
	cb, err := surface.Frame()
	if err != nil {
		fmt.Fprintf(os.Stderr, "frame: %v\n", err)
		return
	}
	cb.OnDone(func(ev wayland.CallbackDoneEvent) {
		select {
		case frameReady <- struct{}{}:
		default:
		}
	})
	_ = surface.Attach(bufObj[bi], 0, 0)
	_ = surface.Damage(0, 0, winW, winH)
	_ = surface.Commit()

	start := time.Now()
	frames := 1

	fmt.Printf("cube: %dx%d, animating...\n", winW, winH)

	for {
		select {
		case <-shutdown:
			goto report
		case <-ctx.Done():
			goto report
		case <-frameReady:
		case <-time.After(time.Second):
		}

		select {
		case bi = <-freeBufs:
		case <-shutdown:
			goto report
		case <-ctx.Done():
			goto report
		case <-time.After(time.Second):
			continue
		}

		elapsed := time.Since(start).Seconds()
		clearBlack(bufData[bi])
		renderCube(bufData[bi], elapsed*speedY, elapsed*speedX)

		cb, err = surface.Frame()
		if err != nil {
			fmt.Fprintf(os.Stderr, "frame: %v\n", err)
			goto report
		}
		cb.OnDone(func(ev wayland.CallbackDoneEvent) {
			select {
			case frameReady <- struct{}{}:
			default:
			}
		})
		_ = surface.Attach(bufObj[bi], 0, 0)
		_ = surface.Damage(0, 0, winW, winH)
		_ = surface.Commit()

		frames++
	}

report:
	elapsed := time.Since(start).Seconds()
	fmt.Printf("%d frames in %.1fs (%.1f fps)\n", frames, elapsed, float64(frames)/elapsed)
}
