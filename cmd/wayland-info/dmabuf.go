package main

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/stable/linuxdmabuf"
)

// gatherDmabuf reports the linux-dmabuf main device and format/modifier
// pairs, from feedback (v4+) or the legacy format/modifier events (v1-v3).
func gatherDmabuf(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	bv := min(g.Version, linuxdmabuf.VersionLinuxDmabufV1)
	dmabuf, err := linuxdmabuf.BindLinuxDmabufV1(s.reg, g.Name, bv)
	if err != nil {
		return err
	}

	var (
		mainDevice uint64
		tranches   []dmabufTranche
		formats    []formatEntry // legacy interfaces
		table      []formatEntry // parsed format table
		pending    dmabufTranche // tranche being assembled
	)

	if bv >= 4 {
		fb, err := dmabuf.GetDefaultFeedback()
		if err != nil {
			return err
		}
		fb.OnFormatTable(func(ev linuxdmabuf.LinuxDmabufFeedbackV1FormatTableEvent) {
			if ev.Size > 0 && ev.Fd >= 0 {
				table = parseFormatTable(readFd(ev.Fd, int(ev.Size)))
			}
			_ = syscall.Close(ev.Fd)
		})
		fb.OnMainDevice(func(ev linuxdmabuf.LinuxDmabufFeedbackV1MainDeviceEvent) { //nolint:staticcheck
			mainDevice = littleEndianUint64(ev.Device)
		})
		fb.OnTrancheTargetDevice(func(ev linuxdmabuf.LinuxDmabufFeedbackV1TrancheTargetDeviceEvent) {
			pending.targetDevice = littleEndianUint64(ev.Device)
		})
		fb.OnTrancheFlags(func(ev linuxdmabuf.LinuxDmabufFeedbackV1TrancheFlagsEvent) {
			pending.flags = ev.Flags
		})
		fb.OnTrancheFormats(func(ev linuxdmabuf.LinuxDmabufFeedbackV1TrancheFormatsEvent) {
			pending.formats = trancheFormats(ev.Indices, table)
			tranches = append(tranches, pending)
			pending = dmabufTranche{}
		})
	} else {
		// v1-v2 report formats only; v3 adds one modifier event per
		// format, immediately after it.
		last := -1
		dmabuf.OnFormat(func(ev linuxdmabuf.LinuxDmabufV1FormatEvent) { //nolint:staticcheck
			formats = append(formats, formatEntry{format: ev.Format})
			last = len(formats) - 1
		})
		dmabuf.OnModifier(func(ev linuxdmabuf.LinuxDmabufV1ModifierEvent) { //nolint:staticcheck
			if last >= 0 {
				formats[last].modifier = uint64(ev.ModifierHi)<<32 | uint64(ev.ModifierLo)
			}
		})
	}
	if err := s.drain(); err != nil {
		return err
	}

	if mainDevice != 0 {
		fmt.Fprintf(b, "\tmain device: %s\n", deviceString(mainDevice))
	}
	for _, t := range tranches {
		fmt.Fprintln(b, "\ttranche")
		fmt.Fprintf(b, "\t\ttarget device: %s\n", deviceString(t.targetDevice))
		fmt.Fprintf(b, "\t\tflags: %s\n", trancheFlagsString(t.flags))
		fmt.Fprintln(b, "\t\tformats (fourcc) and modifiers:")
		for _, f := range t.formats {
			fmt.Fprintf(b, "\t\t%s\n", formatString(f, true))
		}
	}
	if bv < 4 && len(formats) > 0 {
		title, showMod := "formats (fourcc):", false
		if bv == 3 {
			title, showMod = "formats (fourcc) and modifiers:", true
		}
		fmt.Fprintf(b, "\t%s\n", title)
		for _, f := range formats {
			fmt.Fprintf(b, "\t%s\n", formatString(f, showMod))
		}
	}
	return nil
}

// dmabufTranche is one feedback tranche: target device, flags, and formats.
type dmabufTranche struct {
	targetDevice uint64
	flags        linuxdmabuf.LinuxDmabufFeedbackV1TrancheFlags
	formats      []formatEntry
}

// parseFormatTable decodes the mapped content of a format_table fd.
func parseFormatTable(table []byte) []formatEntry {
	var entries []formatEntry
	for off := 0; off+16 <= len(table); off += 16 {
		entries = append(entries, formatEntry{
			format:   binary.LittleEndian.Uint32(table[off : off+4]),
			modifier: binary.LittleEndian.Uint64(table[off+8 : off+16]),
		})
	}
	return entries
}

// littleEndianUint64 decodes the 8-byte device id in main_device and
// tranche_target_device events.
func littleEndianUint64(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(b[:8])
}

// trancheFormats resolves a tranche's little-endian uint16 format indices
// against the format table.
func trancheFormats(indices []byte, entries []formatEntry) []formatEntry {
	var formats []formatEntry
	for i := 0; i+1 < len(indices); i += 2 {
		idx := binary.LittleEndian.Uint16(indices[i : i+2])
		if int(idx) < len(entries) {
			formats = append(formats, entries[idx])
		}
	}
	return formats
}

// trancheFlagsString renders the tranche flags as words.
func trancheFlagsString(flags linuxdmabuf.LinuxDmabufFeedbackV1TrancheFlags) string {
	var parts []string
	if flags&linuxdmabuf.LinuxDmabufFeedbackV1TrancheFlagsScanout != 0 {
		parts = append(parts, "scanout")
	}

	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// readFd maps an fd read-only, copies size bytes, and unmaps it. The
// caller still owns the fd.
func readFd(fd, size int) []byte {
	data, err := syscall.Mmap(fd, 0, size, syscall.PROT_READ, syscall.MAP_PRIVATE)
	if err != nil {
		return nil
	}
	defer syscall.Munmap(data) //nolint: errcheck
	b := make([]byte, size)
	copy(b, data)
	return b
}

// formatEntry is one 16-byte linux-dmabuf format table entry (format,
// padding, modifier).
type formatEntry struct {
	format   uint32
	modifier uint64
}

// formatString renders one dmabuf format/modifier pair, showing the
// modifier only when the interface reports it.
func formatString(f formatEntry, showModifier bool) string {
	if showModifier {
		return fmt.Sprintf("0x%08x = '%s'; mod=0x%016x", f.format, fourccStr(f.format), f.modifier)
	}
	return fmt.Sprintf("0x%08x = '%s'", f.format, fourccStr(f.format))
}
