package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"syscall"

	"github.com/xogas/wayland"
)

// transferReq is a queued clipboard/drop receive: the actual Receive plus
// roundtrip happens in the main loop, never inside an event handler.
type transferReq struct {
	offer *wayland.DataOffer
	mime  string
	rfd   int
	wfd   int
	drop  bool // finish and destroy the offer after the transfer
}

// pipe2 creates a pipe with both ends close-on-exec.
func pipe2() (rfd, wfd int, err error) {
	var fds [2]int
	if err := syscall.Pipe2(fds[:], syscall.O_CLOEXEC); err != nil {
		return 0, 0, err
	}
	return fds[0], fds[1], nil
}

// writeAndClose writes s to fd until the pipe is full or closed, then closes
// the fd. The compositor drains the pipe through the fd it received.
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

// readAndClose drains fd into a string and closes it.
func readAndClose(fd int) string {
	var buf [4096]byte
	n, err := syscall.Read(fd, buf[:])
	_ = syscall.Close(fd)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

// pickMime returns the first offered mime type that is in preferred, or the
// first offer if none match.
func pickMime(mimes []string, preferred []string) string {
	for _, m := range mimes {
		if slices.Contains(preferred, m) {
			return m
		}
	}
	if len(mimes) > 0 {
		return mimes[0]
	}
	return ""
}

// doTransfer performs one queued receive: request the data, wait for the
// roundtrip so the compositor writes into the pipe, then read and report it.
func (a *app) doTransfer(ctx context.Context, req *transferReq) {
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
	if err := a.dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s: roundtrip: %v\n", kind, err)
		_ = syscall.Close(req.rfd)
		return
	}
	data := readAndClose(req.rfd)
	if !req.drop {
		fmt.Printf("clipboard: paste mime=%q data=%q\n", req.mime, data)
		return
	}

	fmt.Printf("dnd: drop data=%q\n", data)
	if a.ddmVersion >= 3 {
		_ = req.offer.Finish()
	}
	_ = req.offer.Destroy()
	id := req.offer.Proxy().ID()
	delete(a.offers, id)
	delete(a.offerMimes, id)
	a.activeOfferID = 0
}
