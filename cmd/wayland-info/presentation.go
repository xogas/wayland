package main

import (
	"fmt"
	"strings"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/presentationtime"
)

// gatherPresentation reports the presentation clock id and its name.
func gatherPresentation(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	pres, err := presentationtime.BindPresentation(s.reg, g.Name, min(g.Version, presentationtime.VersionPresentation))
	if err != nil {
		return err
	}
	var clock uint32
	pres.OnClockID(func(ev presentationtime.PresentationClockIDEvent) {
		clock = ev.ClkID
	})
	if err := s.drain(); err != nil {
		return err
	}
	fmt.Fprintf(b, "\tpresentation clock id: %d (%s)\n", clock, clockName(clock))
	return nil
}

// clockName names a presentation clock id, falling back to "unknown (id)".
func clockName(id uint32) string {
	if n, ok := clockNames[id]; ok {
		return n
	}
	return fmt.Sprintf("unknown (%d)", id)
}

// clockNames maps the Linux clockid values reported by wp_presentation.
var clockNames = map[uint32]string{
	0: "CLOCK_REALTIME",
	1: "CLOCK_MONOTONIC",
	4: "CLOCK_MONOTONIC_RAW",
	5: "CLOCK_REALTIME_COARSE",
	6: "CLOCK_MONOTONIC_COARSE",
	7: "CLOCK_BOOTTIME",
}
