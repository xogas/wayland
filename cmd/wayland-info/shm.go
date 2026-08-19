package main

import (
	"fmt"
	"strings"

	"github.com/xogas/wayland"
)

// gatherShm lists the wl_shm formats.
func gatherShm(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	shm, err := wayland.BindShm(s.reg, g.Name, min(g.Version, wayland.VersionShm))
	if err != nil {
		return err
	}
	var formats []wayland.ShmFormat
	shm.OnFormat(func(ev wayland.ShmFormatEvent) {
		formats = append(formats, ev.Format)
	})
	if err := s.drain(); err != nil {
		return err
	}
	fmt.Fprintln(b, "\tformats (fourcc):")
	for _, f := range formats {
		name := drmFourccNames[uint32(f)]
		if name == "" {
			name = fourccStr(uint32(f))
		}
		fmt.Fprintf(b, "\t0x%08x = '%s'\n", f, name)
	}
	return nil
}

// drmFourccNames maps common DRM fourcc values to their drm_fourcc.h names.
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

// fourccStr renders a fourcc value as four characters, with unprintable
// bytes replaced by '?'.
func fourccStr(v uint32) string {
	var b [4]byte
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	for i := range 4 {
		if b[i] < 32 || b[i] > 126 {
			b[i] = '?'
		}
	}
	return string(b[:])
}

// fourcc packs four ASCII characters into the little-endian uint32 used on
// the wire for DRM/wl_shm formats.
func fourcc(a, b, c, d byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24
}
