package main

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/tablet"
	"github.com/xogas/wayland/wire"
)

// gatherTablet reports the tablets, pads, and tools of every seat. The
// sub-objects arrive over several roundtrips; the handlers set s.pending
// to keep draining until everything has arrived.
func gatherTablet(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	mgr, err := tablet.BindTabletManagerV2(s.reg, g.Name, min(g.Version, tablet.VersionTabletManagerV2))
	if err != nil {
		return err
	}
	var seats []*tabletSeatData
	for _, sg := range s.globals {
		if sg.Interface != wayland.InterfaceSeat {
			continue
		}
		seat, err := wayland.BindSeat(s.reg, sg.Name, min(sg.Version, wayland.VersionSeat))
		if err != nil {
			continue
		}
		ts, err := mgr.GetTabletSeat(wire.ObjectID(seat.Proxy().ID()))
		if err != nil {
			continue
		}
		sd := &tabletSeatData{}
		seat.OnName(func(ev wayland.SeatNameEvent) {
			sd.name = ev.Name
		})
		ts.OnTabletAdded(func(ev tablet.TabletSeatV2TabletAddedEvent) {
			sd.attachTablet(ev.ID)
			s.pending = true
		})
		ts.OnPadAdded(func(ev tablet.TabletSeatV2PadAddedEvent) {
			sd.attachPad(s, ev.ID)
			s.pending = true
		})
		ts.OnToolAdded(func(ev tablet.TabletSeatV2ToolAddedEvent) {
			sd.attachTool(ev.ID)
			s.pending = true
		})
		seats = append(seats, sd)
	}
	if err := s.drain(); err != nil {
		return err
	}
	for _, sd := range seats {
		// Skip tablet seats without devices, they are irrelevant.
		if len(sd.tablets) == 0 && len(sd.pads) == 0 && len(sd.tools) == 0 {
			continue
		}
		fmt.Fprintf(b, "\ttablet_seat: %s\n", sd.name)
		for _, t := range sd.tablets {
			t.print(b)
		}
		for _, p := range sd.pads {
			p.print(b)
		}
		for _, t := range sd.tools {
			t.print(b)
		}
	}
	return nil
}

// tabletSeatData collects the tablets, pads, and tools of one seat.
type tabletSeatData struct {
	name    string
	tablets []*tabletInfo
	pads    []*padInfo
	tools   []*toolInfo
}

// tabletInfo collects the static info of one zwp_tablet_v2.
type tabletInfo struct {
	name, path string
	vid, pid   uint32
	bustype    tablet.TabletV2Bustype
}

// padInfo collects the static info of one zwp_tablet_pad_v2.
type padInfo struct {
	paths   []string
	buttons uint32
	groups  []*padGroupInfo
}

// padGroupInfo collects the static info of one zwp_tablet_pad_group_v2.
type padGroupInfo struct {
	modes   uint32
	rings   int
	strips  int
	dials   int
	buttons []uint32
}

// toolInfo collects the static info of one zwp_tablet_tool_v2.
type toolInfo struct {
	toolType tablet.TabletToolV2Type
	serial   uint64
	wacom    uint64
	caps     []tablet.TabletToolV2Capability
}

// attachTablet listens for the static events of one tablet.
func (sd *tabletSeatData) attachTablet(t *tablet.TabletV2) {
	ti := &tabletInfo{}
	t.OnName(func(ev tablet.TabletV2NameEvent) {
		ti.name = ev.Name
	})
	t.OnID(func(ev tablet.TabletV2IDEvent) {
		ti.vid, ti.pid = ev.Vid, ev.Pid
	})
	t.OnPath(func(ev tablet.TabletV2PathEvent) {
		ti.path = ev.Path
	})
	t.OnBustype(func(ev tablet.TabletV2BustypeEvent) {
		ti.bustype = ev.Bustype
	})
	sd.tablets = append(sd.tablets, ti)
}

// attachPad listens for the static events of one pad, including its groups.
func (sd *tabletSeatData) attachPad(s *session, p *tablet.TabletPadV2) {
	pi := &padInfo{}
	p.OnPath(func(ev tablet.TabletPadV2PathEvent) {
		pi.paths = append(pi.paths, ev.Path)
	})
	p.OnButtons(func(ev tablet.TabletPadV2ButtonsEvent) {
		pi.buttons = ev.Buttons
	})
	p.OnGroup(func(ev tablet.TabletPadV2GroupEvent) {
		gi := &padGroupInfo{}
		ev.PadGroup.OnModes(func(ev tablet.TabletPadGroupV2ModesEvent) {
			gi.modes = ev.Modes
		})
		ev.PadGroup.OnButtons(func(ev tablet.TabletPadGroupV2ButtonsEvent) {
			gi.buttons = parseUint32s(ev.Buttons)
		})
		ev.PadGroup.OnRing(func(ev tablet.TabletPadGroupV2RingEvent) {
			gi.rings++
		})
		ev.PadGroup.OnStrip(func(ev tablet.TabletPadGroupV2StripEvent) {
			gi.strips++
		})
		ev.PadGroup.OnDial(func(ev tablet.TabletPadGroupV2DialEvent) {
			gi.dials++
		})
		pi.groups = append(pi.groups, gi)
		s.pending = true
	})
	sd.pads = append(sd.pads, pi)
}

// attachTool listens for the static events of one tool.
func (sd *tabletSeatData) attachTool(t *tablet.TabletToolV2) {
	ti := &toolInfo{}
	t.OnType(func(ev tablet.TabletToolV2TypeEvent) {
		ti.toolType = ev.ToolType
	})
	t.OnHardwareSerial(func(ev tablet.TabletToolV2HardwareSerialEvent) {
		ti.serial = uint64(ev.HardwareSerialHi)<<32 | uint64(ev.HardwareSerialLo)
	})
	t.OnHardwareIDWacom(func(ev tablet.TabletToolV2HardwareIDWacomEvent) {
		ti.wacom = uint64(ev.HardwareIDHi)<<32 | uint64(ev.HardwareIDLo)
	})
	t.OnCapability(func(ev tablet.TabletToolV2CapabilityEvent) {
		ti.caps = append(ti.caps, ev.Capability)
	})
	sd.tools = append(sd.tools, ti)
}

func (t *tabletInfo) print(b *strings.Builder) {
	fmt.Fprintf(b, "\t\ttablet: %s\n", t.name)
	fmt.Fprintf(b, "\t\t\tvendor: %d\n", t.vid)
	fmt.Fprintf(b, "\t\t\tproduct: %d\n", t.pid)
	if t.bustype != 0 {
		fmt.Fprintf(b, "\t\t\tbustype: %s (%d)\n", bustypeName(t.bustype), t.bustype)
	}
	if t.path != "" {
		fmt.Fprintf(b, "\t\t\tpath: %s\n", t.path)
	}
}

func (p *padInfo) print(b *strings.Builder) {
	fmt.Fprintln(b, "\t\tpad:")
	fmt.Fprintf(b, "\t\t\tbuttons: %d\n", p.buttons)
	for _, path := range p.paths {
		fmt.Fprintf(b, "\t\t\tpath: %s\n", path)
	}
	for _, g := range p.groups {
		fmt.Fprintln(b, "\t\t\tgroup:")
		fmt.Fprintf(b, "\t\t\t\tmodes: %d\n", g.modes)
		fmt.Fprintf(b, "\t\t\t\tstrips: %d\n", g.strips)
		fmt.Fprintf(b, "\t\t\t\trings: %d\n", g.rings)
		fmt.Fprintf(b, "\t\t\t\tdials: %d\n", g.dials)
		if len(g.buttons) > 0 {
			fmt.Fprintf(b, "\t\t\t\tbuttons: %s\n", joinUint32s(g.buttons))
		}
	}
}

func (t *toolInfo) print(b *strings.Builder) {
	fmt.Fprintf(b, "\t\ttablet_tool: %s\n", toolTypeName(t.toolType))
	if t.serial != 0 {
		fmt.Fprintf(b, "\t\t\thardware serial: 0x%x\n", t.serial)
	}
	if t.wacom != 0 {
		fmt.Fprintf(b, "\t\t\thardware wacom: 0x%x\n", t.wacom)
	}

	caps := strings.Join(toolCapabilityNames(t.caps), " ")
	if caps == "" {
		caps = "none"
	}
	fmt.Fprintf(b, "\t\t\tcapabilities: %s\n", caps)
}

// joinUint32s renders a list of numbers separated by spaces.
func joinUint32s(vs []uint32) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = fmt.Sprintf("%d", v)
	}
	return strings.Join(parts, " ")
}

func toolTypeName(t tablet.TabletToolV2Type) string {
	switch t {
	case tablet.TabletToolV2TypePen:
		return "pen"
	case tablet.TabletToolV2TypeEraser:
		return "eraser"
	case tablet.TabletToolV2TypeBrush:
		return "brush"
	case tablet.TabletToolV2TypePencil:
		return "pencil"
	case tablet.TabletToolV2TypeAirbrush:
		return "airbrush"
	case tablet.TabletToolV2TypeFinger:
		return "finger"
	case tablet.TabletToolV2TypeMouse:
		return "mouse"
	case tablet.TabletToolV2TypeLens:
		return "lens"
	default:
		return fmt.Sprintf("unknown (0x%x)", uint32(t))
	}
}

func toolCapabilityNames(caps []tablet.TabletToolV2Capability) []string {
	var names []string
	for _, c := range caps {
		switch c {
		case tablet.TabletToolV2CapabilityTilt:
			names = append(names, "tilt")
		case tablet.TabletToolV2CapabilityPressure:
			names = append(names, "pressure")
		case tablet.TabletToolV2CapabilityDistance:
			names = append(names, "distance")
		case tablet.TabletToolV2CapabilityRotation:
			names = append(names, "rotation")
		case tablet.TabletToolV2CapabilitySlider:
			names = append(names, "slider")
		case tablet.TabletToolV2CapabilityWheel:
			names = append(names, "wheel")
		}
	}
	return names
}

func bustypeName(t tablet.TabletV2Bustype) string {
	switch t {
	case tablet.TabletV2BustypeUsb:
		return "usb"
	case tablet.TabletV2BustypeBluetooth:
		return "bluetooth"
	case tablet.TabletV2BustypeVirtual:
		return "virtual"
	case tablet.TabletV2BustypeSerial:
		return "serial"
	case tablet.TabletV2BustypeI2c:
		return "i2c"
	default:
		return ""
	}
}

// parseUint32s decodes a little-endian wl_array of uint32 values.
func parseUint32s(b []byte) []uint32 {
	var out []uint32
	for i := 0; i+4 <= len(b); i += 4 {
		out = append(out, binary.LittleEndian.Uint32(b[i:i+4]))
	}
	return out
}
