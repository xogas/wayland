package main

import (
	"encoding/xml"
	"strconv"
)

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

type Arg struct {
	Name        string      `xml:"name,attr"`
	Type        string      `xml:"type,attr"`
	Summary     string      `xml:"summary,attr"`
	Interface   string      `xml:"interface,attr"`
	AllowNull   bool        `xml:"allow-null,attr"`
	Enum        string      `xml:"enum,attr"`
	Description Description `xml:"description"`
}

type Enum struct {
	Name        string      `xml:"name,attr"`
	Since       int         `xml:"since,attr"`
	BitField    bool        `xml:"bitfield,attr"`
	Description Description `xml:"description"`
	Entries     []Entry     `xml:"entry"`
}

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
