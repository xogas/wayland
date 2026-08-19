//go:build linux

// Unified input event viewer: logs every wl_keyboard, wl_pointer and
// wl_touch event to the terminal and shows the last few lines in the window.
// The whole buffer is redrawn per event (deliberately simple, not optimized).
package main

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
)

const (
	winW   = 640
	winH   = 260
	scale  = 2
	margin = 8
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

	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		return err
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Event Demo", "eventdemo", winW, winH, nil)
	if err != nil {
		return err
	}

	var (
		logLines     []string
		havePointer  bool
		haveKeyboard bool
		haveTouch    bool
	)
	const maxLines = 10

	// addLog prints the line and redraws the window: a fresh shm buffer is
	// created per event and destroyed right after the commit.
	addLog := func(line string) {
		fmt.Println(line)
		logLines = append(logLines, line)
		if len(logLines) > maxLines {
			logLines = logLines[len(logLines)-maxLines:]
		}
		cleanup, err := shared.StaticBuffer(toplevel.Surface, core.Shm, winW, winH,
			func(pixels []byte, stride int32) {
				shared.FillSolid(pixels, 0xff, 0xff, 0xff)
				lineH := shared.TextHeight(scale)
				for i, ln := range logLines {
					shared.DrawText(pixels, int(stride), winW, winH, ln, margin, margin+i*lineH, scale, 0x000000)
				}
			})
		if err != nil {
			fmt.Fprintf(os.Stderr, "buffer: %v\n", err)
			return
		}
		cleanup()
	}

	registerPointer := func(p *wayland.Pointer) {
		p.OnEnter(func(ev wayland.PointerEnterEvent) {
			addLog(fmt.Sprintf("pointer enter: serial=%d x=%.1f y=%.1f", ev.Serial, ev.SurfaceX.Float64(), ev.SurfaceY.Float64()))
		})
		p.OnLeave(func(ev wayland.PointerLeaveEvent) {
			addLog(fmt.Sprintf("pointer leave: serial=%d", ev.Serial))
		})
		p.OnMotion(func(ev wayland.PointerMotionEvent) {
			addLog(fmt.Sprintf("pointer motion: time=%d x=%.1f y=%.1f", ev.Time, ev.SurfaceX.Float64(), ev.SurfaceY.Float64()))
		})
		p.OnButton(func(ev wayland.PointerButtonEvent) {
			st := "release"
			if ev.State == wayland.PointerButtonStatePressed {
				st = "press"
			}
			addLog(fmt.Sprintf("pointer button: %s %s serial=%d", btnName(ev.Button), st, ev.Serial))
		})
		p.OnAxis(func(ev wayland.PointerAxisEvent) {
			addLog(fmt.Sprintf("pointer axis: %s value=%.1f", axisName(ev.Axis), ev.Value.Float64()))
		})
		p.OnFrame(func(ev wayland.PointerFrameEvent) {
			addLog("pointer frame")
		})
	}

	registerKeyboard := func(k *wayland.Keyboard) {
		k.OnKeymap(func(ev wayland.KeyboardKeymapEvent) {
			_ = syscall.Close(ev.Fd)
			addLog(fmt.Sprintf("keyboard keymap: format=%d size=%d", ev.Format, ev.Size))
		})
		k.OnEnter(func(ev wayland.KeyboardEnterEvent) {
			addLog(fmt.Sprintf("keyboard enter: serial=%d", ev.Serial))
		})
		k.OnLeave(func(ev wayland.KeyboardLeaveEvent) {
			addLog(fmt.Sprintf("keyboard leave: serial=%d", ev.Serial))
		})
		k.OnKey(func(ev wayland.KeyboardKeyEvent) {
			st := "release"
			if ev.State == wayland.KeyboardKeyStatePressed {
				st = "press"
			}
			addLog(fmt.Sprintf("keyboard key: %s %s serial=%d", keyName(ev.Key), st, ev.Serial))
		})
		k.OnModifiers(func(ev wayland.KeyboardModifiersEvent) {
			addLog(fmt.Sprintf("keyboard modifiers: mods=%d latched=%d locked=%d group=%d",
				ev.ModsDepressed, ev.ModsLatched, ev.ModsLocked, ev.Group))
		})
		k.OnRepeatInfo(func(ev wayland.KeyboardRepeatInfoEvent) {
			addLog(fmt.Sprintf("keyboard repeat: rate=%d delay=%d", ev.Rate, ev.Delay))
		})
	}

	registerTouch := func(t *wayland.Touch) {
		t.OnDown(func(ev wayland.TouchDownEvent) {
			addLog(fmt.Sprintf("touch down: serial=%d id=%d x=%.1f y=%.1f", ev.Serial, ev.ID, ev.X.Float64(), ev.Y.Float64()))
		})
		t.OnUp(func(ev wayland.TouchUpEvent) {
			addLog(fmt.Sprintf("touch up: serial=%d id=%d", ev.Serial, ev.ID))
		})
		t.OnMotion(func(ev wayland.TouchMotionEvent) {
			addLog(fmt.Sprintf("touch motion: time=%d id=%d x=%.1f y=%.1f", ev.Time, ev.ID, ev.X.Float64(), ev.Y.Float64()))
		})
	}

	// Bind the devices advertised by the first capabilities event, then
	// follow later changes (hot-plugged devices).
	var caps wayland.SeatCapability
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		return err
	}
	bindDevices := func() {
		if caps&wayland.SeatCapabilityPointer != 0 && !havePointer {
			if p, err := seat.GetPointer(); err == nil {
				registerPointer(p)
				havePointer = true
			}
		}
		if caps&wayland.SeatCapabilityKeyboard != 0 && !haveKeyboard {
			if k, err := seat.GetKeyboard(); err == nil {
				registerKeyboard(k)
				haveKeyboard = true
			}
		}
		if caps&wayland.SeatCapabilityTouch != 0 && !haveTouch {
			if t, err := seat.GetTouch(); err == nil {
				registerTouch(t)
				haveTouch = true
			}
		}
	}
	bindDevices()
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
		bindDevices()
	})

	addLog("eventdemo: listening...")

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
