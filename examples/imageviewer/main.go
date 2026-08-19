//go:build linux

// A simple Wayland image viewer: decodes PNG/JPEG/GIF with the standard
// library, scales down images larger than 1600x1000, and shows them in an
// xdg toplevel window.
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
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
)

const (
	maxWidth  = 1600
	maxHeight = 1000
)

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
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()
	dstW, dstH := fitSize(srcW, srcH)

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

	toplevel, err := shared.NewToplevel(ctx, dpy, core, filepath.Base(imagePath), "go-wayland-imageviewer", int32(dstW), int32(dstH), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	bufID, data, cleanup, err := shared.NewBuffer(core.Shm, int32(dstW), int32(dstH), wayland.ShmFormatXrgb8888)
	if err != nil {
		fmt.Fprintf(os.Stderr, "buffer: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	drawImage(data, img, dstW, dstH, dstW*4)
	_ = toplevel.Surface.Attach(bufID, 0, 0)
	_ = toplevel.Surface.Damage(0, 0, int32(dstW), int32(dstH))
	_ = toplevel.Surface.Commit()

	fmt.Printf("imageviewer: %dx%d %s, waiting for close or 60s timeout.\n", dstW, dstH, imagePath)

	errCh := shared.DispatchLoop(ctx, dpy)
	for {
		select {
		case <-toplevel.Closed:
			fmt.Println("window closed by compositor.")
			return
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch error: %v\n", err)
			}
			return
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
