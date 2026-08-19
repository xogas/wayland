package main

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/staging/colormanagement"
	"github.com/xogas/wayland/protocol/staging/colorrepresentation"
	"github.com/xogas/wayland/wire"
)

// gatherColorManager reports the supported intents, features, transfer
// functions, and primaries, plus every output's image description.
func gatherColorManager(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	bv := min(g.Version, colormanagement.VersionColorManagerV1)
	cm, err := colormanagement.BindColorManagerV1(s.reg, g.Name, bv)
	if err != nil {
		return err
	}
	var d colorManagerData
	cm.OnSupportedIntent(func(ev colormanagement.ColorManagerV1SupportedIntentEvent) {
		d.intents = append(d.intents, ev.RenderIntent)
	})
	cm.OnSupportedFeature(func(ev colormanagement.ColorManagerV1SupportedFeatureEvent) {
		d.features = append(d.features, ev.Feature)
	})
	cm.OnSupportedTfNamed(func(ev colormanagement.ColorManagerV1SupportedTfNamedEvent) {
		d.tfs = append(d.tfs, ev.Tf)
	})
	cm.OnSupportedPrimariesNamed(func(ev colormanagement.ColorManagerV1SupportedPrimariesNamedEvent) {
		d.primaries = append(d.primaries, ev.Primaries)
	})
	// Per-output image descriptions need color manager v2 and a wl_output proxy to attach to.
	if bv >= 2 {
		for _, og := range s.globals {
			if og.Interface != wayland.InterfaceOutput {
				continue
			}
			out, err := wayland.BindOutput(s.reg, og.Name, min(og.Version, wayland.VersionOutput))
			if err != nil {
				continue
			}
			d.outputs = append(d.outputs, newColorOutput(s, cm, og.Name, out))
		}
	}
	if err := s.drain(); err != nil {
		return err
	}
	sections := []struct {
		title string
		names []string
	}{
		{"supported rendering intents", named(d.intents, intentName)},
		{"supported features", named(d.features, featureName)},
		{"supported named transfer functions", named(d.tfs, tfName)},
		{"supported named primaries", named(d.primaries, primariesName)},
	}
	for _, sec := range sections {
		fmt.Fprintf(b, "\t%s:\n", sec.title)
		for _, n := range sec.names {
			fmt.Fprintf(b, "\t\t%s\n", n)
		}
	}
	for _, o := range d.outputs {
		o.print(b)
	}
	return nil
}

// colorManagerData collects the wp_color_manager_v1 details.
type colorManagerData struct {
	intents   []colormanagement.ColorManagerV1RenderIntent
	features  []colormanagement.ColorManagerV1Feature
	tfs       []colormanagement.ColorManagerV1TransferFunction
	primaries []colormanagement.ColorManagerV1Primaries
	outputs   []*colorOutputData
}

// newColorOutput wires up the color-management objects for one wl_output. The ready
// handler creates the info object and schedules another roundtrip for its events.
func newColorOutput(s *session, cm *colormanagement.ColorManagerV1, id uint32, out *wayland.Output) *colorOutputData {
	d := &colorOutputData{outputID: id}
	colorOut, err := cm.GetOutput(wire.ObjectID(out.Proxy().ID()))
	if err != nil {
		return d
	}
	desc, err := colorOut.GetImageDescription()
	if err != nil {
		return d
	}
	desc.OnFailed(func(ev colormanagement.ImageDescriptionV1FailedEvent) {
		d.failed = true
	})
	desc.OnReady(func(ev colormanagement.ImageDescriptionV1ReadyEvent) { //nolint:staticcheck
		d.identity = uint64(ev.Identity)
		d.attachInfo(s, desc)
	})
	desc.OnReady2(func(ev colormanagement.ImageDescriptionV1Ready2Event) {
		d.identity = uint64(ev.IdentityHi)<<32 | uint64(ev.IdentityLo)
		d.attachInfo(s, desc)
	})
	return d
}

// colorOutputData collects the image description of one wl_output.
type colorOutputData struct {
	outputID uint32
	identity uint64
	failed   bool

	hasICC  bool
	iccSize uint32

	primaries      colorPrimaries
	hasNamedPrim   bool
	namedPrim      colormanagement.ColorManagerV1Primaries
	hasTFPower     bool
	tfPower        uint32
	hasTFNamed     bool
	tfNamed        colormanagement.ColorManagerV1TransferFunction
	minLum, maxLum uint32
	refLum         uint32

	targetPrimaries colorPrimaries
	targetMinLum    uint32
	targetMaxLum    uint32
	hasMaxCLL       bool
	maxCLL          uint32
	hasMaxFALL      bool
	maxFALL         uint32
}

// attachInfo asks for the image description details and collects them.
func (d *colorOutputData) attachInfo(s *session, desc *colormanagement.ImageDescriptionV1) {
	info, err := desc.GetInformation()
	if err != nil {
		return
	}
	info.OnIccFile(func(ev colormanagement.ImageDescriptionInfoV1IccFileEvent) {
		d.hasICC = true
		d.iccSize = ev.IccSize
		_ = syscall.Close(ev.Icc)
	})
	info.OnPrimaries(func(ev colormanagement.ImageDescriptionInfoV1PrimariesEvent) {
		d.primaries = colorPrimaries{ev.RX, ev.RY, ev.GX, ev.GY, ev.BX, ev.BY, ev.WX, ev.WY}
	})
	info.OnPrimariesNamed(func(ev colormanagement.ImageDescriptionInfoV1PrimariesNamedEvent) {
		d.hasNamedPrim = true
		d.namedPrim = ev.Primaries
	})
	info.OnTfPower(func(ev colormanagement.ImageDescriptionInfoV1TfPowerEvent) {
		d.hasTFPower = true
		d.tfPower = ev.Eexp
	})
	info.OnTfNamed(func(ev colormanagement.ImageDescriptionInfoV1TfNamedEvent) {
		d.hasTFNamed = true
		d.tfNamed = ev.Tf
	})
	info.OnLuminances(func(ev colormanagement.ImageDescriptionInfoV1LuminancesEvent) {
		d.minLum, d.maxLum, d.refLum = ev.MinLum, ev.MaxLum, ev.ReferenceLum
	})
	info.OnTargetPrimaries(func(ev colormanagement.ImageDescriptionInfoV1TargetPrimariesEvent) {
		d.targetPrimaries = colorPrimaries{ev.RX, ev.RY, ev.GX, ev.GY, ev.BX, ev.BY, ev.WX, ev.WY}
	})
	info.OnTargetLuminance(func(ev colormanagement.ImageDescriptionInfoV1TargetLuminanceEvent) {
		d.targetMinLum, d.targetMaxLum = ev.MinLum, ev.MaxLum
	})
	info.OnTargetMaxCll(func(ev colormanagement.ImageDescriptionInfoV1TargetMaxCllEvent) {
		d.hasMaxCLL = true
		d.maxCLL = ev.MaxCll
	})
	info.OnTargetMaxFall(func(ev colormanagement.ImageDescriptionInfoV1TargetMaxFallEvent) {
		d.hasMaxFALL = true
		d.maxFALL = ev.MaxFall
	})
	s.pending = true
}

// print writes the per-output image description details.
func (d *colorOutputData) print(b *strings.Builder) {
	fmt.Fprintf(b, "\toutput: %d\n", d.outputID)
	if d.failed {
		fmt.Fprintln(b, "\t\tdescription failed")
		return
	}
	fmt.Fprintf(b, "\t\timage description id: %d\n", d.identity)
	if d.hasICC {
		fmt.Fprintf(b, "\t\ticc file: size %d\n", d.iccSize)
		return
	}
	if d.primaries != (colorPrimaries{}) {
		fmt.Fprintln(b, "\t\tprimaries (xy):")
		d.primaries.print(b, "\t\t\t")
	}
	if d.hasNamedPrim {
		fmt.Fprintf(b, "\t\tprimaries_named: %s\n", nameOf(d.namedPrim, primariesName))
	}
	if d.hasTFPower {
		fmt.Fprintf(b, "\t\ttf_power: %s\n", fixedPoint(int32(d.tfPower), 10_000))
	}
	if d.hasTFNamed {
		fmt.Fprintf(b, "\t\ttf_named: %s\n", nameOf(d.tfNamed, tfName))
	}
	fmt.Fprintf(b, "\t\tluminances (cd/m2): min %s max %d reference %d\n",
		fixedPoint(int32(d.minLum), 10_000), d.maxLum, d.refLum)
	if d.targetPrimaries != (colorPrimaries{}) {
		fmt.Fprintln(b, "\t\ttarget primaries (xy):")
		d.targetPrimaries.print(b, "\t\t\t")
	}
	fmt.Fprintf(b, "\t\ttarget_luminances (cd/m2): min %s max %d\n",
		fixedPoint(int32(d.targetMinLum), 10_000), d.targetMaxLum)
	if d.hasMaxCLL {
		fmt.Fprintf(b, "\t\tmax_cll (cd/m2): %d\n", d.maxCLL)
	}
	if d.hasMaxFALL {
		fmt.Fprintf(b, "\t\tmax_fall (cd/m2): %d\n", d.maxFALL)
	}
}

// colorPrimaries holds CIE 1931 xy chromaticity coordinates scaled by 1e6.
type colorPrimaries struct {
	rx, ry, gx, gy, bx, by, wx, wy int32
}

func (p colorPrimaries) print(b *strings.Builder, indent string) {
	fmt.Fprintf(b, "%sred: %s %s green: %s %s\n", indent,
		fixedPoint(p.rx, 1_000_000), fixedPoint(p.ry, 1_000_000),
		fixedPoint(p.gx, 1_000_000), fixedPoint(p.gy, 1_000_000))
	fmt.Fprintf(b, "%sblue: %s %s white: %s %s\n", indent,
		fixedPoint(p.bx, 1_000_000), fixedPoint(p.by, 1_000_000),
		fixedPoint(p.wx, 1_000_000), fixedPoint(p.wy, 1_000_000))
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

func featureName(v colormanagement.ColorManagerV1Feature) string {
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
	case colormanagement.ColorManagerV1TransferFunctionSrgb: //nolint:staticcheck
		return "srgb"
	case colormanagement.ColorManagerV1TransferFunctionExtSrgb: //nolint:staticcheck
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

// gatherColorRepresentation reports the supported alpha modes and
// coefficients/range combinations.
func gatherColorRepresentation(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	cr, err := colorrepresentation.BindColorRepresentationManagerV1(s.reg, g.Name, min(g.Version, colorrepresentation.VersionColorRepresentationManagerV1))
	if err != nil {
		return err
	}
	var (
		alphaModes []colorrepresentation.ColorRepresentationSurfaceV1AlphaMode
		pairs      []colorRepresentationPair
	)
	cr.OnSupportedAlphaMode(func(ev colorrepresentation.ColorRepresentationManagerV1SupportedAlphaModeEvent) {
		alphaModes = append(alphaModes, ev.AlphaMode)
	})
	cr.OnSupportedCoefficientsAndRanges(func(ev colorrepresentation.ColorRepresentationManagerV1SupportedCoefficientsAndRangesEvent) {
		pairs = append(pairs, colorRepresentationPair{coefficients: ev.Coefficients, colorRange: ev.Range})
	})
	if err := s.drain(); err != nil {
		return err
	}
	fmt.Fprintln(b, "\tsupported alpha modes:")
	for _, n := range named(alphaModes, alphaModeName) {
		fmt.Fprintf(b, "\t\t%s\n", n)
	}
	fmt.Fprintln(b, "\tsupported matrix coefficients and ranges:")
	for _, p := range pairs {
		fmt.Fprintf(b, "\t\t%s, %s range\n", nameOf(p.coefficients, coefficientsName), nameOf(p.colorRange, rangeName))
	}
	return nil
}

// colorRepresentationPair pairs supported coefficients with their range.
type colorRepresentationPair struct {
	coefficients colorrepresentation.ColorRepresentationSurfaceV1Coefficients
	colorRange   colorrepresentation.ColorRepresentationSurfaceV1Range
}

func alphaModeName(v colorrepresentation.ColorRepresentationSurfaceV1AlphaMode) string {
	switch v {
	case colorrepresentation.ColorRepresentationSurfaceV1AlphaModePremultipliedElectrical:
		return "premultiplied-electrical"
	case colorrepresentation.ColorRepresentationSurfaceV1AlphaModePremultipliedOptical:
		return "premultiplied-optical"
	case colorrepresentation.ColorRepresentationSurfaceV1AlphaModeStraight:
		return "straight"
	default:
		return ""
	}
}

func coefficientsName(v colorrepresentation.ColorRepresentationSurfaceV1Coefficients) string {
	switch v {
	case colorrepresentation.ColorRepresentationSurfaceV1CoefficientsIdentity:
		return "identity"
	case colorrepresentation.ColorRepresentationSurfaceV1CoefficientsBt709:
		return "bt709"
	case colorrepresentation.ColorRepresentationSurfaceV1CoefficientsFcc:
		return "fcc"
	case colorrepresentation.ColorRepresentationSurfaceV1CoefficientsBt601:
		return "bt601"
	case colorrepresentation.ColorRepresentationSurfaceV1CoefficientsSmpte240:
		return "smpte240"
	case colorrepresentation.ColorRepresentationSurfaceV1CoefficientsBt2020:
		return "bt2020"
	case colorrepresentation.ColorRepresentationSurfaceV1CoefficientsBt2020Cl:
		return "bt2020_cl"
	case colorrepresentation.ColorRepresentationSurfaceV1CoefficientsIctcp:
		return "ictcp"
	default:
		return ""
	}
}

func rangeName(v colorrepresentation.ColorRepresentationSurfaceV1Range) string {
	switch v {
	case colorrepresentation.ColorRepresentationSurfaceV1RangeFull:
		return "full"
	case colorrepresentation.ColorRepresentationSurfaceV1RangeLimited:
		return "limited"
	default:
		return ""
	}
}

// fixedPoint renders a fixed-point value at the given scale as
// "whole.fraction", e.g. fixedPoint(640000, 1000000) -> "0.640000".
func fixedPoint(v int32, scale int32) string {
	whole := v / scale
	frac := v % scale
	if frac < 0 {
		frac = -frac
	}
	digits := 0
	for p := scale; p > 1; p /= 10 {
		digits++
	}
	return fmt.Sprintf("%d.%0*d", whole, digits, frac)
}

// nameOf names an enum value, falling back to its raw hex value.
func nameOf[T ~uint32](v T, name func(T) string) string {
	if n := name(v); n != "" {
		return n
	}
	return fmt.Sprintf("unknown (0x%x)", uint32(v))
}

// named converts enum values to their names, falling back to raw hex.
func named[T ~uint32](values []T, name func(T) string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, nameOf(v, name))
	}
	return out
}
