package shared

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
	"github.com/xogas/wayland/wire"
)

// ParseStates decodes the raw byte array of an xdg_toplevel.configure event
// into typed toplevel states.
func ParseStates(data []byte) []xdgshell.ToplevelState {
	if len(data) < 4 {
		return nil
	}
	n := len(data) / 4
	out := make([]xdgshell.ToplevelState, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, xdgshell.ToplevelState(binary.LittleEndian.Uint32(data[i*4:(i+1)*4])))
	}
	return out
}

// Toplevel is a configured xdg toplevel window. Every configure event is
// acknowledged immediately, so any later surface commit is protocol-safe.
type Toplevel struct {
	Surface    *wayland.Surface
	XdgSurface *xdgshell.Surface
	Toplevel   *xdgshell.Toplevel

	// W and H hold the latest size suggested by the compositor.
	W, H int32
	// Closed receives a token when the compositor requests the window to close.
	Closed chan struct{}

	onConfigure func(t *Toplevel, w, h int32, states []xdgshell.ToplevelState)
	configured  bool

	// Compositors may deliver xdg_toplevel.configure before the matching
	// xdg_surface.configure; the callback must not run (it usually commits)
	// until that configure has been acked, so the event is deferred.
	pendingCfg    bool
	pendingW      int32
	pendingH      int32
	pendingStates []xdgshell.ToplevelState
}

// NewToplevel creates a toplevel window, commits it and blocks until the
// first configure event arrives (5s timeout). Every configure is acked in the
// handler, so the returned window may commit at any time. onConfigure is
// invoked with the window itself after every complete configure event.
func NewToplevel(ctx context.Context, dpy *wayland.Display, core *Core, title, appID string, w, h int32, onConfigure func(t *Toplevel, w, h int32, states []xdgshell.ToplevelState)) (*Toplevel, error) {
	surface, err := core.Compositor.CreateSurface()
	if err != nil {
		return nil, fmt.Errorf("create_surface: %w", err)
	}
	xdgSurface, err := core.WmBase.GetXdgSurface(wire.ObjectID(surface.Proxy().ID()))
	if err != nil {
		return nil, fmt.Errorf("get_xdg_surface: %w", err)
	}
	toplevel, err := xdgSurface.GetToplevel()
	if err != nil {
		return nil, fmt.Errorf("get_toplevel: %w", err)
	}

	t := &Toplevel{
		Surface:     surface,
		XdgSurface:  xdgSurface,
		Toplevel:    toplevel,
		W:           w,
		H:           h,
		Closed:      make(chan struct{}, 1),
		onConfigure: onConfigure,
	}

	xdgSurface.OnConfigure(func(ev xdgshell.SurfaceConfigureEvent) {
		t.configured = true
		_ = xdgSurface.AckConfigure(ev.Serial)
		if t.pendingCfg {
			t.pendingCfg = false
			if t.onConfigure != nil {
				t.onConfigure(t, t.pendingW, t.pendingH, t.pendingStates)
			}
		}
	})
	toplevel.OnConfigure(func(ev xdgshell.ToplevelConfigureEvent) {
		if ev.Width > 0 && ev.Height > 0 {
			t.W, t.H = ev.Width, ev.Height
		}
		if !t.configured {
			// The matching xdg_surface.configure has not been acked yet:
			// defer the callback until it is, so any commit it makes is safe.
			t.pendingCfg = true
			t.pendingW, t.pendingH = t.W, t.H
			t.pendingStates = ParseStates(ev.States)
			return
		}
		if t.onConfigure != nil {
			t.onConfigure(t, t.W, t.H, ParseStates(ev.States))
		}
	})
	toplevel.OnClose(func(ev xdgshell.ToplevelCloseEvent) {
		select {
		case t.Closed <- struct{}{}:
		default:
		}
	})

	_ = toplevel.SetTitle(title)
	_ = toplevel.SetAppID(appID)
	_ = surface.Commit()

	// Dispatch until the first configure has been acked (NewToplevel runs on
	// the same goroutine as the caller's dispatch, so the flag is race-free).
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for !t.configured {
		if err := dpy.Dispatch(waitCtx); err != nil {
			if waitCtx.Err() != nil {
				return nil, errors.New("timeout waiting for configure")
			}
			return nil, err
		}
	}
	return t, nil
}
