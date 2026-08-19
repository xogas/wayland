package main

import (
	"fmt"
	"strings"

	"github.com/xogas/wayland"
)

// gatherSeat reports the wl_seat name, capabilities, and keyboard repeat.
func gatherSeat(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	seat, err := wayland.BindSeat(s.reg, g.Name, min(g.Version, wayland.VersionSeat))
	if err != nil {
		return err
	}
	var d seatData
	seat.OnCapabilities(func(ev wayland.SeatCapabilitiesEvent) {
		d.caps = ev.Capabilities
	})
	if g.Version >= 2 {
		seat.OnName(func(ev wayland.SeatNameEvent) {
			d.name = ev.Name
		})
	}
	if err := s.drain(); err != nil {
		return err
	}
	// wl_keyboard.repeat_info exists since wl_seat v4; without it there is
	// nothing more to learn.
	if d.caps&wayland.SeatCapabilityKeyboard != 0 && g.Version >= 4 {
		if kb, err := seat.GetKeyboard(); err == nil {
			kb.OnRepeatInfo(func(ev wayland.KeyboardRepeatInfoEvent) {
				d.repeatRate = ev.Rate
				d.repeatDelay = ev.Delay
			})
			if err := s.drain(); err != nil {
				return err
			}
		}
	}
	if d.name != "" {
		fmt.Fprintf(b, "\tname: %s\n", d.name)
	}
	fmt.Fprintf(b, "\tcapabilities: %s\n", capsString(d.caps))
	if d.repeatRate > 0 {
		fmt.Fprintf(b, "\tkeyboard repeat rate: %d\n", d.repeatRate)
	}
	if d.repeatDelay > 0 {
		fmt.Fprintf(b, "\tkeyboard repeat delay: %d\n", d.repeatDelay)
	}
	return nil
}

// seatData collects the wl_seat details.
type seatData struct {
	name                    string
	caps                    wayland.SeatCapability
	repeatRate, repeatDelay int32
}

// capsString renders the wl_seat capability flags as words.
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
