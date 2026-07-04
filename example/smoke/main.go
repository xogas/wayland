//go:build linux

// A Jos Stam fluid smoke simulation in a Wayland window, inspired by weston smoke.
package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const (
	winW       = 400
	winH       = 400
	stride     = winW * 4
	simW       = 200
	simH       = 200
	simScale   = winW / simW
	simCells   = simW * simH
	pixFmt     = uint32(wayland.ShmFormatXrgb8888)
	diffuseN   = 2
	projectN   = 4
	simDt      = 0.12
	simVisc    = 0.000001
	simDiff    = 0.000001
	autoPeriod = 15
)

func simIX(x, y int) int { return x + y*simW }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type Sim struct {
	dens, u, v          []float64
	dens0, u0, v0       []float64
	p, div              []float64
	srcDens, srcU, srcV []float64
	autoTick            int
}

func newSim() *Sim {
	n := simCells
	return &Sim{
		dens:    make([]float64, n),
		u:       make([]float64, n),
		v:       make([]float64, n),
		dens0:   make([]float64, n),
		u0:      make([]float64, n),
		v0:      make([]float64, n),
		p:       make([]float64, n),
		div:     make([]float64, n),
		srcDens: make([]float64, n),
		srcU:    make([]float64, n),
		srcV:    make([]float64, n),
	}
}

func (s *Sim) setBnd(b int, x []float64) {
	for i := 1; i < simW-1; i++ {
		if b == 1 {
			x[simIX(0, i)] = -x[simIX(1, i)]
			x[simIX(simW-1, i)] = -x[simIX(simW-2, i)]
		} else {
			x[simIX(0, i)] = x[simIX(1, i)]
			x[simIX(simW-1, i)] = x[simIX(simW-2, i)]
		}
	}
	for i := 1; i < simH-1; i++ {
		if b == 2 {
			x[simIX(i, 0)] = -x[simIX(i, 1)]
			x[simIX(i, simH-1)] = -x[simIX(i, simH-2)]
		} else {
			x[simIX(i, 0)] = x[simIX(i, 1)]
			x[simIX(i, simH-1)] = x[simIX(i, simH-2)]
		}
	}
	x[simIX(0, 0)] = 0.5 * (x[simIX(1, 0)] + x[simIX(0, 1)])
	x[simIX(0, simH-1)] = 0.5 * (x[simIX(1, simH-1)] + x[simIX(0, simH-2)])
	x[simIX(simW-1, 0)] = 0.5 * (x[simIX(simW-2, 0)] + x[simIX(simW-1, 1)])
	x[simIX(simW-1, simH-1)] = 0.5 * (x[simIX(simW-2, simH-1)] + x[simIX(simW-1, simH-2)])
}

func (s *Sim) addSource(x []float64, s0 []float64, dt float64) {
	for i := range x {
		x[i] += dt * s0[i]
	}
}

func (s *Sim) diffuse(b int, x []float64, x0 []float64, diff float64, dt float64) {
	a := dt * diff * float64(simW*simH)
	for range diffuseN {
		for i := 1; i < simW-1; i++ {
			for j := 1; j < simH-1; j++ {
				idx := simIX(i, j)
				x[idx] = (x0[idx] + a*(x[simIX(i-1, j)]+x[simIX(i+1, j)]+x[simIX(i, j-1)]+x[simIX(i, j+1)])) / (1 + 4*a)
			}
		}
		s.setBnd(b, x)
	}
}

func (s *Sim) advect(b int, d []float64, d0 []float64, u []float64, v []float64, dt float64) {
	dt0 := dt * float64(simW)
	for i := 1; i < simW-1; i++ {
		for j := 1; j < simH-1; j++ {
			idx := simIX(i, j)
			x := float64(i) - dt0*u[idx]
			y := float64(j) - dt0*v[idx]
			x = clamp(x, 0.5, float64(simW)-1.5)
			y = clamp(y, 0.5, float64(simH)-1.5)
			i0 := int(x)
			j0 := int(y)
			i1 := i0 + 1
			j1 := j0 + 1
			s1 := x - float64(i0)
			s0 := 1.0 - s1
			t1 := y - float64(j0)
			t0 := 1.0 - t1
			d[idx] = s0*(t0*d0[simIX(i0, j0)]+t1*d0[simIX(i0, j1)]) + s1*(t0*d0[simIX(i1, j0)]+t1*d0[simIX(i1, j1)])
		}
	}
	s.setBnd(b, d)
}

func (s *Sim) project(u []float64, v []float64, p []float64, div []float64) {
	h := 1.0 / float64(simW)
	for i := 1; i < simW-1; i++ {
		for j := 1; j < simH-1; j++ {
			idx := simIX(i, j)
			div[idx] = -0.5 * h * (u[simIX(i+1, j)] - u[simIX(i-1, j)] + v[simIX(i, j+1)] - v[simIX(i, j-1)])
			p[idx] = 0
		}
	}
	s.setBnd(0, div)
	s.setBnd(0, p)
	for range projectN {
		for i := 1; i < simW-1; i++ {
			for j := 1; j < simH-1; j++ {
				idx := simIX(i, j)
				p[idx] = (div[idx] + p[simIX(i-1, j)] + p[simIX(i+1, j)] + p[simIX(i, j-1)] + p[simIX(i, j+1)]) / 4.0
			}
		}
		s.setBnd(0, p)
	}
	for i := 1; i < simW-1; i++ {
		for j := 1; j < simH-1; j++ {
			idx := simIX(i, j)
			u[idx] -= 0.5 * (p[simIX(i+1, j)] - p[simIX(i-1, j)]) / h
			v[idx] -= 0.5 * (p[simIX(i, j+1)] - p[simIX(i, j-1)]) / h
		}
	}
	s.setBnd(1, u)
	s.setBnd(2, v)
}

func (s *Sim) step(dt float64) {
	s.addSource(s.u, s.srcU, dt)
	s.addSource(s.v, s.srcV, dt)
	for i := range s.srcU {
		s.srcU[i] = 0
		s.srcV[i] = 0
	}

	copy(s.u0, s.u)
	copy(s.v0, s.v)
	s.diffuse(1, s.u, s.u0, simVisc, dt)
	s.diffuse(2, s.v, s.v0, simVisc, dt)
	s.project(s.u, s.v, s.p, s.div)

	copy(s.u0, s.u)
	copy(s.v0, s.v)
	s.advect(1, s.u, s.u0, s.u0, s.v0, dt)
	s.advect(2, s.v, s.v0, s.u0, s.v0, dt)
	s.project(s.u, s.v, s.p, s.div)

	s.addSource(s.dens, s.srcDens, dt)
	for i := range s.srcDens {
		s.srcDens[i] = 0
	}
	copy(s.dens0, s.dens)
	s.diffuse(0, s.dens, s.dens0, simDiff, dt)
	copy(s.dens0, s.dens)
	s.advect(0, s.dens, s.dens0, s.u, s.v, dt)

	s.autoTick++
	if s.autoTick >= autoPeriod {
		s.autoTick = 0
		s.injectRandom()
	}
}

func (s *Sim) injectMotion(px, py int, dx, dy float64) {
	r := 4
	spd := math.Sqrt(dx*dx+dy*dy) * 3.0
	for dy2 := -r; dy2 <= r; dy2++ {
		for dx2 := -r; dx2 <= r; dx2++ {
			nx := px + dx2
			ny := py + dy2
			if nx < 0 || nx >= simW || ny < 0 || ny >= simH {
				continue
			}
			d2 := float64(dx2*dx2 + dy2*dy2)
			f := math.Exp(-d2 / 12.0)
			idx := simIX(nx, ny)
			s.srcDens[idx] += f * spd * 3
			s.srcU[idx] += dx * f * 4
			s.srcV[idx] += dy * f * 4
		}
	}
}

func (s *Sim) injectRandom() {
	cx := rand.Intn(simW/2) + simW/4
	cy := rand.Intn(simH/2) + simH/4
	rr := 5 + rand.Intn(18)
	spd := 2.0 + rand.Float64()*3.0
	u0 := (rand.Float64() - 0.5) * 80
	v0 := (rand.Float64() - 0.5) * 80
	rsq := float64(rr*rr) / 3.0
	for dy := -rr; dy <= rr; dy++ {
		for dx := -rr; dx <= rr; dx++ {
			nx := cx + dx
			ny := cy + dy
			if nx < 0 || nx >= simW || ny < 0 || ny >= simH {
				continue
			}
			d2 := float64(dx*dx + dy*dy)
			f := math.Exp(-d2 / rsq)
			idx := simIX(nx, ny)
			s.srcDens[idx] += f * spd
			s.srcU[idx] += u0 * f * 0.5
			s.srcV[idx] += v0 * f * 0.5
		}
	}
}

func render(data []byte, sim *Sim) {
	for y := range winH {
		sy := y / simScale
		for x := range winW {
			sx := x / simScale
			d := sim.dens[simIX(sx, sy)]
			r := byte(clamp(d*400, 0, 255))
			g := byte(clamp(d*180, 0, 255))
			b := byte(clamp(d*80, 0, 255))
			off := y*stride + x*4
			data[off+0] = b
			data[off+1] = g
			data[off+2] = r
			data[off+3] = 0xff
		}
	}
}

type bbuf struct {
	data  []byte
	pool  *wayland.ShmPool
	buf   *wayland.Buffer
	file  *os.File
	ready chan struct{}
}

func newBBuf(shm *wayland.Shm, size int64) (*bbuf, error) {
	f, err := os.CreateTemp("", "wayland-smoke-*")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(f.Name())
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return nil, err
	}
	fd := int(f.Fd())
	data, err := syscall.Mmap(fd, 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	pool, err := shm.CreatePool(fd, int32(size))
	if err != nil {
		_ = syscall.Munmap(data)
		_ = f.Close()
		return nil, err
	}
	buf, err := pool.CreateBuffer(0, int32(winW), int32(winH), int32(stride), pixFmt)
	if err != nil {
		_ = pool.Destroy()
		_ = syscall.Munmap(data)
		_ = f.Close()
		return nil, err
	}
	bb := &bbuf{
		data:  data,
		pool:  pool,
		buf:   buf,
		file:  f,
		ready: make(chan struct{}, 1),
	}
	bb.ready <- struct{}{}
	bb.buf.OnRelease(func(ev wayland.BufferReleaseEvent) {
		select {
		case bb.ready <- struct{}{}:
		default:
		}
	})
	return bb, nil
}

func (bb *bbuf) close() {
	_ = bb.buf.Destroy()
	_ = bb.pool.Destroy()
	_ = syscall.Munmap(bb.data)
	_ = bb.file.Close()
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
		fmt.Fprintf(os.Stderr, "protocol error: obj=%d code=%d msg=%q\n", pe.ObjectID, pe.Code, pe.Message)
	})

	reg, err := dpy.GetRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_registry: %v\n", err)
		os.Exit(1)
	}
	var (
		compG wayland.RegistryGlobalEvent
		shmG  wayland.RegistryGlobalEvent
		wmG   wayland.RegistryGlobalEvent
		seatG wayland.RegistryGlobalEvent
	)
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
		case wayland.InterfaceSeat:
			seatG = g
		}
	}
	if compG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_compositor global")
		os.Exit(1)
	}
	if shmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_shm global")
		os.Exit(1)
	}
	if wmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no xdg_wm_base global")
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

	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) {
		_ = wmBase.Pong(ev.Serial)
	})

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

	cfgAcked := false
	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		_ = xdgSurface.AckConfigure(ev.Serial)
		cfgAcked = true
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})

	shutdown := make(chan struct{})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		close(shutdown)
	})

	_ = toplevel.SetTitle("Smoke")
	_ = toplevel.SetAppID("go-wayland-smoke")
	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for !cfgAcked {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure")
				os.Exit(1)
			}
			break
		}
	}
	if !cfgAcked {
		fmt.Fprintln(os.Stderr, "configure not acked")
		os.Exit(1)
	}

	bufSize := int64(winH) * int64(stride)
	bb0, err := newBBuf(shm, bufSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "buffer 0: %v\n", err)
		os.Exit(1)
	}
	defer bb0.close()
	bb1, err := newBBuf(shm, bufSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "buffer 1: %v\n", err)
		os.Exit(1)
	}
	defer bb1.close()
	bbs := [2]*bbuf{bb0, bb1}

	sim := newSim()
	sim.injectRandom()

	var ptrPX, ptrPY float64
	var ptrEntered bool
	if seatG.Interface != "" {
		seat, err := wayland.BindSeat(reg, seatG.Name, seatG.Version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind seat: %v\n", err)
		} else {
			ptr, err := seat.GetPointer()
			if err != nil {
				fmt.Fprintf(os.Stderr, "get_pointer: %v\n", err)
			} else {
				ptr.OnEnter(func(ev wayland.PointerEnterEvent) {
					ptrEntered = true
					ptrPX = ev.SurfaceX.Float64()
					ptrPY = ev.SurfaceY.Float64()
				})
				ptr.OnLeave(func(ev wayland.PointerLeaveEvent) {
					ptrEntered = false
				})
				ptr.OnMotion(func(ev wayland.PointerMotionEvent) {
					if !ptrEntered {
						return
					}
					nx := ev.SurfaceX.Float64()
					ny := ev.SurfaceY.Float64()
					dx := nx - ptrPX
					dy := ny - ptrPY
					ptrPX = nx
					ptrPY = ny
					if dx == 0 && dy == 0 {
						return
					}
					sx := int(nx) / simScale
					sy := int(ny) / simScale
					sim.injectMotion(sx, sy, dx, dy)
				})
			}
		}
	}

	frameCount := 0
	start := time.Now()
	bufIdx := 0
	var frameDone chan struct{}

	for {
		select {
		case <-shutdown:
			elapsed := time.Since(start)
			fmt.Printf("closed. frames=%d fps=%.1f\n", frameCount, float64(frameCount)/elapsed.Seconds())
			return
		case <-ctx.Done():
			elapsed := time.Since(start)
			fmt.Printf("timeout. frames=%d fps=%.1f\n", frameCount, float64(frameCount)/elapsed.Seconds())
			return
		default:
		}

		if frameDone != nil {
			select {
			case <-frameDone:
			default:
				dctx, dcancel := context.WithTimeout(ctx, 5*time.Millisecond)
				err := dpy.Dispatch(dctx)
				dcancel()
				if err != nil {
					if ctx.Err() != nil {
						goto loopExit
					}
					if errors.Is(err, context.DeadlineExceeded) {
						continue
					}
					fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
					goto loopExit
				}
				continue
			}
		}

		select {
		case <-bbs[bufIdx].ready:
		default:
			dctx, dcancel := context.WithTimeout(ctx, 5*time.Millisecond)
			err := dpy.Dispatch(dctx)
			dcancel()
			if err != nil {
				if ctx.Err() != nil {
					goto loopExit
				}
				if errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
				goto loopExit
			}
			continue
		}

		sim.step(simDt)
		render(bbs[bufIdx].data, sim)

		frameDone = make(chan struct{})
		cb, err := surface.Frame()
		if err != nil {
			fmt.Fprintf(os.Stderr, "frame: %v\n", err)
			continue
		}
		cb.OnDone(func(ev wayland.CallbackDoneEvent) {
			close(frameDone)
		})

		_ = surface.Attach(wire.ObjectID(bbs[bufIdx].buf.Proxy().ID()), 0, 0)
		_ = surface.Damage(0, 0, int32(winW), int32(winH))
		_ = surface.Commit()

		frameCount++
		bufIdx = 1 - bufIdx

		if frameCount%60 == 0 {
			elapsed := time.Since(start)
			fmt.Printf("frames=%d elapsed=%.1fs fps=%.1f\n", frameCount, elapsed.Seconds(), float64(frameCount)/elapsed.Seconds())
		}
	}
loopExit:
}
