//go:build linux

// eventdemo: unified input event viewer for wl_keyboard, wl_pointer and wl_touch.
package main

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

var keyNames = map[uint32]string{
	1: "Esc",
	2: "1", 3: "2", 4: "3", 5: "4", 6: "5", 7: "6", 8: "7", 9: "8", 10: "9", 11: "0",
	12: "-", 13: "=",
	14: "BS",
	15: "Tab",
	16: "Q", 17: "W", 18: "E", 19: "R", 20: "T", 21: "Y", 22: "U", 23: "I", 24: "O", 25: "P",
	26: "[", 27: "]",
	28: "Enter",
	29: "LCtrl",
	30: "A", 31: "S", 32: "D", 33: "F", 34: "G", 35: "H", 36: "J", 37: "K", 38: "L",
	39: ";", 40: "'",
	41: "`",
	42: "LShift",
	43: "\\",
	44: "Z", 45: "X", 46: "C", 47: "V", 48: "B", 49: "N", 50: "M",
	51: ",", 52: ".", 53: "/",
	54: "RShift",
	55: "KP*",
	56: "LAlt",
	57: "Space",
	58: "Caps",
	59: "F1", 60: "F2", 61: "F3", 62: "F4", 63: "F5", 64: "F6", 65: "F7", 66: "F8", 67: "F9", 68: "F10",
	87: "F11", 88: "F12",
	96:  "KPEnt",
	97:  "RCtrl",
	100: "RAlt",
	102: "Home",
	103: "Up",
	104: "PgUp",
	105: "Left",
	106: "Right",
	107: "End",
	108: "Down",
	109: "PgDn",
	110: "Ins",
	111: "Del",
	125: "LMeta",
	126: "RMeta",
	127: "Compose",
}

func keyName(code uint32) string {
	if n, ok := keyNames[code]; ok {
		return n
	}
	return fmt.Sprintf("%d", code)
}

var btnNames = map[uint32]string{
	0x110: "L",
	0x111: "R",
	0x112: "M",
	0x113: "S",
	0x114: "E",
}

func btnName(code uint32) string {
	if n, ok := btnNames[code]; ok {
		return n
	}
	return fmt.Sprintf("0x%x", code)
}

func axisName(code uint32) string {
	switch code {
	case 0:
		return "V"
	case 1:
		return "H"
	}
	return fmt.Sprintf("?%d", code)
}

func fillWhite(data []byte) {
	for i := 0; i < len(data); i += 4 {
		data[i+0] = 0xFF
		data[i+1] = 0xFF
		data[i+2] = 0xFF
		data[i+3] = 0xFF
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

	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})

	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}

	var compG, shmG, wmG, seatG wayland.RegistryGlobalEvent
	for _, g := range globals {
		switch g.Interface {
		case wayland.InterfaceCompositor:
			compG = g
		case wayland.InterfaceShm:
			shmG = g
		case xdgshell.InterfaceWmBase:
			wmG = g
		case wayland.InterfaceSeat:
			if seatG.Interface == "" {
				seatG = g
			}
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
	if seatG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_seat global")
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
	seat, err := wayland.BindSeat(reg, seatG.Name, seatG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind seat: %v\n", err)
		os.Exit(1)
	}

	var caps uint32
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}

	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) { _ = wmBase.Pong(ev.Serial) })

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

	_ = toplevel.SetTitle("Event Demo")
	_ = toplevel.SetAppID("eventdemo")

	var cfgSerial uint32
	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		cfgSerial = ev.Serial
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {})

	shutdown := make(chan struct{})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) { close(shutdown) })

	_ = surface.Commit()

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for cfgSerial == 0 {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				fmt.Fprintln(os.Stderr, "timeout waiting for configure")
				os.Exit(1)
			}
			break
		}
	}
	if cfgSerial == 0 {
		fmt.Fprintln(os.Stderr, "no configure event received")
		os.Exit(1)
	}
	_ = xdgSurface.AckConfigure(cfgSerial)

	const (
		winW   = 640
		winH   = 260
		scale  = 2
		margin = 8
	)
	stride := int32(winW * 4)
	bufSize := int64(winH) * int64(stride)

	var logLines []string
	const maxLines = 10

	addLog := func(line string) {
		fmt.Println(line)
		logLines = append(logLines, line)
		if len(logLines) > maxLines {
			logLines = logLines[len(logLines)-maxLines:]
		}
		fd, closeFd, err := shmFile(bufSize)
		if err != nil {
			fmt.Fprintf(os.Stderr, "shm: %v\n", err)
			return
		}
		data, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mmap: %v\n", err)
			closeFd()
			return
		}
		fillWhite(data)
		lineH := textHeight(scale)
		for i, ln := range logLines {
			drawText(data, int(stride), int(winW), int(winH), ln, margin, margin+i*lineH, scale, 0x000000)
		}
		pool, err := shm.CreatePool(fd, int32(bufSize))
		if err != nil {
			fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
			_ = syscall.Munmap(data)
			closeFd()
			return
		}
		buf, err := pool.CreateBuffer(0, winW, winH, stride, uint32(wayland.ShmFormatXrgb8888))
		if err != nil {
			fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
			_ = pool.Destroy()
			_ = syscall.Munmap(data)
			closeFd()
			return
		}
		_ = surface.Attach(wire.ObjectID(buf.Proxy().ID()), 0, 0)
		_ = surface.Damage(0, 0, winW, winH)
		_ = surface.Commit()
		_ = buf.Destroy()
		_ = pool.Destroy()
		_ = syscall.Munmap(data)
		closeFd()
	}

	registerPointer := func(p *wayland.Pointer) {
		p.OnEnter(func(ev wayland.PointerEnterEvent) {
			addLog(fmt.Sprintf("ptr enter: s=%d x=%.1f y=%.1f", ev.Serial, ev.SurfaceX.Float64(), ev.SurfaceY.Float64()))
		})
		p.OnLeave(func(ev wayland.PointerLeaveEvent) {
			addLog(fmt.Sprintf("ptr leave: s=%d", ev.Serial))
		})
		p.OnMotion(func(ev wayland.PointerMotionEvent) {
			addLog(fmt.Sprintf("ptr motion: t=%d x=%.1f y=%.1f", ev.Time, ev.SurfaceX.Float64(), ev.SurfaceY.Float64()))
		})
		p.OnButton(func(ev wayland.PointerButtonEvent) {
			st := "up"
			if ev.State == 1 {
				st = "dn"
			}
			addLog(fmt.Sprintf("ptr btn: %s %s t=%d", btnName(ev.Button), st, ev.Time))
		})
		p.OnAxis(func(ev wayland.PointerAxisEvent) {
			addLog(fmt.Sprintf("ptr axis: %s %.1f t=%d", axisName(ev.Axis), ev.Value.Float64(), ev.Time))
		})
		p.OnFrame(func(ev wayland.PointerFrameEvent) {
			addLog("ptr frame")
		})
	}

	registerKeyboard := func(k *wayland.Keyboard) {
		k.OnKeymap(func(ev wayland.KeyboardKeymapEvent) {
			fmtName := "none"
			if ev.Format == 1 {
				fmtName = "xkb_v1"
			}
			line := fmt.Sprintf("keymap: fmt=%s size=%d", fmtName, ev.Size)
			data, err := syscall.Mmap(ev.Fd, 0, int(ev.Size), syscall.PROT_READ, syscall.MAP_PRIVATE)
			if err != nil {
				line += " mmap_ERR"
			} else {
				preview := len(data)
				if preview > 32 {
					preview = 32
				}
				line += fmt.Sprintf(" [% x]", data[:preview])
				_ = syscall.Munmap(data)
			}
			_ = syscall.Close(ev.Fd)
			addLog(line)
		})
		k.OnEnter(func(ev wayland.KeyboardEnterEvent) {
			addLog(fmt.Sprintf("kbd enter: s=%d", ev.Serial))
		})
		k.OnLeave(func(ev wayland.KeyboardLeaveEvent) {
			addLog(fmt.Sprintf("kbd leave: s=%d", ev.Serial))
		})
		k.OnKey(func(ev wayland.KeyboardKeyEvent) {
			st := "rel"
			switch ev.State {
			case 1:
				st = "prs"
			case 2:
				st = "rpt"
			}
			addLog(fmt.Sprintf("kbd key: c=%d st=%s n=%s", ev.Key, st, keyName(ev.Key)))
		})
		k.OnModifiers(func(ev wayland.KeyboardModifiersEvent) {
			addLog(fmt.Sprintf("kbd mod: d=%d l=%d k=%d g=%d", ev.ModsDepressed, ev.ModsLatched, ev.ModsLocked, ev.Group))
		})
		k.OnRepeatInfo(func(ev wayland.KeyboardRepeatInfoEvent) {
			addLog(fmt.Sprintf("kbd rpt: rate=%d delay=%d", ev.Rate, ev.Delay))
		})
	}

	registerTouch := func(t *wayland.Touch) {
		t.OnDown(func(ev wayland.TouchDownEvent) {
			addLog(fmt.Sprintf("tch down: s=%d id=%d x=%.1f y=%.1f", ev.Serial, ev.ID, ev.X.Float64(), ev.Y.Float64()))
		})
		t.OnUp(func(ev wayland.TouchUpEvent) {
			addLog(fmt.Sprintf("tch up: s=%d id=%d", ev.Serial, ev.ID))
		})
		t.OnMotion(func(ev wayland.TouchMotionEvent) {
			addLog(fmt.Sprintf("tch motion: id=%d x=%.1f y=%.1f", ev.ID, ev.X.Float64(), ev.Y.Float64()))
		})
	}

	var (
		havePointer  bool
		haveKeyboard bool
		haveTouch    bool
	)

	if caps&uint32(wayland.SeatCapabilityPointer) != 0 {
		p, err := seat.GetPointer()
		if err == nil {
			registerPointer(p)
			havePointer = true
		}
	}
	if caps&uint32(wayland.SeatCapabilityKeyboard) != 0 {
		k, err := seat.GetKeyboard()
		if err == nil {
			registerKeyboard(k)
			haveKeyboard = true
		}
	}
	if caps&uint32(wayland.SeatCapabilityTouch) != 0 {
		t, err := seat.GetTouch()
		if err == nil {
			registerTouch(t)
			haveTouch = true
		}
	}

	var ready bool
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		if !ready {
			return
		}
		if ev.Capabilities&uint32(wayland.SeatCapabilityPointer) != 0 && !havePointer {
			p, err := seat.GetPointer()
			if err == nil {
				registerPointer(p)
				havePointer = true
			}
		}
		if ev.Capabilities&uint32(wayland.SeatCapabilityKeyboard) != 0 && !haveKeyboard {
			k, err := seat.GetKeyboard()
			if err == nil {
				registerKeyboard(k)
				haveKeyboard = true
			}
		}
		if ev.Capabilities&uint32(wayland.SeatCapabilityTouch) != 0 && !haveTouch {
			t, err := seat.GetTouch()
			if err == nil {
				registerTouch(t)
				haveTouch = true
			}
		}
	})
	ready = true

	addLog("eventdemo: listening...")

	fmt.Printf("eventdemo: window %dx%d, listening for input events (60s timeout).\n", winW, winH)

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
