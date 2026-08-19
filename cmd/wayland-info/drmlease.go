package main

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/staging/drmlease"
)

// gatherDrmLease reports the DRM device path and its connectors.
func gatherDrmLease(s *session, b *strings.Builder, g wayland.RegistryGlobalEvent) error {
	leaseDev, err := drmlease.BindDrmLeaseDeviceV1(s.reg, g.Name, min(g.Version, drmlease.VersionDrmLeaseDeviceV1))
	if err != nil {
		return err
	}
	var (
		path       string
		connectors []*drmLeaseConnector
	)
	leaseDev.OnDrmFd(func(ev drmlease.DrmLeaseDeviceV1DrmFdEvent) {
		path = drmFdPath(ev.Fd)
		_ = syscall.Close(ev.Fd)
	})
	leaseDev.OnConnector(func(ev drmlease.DrmLeaseDeviceV1ConnectorEvent) {
		conn := &drmLeaseConnector{}
		ev.ID.OnName(func(ev drmlease.DrmLeaseConnectorV1NameEvent) {
			conn.name = ev.Name
		})
		ev.ID.OnDescription(func(ev drmlease.DrmLeaseConnectorV1DescriptionEvent) {
			conn.description = ev.Description
		})
		ev.ID.OnConnectorID(func(ev drmlease.DrmLeaseConnectorV1ConnectorIDEvent) {
			conn.id = ev.ConnectorID
		})
		connectors = append(connectors, conn)
	})
	if err := s.drain(); err != nil {
		return err
	}
	if path != "" {
		fmt.Fprintf(b, "\tpath: %s\n", path)
	}
	for _, conn := range connectors {
		fmt.Fprintln(b, "\tconnector:")
		fmt.Fprintf(b, "\t\tid: %d\n", conn.id)
		fmt.Fprintf(b, "\t\tname: %s\n", conn.name)
		fmt.Fprintf(b, "\t\tdescription: %s\n", conn.description)
	}
	return nil
}

// drmLeaseConnector collects one connector's details.
type drmLeaseConnector struct {
	name, description string
	id                uint32
}

// drmFdPath identifies the DRM device behind an open fd.
func drmFdPath(fd int) string {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return fmt.Sprintf("unknown (fd=%d)", fd)
	}
	return deviceString(stat.Rdev)
}
