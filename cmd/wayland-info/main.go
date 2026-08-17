package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
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
	width, height, refresh int32
	flags                  wayland.OutputMode
}

type trancheInfo struct {
	targetDevice []byte
	flags        linuxdmabuf.LinuxDmabufFeedbackV1TrancheFlags
	formats      string
}

type connectorInfo struct {
	name, description string
	connectorID       uint32
}

type dmabufInfo struct {
	mainDevice []byte
	tranches   []trancheInfo
}

type seatData struct {
	name                    string
	capabilities            wayland.SeatCapability
	repeatRate, repeatDelay int32
}

type xdgOutputInfo struct {
	wlOutputName           uint32
	name, description      string
	logX, logY, logW, logH int32
}

type outputData struct {
	name, description      string
	x, y, physW, physH     int32
	subpixel, make_, model string
	transform, scale       int32
	modes                  []modeInfo
}

type collectedData struct {
	shmFormats       []wayland.ShmFormat
	presClockID      uint32
	dmabuf           *dmabufInfo
	drmLeaseFd       int
	drmLeasePath     string
	drmLeaseConn     []connectorInfo
	cmIntents        []colormanagement.ColorManagerV1RenderIntent
	cmFeatures       []colormanagement.ColorManagerV1Feature
	cmTf             []colormanagement.ColorManagerV1TransferFunction
	cmPrimaries      []colormanagement.ColorManagerV1Primaries
	crAlphaModes     []colorrepresentation.ColorRepresentationSurfaceV1AlphaMode
	crCoeffAndRanges []coeffRange
	outputs          map[uint32]*outputData
	xdgOutputs       []*xdgOutputInfo
	seats            map[uint32]*seatData
}

type coeffRange struct {
	coefficients colorrepresentation.ColorRepresentationSurfaceV1Coefficients
	rangeVal     colorrepresentation.ColorRepresentationSurfaceV1Range
}

func main() {
	ifaceFilter := flag.String("i", "", "only show info for the specified interface")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dpy, err := wayland.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	reg, err := dpy.GetRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get_registry: %v\n", err)
		os.Exit(1)
	}

	var globals []wayland.RegistryGlobalEvent
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		globals = append(globals, ev)
	})
	if err := dpy.Roundtrip(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
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
				od.subpixel = subpixelName(ev.Subpixel)
				od.make_ = ev.Make
				od.model = ev.Model
				od.transform = ev.Transform
			})
			out.OnMode(func(ev wayland.OutputModeEvent) {
				od.modes = append(od.modes, modeInfo{
					width: ev.Width, height: ev.Height,
					refresh: ev.Refresh, flags: ev.Flags,
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
					di.tranches = append(di.tranches, trancheInfo{targetDevice: append([]byte{}, ev.Device...)})
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
						fmt.Fprintf(&sb, "    0x%08x %s mod=0x%016x\n", format, fourccStr(format), modifier)
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
				wrapped := ev.ID
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
		fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
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
			if si.capabilities&wayland.SeatCapabilityKeyboard != 0 {
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
			fmt.Fprintf(os.Stderr, "roundtrip: %v\n", err)
			os.Exit(1)
		}
	}

	var b strings.Builder
	for _, g := range globals {
		if *ifaceFilter != "" && g.Interface != *ifaceFilter {
			continue
		}
		fmt.Fprintf(&b, "  %s v%d\n", g.Interface, g.Version)
		printDetail(&b, g, cd)
	}
	fmt.Print(b.String())
}

func printDetail(b *strings.Builder, g wayland.RegistryGlobalEvent, cd *collectedData) {
	switch g.Interface {
	case wayland.InterfaceShm:
		for _, f := range cd.shmFormats {
			if name, ok := drmFourccNames[uint32(f)]; ok {
				fmt.Fprintf(b, "    format %s (0x%08x)\n", name, f)
			} else {
				fmt.Fprintf(b, "    format 0x%08x\n", f)
			}
		}

	case wayland.InterfaceOutput:
		od, ok := cd.outputs[g.Name]
		if !ok {
			return
		}
		if od.name != "" {
			fmt.Fprintf(b, "    name: %s\n", od.name)
		}
		fmt.Fprintf(b, "    %dx%d+%d+%d scale=%d\n", od.physW, od.physH, od.x, od.y, od.scale)
		for _, m := range od.modes {
			hz := float64(m.refresh) / 1000.0
			cur := ""
			if m.flags&wayland.OutputModeCurrent != 0 {
				cur = " *"
			}
			fmt.Fprintf(b, "    mode %dx%d %.2fHz%s\n", m.width, m.height, hz, cur)
		}

	case wayland.InterfaceSeat:
		sd, ok := cd.seats[g.Name]
		if !ok {
			return
		}
		if sd.name != "" {
			fmt.Fprintf(b, "    name: %s\n", sd.name)
		}
		fmt.Fprintf(b, "    caps: %s\n", capsString(sd.capabilities))
		if sd.repeatRate > 0 {
			fmt.Fprintf(b, "    repeat rate=%d delay=%d\n", sd.repeatRate, sd.repeatDelay)
		}

	case linuxdmabuf.InterfaceLinuxDmabufV1:
		if cd.dmabuf == nil {
			return
		}
		if len(cd.dmabuf.mainDevice) >= 8 {
			dev := binary.LittleEndian.Uint64(cd.dmabuf.mainDevice[:8])
			fmt.Fprintf(b, "    main device: 0x%X\n", dev)
		}
		for _, t := range cd.dmabuf.tranches {
			if len(t.targetDevice) >= 8 {
				dev := binary.LittleEndian.Uint64(t.targetDevice[:8])
				fmt.Fprintf(b, "    target device: 0x%X\n", dev)
			}
			b.WriteString(t.formats)
		}

	case presentationtime.InterfacePresentation:
		fmt.Fprintf(b, "    clock: %d\n", cd.presClockID)

	case drmlease.InterfaceDrmLeaseDeviceV1:
		if cd.drmLeasePath != "" {
			fmt.Fprintf(b, "    path: %s\n", cd.drmLeasePath)
		}
		for _, c := range cd.drmLeaseConn {
			fmt.Fprintf(b, "    connector: %s (id=%d)\n", c.name, c.connectorID)
		}

	case colormanagement.InterfaceColorManagerV1:
		for _, v := range cd.cmIntents {
			if n := intentName(v); n != "" {
				fmt.Fprintf(b, "    intent: %s\n", n)
			}
		}
		for _, v := range cd.cmFeatures {
			if n := cmFeatureName(v); n != "" {
				fmt.Fprintf(b, "    feature: %s\n", n)
			}
		}
		for _, v := range cd.cmTf {
			if n := tfName(v); n != "" {
				fmt.Fprintf(b, "    tf: %s\n", n)
			}
		}
		for _, v := range cd.cmPrimaries {
			if n := primariesName(v); n != "" {
				fmt.Fprintf(b, "    primaries: %s\n", n)
			}
		}

	case colorrepresentation.InterfaceColorRepresentationManagerV1:
		for _, cr := range cd.crCoeffAndRanges {
			fmt.Fprintf(b, "    coeff=%d range=%d\n", cr.coefficients, cr.rangeVal)
		}
	}
}

func capsString(caps wayland.SeatCapability) string {
	var parts []string
	if caps&wayland.SeatCapabilityPointer != 0 {
		parts = append(parts, "pointer")
	}
	if caps&wayland.SeatCapabilityKeyboard != 0 {
		parts = append(parts, "keyboard")
	}
	if caps&wayland.SeatCapabilityTouch != 0 {
		parts = append(parts, "touch")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// fourcc packs four ASCII characters into the little-endian uint32 that the
// DRM/wl_shm format values use on the wire.
func fourcc(a, b, c, d byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24
}

// drmFourccNames maps common DRM fourcc values to their canonical names from
// drm_fourcc.h. Unknown formats fall back to a raw hex listing.
var drmFourccNames = map[uint32]string{
	fourcc('R', '8', ' ', ' '): "R8",
	fourcc('R', '1', '6', ' '): "R16",
	fourcc('R', 'G', '8', '8'): "RG88",
	fourcc('G', 'R', '8', '8'): "GR88",
	fourcc('R', 'G', '1', '6'): "RG1616",
	fourcc('G', 'R', '1', '6'): "GR1616",
	fourcc('A', 'R', '2', '4'): "AR24",
	fourcc('X', 'R', '2', '4'): "XR24",
	fourcc('A', 'B', '2', '4'): "AB24",
	fourcc('X', 'B', '2', '4'): "XB24",
	fourcc('A', 'R', '3', '0'): "AR30",
	fourcc('X', 'R', '3', '0'): "XR30",
	fourcc('A', 'B', '3', '0'): "AB30",
	fourcc('X', 'B', '3', '0'): "XB30",
	fourcc('A', 'R', '1', '5'): "AR15",
	fourcc('X', 'R', '1', '5'): "XR15",
	fourcc('A', 'B', '1', '5'): "AB15",
	fourcc('X', 'B', '1', '5'): "XB15",
	fourcc('A', 'R', '1', '2'): "AR12",
	fourcc('X', 'R', '1', '2'): "XR12",
	fourcc('A', 'B', '1', '2'): "AB12",
	fourcc('X', 'B', '1', '2'): "XB12",
	fourcc('A', 'R', '1', '0'): "AR10",
	fourcc('X', 'R', '1', '0'): "XR10",
	fourcc('A', 'B', '1', '0'): "AB10",
	fourcc('X', 'B', '1', '0'): "XB10",
	fourcc('A', 'R', '1', '6'): "AR16",
	fourcc('X', 'R', '1', '6'): "XR16",
	fourcc('A', 'B', '1', '6'): "AB16",
	fourcc('X', 'B', '1', '6'): "XB16",
	fourcc('Y', 'U', 'Y', 'V'): "YUYV",
	fourcc('U', 'Y', 'V', 'Y'): "UYVY",
	fourcc('Y', 'V', 'Y', 'U'): "YVYU",
	fourcc('N', 'V', '1', '2'): "NV12",
	fourcc('N', 'V', '2', '1'): "NV21",
	fourcc('N', 'V', '1', '6'): "NV16",
	fourcc('N', 'V', '6', '1'): "NV61",
	fourcc('P', '0', '1', '0'): "P010",
	fourcc('P', '0', '1', '2'): "P012",
	fourcc('P', '0', '1', '6'): "P016",
	fourcc('Y', '2', '1', '0'): "Y210",
	fourcc('Y', '2', '1', '2'): "Y212",
	fourcc('Y', '2', '1', '6'): "Y216",
	fourcc('Y', '4', '1', '0'): "Y410",
	fourcc('Y', '4', '1', '2'): "Y412",
	fourcc('Y', '4', '1', '6'): "Y416",
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

func intentName(v colormanagement.ColorManagerV1RenderIntent) string {
	switch v {
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
		return ""
	}
}

func cmFeatureName(v colormanagement.ColorManagerV1Feature) string {
	switch v {
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
		return ""
	}
}

func tfName(v colormanagement.ColorManagerV1TransferFunction) string {
	switch v {
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
		return ""
	}
}

func primariesName(v colormanagement.ColorManagerV1Primaries) string {
	switch v {
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
		return ""
	}
}
