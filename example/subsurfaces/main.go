//go:build linux

// Subsurface demo: animated child surface moving on a circular path with
// sync/desync toggle and z-order control.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/example/internal/shared"
	"github.com/xogas/wayland/wire"
)

const (
	mainW = 400
	mainH = 400
	subW  = 120
	subH  = 120
	keyS  = 31
	keyR  = 19
)

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
	subcompG, ok := globals.Find(wayland.InterfaceSubcompositor)
	if !ok {
		fmt.Fprintln(os.Stderr, "no wl_subcompositor global")
		os.Exit(1)
	}
	subcomp, err := wayland.BindSubcompositor(reg, subcompG.Name, min(subcompG.Version, wayland.VersionSubcompositor))
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind subcompositor: %v\n", err)
		os.Exit(1)
	}
	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "subsurfaces", "subsurfaces-demo", mainW, mainH, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	mainSurface := toplevel.Surface

	// Static gradient main buffer.
	mainID, mainData, mainCleanup, err := shared.NewBuffer(core.Shm, mainW, mainH, wayland.ShmFormatXrgb8888)
	if err != nil {
		fmt.Fprintf(os.Stderr, "main buffer: %v\n", err)
		os.Exit(1)
	}
	defer mainCleanup()
	drawGradient(mainData, int(mainW), int(mainH), int(mainW*4))

	// Child surface + wl_subsurface role.
	subSurface, err := core.Compositor.CreateSurface()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_surface sub: %v\n", err)
		os.Exit(1)
	}
	subsurface, err := subcomp.GetSubsurface(wire.ObjectID(subSurface.Proxy().ID()), wire.ObjectID(mainSurface.Proxy().ID()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_subsurface: %v\n", err)
		os.Exit(1)
	}
	_ = subsurface.SetPosition(int32((mainW-subW)/2), int32((mainH-subH)/2))

	subBufs, err := shared.NewDoubleBuffer(core.Shm, subW, subH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sub buffers: %v\n", err)
		os.Exit(1)
	}
	defer subBufs.Close()

	kbd, err := seat.GetKeyboard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
		os.Exit(1)
	}
	desyncMode := false
	placeAbove := true
	kbd.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.State != wayland.KeyboardKeyStatePressed {
			return
		}
		switch ev.Key {
		case keyS:
			if desyncMode {
				_ = subsurface.SetSync()
				desyncMode = false
				fmt.Println("mode: sync")
			} else {
				_ = subsurface.SetDesync()
				desyncMode = true
				fmt.Println("mode: desync")
			}
		case keyR:
			if placeAbove {
				_ = subsurface.PlaceBelow(wire.ObjectID(mainSurface.Proxy().ID()))
				placeAbove = false
				fmt.Println("place_below parent")
			} else {
				_ = subsurface.PlaceAbove(wire.ObjectID(mainSurface.Proxy().ID()))
				placeAbove = true
				fmt.Println("place_above parent")
			}
		}
	})

	_ = mainSurface.Attach(mainID, 0, 0)
	_ = mainSurface.Damage(0, 0, mainW, mainH)
	_ = mainSurface.Commit()

	errCh := shared.DispatchLoop(ctx, dpy)
	start := time.Now()
	frames := 0

	for {
		select {
		case <-toplevel.Closed:
			printStats(start, frames)
			return
		case <-ctx.Done():
			printStats(start, frames)
			return
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			}
			printStats(start, frames)
			return
		case idx := <-subBufs.Free():
			drawSub(subBufs.Pixels[idx], int(subBufs.Stride), frames)
			px, py := subPosition(frames)
			_ = subsurface.SetPosition(int32(px), int32(py))

			done := make(chan struct{})
			cb, err := mainSurface.Frame()
			if err != nil {
				fmt.Fprintf(os.Stderr, "frame: %v\n", err)
				return
			}
			cb.OnDone(func(ev wayland.CallbackDoneEvent) {
				close(done)
			})

			_ = subSurface.Attach(subBufs.IDs[idx], 0, 0)
			_ = subSurface.Damage(0, 0, subW, subH)
			_ = subSurface.Commit()
			_ = mainSurface.Commit()

			select {
			case <-toplevel.Closed:
				printStats(start, frames)
				return
			case <-ctx.Done():
				printStats(start, frames)
				return
			case err := <-errCh:
				if err != nil {
					fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
				}
				printStats(start, frames)
				return
			case <-done:
			}
			frames++
			if frames%60 == 0 {
				elapsed := time.Since(start).Seconds()
				fmt.Printf("%d frames (%.1f fps)\n", frames, float64(frames)/elapsed)
			}
		}
	}
}

func drawGradient(data []byte, w, h, stride int) {
	for y := 0; y < h; y++ {
		rowOff := y * stride
		for x := 0; x < w; x++ {
			off := rowOff + x*4
			data[off+0] = uint8(float64(x) / float64(w) * 96)
			data[off+1] = uint8(float64(y) / float64(h) * 128)
			data[off+2] = uint8(40 + float64(x)/float64(w)*64 + float64(y)/float64(h)*40)
			data[off+3] = 0xff
		}
	}
}

func drawSub(data []byte, stride, frame int) {
	cx := float64(subW) * 0.5
	cy := float64(subH) * 0.5
	t := float64(frame) * 0.06

	for y := 0; y < subH; y++ {
		rowOff := y * stride
		dy := float64(y) - cy
		for x := 0; x < subW; x++ {
			dx := float64(x) - cx
			d := math.Sqrt(dx*dx + dy*dy)
			rMax := 40.0 + 20.0*math.Sin(t*1.3)
			off := rowOff + x*4
			if d < rMax {
				hue := math.Atan2(dy, dx) + t
				r := uint8((math.Sin(hue) + 1) * 0.5 * 255)
				g := uint8((math.Sin(hue+2.094) + 1) * 0.5 * 255)
				b := uint8((math.Sin(hue+4.189) + 1) * 0.5 * 255)
				fade := 1.0 - d/rMax
				data[off+0] = uint8(float64(b) * fade)
				data[off+1] = uint8(float64(g) * fade)
				data[off+2] = uint8(float64(r) * fade)
				data[off+3] = 0xff
			} else {
				data[off+0] = 0
				data[off+1] = 0
				data[off+2] = 0
				data[off+3] = 0
			}
		}
	}
}

func subPosition(frame int) (int, int) {
	radius := float64((mainW-subW)/2 - 20)
	t := float64(frame) * 0.04
	px := int(float64(mainW)*0.5 - float64(subW)*0.5 + radius*math.Cos(t))
	py := int(float64(mainH)*0.5 - float64(subH)*0.5 + radius*math.Sin(t))
	return px, py
}

func printStats(start time.Time, frames int) {
	elapsed := time.Since(start).Seconds()
	if elapsed > 0 {
		fmt.Printf("%d frames in %.1fs (%.1f fps)\n", frames, elapsed, float64(frames)/elapsed)
	}
}
