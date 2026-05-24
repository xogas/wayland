package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
)

// Parse reads and validates a Wayland protocol XML file.
func Parse(xmlPath string) (*Protocol, error) {
	f, err := os.Open(xmlPath)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint: errcheck
	return ParseReader(f)
}

// ParseReader decodes and validates a Wayland protocol XML from a reader.
func ParseReader(r io.Reader) (*Protocol, error) {
	var proto Protocol
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&proto); err != nil {
		return nil, fmt.Errorf("decode xml: %w", err)
	}
	if err := validate(&proto); err != nil {
		return nil, err
	}
	return &proto, nil
}

func validate(proto *Protocol) error {
	if proto.Name == "" {
		return fmt.Errorf("protocol name is empty")
	}
	for i := range proto.Interfaces {
		iface := &proto.Interfaces[i]
		if iface.Name == "" {
			return fmt.Errorf("interface name is empty")
		}
		if iface.Version < 1 {
			return fmt.Errorf("interface %q version is %d, must be >= 1", iface.Name, iface.Version)
		}
		for j := range iface.Requests {
			req := &iface.Requests[j]
			if req.Name == "" {
				return fmt.Errorf("interface %q: request name is empty", iface.Name)
			}
			for k := range req.Args {
				if req.Args[k].Name == "" {
					return fmt.Errorf("interface %q request %q: arg name is empty", iface.Name, req.Name)
				}
			}
		}
		for j := range iface.Events {
			ev := &iface.Events[j]
			if ev.Name == "" {
				return fmt.Errorf("interface %q: event name is empty", iface.Name)
			}
			for k := range ev.Args {
				if ev.Args[k].Name == "" {
					return fmt.Errorf("interface %q event %q: arg name is empty", iface.Name, ev.Name)
				}
			}
		}
	}
	return nil
}
