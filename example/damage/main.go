//go:build linux

// Incremental damage demonstration with a small square moving along a circular path.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const (
	winW = 400
	winH = 400
	sqSz = 24
	keyD = 32
)

type bufSlot struct {
	id    wire.ObjectID
	wl    *wayland.Buffer
	base  []byte
	prevX int32
	prevY int32
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
	if compG.Interface == "" || shmG.Interface == "" || wmG.Interface == "" || seatG.Interface == "" {
		fmt.Fprintln(os.Stderr, "missing required globals (compositor, shm, xdg_wm_base, seat)")
		os.Exit(1)
	}

	compVer := compG.Version
	if compVer > wayland.VersionCompositor {
		compVer = wayland.VersionCompositor
	}
	compositor, err := wayland.BindCompositor(reg, compG.Name, compVer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind compositor: %v\n", err)
		os.Exit(1)
	}

	useDamageBuffer := compVer >= 4
	if useDamageBuffer {
		fmt.Printf("using DamageBuffer (compositor version %d)\n", compVer)
	} else {
		fmt.Println("using Damage (surface coordinates)")
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
	seat, err := wayland.BindSeat(reg, seatG.Name, seatG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind seat: %v\n", err)
		os.Exit(1)
	}

	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) {
		_ = wmBase.Pong(ev.Serial)
	})

	surface, err := compositor.CreateSurface()
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

	keyboard, err := seat.GetKeyboard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
		os.Exit(1)
	}

	incDamage := true
	keyboard.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.Key == keyD && ev.State == uint32(wayland.KeyboardKeyStatePressed) {
			incDamage = !incDamage
			if incDamage {
				fmt.Println("switched to incremental damage")
			} else {
				fmt.Println("switched to full damage")
			}
		}
	})

	shutdown := make(chan struct{})
	var cfgSerial uint32
	cfgDone := false

	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {
		cfgDone = true
	})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		close(shutdown)
	})

	_ = toplevel.SetTitle("damage")
	_ = toplevel.SetAppID("damagedemo")
	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for cfgSerial == 0 || !cfgDone {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure events")
				return
			}
			break
		}
	}
	if cfgSerial == 0 {
		fmt.Fprintln(os.Stderr, "no configure serial received")
		return
	}
	_ = xdgSurface.AckConfigure(cfgSerial)

	stride := int32(winW * 4)
	bufH := int32(winH)
	oneSize := int64(bufH * stride)
	poolSize := oneSize * 2

	fd, closeFd, err := shmFile(poolSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shm: %v\n", err)
		os.Exit(1)
	}
	defer closeFd()

	data, err := syscall.Mmap(fd, 0, int(poolSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mmap: %v\n", err)
		os.Exit(1)
	}
	defer syscall.Munmap(data) //nolint: errcheck

	pool, err := shm.CreatePool(fd, int32(poolSize))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Destroy() //nolint: errcheck

	bufs := [2]bufSlot{}
	for i := 0; i < 2; i++ {
		off := int32(i) * int32(oneSize)
		wlBuf, err := pool.CreateBuffer(off, int32(winW), bufH, stride, uint32(wayland.ShmFormatXrgb8888))
		if err != nil {
			fmt.Fprintf(os.Stderr, "create_buffer %d: %v\n", i, err)
			os.Exit(1)
		}
		bufs[i] = bufSlot{
			id:    wire.ObjectID(wlBuf.Proxy().ID()),
			wl:    wlBuf,
			base:  data[off : off+int32(oneSize)],
			prevX: -sqSz,
			prevY: -sqSz,
		}
		fillFull(bufs[i].base, int(stride))
	}

	freeCh := make(chan int, 2)
	bufs[0].wl.OnRelease(func(ev wayland.BufferReleaseEvent) {
		freeCh <- 0
	})
	bufs[1].wl.OnRelease(func(ev wayland.BufferReleaseEvent) {
		freeCh <- 1
	})
	freeCh <- 0
	freeCh <- 1

	go func() {
		for {
			if err := dpy.Dispatch(ctx); err != nil {
				return
			}
		}
	}()

	cx := float64(winW) * 0.5
	cy := float64(winH) * 0.5
	radius := 150.0

	frames := 0
	var accumArea int64
	fullArea := int64(winW) * int64(winH)

	for {
		select {
		case <-shutdown:
			return
		case <-ctx.Done():
			return
		case idx := <-freeCh:
			buf := &bufs[idx]
			t := float64(frames) * 0.06
			nx := int32(cx + radius*math.Cos(t) - sqSz/2)
			ny := int32(cy + radius*math.Sin(t) - sqSz/2)

			eraseRect(buf.base, int(stride), buf.prevX, buf.prevY, sqSz, sqSz)
			fillRect(buf.base, int(stride), nx, ny, sqSz, sqSz)

			done := make(chan struct{})
			cb, err := surface.Frame()
			if err != nil {
				fmt.Fprintf(os.Stderr, "frame: %v\n", err)
				return
			}
			cb.OnDone(func(ev wayland.CallbackDoneEvent) {
				close(done)
			})

			if err := surface.Attach(buf.id, 0, 0); err != nil {
				fmt.Fprintf(os.Stderr, "attach: %v\n", err)
				return
			}

			if incDamage {
				a1 := damageRect(surface, useDamageBuffer, buf.prevX, buf.prevY, sqSz, sqSz)
				a2 := damageRect(surface, useDamageBuffer, nx, ny, sqSz, sqSz)
				accumArea += int64(a1) + int64(a2)
			} else {
				damageRect(surface, useDamageBuffer, 0, 0, int32(winW), bufH)
				accumArea += fullArea
			}

			if err := surface.Commit(); err != nil {
				fmt.Fprintf(os.Stderr, "commit: %v\n", err)
				return
			}

			buf.prevX = nx
			buf.prevY = ny

			select {
			case <-shutdown:
				return
			case <-ctx.Done():
				return
			case <-done:
			}
			frames++

			if frames%120 == 0 {
				ratio := float64(accumArea) / float64(int64(frames)*fullArea)
				fmt.Printf("%d frames, damage/total area ratio: %.3f\n", frames, ratio)
			}
		}
	}
}

func damageRect(s *wayland.Surface, useDB bool, x, y, w, h int32) int32 {
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > int32(winW) {
		w = int32(winW) - x
	}
	if y+h > int32(winH) {
		h = int32(winH) - y
	}
	if w <= 0 || h <= 0 {
		return 0
	}
	if useDB {
		_ = s.DamageBuffer(x, y, w, h)
	} else {
		_ = s.Damage(x, y, w, h)
	}
	return w * h
}

func fillFull(data []byte, stride int) {
	for y := 0; y < winH; y++ {
		off := y * stride
		for x := 0; x < winW; x++ {
			data[off+0] = 0x30
			data[off+1] = 0x30
			data[off+2] = 0x40
			data[off+3] = 0xff
			off += 4
		}
	}
}

func eraseRect(data []byte, stride int, x, y, w, h int32) {
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > int32(winW) {
		w = int32(winW) - x
	}
	if y+h > int32(winH) {
		h = int32(winH) - y
	}
	if w <= 0 || h <= 0 {
		return
	}
	for row := y; row < y+h; row++ {
		off := int(row)*stride + int(x)*4
		for col := int32(0); col < w; col++ {
			data[off+0] = 0x30
			data[off+1] = 0x30
			data[off+2] = 0x40
			data[off+3] = 0xff
			off += 4
		}
	}
}

func fillRect(data []byte, stride int, x, y, w, h int32) {
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > int32(winW) {
		w = int32(winW) - x
	}
	if y+h > int32(winH) {
		h = int32(winH) - y
	}
	if w <= 0 || h <= 0 {
		return
	}
	for row := y; row < y+h; row++ {
		off := int(row)*stride + int(x)*4
		for col := int32(0); col < w; col++ {
			data[off+0] = 0x00
			data[off+1] = 0xcc
			data[off+2] = 0xff
			data[off+3] = 0xff
			off += 4
		}
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
