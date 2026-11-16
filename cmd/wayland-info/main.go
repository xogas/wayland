// Command wayland-info prints the Wayland globals advertised by the compositor
// and the details of the interfaces a client uses to pick outputs, seats and
// buffer formats.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xogas/wayland"
	xdgoutput "github.com/xogas/wayland/protocols/unstable/xdg-output"
)

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: wayland-info")
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "wayland-info:", err)
		os.Exit(1)
	}
}

func run() error {
	d, err := wayland.Connect()
	if err != nil {
		return fmt.Errorf("connect: set WAYLAND_DISPLAY (with XDG_RUNTIME_DIR) or WAYLAND_SOCKET: %w", err)
	}
	defer func() {
		if cerr := d.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "wayland-info: close:", cerr)
		}
	}()

	registry := &registryHandler{}
	reg, err := d.GetRegistry(registry)
	if err != nil {
		return fmt.Errorf("get_registry: %w", err)
	}
	if err := roundtrip(d); err != nil {
		return fmt.Errorf("initial roundtrip: %w", err)
	}

	infos, xdgManager, err := collect(reg, registry.globals)
	if err != nil {
		return err
	}
	defer destroy(infos, xdgManager)

	if err := roundtrip(d); err != nil {
		return fmt.Errorf("detail roundtrip: %w", err)
	}
	printInfos(infos)
	return nil
}

func roundtrip(d *wayland.Display) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.Roundtrip(ctx)
}

// registryHandler collects globals in advertisement order.
type registryHandler struct {
	globals []wayland.Global
}

func (h *registryHandler) Global(g wayland.Global) {
	h.globals = append(h.globals, g)
}

func (h *registryHandler) GlobalRemove(name uint32) {
	for i := range h.globals {
		if h.globals[i].Name == name {
			h.globals = append(h.globals[:i], h.globals[i+1:]...)
			return
		}
	}
}

// info holds the collected detail for one advertised global.
type info struct {
	global wayland.Global
	shm    *shmInfo
	seat   *seatInfo
	output *outputInfo
}

type shmInfo struct {
	obj     *wayland.Shm
	formats []uint32
}

type seatInfo struct {
	obj          *wayland.Seat
	name         string
	hasName      bool
	capabilities uint32
}

type outputInfo struct {
	obj         *wayland.Output
	geometry    outputGeometry
	hasGeometry bool
	modes       []outputMode
	scale       int32
	name        string
	hasName     bool
	description string
	xdg         *xdgOutputInfo
}

type outputGeometry struct {
	x, y                          int32
	physicalWidth, physicalHeight int32
	subpixel, transform           int32
	make, model                   string
}

type outputMode struct {
	flags   uint32
	width   int32
	height  int32
	refresh int32
}

type xdgOutputInfo struct {
	obj           *xdgoutput.V1
	x, y          int32
	width, height int32
	hasPosition   bool
	hasSize       bool
	name          string
	description   string
}

// collect binds the globals whose events this tool prints; the other globals
// are only listed. The xdg-output manager is bound first so its logical
// geometry can be attached to each wl_output.
func collect(reg *wayland.Registry, globals []wayland.Global) ([]*info, *xdgoutput.ManagerV1, error) {
	var xdgManager *xdgoutput.ManagerV1
	for _, g := range globals {
		if g.Interface != "zxdg_output_manager_v1" {
			continue
		}
		// Bind version 2: it still carries the name and description events,
		// while version 3 deprecates its own done event.
		version := min(g.Version, 2)
		p, err := reg.Bind(g, version)
		if err != nil {
			return nil, nil, fmt.Errorf("bind %s v%d: %w", g.Interface, version, err)
		}
		xdgManager = xdgoutput.NewManagerV1(p)
	}

	infos := make([]*info, 0, len(globals))
	for _, g := range globals {
		in := &info{global: g}
		var bindErr error
		switch g.Interface {
		case "wl_shm":
			in.shm = &shmInfo{}
			in.shm.obj, bindErr = wayland.BindShm(reg, g, &shmHandler{info: in.shm})
		case "wl_seat":
			in.seat = &seatInfo{}
			in.seat.obj, bindErr = wayland.BindSeat(reg, g, &seatHandler{info: in.seat})
		case "wl_output":
			in.output = &outputInfo{scale: 1}
			in.output.obj, bindErr = wayland.BindOutput(reg, g, &outputHandler{info: in.output})
			if bindErr == nil && xdgManager != nil {
				xo := &xdgOutputInfo{}
				xo.obj, bindErr = xdgManager.GetXdgOutput(in.output.obj, &xdgOutputHandler{info: xo})
				in.output.xdg = xo
			}
		}
		if bindErr != nil {
			return nil, nil, fmt.Errorf("bind %s: %w", g.Interface, bindErr)
		}
		infos = append(infos, in)
	}
	return infos, xdgManager, nil
}

// destroy releases the bound objects. The version checks avoid asking a
// compositor for a release request that the bound object version lacks.
func destroy(infos []*info, xdgManager *xdgoutput.ManagerV1) {
	for _, in := range infos {
		switch {
		case in.output != nil:
			if in.output.xdg != nil && in.output.xdg.obj != nil {
				_ = in.output.xdg.obj.Destroy()
			}
			if in.output.obj.Proxy().Version() >= 3 {
				_ = in.output.obj.Release()
			}
		case in.seat != nil:
			if in.seat.obj.Proxy().Version() >= 5 {
				_ = in.seat.obj.Release()
			}
		case in.shm != nil:
			if in.shm.obj.Proxy().Version() >= 2 {
				_ = in.shm.obj.Release()
			}
		}
	}
	if xdgManager != nil {
		_ = xdgManager.Destroy()
	}
}

func printInfos(infos []*info) {
	formats := formatNames()
	for _, in := range infos {
		g := in.global
		fmt.Printf("interface: '%s', version: %d, name: %d\n", g.Interface, g.Version, g.Name)
		switch {
		case in.shm != nil:
			printShm(in.shm, formats)
		case in.seat != nil:
			printSeat(in.seat)
		case in.output != nil:
			printOutput(in.output)
		}
	}
}

func printShm(s *shmInfo, names map[uint32]string) {
	parts := make([]string, 0, len(s.formats))
	for _, format := range s.formats {
		name, ok := names[format]
		if !ok {
			name = fmt.Sprintf("0x%08x", format)
		}
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		parts = append(parts, "none")
	}
	fmt.Printf("\tformats: %s\n", strings.Join(parts, " "))
}

func printSeat(s *seatInfo) {
	if s.hasName {
		fmt.Printf("\tname: %q\n", s.name)
	}
	fmt.Printf("\tcapabilities: %s\n", flagNames("wl_seat", "capability", s.capabilities))
}

func printOutput(o *outputInfo) {
	if o.hasName {
		fmt.Printf("\tname: %q\n", o.name)
	}
	if o.description != "" {
		fmt.Printf("\tdescription: %q\n", o.description)
	}
	if o.hasGeometry {
		fmt.Printf("\tx: %d, y: %d\n", o.geometry.x, o.geometry.y)
		fmt.Printf("\tphysical size: %dx%d mm\n", o.geometry.physicalWidth, o.geometry.physicalHeight)
		fmt.Printf("\tmake: %q, model: %q\n", o.geometry.make, o.geometry.model)
		fmt.Printf("\tsubpixel: %s, transform: %s\n",
			enumName("wl_output", "subpixel", int64(o.geometry.subpixel)),
			enumName("wl_output", "transform", int64(o.geometry.transform)))
	}
	fmt.Printf("\tscale: %d\n", o.scale)
	for _, mode := range o.modes {
		fmt.Printf("\tmode: %dx%d px, refresh: %.3f Hz, flags: %s\n",
			mode.width, mode.height, float64(mode.refresh)/1000, flagNames("wl_output", "mode", mode.flags))
	}
	if o.xdg != nil {
		printXdgOutput(o.xdg, o.name, o.description)
	}
}

func printXdgOutput(x *xdgOutputInfo, outputName, outputDescription string) {
	var parts []string
	if x.hasPosition {
		parts = append(parts, fmt.Sprintf("position %d,%d", x.x, x.y))
	}
	if x.hasSize {
		parts = append(parts, fmt.Sprintf("size %dx%d", x.width, x.height))
	}
	if x.name != "" && x.name != outputName {
		parts = append(parts, fmt.Sprintf("name %q", x.name))
	}
	if x.description != "" && x.description != outputDescription {
		parts = append(parts, fmt.Sprintf("description %q", x.description))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Printf("\txdg_output: %s\n", strings.Join(parts, ", "))
}

// formatNames maps wl_shm format values to the names of their protocol enums.
func formatNames() map[uint32]string {
	names := make(map[uint32]string)
	iface, ok := wayland.InterfaceByName("wl_shm")
	if !ok {
		return names
	}
	for _, e := range iface.Enums {
		if e.Name != "format" {
			continue
		}
		for _, entry := range e.Entries {
			names[uint32(entry.Value)] = strings.ToUpper(entry.Name)
		}
	}
	return names
}

// enumName resolves one value of a generated enum to its protocol name.
func enumName(interfaceName, name string, value int64) string {
	iface, ok := wayland.InterfaceByName(interfaceName)
	if !ok {
		return fmt.Sprintf("%d", value)
	}
	for _, e := range iface.Enums {
		if e.Name != name {
			continue
		}
		for _, entry := range e.Entries {
			if entry.Value == value {
				return entry.Name
			}
		}
	}
	return fmt.Sprintf("%d", value)
}

// flagNames resolves the bits set in a bitfield enum to their protocol names.
func flagNames(interfaceName, enumName string, value uint32) string {
	iface, ok := wayland.InterfaceByName(interfaceName)
	if !ok {
		return fmt.Sprintf("0x%x", value)
	}
	var names []string
	for _, e := range iface.Enums {
		if e.Name != enumName {
			continue
		}
		for _, entry := range e.Entries {
			if entry.Value != 0 && value&uint32(entry.Value) == uint32(entry.Value) {
				names = append(names, entry.Name)
			}
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, " ")
}

type shmHandler struct {
	info *shmInfo
}

func (h *shmHandler) Format(format uint32) {
	h.info.formats = append(h.info.formats, format)
}

type seatHandler struct {
	info *seatInfo
}

func (h *seatHandler) Capabilities(capabilities uint32) {
	h.info.capabilities = capabilities
}

func (h *seatHandler) Name(name string) {
	h.info.name = name
	h.info.hasName = true
}

type outputHandler struct {
	info *outputInfo
}

func (h *outputHandler) Geometry(x int32, y int32, physicalWidth int32, physicalHeight int32, subpixel int32, makeName string, modelName string, transform int32) {
	h.info.geometry = outputGeometry{
		x:              x,
		y:              y,
		physicalWidth:  physicalWidth,
		physicalHeight: physicalHeight,
		subpixel:       subpixel,
		transform:      transform,
		make:           makeName,
		model:          modelName,
	}
	h.info.hasGeometry = true
}

func (h *outputHandler) Mode(flags uint32, width int32, height int32, refresh int32) {
	h.info.modes = append(h.info.modes, outputMode{flags: flags, width: width, height: height, refresh: refresh})
}

func (h *outputHandler) Done() {}

func (h *outputHandler) Scale(factor int32) {
	h.info.scale = factor
}

func (h *outputHandler) Name(name string) {
	h.info.name = name
	h.info.hasName = true
}

func (h *outputHandler) Description(description string) {
	h.info.description = description
}

type xdgOutputHandler struct {
	info *xdgOutputInfo
}

func (h *xdgOutputHandler) LogicalPosition(x int32, y int32) {
	h.info.x = x
	h.info.y = y
	h.info.hasPosition = true
}

func (h *xdgOutputHandler) LogicalSize(width int32, height int32) {
	h.info.width = width
	h.info.height = height
	h.info.hasSize = true
}

func (h *xdgOutputHandler) Done() {}

func (h *xdgOutputHandler) Name(name string) {
	h.info.name = name
}

func (h *xdgOutputHandler) Description(description string) {
	h.info.description = description
}
