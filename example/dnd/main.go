//go:build linux

// Clipboard and drag-and-drop demo: 4 draggable color boxes, keyboard copy/paste.
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

type colorBox struct {
	x, y, w, h int32
	r, g, b    byte
	colorHex   string
}

var boxes = []colorBox{
	{20, 20, 220, 120, 0xFF, 0x00, 0x00, "#FF0000"},
	{260, 20, 220, 120, 0x00, 0xFF, 0x00, "#00FF00"},
	{20, 160, 220, 120, 0x00, 0x00, 0xFF, "#0000FF"},
	{260, 160, 220, 120, 0xFF, 0xFF, 0x00, "#FFFF00"},
}

func boxAt(x, y int32) *colorBox {
	for i := range boxes {
		b := &boxes[i]
		if x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h {
			return b
		}
	}
	return nil
}

func fillRect(data []byte, stride int, x, y, w, h int, r, g, b byte) {
	for dy := range h {
		for dx := range w {
			off := (y+dy)*stride + (x+dx)*4
			data[off+0] = b
			data[off+1] = g
			data[off+2] = r
			data[off+3] = 0xFF
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

func pipe2() (rfd, wfd int, err error) {
	var fds [2]int
	if err := syscall.Pipe2(fds[:], syscall.O_CLOEXEC); err != nil {
		return 0, 0, err
	}
	return fds[0], fds[1], nil
}

func writeAndClose(fd int, s string) {
	data := []byte(s)
	for len(data) > 0 {
		n, err := syscall.Write(fd, data)
		if err != nil {
			break
		}
		data = data[n:]
	}
	_ = syscall.Close(fd)
}

func readAndClose(fd int) string {
	var buf [4096]byte
	n, _ := syscall.Read(fd, buf[:])
	_ = syscall.Close(fd)
	return string(buf[:n])
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	var compG, shmG, wmG, seatG, ddmG wayland.RegistryGlobalEvent
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
		case wayland.InterfaceDataDeviceManager:
			ddmG = g
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
	if ddmG.Interface == "" {
		fmt.Fprintln(os.Stderr, "no wl_data_device_manager global")
		os.Exit(1)
	}

	ddmVersion := ddmG.Version

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
	ddm, err := wayland.BindDataDeviceManager(reg, ddmG.Name, ddmG.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind data_device_manager: %v\n", err)
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

	if caps&uint32(wayland.SeatCapabilityKeyboard) == 0 {
		fmt.Fprintln(os.Stderr, "seat has no keyboard capability")
		os.Exit(1)
	}
	if caps&uint32(wayland.SeatCapabilityPointer) == 0 {
		fmt.Fprintln(os.Stderr, "seat has no pointer capability")
		os.Exit(1)
	}

	kb, err := seat.GetKeyboard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_keyboard: %v\n", err)
		os.Exit(1)
	}
	kb.OnKeymap(func(ev wayland.KeyboardKeymapEvent) {
		_ = syscall.Close(ev.Fd)
	})

	ptr, err := seat.GetPointer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_pointer: %v\n", err)
		os.Exit(1)
	}

	dd, err := ddm.GetDataDevice(wire.ObjectID(seat.Proxy().ID()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_data_device: %v\n", err)
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

	const (
		winW = 500
		winH = 300
	)

	_ = toplevel.SetTitle("wayland-dnd")
	_ = toplevel.SetAppID("wayland-dnd")

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

	stride := int32(winW * 4)
	bufSize := int64(winH) * int64(stride)
	{
		fd, closeFd, err := shmFile(bufSize)
		if err != nil {
			fmt.Fprintf(os.Stderr, "shm: %v\n", err)
			os.Exit(1)
		}
		data, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mmap: %v\n", err)
			closeFd()
			os.Exit(1)
		}
		for i := 0; i < len(data); i += 4 {
			data[i+0] = 0xFF
			data[i+1] = 0xFF
			data[i+2] = 0xFF
			data[i+3] = 0xFF
		}
		for _, b := range boxes {
			fillRect(data, int(stride), int(b.x), int(b.y), int(b.w), int(b.h), b.r, b.g, b.b)
		}
		pool, err := shm.CreatePool(fd, int32(bufSize))
		if err != nil {
			fmt.Fprintf(os.Stderr, "create_pool: %v\n", err)
			_ = syscall.Munmap(data)
			closeFd()
			os.Exit(1)
		}
		buf, err := pool.CreateBuffer(0, winW, winH, stride, uint32(wayland.ShmFormatXrgb8888))
		if err != nil {
			fmt.Fprintf(os.Stderr, "create_buffer: %v\n", err)
			_ = pool.Destroy()
			_ = syscall.Munmap(data)
			closeFd()
			os.Exit(1)
		}
		_ = surface.Attach(wire.ObjectID(buf.Proxy().ID()), 0, 0)
		_ = surface.Damage(0, 0, winW, winH)
		_ = surface.Commit()
		_ = buf.Destroy()
		_ = pool.Destroy()
		_ = syscall.Munmap(data)
		closeFd()
	}

	var kbSerial uint32
	var ptrX, ptrY int32
	offerMap := map[uint32]*wayland.DataOffer{}
	offerMimes := map[uint32][]string{}
	var selectionOffer *wayland.DataOffer
	var clipboardSource *wayland.DataSource
	var activeOfferID uint32

	conn := dpy.Conn()

	kb.OnEnter(func(ev wayland.KeyboardEnterEvent) {
		kbSerial = ev.Serial
		fmt.Printf("keyboard: enter serial=%d\n", ev.Serial)
	})
	kb.OnLeave(func(ev wayland.KeyboardLeaveEvent) {
		fmt.Printf("keyboard: leave serial=%d\n", ev.Serial)
	})
	kb.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.State != uint32(wayland.KeyboardKeyStatePressed) {
			return
		}
		switch ev.Key {
		case 46:
			if clipboardSource != nil {
				_ = clipboardSource.Destroy()
				clipboardSource = nil
			}
			src, err := ddm.CreateDataSource()
			if err != nil {
				fmt.Fprintf(os.Stderr, "create_data_source: %v\n", err)
				return
			}
			clipboardSource = src
			_ = src.Offer("text/plain;charset=utf-8")
			_ = src.Offer("text/plain")
			src.OnSend(func(ev wayland.DataSourceSendEvent) {
				fmt.Printf("clipboard: send mime=%q\n", ev.MimeType)
				payload := "wayland-dnd clipboard: " + time.Now().Format(time.RFC3339Nano)
				writeAndClose(ev.Fd, payload)
			})
			src.OnCancelled(func(ev wayland.DataSourceCancelledEvent) {
				fmt.Println("clipboard: cancelled")
				_ = src.Destroy()
				if clipboardSource == src {
					clipboardSource = nil
				}
			})
			if kbSerial == 0 {
				fmt.Fprintln(os.Stderr, "clipboard: no keyboard enter serial")
				return
			}
			_ = dd.SetSelection(wire.ObjectID(src.Proxy().ID()), kbSerial)
			fmt.Println("clipboard: copy (set_selection)")

		case 47:
			if selectionOffer == nil {
				fmt.Println("clipboard: no selection offer to paste")
				return
			}
			mime := ""
			for _, m := range offerMimes[selectionOffer.Proxy().ID()] {
				if m == "text/plain;charset=utf-8" || m == "text/plain" {
					mime = m
					break
				}
			}
			if mime == "" {
				mime = "text/plain;charset=utf-8"
			}
			rfd, wfd, err := pipe2()
			if err != nil {
				fmt.Fprintf(os.Stderr, "pipe: %v\n", err)
				return
			}
			if err := selectionOffer.Receive(mime, wfd); err != nil {
				fmt.Fprintf(os.Stderr, "receive: %v\n", err)
				_ = syscall.Close(rfd)
				_ = syscall.Close(wfd)
				return
			}
			_ = syscall.Close(wfd)
			_ = dpy.Flush()
			_ = dpy.Roundtrip(ctx)
			data := readAndClose(rfd)
			fmt.Printf("clipboard: paste mime=%q data=%q\n", mime, data)
		}
	})

	ptr.OnEnter(func(ev wayland.PointerEnterEvent) {
		ptrX = int32(ev.SurfaceX.Float64())
		ptrY = int32(ev.SurfaceY.Float64())
		fmt.Printf("pointer: enter serial=%d x=%d y=%d\n", ev.Serial, ptrX, ptrY)
	})
	ptr.OnLeave(func(ev wayland.PointerLeaveEvent) {
		fmt.Printf("pointer: leave serial=%d\n", ev.Serial)
	})
	ptr.OnMotion(func(ev wayland.PointerMotionEvent) {
		ptrX = int32(ev.SurfaceX.Float64())
		ptrY = int32(ev.SurfaceY.Float64())
	})
	ptr.OnButton(func(ev wayland.PointerButtonEvent) {
		st := "release"
		if ev.State == uint32(wayland.PointerButtonStatePressed) {
			st = "press"
		}
		fmt.Printf("pointer: button=%d state=%s serial=%d\n", ev.Button, st, ev.Serial)
		if ev.State == uint32(wayland.PointerButtonStatePressed) && ev.Button == 272 {
			b := boxAt(ptrX, ptrY)
			if b != nil {
				fmt.Printf("dnd: start_drag color=%s\n", b.colorHex)
				src, err := ddm.CreateDataSource()
				if err != nil {
					fmt.Fprintf(os.Stderr, "create_data_source: %v\n", err)
					return
				}
				_ = src.Offer("application/x-color")
				src.OnTarget(func(ev wayland.DataSourceTargetEvent) {
					fmt.Printf("dnd: target mime=%q\n", ev.MimeType)
				})
				src.OnSend(func(ev wayland.DataSourceSendEvent) {
					fmt.Printf("dnd: send mime=%q\n", ev.MimeType)
					writeAndClose(ev.Fd, b.colorHex)
				})
				src.OnCancelled(func(ev wayland.DataSourceCancelledEvent) {
					fmt.Println("dnd: cancelled")
					_ = src.Destroy()
				})
				src.OnDndDropPerformed(func(ev wayland.DataSourceDndDropPerformedEvent) {
					fmt.Println("dnd: drop_performed")
				})
				src.OnDndFinished(func(ev wayland.DataSourceDndFinishedEvent) {
					fmt.Println("dnd: finished")
					_ = src.Destroy()
				})
				if ddmVersion >= 3 {
					_ = src.SetActions(uint32(wayland.DataDeviceManagerDndActionCopy))
				}
				_ = dd.StartDrag(wire.ObjectID(src.Proxy().ID()), wire.ObjectID(surface.Proxy().ID()), 0, ev.Serial)
			}
		}
	})

	dd.OnDataOffer(func(ev wayland.DataDeviceDataOfferEvent) {
		id := uint32(ev.ID)
		fmt.Printf("data_device: data_offer id=%d\n", id)
		p := wayland.NewProxyWithID(conn, id)
		if ddmVersion >= 3 {
			p.SetVersion(ddmVersion)
		}
		conn.RegisterProxy(p)
		offer := wayland.NewDataOffer(p)
		offerMap[id] = offer
		offerMimes[id] = nil
		offer.OnOffer(func(ev wayland.DataOfferOfferEvent) {
			fmt.Printf("data_offer: offer mime=%q\n", ev.MimeType)
			offerMimes[id] = append(offerMimes[id], ev.MimeType)
		})
		offer.OnSourceActions(func(ev wayland.DataOfferSourceActionsEvent) {
			fmt.Printf("data_offer: source_actions=%d\n", ev.SourceActions)
		})
		offer.OnAction(func(ev wayland.DataOfferActionEvent) {
			fmt.Printf("data_offer: action=%d\n", ev.DndAction)
		})
	})
	dd.OnEnter(func(ev wayland.DataDeviceEnterEvent) {
		id := uint32(ev.ID)
		activeOfferID = id
		fmt.Printf("data_device: enter serial=%d surface=%d offer=%d x=%.2f y=%.2f\n",
			ev.Serial, uint32(ev.Surface), id, ev.X.Float64(), ev.Y.Float64())
		if mimes, ok := offerMimes[id]; ok {
			fmt.Printf("data_device: enter mimes=%v\n", mimes)
		}
		offer := offerMap[id]
		if offer != nil {
			_ = offer.Accept(ev.Serial, "application/x-color")
			if ddmVersion >= 3 {
				_ = offer.SetActions(uint32(wayland.DataDeviceManagerDndActionCopy), uint32(wayland.DataDeviceManagerDndActionCopy))
			}
		}
	})
	dd.OnMotion(func(ev wayland.DataDeviceMotionEvent) {
		fmt.Printf("data_device: motion time=%d x=%.2f y=%.2f\n", ev.Time, ev.X.Float64(), ev.Y.Float64())
	})
	dd.OnDrop(func(ev wayland.DataDeviceDropEvent) {
		fmt.Println("data_device: drop")
		offer := offerMap[activeOfferID]
		if offer == nil {
			return
		}
		mimes := offerMimes[activeOfferID]
		mime := ""
		for _, m := range mimes {
			if m == "application/x-color" {
				mime = m
				break
			}
		}
		if mime == "" && len(mimes) > 0 {
			mime = mimes[0]
		}
		if mime == "" {
			return
		}
		rfd, wfd, err := pipe2()
		if err != nil {
			fmt.Fprintf(os.Stderr, "pipe: %v\n", err)
			return
		}
		if err := offer.Receive(mime, wfd); err != nil {
			fmt.Fprintf(os.Stderr, "receive: %v\n", err)
			_ = syscall.Close(rfd)
			_ = syscall.Close(wfd)
			return
		}
		_ = syscall.Close(wfd)
		_ = dpy.Flush()
		_ = dpy.Roundtrip(ctx)
		data := readAndClose(rfd)
		fmt.Printf("dnd: drop data=%q\n", data)
		if ddmVersion >= 3 {
			_ = offer.Finish()
		}
		_ = offer.Destroy()
		delete(offerMap, activeOfferID)
		delete(offerMimes, activeOfferID)
		activeOfferID = 0
	})
	dd.OnLeave(func(ev wayland.DataDeviceLeaveEvent) {
		fmt.Println("data_device: leave")
		activeOfferID = 0
	})
	dd.OnSelection(func(ev wayland.DataDeviceSelectionEvent) {
		id := uint32(ev.ID)
		fmt.Printf("data_device: selection offer=%d\n", id)
		if ev.ID == 0 {
			selectionOffer = nil
			return
		}
		offer := offerMap[id]
		if offer == nil {
			fmt.Printf("data_device: selection offer=%d not found in offerMap\n", id)
			return
		}
		selectionOffer = offer
		if mimes, ok := offerMimes[id]; ok {
			fmt.Printf("data_device: selection mimes=%v\n", mimes)
		} else {
			fmt.Printf("data_device: selection (no mimes recorded)\n")
		}
	})

	fmt.Printf("wayland-dnd: window %dx%d, 120s timeout. c=copy v=paste, drag boxes with left mouse.\n", winW, winH)

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
			fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			break
		}
	}
}
