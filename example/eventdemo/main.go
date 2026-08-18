//go:build linux

// eventdemo: unified input event viewer for wl_keyboard, wl_pointer and wl_touch.
// Every event is logged to the terminal and drawn into the window; the whole
// buffer is redrawn per event (deliberately simple, not performance-optimized).
package main

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/example/internal/shared"
)

const (
	winW   = 640
	winH   = 260
	scale  = 2
	margin = 8
)

func keyName(code uint32) string {
	names := map[uint32]string{
		1: "ESC", 2: "1", 3: "2", 4: "3", 5: "4", 6: "5", 7: "6", 8: "7",
		9: "8", 10: "9", 11: "0", 12: "-", 13: "=", 14: "BACKSPACE", 15: "TAB",
		16: "Q", 17: "W", 18: "E", 19: "R", 20: "T", 21: "Y", 22: "U",
		23: "I", 24: "O", 25: "P", 26: "[", 27: "]", 28: "ENTER", 29: "LCTRL",
		30: "A", 31: "S", 32: "D", 33: "F", 34: "G", 35: "H", 36: "J",
		37: "K", 38: "L", 39: ";", 40: "'", 41: "`", 42: "LSHIFT", 43: "\\",
		44: "Z", 45: "X", 46: "C", 47: "V", 48: "B", 49: "N", 50: "M",
		51: ",", 52: ".", 53: "/", 54: "RSHIFT", 56: "LALT", 57: "SPACE",
		58: "CAPS", 59: "F1", 60: "F2", 61: "F3", 62: "F4", 63: "F5",
		64: "F6", 65: "F7", 66: "F8", 67: "F9", 68: "F10", 87: "F11",
		88: "F12", 97: "RCTRL", 100: "RALT", 105: "LEFT", 106: "RIGHT",
		107: "DOWN", 103: "UP", 110: "INSERT", 111: "DELETE", 119: "PAUSE",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return fmt.Sprintf("key(%d)", code)
}

func btnName(code uint32) string {
	names := map[uint32]string{
		272: "left", 273: "right", 274: "middle", 275: "side1", 276: "side2",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return fmt.Sprintf("btn(%d)", code)
}

func axisName(code wayland.PointerAxis) string {
	switch code {
	case wayland.PointerAxisVerticalScroll:
		return "vertical"
	case wayland.PointerAxisHorizontalScroll:
		return "horizontal"
	}
	return "?"
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
		fmt.Fprintf(os.Stderr, "protocol error: %v\n", pe)
	})

	core, err := shared.BindCore(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	seat, err := shared.BindSeat(reg, globals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "Event Demo", "eventdemo", winW, winH, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	surface := toplevel.Surface

	stride := int32(winW * 4)
	var logLines []string
	const maxLines = 10

	// addLog prints the line and redraws the window: a fresh shm buffer is
	// created per event and destroyed right after the commit.
	addLog := func(line string) {
		fmt.Println(line)
		logLines = append(logLines, line)
		if len(logLines) > maxLines {
			logLines = logLines[len(logLines)-maxLines:]
		}
		bufID, data, cleanup, err := shared.NewBuffer(core.Shm, winW, winH, wayland.ShmFormatXrgb8888)
		if err != nil {
			fmt.Fprintf(os.Stderr, "buffer: %v\n", err)
			return
		}
		shared.FillSolid(data, 0xff, 0xff, 0xff)
		lineH := shared.TextHeight(scale)
		for i, ln := range logLines {
			shared.DrawText(data, int(stride), winW, winH, ln, margin, margin+i*lineH, scale, 0x000000)
		}
		_ = surface.Attach(bufID, 0, 0)
		_ = surface.Damage(0, 0, winW, winH)
		_ = surface.Commit()
		cleanup()
	}

	var (
		havePointer  bool
		haveKeyboard bool
		haveTouch    bool
	)

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

	// Bind the devices advertised by the initial capabilities event.
	var caps wayland.SeatCapability
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}
	if caps&wayland.SeatCapabilityPointer != 0 {
		if p, err := seat.GetPointer(); err == nil {
			registerPointer(p)
			havePointer = true
		}
	}
	if caps&wayland.SeatCapabilityKeyboard != 0 {
		if k, err := seat.GetKeyboard(); err == nil {
			registerKeyboard(k)
			haveKeyboard = true
		}
	}
	if caps&wayland.SeatCapabilityTouch != 0 {
		if t, err := seat.GetTouch(); err == nil {
			registerTouch(t)
			haveTouch = true
		}
	}

	// Follow capability changes (hot-plugged devices).
	var ready bool
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		if !ready {
			return
		}
		if ev.Capabilities&wayland.SeatCapabilityPointer != 0 && !havePointer {
			if p, err := seat.GetPointer(); err == nil {
				registerPointer(p)
				havePointer = true
			}
		}
		if ev.Capabilities&wayland.SeatCapabilityKeyboard != 0 && !haveKeyboard {
			if k, err := seat.GetKeyboard(); err == nil {
				registerKeyboard(k)
				haveKeyboard = true
			}
		}
		if ev.Capabilities&wayland.SeatCapabilityTouch != 0 && !haveTouch {
			if t, err := seat.GetTouch(); err == nil {
				registerTouch(t)
				haveTouch = true
			}
		}
	})
	ready = true

	addLog("eventdemo: listening...")

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
