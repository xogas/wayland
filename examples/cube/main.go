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
	"github.com/xogas/wayland/examples/internal/shared"
)

const (
	winW     = 480
	winH     = 480
	stride   = winW * 4
	focal    = 300.0
	cubeDist = 4.0
	speedY   = 0.9
	speedX   = 0.6
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

	// Painter's algorithm: far faces first.
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

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: object=%d code=%d message=%q\n", pe.ObjectID, pe.Code, pe.Message)
	})

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Rotating Cube", "go-wayland-cube", winW, winH, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
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

	db, err := shared.NewDoubleBuffer(core.Shm, winW, winH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	errCh := shared.DispatchLoop(ctx, dpy)
	frameReady := make(chan struct{}, 1)

	start := time.Now()
	frames := 0
	bi := db.Next()

	cb, err := toplevel.Surface.Frame()
	if err != nil {
		fmt.Fprintf(os.Stderr, "frame: %v\n", err)
		os.Exit(1)
	}
	cb.OnDone(func(ev wayland.CallbackDoneEvent) {
		select {
		case frameReady <- struct{}{}:
		default:
		}
	})
	clearBlack(db.Pixels[bi])
	renderCube(db.Pixels[bi], 0, 0)
	_ = toplevel.Surface.Attach(db.IDs[bi], 0, 0)
	_ = toplevel.Surface.Damage(0, 0, winW, winH)
	_ = toplevel.Surface.Commit()
	frames = 1

	fmt.Printf("cube: %dx%d, animating...\n", winW, winH)

	for {
		select {
		case <-toplevel.Closed:
			goto report
		case <-ctx.Done():
			goto report
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			}
			goto report
		case <-frameReady:
		case <-time.After(time.Second):
			// No frame callback (window hidden): keep rendering, throttled by
			// buffer release.
		}

		select {
		case bi = <-db.Free():
		case <-toplevel.Closed:
			goto report
		case <-ctx.Done():
			goto report
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			}
			goto report
		case <-time.After(time.Second):
			continue
		}

		elapsed := time.Since(start).Seconds()
		clearBlack(db.Pixels[bi])
		renderCube(db.Pixels[bi], elapsed*speedY, elapsed*speedX)
		cb, err := toplevel.Surface.Frame()
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
		_ = toplevel.Surface.Attach(db.IDs[bi], 0, 0)
		_ = toplevel.Surface.Damage(0, 0, winW, winH)
		_ = toplevel.Surface.Commit()

		frames++
	}

report:
	elapsed := time.Since(start).Seconds()
	fmt.Printf("%d frames in %.1fs (%.1f fps)\n", frames, elapsed, float64(frames)/elapsed)
}
