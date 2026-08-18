//go:build linux

// Connects to the Wayland compositor, binds all wl_output globals, prints
// display information (name, resolution, refresh rate, scale), then
// demonstrates dynamic global monitoring for 5 seconds.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/example/internal/shared"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dpy, reg, globals, err := shared.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	type modeInfo struct {
		width   int32
		height  int32
		refresh int32
		flags   wayland.OutputMode
	}
	type outputInfo struct {
		name        string
		description string
		modes       []modeInfo
		scale       int32
	}

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
		out.OnGeometry(func(ev wayland.OutputGeometryEvent) {})
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
			fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
			os.Exit(1)
		}
	}

	for _, o := range outputs {
		name := o.name
		if name == "" {
			name = "(unnamed)"
		}
		desc := o.description
		fmt.Printf("output: %s", name)
		if desc != "" {
			fmt.Printf(" (%s)", desc)
		}
		fmt.Println()
		if o.scale > 0 {
			fmt.Printf("\tscale: %d\n", o.scale)
		}
		for _, m := range o.modes {
			flagStr := "none"
			switch {
			case m.flags&wayland.OutputModeCurrent != 0 && m.flags&wayland.OutputModePreferred != 0:
				flagStr = "current | preferred"
			case m.flags&wayland.OutputModeCurrent != 0:
				flagStr = "current"
			case m.flags&wayland.OutputModePreferred != 0:
				flagStr = "preferred"
			}
			fmt.Printf("\t\t%dx%d @ %.3f Hz, flags: %s\n",
				m.width, m.height, float64(m.refresh)/1000.0, flagStr)
		}
	}

	fmt.Println("\n--- Dynamic monitoring: hot-plug a display or wait 5 seconds ---")
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		fmt.Printf("new global: '%s' version %d name %d\n", ev.Interface, ev.Version, ev.Name)
	})
	reg.OnGlobalRemove(func(ev wayland.RegistryGlobalRemoveEvent) {
		fmt.Printf("global removed: name %d\n", ev.Name)
	})

	// Monitor for 5 seconds, then exit (matching the original behavior).
	monitorCtx, monitorCancel := context.WithTimeout(ctx, 5*time.Second)
	defer monitorCancel()
	errCh := shared.DispatchLoop(monitorCtx, dpy)
	select {
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
			os.Exit(1)
		}
	case <-monitorCtx.Done():
	}
	fmt.Println("monitoring finished.")
}
