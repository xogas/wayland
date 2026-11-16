// Command output prints the outputs advertised by the compositor.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland"
)

type registryHandler struct {
	globals *[]wayland.Global
}

func (h registryHandler) Global(g wayland.Global) {
	*h.globals = append(*h.globals, g)
}

func (h registryHandler) GlobalRemove(name uint32) {
	gs := *h.globals
	for i := range gs {
		if gs[i].Name == name {
			gs = append(gs[:i], gs[i+1:]...)
			*h.globals = gs
			return
		}
	}
}

type outputState struct {
	global         wayland.Global
	name           string
	hasName        bool
	description    string
	hasDescription bool
	width          int32
	height         int32
	refresh        int32
	hasCurrent     bool
	preferredW     int32
	preferredH     int32
	hasPreferred   bool
	scale          int32
	hasScale       bool
	x              int32
	y              int32
	physicalWidth  int32
	physicalHeight int32
	subpixel       int32
	make           string
	model          string
	transform      int32
	hasGeometry    bool
	done           bool
}

func (o *outputState) Geometry(x int32, y int32, physicalWidth int32, physicalHeight int32, subpixel int32, makeName string, model string, transform int32) {
	o.x = x
	o.y = y
	o.physicalWidth = physicalWidth
	o.physicalHeight = physicalHeight
	o.subpixel = subpixel
	o.make = makeName
	o.model = model
	o.transform = transform
	o.hasGeometry = true
}

func (o *outputState) Mode(flags uint32, width int32, height int32, refresh int32) {
	if flags&uint32(wayland.OutputModeCurrent) != 0 {
		o.width = width
		o.height = height
		o.refresh = refresh
		o.hasCurrent = true
		return
	}
	if flags&uint32(wayland.OutputModePreferred) != 0 {
		o.preferredW = width
		o.preferredH = height
		o.hasPreferred = true
	}
}

func (o *outputState) Done() {
	o.done = true
}

func (o *outputState) Scale(factor int32) {
	o.scale = factor
	o.hasScale = true
}

func (o *outputState) Name(name string) {
	o.name = name
	o.hasName = true
}

func (o *outputState) Description(description string) {
	o.description = description
	o.hasDescription = true
}

func connect() (*wayland.Display, error) {
	d, err := wayland.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect: set WAYLAND_DISPLAY (with XDG_RUNTIME_DIR) or WAYLAND_SOCKET: %w", err)
	}
	return d, nil
}

func run() error {
	d, err := connect()
	if err != nil {
		return err
	}
	defer func() {
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "output: close:", cerr)
		}
	}()

	var globals []wayland.Global
	reg, err := d.GetRegistry(registryHandler{globals: &globals})
	if err != nil {
		return fmt.Errorf("get_registry: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		return fmt.Errorf("initial roundtrip: %w", err)
	}
	if len(globals) == 0 {
		return fmt.Errorf("no globals advertised")
	}

	var outputs []*outputState
	for _, g := range globals {
		if g.Interface != "wl_output" {
			continue
		}
		state := &outputState{global: g, scale: 1}
		// BindOutput binds at the advertised version, capped by the
		// generated wl_output interface version.
		if _, err := wayland.BindOutput(reg, g, state); err != nil {
			return fmt.Errorf("bind wl_output name=%d v%d: %w", g.Name, g.Version, err)
		}
		outputs = append(outputs, state)
	}
	if len(outputs) == 0 {
		return fmt.Errorf("no outputs: zero wl_output globals advertised")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := d.Roundtrip(ctx2); err != nil {
		return fmt.Errorf("output roundtrip: %w", err)
	}

	for i, o := range outputs {
		name := "unknown"
		if o.hasName {
			name = o.name
		}
		width, height, source := o.width, o.height, "current"
		if !o.hasCurrent && o.hasPreferred {
			width, height, source = o.preferredW, o.preferredH, "preferred"
		}
		if width == 0 && height == 0 {
			source = "unknown"
		}
		var model string
		if o.make != "" || o.model != "" {
			model = fmt.Sprintf(" make=%q model=%q", o.make, o.model)
		}
		geometry := "geometry=unavailable"
		if o.hasGeometry {
			geometry = fmt.Sprintf("geometry=%d,%d %dx%dmm", o.x, o.y, o.physicalWidth, o.physicalHeight)
		}
		fmt.Printf("output %d: name=%q mode=%dx%d (%s) scale=%d refresh=%d %s%s\n",
			i, name, width, height, source, o.scale, o.refresh, geometry, model)
		if o.hasDescription {
			fmt.Printf("output %d: description=%q\n", i, o.description)
		}
	}
	fmt.Printf("outputs: %d\n", len(outputs))
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "output:", err)
		os.Exit(1)
	}
}
