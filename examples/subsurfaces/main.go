//go:build linux

// Subsurface demo: an animated child surface orbiting the parent window,
// with sync/desync mode toggling and z-order control.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
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

	subcompG, ok := globals.Find(wayland.InterfaceSubcompositor)
	if !ok {
		return fmt.Errorf("no wl_subcompositor global")
	}
	subcomp, err := wayland.BindSubcompositor(reg, subcompG.Name, min(subcompG.Version, wayland.VersionSubcompositor))
	if err != nil {
		return fmt.Errorf("bind subcompositor: %w", err)
	}

	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		return err
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "subsurfaces", "subsurfaces-demo", mainW, mainH, nil)
	if err != nil {
		return err
	}
	mainSurface := toplevel.Surface

	// The parent content is static: a gradient.
	mainCleanup, err := shared.StaticBuffer(mainSurface, core.Shm, mainW, mainH,
		func(pixels []byte, stride int32) { drawGradient(pixels, int(mainW), int(mainH), int(stride)) })
	if err != nil {
		return err
	}
	defer mainCleanup()

	// Child surface with a wl_subsurface role on the parent.
	subSurface, err := core.Compositor.CreateSurface()
	if err != nil {
		return fmt.Errorf("create sub-surface: %w", err)
	}
	subsurface, err := subcomp.GetSubsurface(wire.ObjectID(subSurface.Proxy().ID()), wire.ObjectID(mainSurface.Proxy().ID()))
	if err != nil {
		return fmt.Errorf("get subsurface: %w", err)
	}
	_ = subsurface.SetPosition(int32((mainW-subW)/2), int32((mainH-subH)/2))

	subBufs, err := shared.NewDoubleBuffer(core.Shm, subW, subH)
	if err != nil {
		return err
	}
	defer subBufs.Close()

	// S toggles sync/desync, R toggles place_above/place_below.
	kbd, err := seat.GetKeyboard()
	if err != nil {
		return fmt.Errorf("get keyboard: %w", err)
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

	errCh := shared.DispatchLoop(ctx, dpy)
	start := time.Now()
	frames := 0

	for {
		select {
		case <-toplevel.Closed:
			shared.ReportFPS(start, frames)
			return nil
		case <-ctx.Done():
			shared.ReportFPS(start, frames)
			return nil
		case err := <-errCh:
			return err
		case idx := <-subBufs.Free():
			drawSub(subBufs.Pixels[idx], int(subBufs.Stride), frames)
			px, py := subPosition(frames)
			_ = subsurface.SetPosition(int32(px), int32(py))

			// A frame callback on the parent paces the combined commit.
			done, err := shared.Frame(mainSurface)
			if err != nil {
				return fmt.Errorf("frame: %w", err)
			}

			_ = subSurface.Attach(subBufs.IDs[idx], 0, 0)
			_ = subSurface.Damage(0, 0, subW, subH)
			_ = subSurface.Commit()
			_ = mainSurface.Commit()

			select {
			case <-done:
			case <-toplevel.Closed:
				shared.ReportFPS(start, frames)
				return nil
			case <-ctx.Done():
				shared.ReportFPS(start, frames)
				return nil
			case err := <-errCh:
				return err
			}
			frames++
			if frames%60 == 0 {
				elapsed := time.Since(start).Seconds()
				fmt.Printf("%d frames (%.1f fps)\n", frames, float64(frames)/elapsed)
			}
		}
	}
}
