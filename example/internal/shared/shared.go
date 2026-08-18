// Package shared provides small helpers shared by the wayland examples:
// display connection and global discovery, toplevel window setup with
// configure serial management, shm double buffering and a dispatch loop.
// It is not part of the public library API.
package shared

import (
	"context"
	"errors"
	"fmt"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
)

// Globals collects the registry global events of one registry scan.
type Globals struct {
	events []wayland.RegistryGlobalEvent
}

// Find returns the first global event advertising the given interface.
func (g *Globals) Find(iface string) (wayland.RegistryGlobalEvent, bool) {
	for _, ev := range g.events {
		if ev.Interface == iface {
			return ev, true
		}
	}
	return wayland.RegistryGlobalEvent{}, false
}

// All returns every collected global event.
func (g *Globals) All() []wayland.RegistryGlobalEvent {
	return g.events
}

// Version returns the advertised version of the given interface.
func (g *Globals) Version(iface string) (uint32, bool) {
	ev, ok := g.Find(iface)
	return ev.Version, ok
}

// Connect connects to the display, binds the registry and collects all
// globals with one roundtrip.
func Connect(ctx context.Context) (*wayland.Display, *wayland.Registry, *Globals, error) {
	dpy, err := wayland.Connect(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect: %w", err)
	}
	reg, err := dpy.GetRegistry()
	if err != nil {
		_ = dpy.Close()
		return nil, nil, nil, fmt.Errorf("get_registry: %w", err)
	}
	g := &Globals{}
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		g.events = append(g.events, ev)
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		_ = dpy.Close()
		return nil, nil, nil, fmt.Errorf("roundtrip: %w", err)
	}
	return dpy, reg, g, nil
}

// Core holds the three globals every toplevel example needs.
type Core struct {
	Compositor *wayland.Compositor
	Shm        *wayland.Shm
	WmBase     *xdgshell.WmBase
}

// BindCore binds wl_compositor, wl_shm and xdg_wm_base, and answers
// xdg_wm_base ping requests.
func BindCore(reg *wayland.Registry, g *Globals) (*Core, error) {
	var missing []string
	compG, ok := g.Find(wayland.InterfaceCompositor)
	if !ok {
		missing = append(missing, wayland.InterfaceCompositor)
	}
	shmG, ok := g.Find(wayland.InterfaceShm)
	if !ok {
		missing = append(missing, wayland.InterfaceShm)
	}
	wmG, ok := g.Find(xdgshell.InterfaceWmBase)
	if !ok {
		missing = append(missing, xdgshell.InterfaceWmBase)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required globals: %v", missing)
	}

	compositor, err := wayland.BindCompositor(reg, compG.Name, min(compG.Version, wayland.VersionCompositor))
	if err != nil {
		return nil, fmt.Errorf("bind compositor: %w", err)
	}
	shm, err := wayland.BindShm(reg, shmG.Name, min(shmG.Version, wayland.VersionShm))
	if err != nil {
		return nil, fmt.Errorf("bind shm: %w", err)
	}
	wmBase, err := xdgshell.BindWmBase(reg, wmG.Name, min(wmG.Version, xdgshell.VersionWmBase))
	if err != nil {
		return nil, fmt.Errorf("bind xdg_wm_base: %w", err)
	}
	wmBase.OnPing(func(ev xdgshell.WmBasePingEvent) {
		_ = wmBase.Pong(ev.Serial)
	})
	return &Core{Compositor: compositor, Shm: shm, WmBase: wmBase}, nil
}

// BindSeat binds the first wl_seat global, or returns ErrNoSeat.
var ErrNoSeat = errors.New("no wl_seat global")

func BindSeat(reg *wayland.Registry, g *Globals) (*wayland.Seat, error) {
	seatG, ok := g.Find(wayland.InterfaceSeat)
	if !ok {
		return nil, ErrNoSeat
	}
	return wayland.BindSeat(reg, seatG.Name, min(seatG.Version, wayland.VersionSeat))
}
