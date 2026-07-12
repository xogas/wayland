//go:build linux

// Package main implements a simple Wayland image viewer.
package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

const maxWidth = 1600
const maxHeight = 1000

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <image-file>\n", os.Args[0])
		os.Exit(1)
	}
	imagePath := os.Args[1]

	f, err := os.Open(imagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer f.Close() //nolint: errcheck

	img, _, err := image.Decode(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode: %v\n", err)
		os.Exit(1)
	}

	srcBounds := img.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	dstW, dstH := fitSize(srcW, srcH)

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

	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})

	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}

	var (
		compositorGlobal wayland.RegistryGlobalEvent
		shmGlobal        wayland.RegistryGlobalEvent
		wmBaseGlobal     wayland.RegistryGlobalEvent
	)
	for _, g := range globals {
		switch g.Interface {
		case wayland.InterfaceCompositor:
			compositorGlobal = g
		case wayland.InterfaceShm:
			shmGlobal = g
		case xdgshell.InterfaceWmBase:
			wmBaseGlobal = g
		}
	}
	if compositorGlobal.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_compositor global found")
		os.Exit(1)
	}
	if shmGlobal.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_shm global found")
		os.Exit(1)
	}
	if wmBaseGlobal.Interface == "" {
		fmt.Fprintln(os.Stderr, "no xdg_wm_base global found")
		os.Exit(1)
	}

	compositor, err := wayland.BindCompositor(reg, compositorGlobal.Name, compositorGlobal.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind compositor: %v\n", err)
		os.Exit(1)
	}
	shm, err := wayland.BindShm(reg, shmGlobal.Name, shmGlobal.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind shm: %v\n", err)
		os.Exit(1)
	}
	wmBase, err := xdgshell.BindWmBase(reg, wmBaseGlobal.Name, wmBaseGlobal.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind wm_base: %v\n", err)
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

	shutdown := make(chan struct{})
	var cfgSerial uint32

	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})

	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})

	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		close(shutdown)
	})

	title := filepath.Base(imagePath)
	_ = toplevel.SetTitle(title)
	_ = toplevel.SetAppID("go-wayland-imageviewer")
	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for cfgSerial == 0 {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure")
				return
			}
			break
		}
	}
	if cfgSerial == 0 {
		fmt.Fprintln(os.Stderr, "no configure event received")
		os.Exit(1)
	}
	_ = xdgSurface.AckConfigure(cfgSerial)

	stride := int32(dstW * 4)
	bufSize := int64(int32(dstH) * stride)

	fd, closeFd, err := shmFile(bufSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shm: %v\n", err)
		os.Exit(1)
	}
	defer closeFd()

	data, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mmap: %v\n", err)
		os.Exit(1)
	}
	defer syscall.Munmap(data) //nolint: errcheck

	drawImage(data, img, dstW, dstH, int(stride))

	pool, err := shm.CreatePool(fd, int32(bufSize))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Destroy() //nolint: errcheck

	buf, err := pool.CreateBuffer(0, int32(dstW), int32(dstH), stride, uint32(wayland.ShmFormatXrgb8888))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
		os.Exit(1)
	}
	defer buf.Destroy() //nolint: errcheck

	_ = surface.Attach(wire.ObjectID(buf.Proxy().ID()), 0, 0)
	_ = surface.Damage(0, 0, int32(dstW), int32(dstH))
	_ = surface.Commit()

	fmt.Printf("imageviewer: %dx%d %s, waiting for close or 60s timeout.\n", dstW, dstH, imagePath)

	for {
		select {
		case <-shutdown:
			fmt.Println("window closed by compositor.")
			return
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return
		default:
		}
		if err := dpy.Dispatch(ctx); err != nil {
			if ctx.Err() != nil {
				fmt.Println("timeout reached.")
				return
			}
			fmt.Fprintf(os.Stderr, "dispatch error: %v\n", err)
			break
		}
	}
}

func fitSize(w, h int) (int, int) {
	if w <= maxWidth && h <= maxHeight {
		return w, h
	}
	scaleW := float64(maxWidth) / float64(w)
	scaleH := float64(maxHeight) / float64(h)
	scale := scaleW
	if scaleH < scaleW {
		scale = scaleH
	}
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh
}

func drawImage(data []byte, img image.Image, dstW, dstH, stride int) {
	srcBounds := img.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	for dy := 0; dy < dstH; dy++ {
		for dx := 0; dx < dstW; dx++ {
			sx := dx * srcW / dstW
			sy := dy * srcH / dstH
			c := color.RGBAModel.Convert(img.At(sx+srcBounds.Min.X, sy+srcBounds.Min.Y)).(color.RGBA)
			off := dy*stride + dx*4
			switch c.A {
			case 255:
				data[off+0] = c.B
				data[off+1] = c.G
				data[off+2] = c.R
			case 0:
				data[off+0] = 0
				data[off+1] = 0
				data[off+2] = 0
			default:
				data[off+0] = byte(uint16(c.B) * uint16(c.A) / 255)
				data[off+1] = byte(uint16(c.G) * uint16(c.A) / 255)
				data[off+2] = byte(uint16(c.R) * uint16(c.A) / 255)
			}
			data[off+3] = 0xff
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
