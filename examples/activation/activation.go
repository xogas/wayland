package main

import (
	"fmt"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/staging/xdgactivation"
	"github.com/xogas/wayland/wire"
)

// colorA and colorB are the window fill colors, stored as {B, G, R, A}.
var colorA = [4]byte{0xFF, 0x00, 0x00, 0xFF} // window A is blue
var colorB = [4]byte{0x00, 0x00, 0xFF, 0xFF} // window B is red

// commitColor fills a fresh buffer with c and commits it to the window.
func commitColor(t *shared.Toplevel, core *shared.Core, c [4]byte) error {
	cleanup, err := shared.StaticBuffer(t.Surface, core.Shm, winW, winH,
		func(pixels []byte, stride int32) { shared.FillSolid(pixels, c[2], c[1], c[0]) })
	if err != nil {
		return err
	}
	cleanup()
	return nil
}

// requestActivation asks the compositor for an activation token that targets
// targetSid, optionally seeded with the current serial and focus surface,
// then activates the target when the token arrives.
func requestActivation(activation *xdgactivation.ActivationV1, seat *wayland.Seat, serial uint32, focusSid, targetSid wire.ObjectID, mode string) {
	fmt.Printf("[%s] requesting token: serial=%d focus=%d target=%d\n", mode, serial, focusSid, targetSid)
	token, err := activation.GetActivationToken()
	if err != nil {
		fmt.Printf("[%s] get_activation_token: %v\n", mode, err)
		return
	}
	if serial != 0 {
		_ = token.SetSerial(serial, wire.ObjectID(seat.Proxy().ID()))
	}
	if focusSid != 0 {
		_ = token.SetSurface(focusSid)
	}
	token.OnDone(func(ev xdgactivation.ActivationTokenV1DoneEvent) {
		fmt.Printf("[%s] token done: token=%q\n", mode, ev.Token)
		if err := activation.Activate(ev.Token, targetSid); err != nil {
			fmt.Printf("[%s] activate error: %v\n", mode, err)
		} else {
			fmt.Printf("[%s] activate sent: token=%q surface=%d\n", mode, ev.Token, targetSid)
		}
		_ = token.Destroy()
	})
	_ = token.Commit()
	fmt.Printf("[%s] token committed\n", mode)
}
