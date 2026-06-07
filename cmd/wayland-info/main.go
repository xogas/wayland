package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/linuxdmabuf"
	"github.com/xogas/wayland/protocol/stable/presentationtime"
	"github.com/xogas/wayland/protocol/staging/colormanagement"
	"github.com/xogas/wayland/protocol/staging/colorrepresentation"
	"github.com/xogas/wayland/protocol/staging/drmlease"
	"github.com/xogas/wayland/protocol/unstable/xdgoutputunstable"
	"github.com/xogas/wayland/wire"
)

type modeInfo struct {
	width   int32
	height  int32
	refresh int32
	flags   uint32
}

type trancheInfo struct {
	targetDevice []byte
	flags        uint32
	formats      string
}

type connectorInfo struct {
	name        string
	description string
	connectorID uint32
}

type dmabufInfo struct {
	mainDevice []byte
	tranches   []trancheInfo
}

type seatData struct {
	name         string
	capabilities uint32
	repeatRate   int32
	repeatDelay  int32
}

type xdgOutputInfo struct {
	wlOutputName uint32
	name         string
	description  string
	logX, logY   int32
	logW, logH   int32
}

type outputData struct {
	name        string
	description string
	x, y        int32
	physW       int32
	physH       int32
	subpixel    int32
	make        string
	model       string
	transform   int32
	scale       int32
	modes       []modeInfo
}

type coeffRange struct {
	coefficients uint32
	rangeVal     uint32
}

type collectedData struct {
	shmFormats       []uint32
	presClockID      uint32
	dmabuf           *dmabufInfo
	drmLeaseFd       int
	drmLeasePath     string
	drmLeaseConn     []connectorInfo
	cmIntents        []uint32
	cmFeatures       []uint32
	cmTf             []uint32
	cmPrimaries      []uint32
	crAlphaModes     []uint32
	crCoeffAndRanges []coeffRange

	outputs    map[uint32]*outputData
	xdgOutputs []*xdgOutputInfo
	seats      map[uint32]*seatData
}

func main() {
	ifaceFilter := flag.String("i", "", "only show information for the specified interface")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dpy, err := wayland.Connect(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "wayland-info: connect: %v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	reg, err := dpy.GetRegistry()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "wayland-info: get_registry: %v\n", err)
		os.Exit(1)
	}

	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})

	if err := dpy.Roundtrip(ctx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "wayland-info: roundtrip: %v\n", err)
		os.Exit(1)
	}

	cd := &collectedData{
		outputs: make(map[uint32]*outputData),
		seats:   make(map[uint32]*seatData),
	}

	outputBindings := make(map[uint32]*wayland.Output)
	var xdgManager *xdgoutputunstable.OutputManagerV1

	for _, g := range globals {
		if *ifaceFilter != "" && g.Interface != *ifaceFilter {
			continue
		}

		switch g.Interface {
		case wayland.InterfaceOutput:
			bv := min(g.Version, wayland.VersionOutput)
			out, err := wayland.BindOutput(reg, g.Name, bv)
			if err != nil {
				continue
			}
			outputBindings[g.Name] = out
			od := &outputData{}
			cd.outputs[g.Name] = od
			out.OnGeometry(func(ev wayland.OutputGeometryEvent) {
				od.x = ev.X
				od.y = ev.Y
				od.physW = ev.PhysicalWidth
				od.physH = ev.PhysicalHeight
				od.subpixel = ev.Subpixel
				od.make = ev.Make
				od.model = ev.Model
				od.transform = ev.Transform
			})
			out.OnMode(func(ev wayland.OutputModeEvent) {
				od.modes = append(od.modes, modeInfo{
					width:   ev.Width,
					height:  ev.Height,
					refresh: ev.Refresh,
					flags:   ev.Flags,
				})
			})
			out.OnScale(func(ev wayland.OutputScaleEvent) {
				od.scale = ev.Factor
			})
			if bv >= 4 {
				out.OnName(func(ev wayland.OutputNameEvent) {
					od.name = ev.Name
				})
				out.OnDescription(func(ev wayland.OutputDescriptionEvent) {
					od.description = ev.Description
				})
			}

		case xdgoutputunstable.InterfaceOutputManagerV1:
			bv := min(g.Version, xdgoutputunstable.VersionOutputManagerV1)
			m, err := xdgoutputunstable.BindOutputManagerV1(reg, g.Name, bv)
			if err != nil {
				continue
			}
			xdgManager = m

		case wayland.InterfaceShm:
			bv := min(g.Version, wayland.VersionShm)
			shm, err := wayland.BindShm(reg, g.Name, bv)
			if err != nil {
				continue
			}
			shm.OnFormat(func(ev wayland.ShmFormatEvent) {
				cd.shmFormats = append(cd.shmFormats, ev.Format)
			})

		case wayland.InterfaceSeat:
			bv := min(g.Version, wayland.VersionSeat)
			seat, err := wayland.BindSeat(reg, g.Name, bv)
			if err != nil {
				continue
			}
			sd := &seatData{}
			cd.seats[g.Name] = sd
			seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
				sd.capabilities = ev.Capabilities
			})
			if bv >= 2 {
				seat.OnName(func(ev wayland.SeatNameEvent) {
					sd.name = ev.Name
				})
			}

		case linuxdmabuf.InterfaceLinuxDmabufV1:
			bv := min(g.Version, linuxdmabuf.VersionLinuxDmabufV1)
			dmabuf, err := linuxdmabuf.BindLinuxDmabufV1(reg, g.Name, bv)
			if err != nil {
				continue
			}
			di := &dmabufInfo{}
			cd.dmabuf = di
			if bv >= 4 {
				fb, err := dmabuf.GetDefaultFeedback()
				if err != nil {
					continue
				}
				var formatTable []byte
				fb.OnFormatTable(func(ev linuxdmabuf.LinuxDmabufFeedbackV1FormatTableEvent) {
					if ev.Size > 0 && ev.Fd >= 0 {
						data, err := syscall.Mmap(ev.Fd, 0, int(ev.Size), syscall.PROT_READ, syscall.MAP_PRIVATE)
						if err == nil {
							formatTable = make([]byte, ev.Size)
							copy(formatTable, data)
							_ = syscall.Munmap(data)
						}
						_ = syscall.Close(ev.Fd)
					}
				})
				fb.OnMainDevice(func(ev linuxdmabuf.LinuxDmabufFeedbackV1MainDeviceEvent) {
					di.mainDevice = append([]byte{}, ev.Device...)
				})
				fb.OnTrancheTargetDevice(func(ev linuxdmabuf.LinuxDmabufFeedbackV1TrancheTargetDeviceEvent) {
					ti := trancheInfo{
						targetDevice: append([]byte{}, ev.Device...),
					}
					di.tranches = append(di.tranches, ti)
				})
				fb.OnTrancheFlags(func(ev linuxdmabuf.LinuxDmabufFeedbackV1TrancheFlagsEvent) {
					if len(di.tranches) > 0 {
						di.tranches[len(di.tranches)-1].flags = ev.Flags
					}
				})
				fb.OnTrancheFormats(func(ev linuxdmabuf.LinuxDmabufFeedbackV1TrancheFormatsEvent) {
					if len(di.tranches) == 0 || len(formatTable) == 0 {
						return
					}
					ti := &di.tranches[len(di.tranches)-1]
					indices := ev.Indices
					var sb strings.Builder
					for i := 0; i+1 < len(indices); i += 2 {
						idx := binary.LittleEndian.Uint16(indices[i : i+2])
						entryOff := int(idx) * 16
						if entryOff+16 > len(formatTable) {
							continue
						}
						entry := formatTable[entryOff : entryOff+16]
						format := binary.LittleEndian.Uint32(entry[0:4])
						modifier := binary.LittleEndian.Uint64(entry[8:16])
						sb.WriteString(fmt.Sprintf("\t\t0x%08x = '%s'; 0x%016x = %s\n", format, fourccStr(format), modifier, modifierName(modifier)))
					}
					ti.formats = sb.String()
				})
			}

		case presentationtime.InterfacePresentation:
			bv := min(g.Version, presentationtime.VersionPresentation)
			pres, err := presentationtime.BindPresentation(reg, g.Name, bv)
			if err != nil {
				continue
			}
			pres.OnClockID(func(ev presentationtime.PresentationClockIDEvent) {
				cd.presClockID = ev.ClkID
			})

		case drmlease.InterfaceDrmLeaseDeviceV1:
			bv := min(g.Version, drmlease.VersionDrmLeaseDeviceV1)
			leaseDev, err := drmlease.BindDrmLeaseDeviceV1(reg, g.Name, bv)
			if err != nil {
				continue
			}
			leaseDev.OnDrmFd(func(ev drmlease.DrmLeaseDeviceV1DrmFdEvent) {
				cd.drmLeaseFd = ev.Fd
				cd.drmLeasePath = drmFdPath(ev.Fd)
				_ = syscall.Close(ev.Fd)
			})
			leaseDev.OnConnector(func(ev drmlease.DrmLeaseDeviceV1ConnectorEvent) {
				rawID := uint32(ev.ID)
				conn := leaseDev.Proxy().Conn()
				p := wayland.NewProxyWithID(conn, rawID)
				wrapped := drmlease.NewDrmLeaseConnectorV1(p)
				conn.RegisterProxy(p)
				ci := connectorInfo{}
				wrapped.OnName(func(ev drmlease.DrmLeaseConnectorV1NameEvent) {
					ci.name = ev.Name
				})
				wrapped.OnDescription(func(ev drmlease.DrmLeaseConnectorV1DescriptionEvent) {
					ci.description = ev.Description
				})
				wrapped.OnConnectorID(func(ev drmlease.DrmLeaseConnectorV1ConnectorIDEvent) {
					ci.connectorID = ev.ConnectorID
				})
				wrapped.OnDone(func(ev drmlease.DrmLeaseConnectorV1DoneEvent) {
					cd.drmLeaseConn = append(cd.drmLeaseConn, ci)
				})
			})

		case colormanagement.InterfaceColorManagerV1:
			bv := min(g.Version, colormanagement.VersionColorManagerV1)
			cm, err := colormanagement.BindColorManagerV1(reg, g.Name, bv)
			if err != nil {
				continue
			}
			cm.OnSupportedIntent(func(ev colormanagement.ColorManagerV1SupportedIntentEvent) {
				cd.cmIntents = append(cd.cmIntents, ev.RenderIntent)
			})
			cm.OnSupportedFeature(func(ev colormanagement.ColorManagerV1SupportedFeatureEvent) {
				cd.cmFeatures = append(cd.cmFeatures, ev.Feature)
			})
			cm.OnSupportedTfNamed(func(ev colormanagement.ColorManagerV1SupportedTfNamedEvent) {
				cd.cmTf = append(cd.cmTf, ev.Tf)
			})
			cm.OnSupportedPrimariesNamed(func(ev colormanagement.ColorManagerV1SupportedPrimariesNamedEvent) {
				cd.cmPrimaries = append(cd.cmPrimaries, ev.Primaries)
			})

		case colorrepresentation.InterfaceColorRepresentationManagerV1:
			bv := min(g.Version, colorrepresentation.VersionColorRepresentationManagerV1)
			cr, err := colorrepresentation.BindColorRepresentationManagerV1(reg, g.Name, bv)
			if err != nil {
				continue
			}
			cr.OnSupportedAlphaMode(func(ev colorrepresentation.ColorRepresentationManagerV1SupportedAlphaModeEvent) {
				cd.crAlphaModes = append(cd.crAlphaModes, ev.AlphaMode)
			})
			cr.OnSupportedCoefficientsAndRanges(func(ev colorrepresentation.ColorRepresentationManagerV1SupportedCoefficientsAndRangesEvent) {
				cd.crCoeffAndRanges = append(cd.crCoeffAndRanges, coeffRange{
					coefficients: ev.Coefficients,
					rangeVal:     ev.Range,
				})
			})
		}
	}

	if xdgManager != nil {
		for wlName, out := range outputBindings {
			xdgOut, err := xdgManager.GetXdgOutput(wire.ObjectID(out.Proxy().ID()))
			if err != nil {
				continue
			}
			xi := &xdgOutputInfo{wlOutputName: wlName}
			xdgOut.OnName(func(ev xdgoutputunstable.OutputV1NameEvent) {
				xi.name = ev.Name
			})
			xdgOut.OnDescription(func(ev xdgoutputunstable.OutputV1DescriptionEvent) {
				xi.description = ev.Description
			})
			xdgOut.OnLogicalPosition(func(ev xdgoutputunstable.OutputV1LogicalPositionEvent) {
				xi.logX = ev.X
				xi.logY = ev.Y
			})
			xdgOut.OnLogicalSize(func(ev xdgoutputunstable.OutputV1LogicalSizeEvent) {
				xi.logW = ev.Width
				xi.logH = ev.Height
			})
			cd.xdgOutputs = append(cd.xdgOutputs, xi)
		}
	}

	if err := dpy.Roundtrip(ctx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "wayland-info: roundtrip: %v\n", err)
		os.Exit(1)
	}

	needSecondRoundtrip := false
	for _, g := range globals {
		if *ifaceFilter != "" && g.Interface != *ifaceFilter {
			continue
		}
		if g.Interface == wayland.InterfaceSeat {
			si, ok := cd.seats[g.Name]
			if !ok {
				continue
			}
			if si.capabilities&uint32(wayland.SeatCapabilityKeyboard) != 0 {
				seat, err := wayland.BindSeat(reg, g.Name, min(g.Version, wayland.VersionSeat))
				if err != nil {
					continue
				}
				kb, err := seat.GetKeyboard()
				if err != nil {
					continue
				}
				kb.OnRepeatInfo(func(ev wayland.KeyboardRepeatInfoEvent) {
					si.repeatRate = ev.Rate
					si.repeatDelay = ev.Delay
				})
				needSecondRoundtrip = true
			}
		}
	}

	if needSecondRoundtrip {
		if err := dpy.Roundtrip(ctx); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "wayland-info: roundtrip: %v\n", err)
			os.Exit(1)
		}
	}

	printAll(os.Stdout, globals, cd, *ifaceFilter)
}

func printAll(w io.Writer, globals []wayland.RegistryGlobalEvent, cd *collectedData, filter string) {
	maxNameLen := 0
	for _, g := range globals {
		if filter != "" && g.Interface != filter {
			continue
		}
		n := len(g.Interface)
		if n > maxNameLen {
			maxNameLen = n
		}
	}
	paddedWidth := maxNameLen + len("interface: '") + len("',")

	for _, g := range globals {
		if filter != "" && g.Interface != filter {
			continue
		}
		padded := fmt.Sprintf("interface: '%s',", g.Interface)
		padSpaces := paddedWidth - len(padded)
		if padSpaces < 0 {
			padSpaces = 0
		}
		_, _ = fmt.Fprintf(w, "%s%*s version: %2d, name: %2d\n", padded, padSpaces, "", g.Version, g.Name)

		printDetail(w, g, cd)
	}
}

func printDetail(w io.Writer, g wayland.RegistryGlobalEvent, cd *collectedData) {
	switch g.Interface {
	case wayland.InterfaceShm:
		if len(cd.shmFormats) > 0 {
			_, _ = fmt.Fprintf(w, "\tformats (fourcc):\n")
			for _, f := range cd.shmFormats {
				switch f {
				case 0:
					_, _ = fmt.Fprintf(w, "\t%10d = '%s'\n", 0, "AR24")
				case 1:
					_, _ = fmt.Fprintf(w, "\t%10d = '%s'\n", 1, "XR24")
				default:
					_, _ = fmt.Fprintf(w, "\t0x%08x = '%s'\n", f, fourccStr(f))
				}
			}
		}
	case wayland.InterfaceOutput:
		od, ok := cd.outputs[g.Name]
		if !ok {
			return
		}
		if od.name != "" {
			_, _ = fmt.Fprintf(w, "\tname: %s\n", od.name)
		}
		if od.description != "" {
			_, _ = fmt.Fprintf(w, "\tdescription: %s\n", od.description)
		}
		_, _ = fmt.Fprintf(w, "\tx: %d, y: %d, scale: %d,\n", od.x, od.y, od.scale)
		_, _ = fmt.Fprintf(w, "\tphysical_width: %d mm, physical_height: %d mm,\n", od.physW, od.physH)
		_, _ = fmt.Fprintf(w, "\tmake: '%s', model: '%s',\n", od.make, od.model)
		_, _ = fmt.Fprintf(w, "\tsubpixel_orientation: %s, output_transform: %s,\n",
			subpixelName(od.subpixel), transformName(od.transform))
		for _, m := range od.modes {
			_, _ = fmt.Fprintf(w, "\tmode:\n")
			_, _ = fmt.Fprintf(w, "\t\twidth: %d px, height: %d px, refresh: %.3f Hz,\n",
				m.width, m.height, float64(m.refresh)/1000.0)
			_, _ = fmt.Fprintf(w, "\t\tflags: %s\n", modeFlagString(m.flags))
		}

	case xdgoutputunstable.InterfaceOutputManagerV1:
		for _, xi := range cd.xdgOutputs {
			_, _ = fmt.Fprintf(w, "\txdg_output_v1\n")
			_, _ = fmt.Fprintf(w, "\t\toutput: %d\n", xi.wlOutputName)
			if xi.name != "" {
				_, _ = fmt.Fprintf(w, "\t\tname: '%s'\n", xi.name)
			}
			if xi.description != "" {
				_, _ = fmt.Fprintf(w, "\t\tdescription: '%s'\n", xi.description)
			}
			_, _ = fmt.Fprintf(w, "\t\tlogical_x: %d, logical_y: %d\n", xi.logX, xi.logY)
			_, _ = fmt.Fprintf(w, "\t\tlogical_width: %d, logical_height: %d\n", xi.logW, xi.logH)
		}

	case wayland.InterfaceSeat:
		sd, ok := cd.seats[g.Name]
		if !ok {
			return
		}
		if sd.name != "" {
			_, _ = fmt.Fprintf(w, "\tname: %s\n", sd.name)
		}
		_, _ = fmt.Fprintf(w, "\tcapabilities: %s\n", capabilitiesString(sd.capabilities))
		if sd.capabilities&uint32(wayland.SeatCapabilityKeyboard) != 0 {
			_, _ = fmt.Fprintf(w, "\tkeyboard repeat rate: %d\n", sd.repeatRate)
			_, _ = fmt.Fprintf(w, "\tkeyboard repeat delay: %d\n", sd.repeatDelay)
		}
	case linuxdmabuf.InterfaceLinuxDmabufV1:
		if cd.dmabuf == nil {
			return
		}
		if len(cd.dmabuf.mainDevice) >= 8 {
			dev := binary.LittleEndian.Uint64(cd.dmabuf.mainDevice[:8])
			_, _ = fmt.Fprintf(w, "\tmain device: 0x%X", dev)
			if p := devPath(dev); p != "" {
				_, _ = fmt.Fprintf(w, " (%s)", p)
			}
			_, _ = fmt.Fprintln(w)
		}
		for _, t := range cd.dmabuf.tranches {
			_, _ = fmt.Fprintf(w, "\ttranche\n")
			if len(t.targetDevice) >= 8 {
				dev := binary.LittleEndian.Uint64(t.targetDevice[:8])
				_, _ = fmt.Fprintf(w, "\t\ttarget device: 0x%X", dev)
				if p := devPath(dev); p != "" {
					_, _ = fmt.Fprintf(w, " (%s)", p)
				}
				_, _ = fmt.Fprintln(w)
			}
			_, _ = fmt.Fprintf(w, "\t\tflags: %s\n", dmabufTrancheFlags(t.flags))
			_, _ = fmt.Fprintf(w, "\t\tformats (fourcc) and modifiers (names):\n%s", t.formats)
		}
	case presentationtime.InterfacePresentation:
		clkName := "CLOCK_REALTIME"
		if cd.presClockID == 1 {
			clkName = "CLOCK_MONOTONIC"
		}
		_, _ = fmt.Fprintf(w, "\tpresentation clock id: %d (%s)\n", cd.presClockID, clkName)
	case drmlease.InterfaceDrmLeaseDeviceV1:
		if cd.drmLeaseFd > 0 {
			_, _ = fmt.Fprintf(w, "\tpath: %s\n", cd.drmLeasePath)
		}
		for _, c := range cd.drmLeaseConn {
			_, _ = fmt.Fprintf(w, "\tconnector: %s\n", c.name)
			if c.description != "" {
				_, _ = fmt.Fprintf(w, "\t\tdescription: %s\n", c.description)
			}
			_, _ = fmt.Fprintf(w, "\t\tconnector id: %d\n", c.connectorID)
		}
	case colormanagement.InterfaceColorManagerV1:
		if len(cd.cmIntents) > 0 {
			_, _ = fmt.Fprintf(w, "\tsupported rendering intents:\n")
			for _, v := range cd.cmIntents {
				_, _ = fmt.Fprintf(w, "\t\t%s\n", renderIntentName(v))
			}
		}
		if len(cd.cmFeatures) > 0 {
			_, _ = fmt.Fprintf(w, "\tsupported features:\n")
			for _, v := range cd.cmFeatures {
				_, _ = fmt.Fprintf(w, "\t\t%s\n", cmFeatureName(v))
			}
		}
		if len(cd.cmTf) > 0 {
			_, _ = fmt.Fprintf(w, "\tsupported transfer functions:\n")
			for _, v := range cd.cmTf {
				_, _ = fmt.Fprintf(w, "\t\t%s\n", tfName(v))
			}
		}
		if len(cd.cmPrimaries) > 0 {
			_, _ = fmt.Fprintf(w, "\tsupported primaries:\n")
			for _, v := range cd.cmPrimaries {
				_, _ = fmt.Fprintf(w, "\t\t%s\n", primariesName(v))
			}
		}
	case colorrepresentation.InterfaceColorRepresentationManagerV1:
		if len(cd.crAlphaModes) > 0 {
			_, _ = fmt.Fprintf(w, "\tsupported alpha modes:\n")
			for _, v := range cd.crAlphaModes {
				_, _ = fmt.Fprintf(w, "\t\t%d\n", v)
			}
		}
		if len(cd.crCoeffAndRanges) > 0 {
			_, _ = fmt.Fprintf(w, "\tsupported matrix coefficients and ranges:\n")
			for _, cr := range cd.crCoeffAndRanges {
				_, _ = fmt.Fprintf(w, "\t\tcoefficients: %d, range: %d\n", cr.coefficients, cr.rangeVal)
			}
		}
	}
}

func fourccStr(v uint32) string {
	var b [4]byte
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	for i := 0; i < 4; i++ {
		if b[i] < 32 || b[i] > 126 {
			b[i] = '?'
		}
	}
	return string(b[:])
}

func modifierName(m uint64) string {
	switch m {
	case 0:
		return "LINEAR"
	case 0x0100000000000001:
		return "INTEL_X_TILED"
	case 0x0100000000000002:
		return "INTEL_Y_TILED"
	case 0x0100000000000004:
		return "INTEL_Y_TILED_CCS"
	default:
		return "UNKNOWN"
	}
}

func devPath(dev uint64) string {
	entries, err := os.ReadDir("/dev/dri")
	if err != nil {
		return ""
	}
	var paths []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "card") && !strings.HasPrefix(name, "renderD") {
			continue
		}
		path := "/dev/dri/" + name
		var stat syscall.Stat_t
		if err := syscall.Stat(path, &stat); err != nil {
			continue
		}
		if stat.Rdev == dev {
			paths = append(paths, path)
		}
	}
	return strings.Join(paths, " or ")
}

func drmFdPath(fd int) string {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return fmt.Sprintf("unknown (fd=%d)", fd)
	}
	p := devPath(stat.Rdev)
	if p == "" {
		return fmt.Sprintf("rdev=0x%X", stat.Rdev)
	}
	return p
}

func dmabufTrancheFlags(flags uint32) string {
	var parts []string
	if flags&uint32(linuxdmabuf.LinuxDmabufFeedbackV1TrancheFlagsScanout) != 0 {
		parts = append(parts, "scanout")
	}
	if flags&uint32(linuxdmabuf.LinuxDmabufFeedbackV1TrancheFlagsSampling) != 0 {
		parts = append(parts, "sampling")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " | ")
}

func capabilitiesString(caps uint32) string {
	var parts []string
	if caps&uint32(wayland.SeatCapabilityPointer) != 0 {
		parts = append(parts, "pointer")
	}
	if caps&uint32(wayland.SeatCapabilityKeyboard) != 0 {
		parts = append(parts, "keyboard")
	}
	if caps&uint32(wayland.SeatCapabilityTouch) != 0 {
		parts = append(parts, "touch")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

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
		return fmt.Sprintf("unknown(%d)", s)
	}
}

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
		return "flipped_90"
	case wayland.OutputTransformFlipped180:
		return "flipped_180"
	case wayland.OutputTransformFlipped270:
		return "flipped_270"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

func modeFlagString(flags uint32) string {
	var parts []string
	if flags&uint32(wayland.OutputModeCurrent) != 0 {
		parts = append(parts, "current")
	}
	if flags&uint32(wayland.OutputModePreferred) != 0 {
		parts = append(parts, "preferred")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " | ")
}

func renderIntentName(v uint32) string {
	switch colormanagement.ColorManagerV1RenderIntent(v) {
	case colormanagement.ColorManagerV1RenderIntentPerceptual:
		return "perceptual"
	case colormanagement.ColorManagerV1RenderIntentRelative:
		return "relative"
	case colormanagement.ColorManagerV1RenderIntentSaturation:
		return "saturation"
	case colormanagement.ColorManagerV1RenderIntentAbsolute:
		return "absolute"
	case colormanagement.ColorManagerV1RenderIntentRelativeBpc:
		return "relative_bpc"
	case colormanagement.ColorManagerV1RenderIntentAbsoluteNoAdaptation:
		return "absolute_no_adaptation"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}

func cmFeatureName(v uint32) string {
	switch colormanagement.ColorManagerV1Feature(v) {
	case colormanagement.ColorManagerV1FeatureIccV2V4:
		return "icc_v2_v4"
	case colormanagement.ColorManagerV1FeatureParametric:
		return "parametric"
	case colormanagement.ColorManagerV1FeatureSetPrimaries:
		return "set_primaries"
	case colormanagement.ColorManagerV1FeatureSetTfPower:
		return "set_tf_power"
	case colormanagement.ColorManagerV1FeatureSetLuminances:
		return "set_luminances"
	case colormanagement.ColorManagerV1FeatureSetMasteringDisplayPrimaries:
		return "set_mastering_display_primaries"
	case colormanagement.ColorManagerV1FeatureExtendedTargetVolume:
		return "extended_target_volume"
	case colormanagement.ColorManagerV1FeatureWindowsScrgb:
		return "windows_scrgb"
	case colormanagement.ColorManagerV1FeatureWindowsBt2100:
		return "windows_bt2100"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}

func tfName(v uint32) string {
	switch colormanagement.ColorManagerV1TransferFunction(v) {
	case colormanagement.ColorManagerV1TransferFunctionBt1886:
		return "bt.1886"
	case colormanagement.ColorManagerV1TransferFunctionGamma22:
		return "gamma-2.2"
	case colormanagement.ColorManagerV1TransferFunctionGamma28:
		return "gamma-2.8"
	case colormanagement.ColorManagerV1TransferFunctionSt240:
		return "st240"
	case colormanagement.ColorManagerV1TransferFunctionExtLinear:
		return "ext_linear"
	case colormanagement.ColorManagerV1TransferFunctionLog100:
		return "log_100"
	case colormanagement.ColorManagerV1TransferFunctionLog316:
		return "log_316"
	case colormanagement.ColorManagerV1TransferFunctionXvycc:
		return "xvycc"
	case colormanagement.ColorManagerV1TransferFunctionSrgb:
		return "srgb"
	case colormanagement.ColorManagerV1TransferFunctionExtSrgb:
		return "ext_srgb"
	case colormanagement.ColorManagerV1TransferFunctionSt2084Pq:
		return "st2084_pq"
	case colormanagement.ColorManagerV1TransferFunctionSt428:
		return "st428"
	case colormanagement.ColorManagerV1TransferFunctionHlg:
		return "hlg"
	case colormanagement.ColorManagerV1TransferFunctionCompoundPower24:
		return "compound_power_24"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}

func primariesName(v uint32) string {
	switch colormanagement.ColorManagerV1Primaries(v) {
	case colormanagement.ColorManagerV1PrimariesSrgb:
		return "srgb"
	case colormanagement.ColorManagerV1PrimariesPalM:
		return "pal_m"
	case colormanagement.ColorManagerV1PrimariesPal:
		return "pal"
	case colormanagement.ColorManagerV1PrimariesNtsc:
		return "ntsc"
	case colormanagement.ColorManagerV1PrimariesGenericFilm:
		return "generic_film"
	case colormanagement.ColorManagerV1PrimariesBt2020:
		return "bt.2020"
	case colormanagement.ColorManagerV1PrimariesCie1931Xyz:
		return "cie_1931_xyz"
	case colormanagement.ColorManagerV1PrimariesDciP3:
		return "dci-p3"
	case colormanagement.ColorManagerV1PrimariesDisplayP3:
		return "display_p3"
	case colormanagement.ColorManagerV1PrimariesAdobeRgb:
		return "adobe_rgb"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}
