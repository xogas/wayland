package main

import (
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/wire"
)

// copy puts the current time as text/plain onto the clipboard.
func (a *app) copy() {
	if a.clipboardSource != nil {
		_ = a.clipboardSource.Destroy()
		a.clipboardSource = nil
	}
	src, err := a.ddm.CreateDataSource()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create_data_source: %v\n", err)
		return
	}
	a.clipboardSource = src
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
		if a.clipboardSource == src {
			a.clipboardSource = nil
		}
	})
	if a.kbSerial == 0 {
		fmt.Fprintln(os.Stderr, "clipboard: no keyboard enter serial")
		return
	}
	_ = a.dd.SetSelection(wire.ObjectID(src.Proxy().ID()), a.kbSerial)
	fmt.Println("clipboard: copy (set_selection)")
}

// paste receives the current clipboard selection as text.
func (a *app) paste() {
	if a.selectionOffer == nil {
		fmt.Println("clipboard: no selection offer to paste")
		return
	}
	mime := pickMime(a.offerMimes[a.selectionOffer.Proxy().ID()],
		[]string{"text/plain;charset=utf-8", "text/plain"})
	if mime == "" {
		mime = "text/plain;charset=utf-8"
	}
	rfd, wfd, err := pipe2()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pipe: %v\n", err)
		return
	}
	a.queueTransfer(&transferReq{offer: a.selectionOffer, mime: mime, rfd: rfd, wfd: wfd})
}

// startDrag begins a drag-and-drop of the box under the pointer, if any.
func (a *app) startDrag(b *colorBox, serial uint32) {
	if b == nil {
		return
	}
	fmt.Printf("dnd: start_drag color=%s\n", b.colorHex)
	src, err := a.ddm.CreateDataSource()
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
	if a.ddmVersion >= 3 {
		_ = src.SetActions(wayland.DataDeviceManagerDndActionCopy)
	}
	_ = a.dd.StartDrag(wire.ObjectID(src.Proxy().ID()), wire.ObjectID(a.toplevel.Surface.Proxy().ID()), 0, serial)
}

// addOffer records a new data offer announced by the compositor.
func (a *app) addOffer(offer *wayland.DataOffer) {
	id := offer.Proxy().ID()
	fmt.Printf("data_device: data_offer id=%d\n", id)
	a.offers[id] = offer
	a.offerMimes[id] = nil
	offer.OnOffer(func(ev wayland.DataOfferOfferEvent) {
		fmt.Printf("data_offer: offer mime=%q\n", ev.MimeType)
		a.offerMimes[id] = append(a.offerMimes[id], ev.MimeType)
	})
	offer.OnSourceActions(func(ev wayland.DataOfferSourceActionsEvent) {
		fmt.Printf("data_offer: source_actions=%d\n", ev.SourceActions)
	})
	offer.OnAction(func(ev wayland.DataOfferActionEvent) {
		fmt.Printf("data_offer: action=%d\n", ev.DndAction)
	})
}

// onDragEnter accepts the hovered offer and offers to copy it.
func (a *app) onDragEnter(ev wayland.DataDeviceEnterEvent) {
	id := uint32(ev.ID)
	a.activeOfferID = id
	fmt.Printf("data_device: enter serial=%d surface=%d offer=%d x=%.2f y=%.2f\n",
		ev.Serial, uint32(ev.Surface), id, ev.X.Float64(), ev.Y.Float64())
	if mimes, ok := a.offerMimes[id]; ok {
		fmt.Printf("data_device: enter mimes=%v\n", mimes)
	}
	if offer := a.offers[id]; offer != nil {
		mime := "application/x-color"
		_ = offer.Accept(ev.Serial, &mime)
		if a.ddmVersion >= 3 {
			_ = offer.SetActions(wayland.DataDeviceManagerDndActionCopy, wayland.DataDeviceManagerDndActionCopy)
		}
	}
}

// onDrop receives the dropped data and reports it.
func (a *app) onDrop() {
	fmt.Println("data_device: drop")
	offer := a.offers[a.activeOfferID]
	if offer == nil {
		return
	}
	mime := pickMime(a.offerMimes[a.activeOfferID], []string{"application/x-color"})
	if mime == "" {
		return
	}
	rfd, wfd, err := pipe2()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pipe: %v\n", err)
		return
	}
	a.queueTransfer(&transferReq{offer: offer, mime: mime, rfd: rfd, wfd: wfd, drop: true})
}

// onSelection tracks the clipboard selection offer.
func (a *app) onSelection(ev wayland.DataDeviceSelectionEvent) {
	id := uint32(ev.ID)
	fmt.Printf("data_device: selection offer=%d\n", id)
	if ev.ID == 0 {
		a.selectionOffer = nil
		return
	}
	offer := a.offers[id]
	if offer == nil {
		fmt.Printf("data_device: selection offer=%d not found in offers\n", id)
		return
	}
	a.selectionOffer = offer
	if mimes, ok := a.offerMimes[id]; ok {
		fmt.Printf("data_device: selection mimes=%v\n", mimes)
	} else {
		fmt.Printf("data_device: selection (no mimes recorded)\n")
	}
}
