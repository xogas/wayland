//go:build linux

// A simple image viewer: decodes PNG/JPEG/GIF with the standard library,
// scales down images larger than 1600x1000, and shows them in an xdg window.
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
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(imagePath string) error {
	f, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()

	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	dstW, dstH := fitSize(img.Bounds().Dx(), img.Bounds().Dy())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = dpy.Close() }()

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		return err
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, filepath.Base(imagePath), "go-wayland-imageviewer", int32(dstW), int32(dstH), nil)
	if err != nil {
		return err
	}

	cleanup, err := shared.StaticBuffer(toplevel.Surface, core.Shm, int32(dstW), int32(dstH),
		func(pixels []byte, stride int32) { drawImage(pixels, img, dstW, dstH, int(stride)) })
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Printf("imageviewer: %dx%d %s, waiting for close or 60s timeout.\n", dstW, dstH, imagePath)

	errCh := shared.DispatchLoop(ctx, dpy)
	for {
		select {
		case <-toplevel.Closed:
			fmt.Println("window closed by compositor.")
			return nil
		case <-ctx.Done():
			fmt.Println("timeout reached.")
			return nil
		case err := <-errCh:
			return err
		}
	}
}

// fitSize scales w x h down to fit within maxWidth x maxHeight, preserving
// the aspect ratio.
func fitSize(w, h int) (int, int) {
	if w <= maxWidth && h <= maxHeight {
		return w, h
	}
	scale := float64(maxWidth) / float64(w)
	if sh := float64(maxHeight) / float64(h); sh < scale {
		scale = sh
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

// drawImage blits img into an XRGB8888 buffer with nearest-neighbor
// sampling, compositing alpha over black.
func drawImage(data []byte, img image.Image, dstW, dstH, stride int) {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	for dy := range dstH {
		for dx := range dstW {
			sx := dx*srcW/dstW + bounds.Min.X
			sy := dy*srcH/dstH + bounds.Min.Y
			c := color.RGBAModel.Convert(img.At(sx, sy)).(color.RGBA)
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
