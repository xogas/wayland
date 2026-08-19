package shared

import (
	"context"

	"github.com/xogas/wayland"
)

// DispatchLoop dispatches events until ctx is done or the connection dies,
// and returns a channel reporting the outcome exactly once: nil when ctx
// expires, the fatal error when the connection dies.
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

// Frame creates a wl_surface.frame callback on surface and returns a channel
// that is closed when the compositor processes the frame. Create the
// callback before the commit that frames, then select on the channel to pace
// the next frame at the compositor's refresh rate.
func Frame(surface *wayland.Surface) (<-chan struct{}, error) {
	cb, err := surface.Frame()
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	cb.OnDone(func(ev wayland.CallbackDoneEvent) {
		close(done)
	})
	return done, nil
}
