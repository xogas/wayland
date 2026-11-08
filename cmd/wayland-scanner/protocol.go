package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
)

// Protocol is a <protocol> element. XMLName looks unused but is the only thing
// making encoding/xml reject a file whose root is not <protocol>.
type Protocol struct {
	XMLName     xml.Name    `xml:"protocol"`
	Name        string      `xml:"name,attr"`
	Copyright   string      `xml:"copyright"`
	Description Description `xml:"description"`
	Interfaces  []Interface `xml:"interface"`
}

type Description struct {
	Summary string `xml:"summary,attr"`
	Text    string `xml:",chardata"`
}
type Interface struct {
	Name        string      `xml:"name,attr"`
	Version     int         `xml:"version,attr"`
	Frozen      bool        `xml:"frozen,attr"`
	Description Description `xml:"description"`
	Requests    []Request   `xml:"request"`
	Events      []Event     `xml:"event"`
	Enums       []Enum      `xml:"enum"`
}

type Request struct {
	Name            string      `xml:"name,attr"`
	Type            string      `xml:"type,attr"`
	Since           int         `xml:"since,attr"`
	DeprecatedSince int         `xml:"deprecated-since,attr"`
	Description     Description `xml:"description"`
	Args            []Arg       `xml:"arg"`
}

type Event struct {
	Name            string      `xml:"name,attr"`
	Type            string      `xml:"type,attr"`
	Since           int         `xml:"since,attr"`
	DeprecatedSince int         `xml:"deprecated-since,attr"`
	Description     Description `xml:"description"`
	Args            []Arg       `xml:"arg"`
}

type Enum struct {
	Name        string      `xml:"name,attr"`
	Since       int         `xml:"since,attr"`
	BitField    bool        `xml:"bitfield,attr"`
	Description Description `xml:"description"`
	Entries     []Entry     `xml:"entry"`
}

type Arg struct {
	Name        string      `xml:"name,attr"`
	Type        string      `xml:"type,attr"`
	Summary     string      `xml:"summary,attr"`
	Interface   string      `xml:"interface,attr"`
	AllowNull   bool        `xml:"allow-null,attr"`
	Enum        string      `xml:"enum,attr"`
	Description Description `xml:"description"`
}

type Entry struct {
	Name            string      `xml:"name,attr"`
	Value           IntValue    `xml:"value,attr"`
	Summary         string      `xml:"summary,attr"`
	Since           int         `xml:"since,attr"`
	DeprecatedSince int         `xml:"deprecated-since,attr"`
	Description     Description `xml:"description"`
}

// CDATA in the DTD; holds decimal or 0x-hex.
type IntValue int

func (v *IntValue) UnmarshalXMLAttr(attr xml.Attr) error {
	parsed, err := strconv.ParseInt(attr.Value, 0, 0)
	if err != nil {
		return fmt.Errorf("value %q: %w", attr.Value, err)
	}
	*v = IntValue(parsed)
	return nil
}

// Parse decodes one protocol XML file.
func Parse(path string) (*Protocol, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var p Protocol
	if err := xml.NewDecoder(f).Decode(&p); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &p, nil
}
