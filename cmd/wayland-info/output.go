package main

import (
	"fmt"
	"strings"

	"github.com/xogas/wayland"
)

// gatherOutput reports the wl_output geometry and modes.
func gatherOutput(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	out, err := wayland.BindOutput(s.reg, g.Name, min(g.Version, wayland.VersionOutput))
	if err != nil {
		return err
	}
	var d outputData
	out.OnGeometry(func(ev wayland.OutputGeometryEvent) {
		d.x, d.y = ev.X, ev.Y
		d.physW, d.physH = ev.PhysicalWidth, ev.PhysicalHeight
		d.subpixel = subpixelName(ev.Subpixel)
		d.make, d.model = ev.Make, ev.Model
		d.transform = ev.Transform
	})
	out.OnMode(func(ev wayland.OutputModeEvent) {
		d.modes = append(d.modes, outputMode{flags: ev.Flags, width: ev.Width, height: ev.Height, refresh: ev.Refresh})
	})
	out.OnScale(func(ev wayland.OutputScaleEvent) {
		d.scale = ev.Factor
	})
	if g.Version >= 4 {
		out.OnName(func(ev wayland.OutputNameEvent) {
			d.name = ev.Name
		})
		out.OnDescription(func(ev wayland.OutputDescriptionEvent) {
			d.description = ev.Description
		})
	}
	if err := s.drain(); err != nil {
		return err
	}
	if d.name != "" {
		fmt.Fprintf(b, "\tname: %s\n", d.name)
	}
	if d.description != "" {
		fmt.Fprintf(b, "\tdescription: %s\n", d.description)
	}
	fmt.Fprintf(b, "\tx: %d, y: %d, scale: %d\n", d.x, d.y, d.scale)
	fmt.Fprintf(b, "\tphysical_width: %d mm, physical_height: %d mm\n", d.physW, d.physH)
	fmt.Fprintf(b, "\tmake: '%s', model: '%s'\n", d.make, d.model)
	if d.subpixel != "" {
		fmt.Fprintf(b, "\tsubpixel_orientation: %s, output_transform: %s\n", d.subpixel, transformName(d.transform))
	}
	for _, m := range d.modes {
		fmt.Fprintln(b, "\tmode:")
		fmt.Fprintf(b, "\t\twidth: %d px, height: %d px, refresh: %.3f Hz\n", m.width, m.height, float64(m.refresh)/1000)
		fmt.Fprintf(b, "\t\tflags: %s\n", flagsString(m.flags))
	}
	return nil
}

// outputData collects the wl_output details.
type outputData struct {
	name, description string
	make, model       string
	subpixel          string
	x, y              int32
	physW, physH      int32
	transform         int32
	scale             int32
	modes             []outputMode
}

// outputMode is one wl_output mode.
type outputMode struct {
	flags         wayland.OutputMode
	width, height int32
	refresh       int32 // mHz
}

// subpixelName names a wl_output subpixel value, or "" if unknown.
func subpixelName(s int32) string {
	switch wayland.OutputSubpixel(s) {
	case wayland.OutputSubpixelUnknown:
		return "unknown"
	case wayland.OutputSubpixelNone:
		return "none"
	case wayland.OutputSubpixelHorizontalRgb:
		return "horizontal_rgb"
	case wayland.OutputSubpixelHorizontalBgr:
		return "horizontal_bgr"
	case wayland.OutputSubpixelVerticalRgb:
		return "vertical_rgb"
	case wayland.OutputSubpixelVerticalBgr:
		return "vertical_bgr"
	default:
		return ""
	}
}

// transformName names a wl_output transform value, or "" if unknown.
func transformName(t int32) string {
	switch wayland.OutputTransform(t) {
	case wayland.OutputTransformNormal:
		return "normal"
	case wayland.OutputTransform90:
		return "90"
	case wayland.OutputTransform180:
		return "180"
	case wayland.OutputTransform270:
		return "270"
	case wayland.OutputTransformFlipped:
		return "flipped"
	case wayland.OutputTransformFlipped90:
		return "flipped-90"
	case wayland.OutputTransformFlipped180:
		return "flipped-180"
	case wayland.OutputTransformFlipped270:
		return "flipped-270"
	default:
		return ""
	}
}

// flagsString renders the wl_output mode flags as words.
func flagsString(flags wayland.OutputMode) string {
	var parts []string
	if flags&wayland.OutputModeCurrent != 0 {
		parts = append(parts, "current")
	}
	if flags&wayland.OutputModePreferred != 0 {
		parts = append(parts, "preferred")
	}
	return strings.Join(parts, " ")
}
