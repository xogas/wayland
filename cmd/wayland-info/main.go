package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/linuxdmabuf"
	"github.com/xogas/wayland/protocol/stable/presentationtime"
	"github.com/xogas/wayland/protocol/stable/tablet"
	"github.com/xogas/wayland/protocol/staging/colormanagement"
	"github.com/xogas/wayland/protocol/staging/colorrepresentation"
	"github.com/xogas/wayland/protocol/staging/drmlease"
	"github.com/xogas/wayland/protocol/unstable/xdgoutputunstable"
)

// gatherFunc writes the detail lines for one registry global.
type gatherFunc func(*session, *strings.Builder, wayland.RegistryGlobalEvent) error

// gatherers dispatches each known interface to its gatherer;
// unknown interfaces only get the header line.
var gatherers = map[string]gatherFunc{
	wayland.InterfaceShm:                                      gatherShm,
	wayland.InterfaceSeat:                                     gatherSeat,
	wayland.InterfaceOutput:                                   gatherOutput,
	linuxdmabuf.InterfaceLinuxDmabufV1:                        gatherDmabuf,
	presentationtime.InterfacePresentation:                    gatherPresentation,
	drmlease.InterfaceDrmLeaseDeviceV1:                        gatherDrmLease,
	colormanagement.InterfaceColorManagerV1:                   gatherColorManager,
	colorrepresentation.InterfaceColorRepresentationManagerV1: gatherColorRepresentation,
	tablet.InterfaceTabletManagerV2:                           gatherTablet,
	xdgoutputunstable.InterfaceOutputManagerV1:                gatherXdgOutputManager,
}

// session carries the connection state shared by all gatherers.
type session struct {
	ctx     context.Context
	display *wayland.Display
	reg     *wayland.Registry
	globals []wayland.RegistryGlobalEvent

	// pending marks that handlers created objects awaiting another roundtrip.
	pending bool
}

// drain roundtrips until no handler created a new object, so all
// subscribed events have arrived.
func (s *session) drain() error {
	for {
		if err := s.display.Roundtrip(s.ctx); err != nil {
			return err
		}
		if !s.pending {
			return nil
		}
		s.pending = false
	}
}

// report renders each matching global: a header line then the gatherer's
// detail lines. Gather errors go to stderr and do not stop the loop.
func (s *session) report(iface string) string {
	var b strings.Builder
	for _, g := range s.globals {
		if iface != "" && !strings.Contains(g.Interface, iface) {
			continue
		}
		fmt.Fprintf(&b, "interface: '%s', version: %d, name: %d\n", g.Interface, g.Version, g.Name)
		gather, ok := gatherers[g.Interface]
		if !ok {
			continue
		}
		if err := gather(s, &b, g); err != nil {
			fmt.Fprintf(os.Stderr, "  %s v%d: %v\n", g.Interface, g.Version, err)
		}
	}
	return b.String()
}

// connect opens the display and collects the globals.
func connect(ctx context.Context) (*session, error) {
	display, err := wayland.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	reg, err := display.GetRegistry()
	if err != nil {
		_ = display.Close()
		return nil, fmt.Errorf("get_registry: %w", err)
	}
	s := &session{ctx: ctx, display: display, reg: reg}
	reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
		s.globals = append(s.globals, ev)
	})
	if err := s.drain(); err != nil {
		_ = display.Close()
		return nil, fmt.Errorf("roundtrip: %w", err)
	}
	return s, nil
}

func main() {
	iface := flag.String("i", "", "only show info for globals whose interface name contains this substring")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := connect(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wayland-info:", err)
		os.Exit(1)
	}
	defer s.display.Close() //nolint: errcheck

	fmt.Print(s.report(*iface))
}
