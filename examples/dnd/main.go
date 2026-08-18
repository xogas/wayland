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
	"github.com/xogas/wayland/example/internal/shared"
	"github.com/xogas/wayland/wire"
)

const (
	keyC = 46
	keyV = 47

	btnLeft = 272

	winW = 500
	winH = 300
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
	n, err := syscall.Read(fd, buf[:])
	_ = syscall.Close(fd)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

// transferReq is a queued clipboard/drop receive: the actual Receive + sync
// roundtrip happens in the main loop, never inside an event handler.
type transferReq struct {
	offer *wayland.DataOffer
	mime  string
	rfd   int
	wfd   int
	drop  bool // finish and destroy the offer after the transfer
}

func pickMime(mimes []string, preferred []string) string {
	for _, m := range mimes {
		for _, p := range preferred {
			if m == p {
				return m
			}
		}
	}
	if len(mimes) > 0 {
		return mimes[0]
	}
	return ""
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	dpy.SetOnError(func(pe *wayland.ProtocolError) {
		fmt.Fprintf(os.Stderr, "protocol error: obj=%d code=%d msg=%q\n", pe.ObjectID, pe.Code, pe.Message)
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
	ddmG, ok := globals.Find(wayland.InterfaceDataDeviceManager)
	if !ok {
		fmt.Fprintln(os.Stderr, "no wl_data_device_manager global")
		os.Exit(1)
	}
	ddmVersion := ddmG.Version
	ddm, err := wayland.BindDataDeviceManager(reg, ddmG.Name, min(ddmG.Version, wayland.VersionDataDeviceManager))
	if err != nil {
		fmt.Fprintf(os.Stderr, "bind data_device_manager: %v\n", err)
		os.Exit(1)
	}

	var caps wayland.SeatCapability
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		caps = ev.Capabilities
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
		os.Exit(1)
	}
	if caps&wayland.SeatCapabilityKeyboard == 0 || caps&wayland.SeatCapabilityPointer == 0 {
		fmt.Fprintln(os.Stderr, "seat needs keyboard and pointer capabilities")
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

	toplevel, err := shared.NewToplevel(ctx, dpy, core, "wayland-dnd", "wayland-dnd", winW, winH, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Static window content: four color boxes.
	winID, data, winCleanup, err := shared.NewBuffer(core.Shm, winW, winH, wayland.ShmFormatXrgb8888)
	if err != nil {
		fmt.Fprintf(os.Stderr, "window buffer: %v\n", err)
		os.Exit(1)
	}
	defer winCleanup()
	shared.FillSolid(data, 0xFF, 0xFF, 0xFF)
	for _, b := range boxes {
		shared.FillRect(data, int(winW)*4, winW, winH, int(b.x), int(b.y), int(b.w), int(b.h), b.r, b.g, b.b)
	}
	_ = toplevel.Surface.Attach(winID, 0, 0)
	_ = toplevel.Surface.Damage(0, 0, winW, winH)
	_ = toplevel.Surface.Commit()

	var kbSerial uint32
	var ptrX, ptrY int32
	offerMap := map[uint32]*wayland.DataOffer{}
	offerMimes := map[uint32][]string{}
	var selectionOffer *wayland.DataOffer
	var clipboardSource *wayland.DataSource
	var activeOfferID uint32
	var transferCh chan *transferReq

	kb.OnEnter(func(ev wayland.KeyboardEnterEvent) {
		kbSerial = ev.Serial
		fmt.Printf("keyboard: enter serial=%d\n", ev.Serial)
	})
	kb.OnLeave(func(ev wayland.KeyboardLeaveEvent) {
		fmt.Printf("keyboard: leave serial=%d\n", ev.Serial)
	})
	kb.OnKey(func(ev wayland.KeyboardKeyEvent) {
		if ev.State != wayland.KeyboardKeyStatePressed {
			return
		}
		switch ev.Key {
		case keyC:
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
		case keyV:
			if selectionOffer == nil {
				fmt.Println("clipboard: no selection offer to paste")
				return
			}
			mime := pickMime(offerMimes[selectionOffer.Proxy().ID()],
				[]string{"text/plain;charset=utf-8", "text/plain"})
			if mime == "" {
				mime = "text/plain;charset=utf-8"
			}
			rfd, wfd, err := pipe2()
			if err != nil {
				fmt.Fprintf(os.Stderr, "pipe: %v\n", err)
				return
			}
			select {
			case transferCh <- &transferReq{offer: selectionOffer, mime: mime, rfd: rfd, wfd: wfd}:
			default:
				_ = syscall.Close(rfd)
				_ = syscall.Close(wfd)
			}
		}
	})

	ptr.OnEnter(func(ev wayland.PointerEnterEvent) {
		ptrX = ev.SurfaceX.Int()
		ptrY = ev.SurfaceY.Int()
		fmt.Printf("pointer: enter serial=%d x=%d y=%d\n", ev.Serial, ptrX, ptrY)
	})
	ptr.OnLeave(func(ev wayland.PointerLeaveEvent) {
		fmt.Printf("pointer: leave serial=%d\n", ev.Serial)
	})
	ptr.OnMotion(func(ev wayland.PointerMotionEvent) {
		ptrX = ev.SurfaceX.Int()
		ptrY = ev.SurfaceY.Int()
	})
	ptr.OnButton(func(ev wayland.PointerButtonEvent) {
		st := "release"
		if ev.State == wayland.PointerButtonStatePressed {
			st = "press"
		}
		fmt.Printf("pointer: button=%d state=%s serial=%d\n", ev.Button, st, ev.Serial)
		if ev.State == wayland.PointerButtonStatePressed && ev.Button == btnLeft {
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
					mime := "<none>"
					if ev.MimeType != nil {
						mime = *ev.MimeType
					}
					fmt.Printf("dnd: target mime=%q\n", mime)
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
					_ = src.SetActions(wayland.DataDeviceManagerDndActionCopy)
				}
				_ = dd.StartDrag(wire.ObjectID(src.Proxy().ID()), wire.ObjectID(toplevel.Surface.Proxy().ID()), 0, ev.Serial)
			}
		}
	})

	dd.OnDataOffer(func(ev wayland.DataDeviceDataOfferEvent) {
		offer := ev.ID
		id := offer.Proxy().ID()
		fmt.Printf("data_device: data_offer id=%d\n", id)
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
			mime := "application/x-color"
			_ = offer.Accept(ev.Serial, &mime)
			if ddmVersion >= 3 {
				_ = offer.SetActions(wayland.DataDeviceManagerDndActionCopy, wayland.DataDeviceManagerDndActionCopy)
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
		mime := pickMime(offerMimes[activeOfferID], []string{"application/x-color"})
		if mime == "" {
			return
		}
		rfd, wfd, err := pipe2()
		if err != nil {
			fmt.Fprintf(os.Stderr, "pipe: %v\n", err)
			return
		}
		select {
		case transferCh <- &transferReq{offer: offer, mime: mime, rfd: rfd, wfd: wfd, drop: true}:
		default:
			_ = syscall.Close(rfd)
			_ = syscall.Close(wfd)
		}
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

	transferCh = make(chan *transferReq, 4)

	// Main loop: dispatch, then run queued transfers (never inside a handler).
	for {
		select {
		case <-toplevel.Closed:
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
			return
		}
		for len(transferCh) > 0 {
			req := <-transferCh
			doTransfer(dpy, ctx, req, ddmVersion, offerMap, offerMimes, &activeOfferID)
		}
	}
}

func doTransfer(dpy *wayland.Display, ctx context.Context, req *transferReq, ddmVersion uint32,
	offerMap map[uint32]*wayland.DataOffer, offerMimes map[uint32][]string, activeOfferID *uint32) {
	kind := "clipboard"
	if req.drop {
		kind = "dnd"
	}
	if err := req.offer.Receive(req.mime, req.wfd); err != nil {
		fmt.Fprintf(os.Stderr, "%s: receive: %v\n", kind, err)
		_ = syscall.Close(req.rfd)
		_ = syscall.Close(req.wfd)
		return
	}
	_ = syscall.Close(req.wfd)
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s: roundtrip: %v\n", kind, err)
		_ = syscall.Close(req.rfd)
		return
	}
	data := readAndClose(req.rfd)
	if req.drop {
		fmt.Printf("dnd: drop data=%q\n", data)
		if ddmVersion >= 3 {
			_ = req.offer.Finish()
		}
		_ = req.offer.Destroy()
		id := req.offer.Proxy().ID()
		delete(offerMap, id)
		delete(offerMimes, id)
		*activeOfferID = 0
	} else {
		fmt.Printf("clipboard: paste mime=%q data=%q\n", req.mime, data)
	}
}
