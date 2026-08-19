package main

import (
	"fmt"
	"strings"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/unstable/xdgoutputunstable"
	"github.com/xogas/wayland/wire"
)

// gatherXdgOutputManager reports the xdg_output of every wl_output.
func gatherXdgOutputManager(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	mgr, err := xdgoutputunstable.BindOutputManagerV1(s.reg, g.Name, min(g.Version, xdgoutputunstable.VersionOutputManagerV1))
	if err != nil {
		return err
	}
	var outputs []*xdgOutputData
	for _, og := range s.globals {
		if og.Interface != wayland.InterfaceOutput {
			continue
		}
		out, err := wayland.BindOutput(s.reg, og.Name, min(og.Version, wayland.VersionOutput))
		if err != nil {
			continue
		}
		xo, err := mgr.GetXdgOutput(wire.ObjectID(out.Proxy().ID()))
		if err != nil {
			continue
		}
		d := &xdgOutputData{outputID: og.Name}
		xo.OnName(func(ev xdgoutputunstable.OutputV1NameEvent) {
			d.name = ev.Name
		})
		xo.OnDescription(func(ev xdgoutputunstable.OutputV1DescriptionEvent) {
			d.description = ev.Description
		})
		xo.OnLogicalPosition(func(ev xdgoutputunstable.OutputV1LogicalPositionEvent) {
			d.logX, d.logY = ev.X, ev.Y
		})
		xo.OnLogicalSize(func(ev xdgoutputunstable.OutputV1LogicalSizeEvent) {
			d.logW, d.logH = ev.Width, ev.Height
		})
		outputs = append(outputs, d)
	}
	if err := s.drain(); err != nil {
		return err
	}
	for _, d := range outputs {
		fmt.Fprintln(b, "\txdg_output_v1")
		fmt.Fprintf(b, "\t\toutput: %d\n", d.outputID)
		if d.name != "" {
			fmt.Fprintf(b, "\t\tname: '%s'\n", d.name)
		}
		if d.description != "" {
			fmt.Fprintf(b, "\t\tdescription: '%s'\n", d.description)
		}
		fmt.Fprintf(b, "\t\tlogical_x: %d, logical_y: %d\n", d.logX, d.logY)
		fmt.Fprintf(b, "\t\tlogical_width: %d, logical_height: %d\n", d.logW, d.logH)
	}
	return nil
}

// xdgOutputData collects the xdg_output details of one wl_output.
type xdgOutputData struct {
	outputID    uint32 // registry name of the wl_output global
	name        string
	description string
	logX, logY  int32
	logW, logH  int32
}
