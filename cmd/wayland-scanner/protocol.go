package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
)

// Protocol represents a parsed Wayland protocol XML file (<protocol>).
type Protocol struct {
	XMLName     xml.Name    `xml:"protocol"`
	Name        string      `xml:"name,attr"`
	Copyright   string      `xml:"copyright"`
	Description Description `xml:"description"`
	Interfaces  []Interface `xml:"interface"`
}

// Description holds the summary and text of a protocol description element.
type Description struct {
	Summary string `xml:"summary,attr"`
	Text    string `xml:",chardata"`
}

// Interface represents a Wayland protocol interface (<interface>).
type Interface struct {
	Name        string      `xml:"name,attr"`
	Version     int         `xml:"version,attr"`
	Frozen      bool        `xml:"frozen,attr"`
	Description Description `xml:"description"`
	Requests    []Request   `xml:"request"`
	Events      []Event     `xml:"event"`
	Enums       []Enum      `xml:"enum"`
}

// Request represents a Wayland protocol request (<request>).
type Request struct {
	Name            string      `xml:"name,attr"`
	Type            string      `xml:"type,attr"`
	Since           int         `xml:"since,attr"`
	DeprecatedSince int         `xml:"deprecated-since,attr"`
	Description     Description `xml:"description"`
	Args            []Arg       `xml:"arg"`
}

// Event represents a Wayland protocol event (<event>).
type Event struct {
	Name            string      `xml:"name,attr"`
	Type            string      `xml:"type,attr"`
	Since           int         `xml:"since,attr"`
	DeprecatedSince int         `xml:"deprecated-since,attr"`
	Description     Description `xml:"description"`
	Args            []Arg       `xml:"arg"`
}

// Arg represents an argument of a request or event (<arg>).
type Arg struct {
	Name        string      `xml:"name,attr"`
	Type        string      `xml:"type,attr"`
	Summary     string      `xml:"summary,attr"`
	Interface   string      `xml:"interface,attr"`
	AllowNull   bool        `xml:"allow-null,attr"`
	Enum        string      `xml:"enum,attr"`
	Description Description `xml:"description"`
}

// Enum represents an enumeration (<enum>).
type Enum struct {
	Name        string      `xml:"name,attr"`
	Since       int         `xml:"since,attr"`
	BitField    bool        `xml:"bitfield,attr"`
	Description Description `xml:"description"`
	Entries     []Entry     `xml:"entry"`
}

// Entry represents an enum entry (<entry>).
type Entry struct {
	Name            string      `xml:"name,attr"`
	Value           IntValue    `xml:"value,attr"`
	Summary         string      `xml:"summary,attr"`
	Since           int         `xml:"since,attr"`
	DeprecatedSince int         `xml:"deprecated-since,attr"`
	Description     Description `xml:"description"`
}

// IntValue is an integer that supports both decimal and 0x-prefixed hexadecimal values.
type IntValue int

// UnmarshalXMLAttr parses decimal or 0x-hex attribute values into an IntValue.
func (v *IntValue) UnmarshalXMLAttr(attr xml.Attr) error {
	parsed, err := strconv.ParseInt(attr.Value, 0, 0)
	if err != nil {
		return err
	}
	*v = IntValue(parsed)
	return nil
}

// validate checks that a parsed Protocol has all required fields populated.
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

// Parse reads and validates a Wayland protocol XML file.
func Parse(xmlPath string) (*Protocol, error) {
	f, err := os.Open(xmlPath)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint: errcheck

	// decodes and validates a Wayland protocol XML from a reader.
	var proto Protocol
	dec := xml.NewDecoder(f)
	if err := dec.Decode(&proto); err != nil {
		return nil, fmt.Errorf("decode xml: %w", err)
	}
	if err := validate(&proto); err != nil {
		return nil, err
	}
	return &proto, nil
}
