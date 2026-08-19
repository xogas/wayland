//go:build linux

// Binds every wl_output global, prints the display information (name,
// description, modes, scale), then watches for hot-plug events for 5 seconds.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
)

// outputInfo accumulates the state of one wl_output object.
type outputInfo struct {
	name        string
	description string
	modes       []modeInfo
	scale       int32
}

type modeInfo struct {
	width   int32
	height  int32
	refresh int32
	flags   wayland.OutputMode
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = dpy.Close() }()

	var outputs []*outputInfo
	for _, g := range globals.All() {
		if g.Interface != wayland.InterfaceOutput {
			continue
		}
		out, err := wayland.BindOutput(reg, g.Name, min(g.Version, wayland.VersionOutput))
		if err != nil {
			fmt.Fprintf(os.Stderr, "bind output %d: %v\n", g.Name, err)
			continue
		}
		oi := &outputInfo{}
		out.OnMode(func(ev wayland.OutputModeEvent) {
			oi.modes = append(oi.modes, modeInfo{ev.Width, ev.Height, ev.Refresh, ev.Flags})
		})
		out.OnScale(func(ev wayland.OutputScaleEvent) { oi.scale = ev.Factor })
		out.OnName(func(ev wayland.OutputNameEvent) { oi.name = ev.Name })
		out.OnDescription(func(ev wayland.OutputDescriptionEvent) { oi.description = ev.Description })
		outputs = append(outputs, oi)
	}

	if len(outputs) > 0 {
		if err := dpy.Roundtrip(ctx); err != nil {
			return err
		}
	}

	for _, o := range outputs {
		fmt.Printf("output: %s", o.name)
		if o.description != "" {
			fmt.Printf(" (%s)", o.description)
		}
		fmt.Println()
		if o.scale > 0 {
			fmt.Printf("\tscale: %d\n", o.scale)
		}
		for _, m := range o.modes {
			fmt.Printf("\t\t%dx%d @ %.3f Hz, flags: %s\n",
				m.width, m.height, float64(m.refresh)/1000.0, modeFlags(m.flags))
		}
	}

	fmt.Println("\n--- Dynamic monitoring: hot-plug a display or wait 5 seconds ---")
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		fmt.Printf("new global: '%s' version %d name %d\n", ev.Interface, ev.Version, ev.Name)
	})
	reg.OnGlobalRemove(func(ev wayland.RegistryGlobalRemoveEvent) {
		fmt.Printf("global removed: name %d\n", ev.Name)
	})

	monitorCtx, monitorCancel := context.WithTimeout(ctx, 5*time.Second)
	defer monitorCancel()
	errCh := shared.DispatchLoop(monitorCtx, dpy)
	select {
	case err := <-errCh:
		return err
	case <-monitorCtx.Done():
	}
	fmt.Println("monitoring finished.")
	return nil
}

// modeFlags describes the current/preferred flags of an output mode.
func modeFlags(f wayland.OutputMode) string {
	switch {
	case f&wayland.OutputModeCurrent != 0 && f&wayland.OutputModePreferred != 0:
		return "current | preferred"
	case f&wayland.OutputModeCurrent != 0:
		return "current"
	case f&wayland.OutputModePreferred != 0:
		return "preferred"
	}
	return "none"
}
