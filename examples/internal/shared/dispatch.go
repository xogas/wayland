package shared

import (
	"context"

	"github.com/xogas/wayland"
)

// DispatchLoop dispatches events until ctx is done or the connection dies,
// and returns a channel reporting the outcome exactly once. The channel
// receives nil when ctx expires and the fatal error when the connection dies.
func DispatchLoop(ctx context.Context, dpy *wayland.Display) <-chan error {
	ch := make(chan error, 1)
	go func() {
		for {
			if err := dpy.Dispatch(ctx); err != nil {
				if ctx.Err() != nil {
					ch <- nil
				} else {
					ch <- err
				}
				close(ch)
				return
			}
		}
	}()
	return ch
}
