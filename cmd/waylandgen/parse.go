package main

import (
	"encoding/xml"
	"fmt"
	"os"
)

// xmlProtocol is the encoding/xml view of one protocol file.
type xmlProtocol struct {
	Name       string         `xml:"name,attr"`
	Interfaces []xmlInterface `xml:"interface"`
}

type xmlInterface struct {
	Name     string       `xml:"name,attr"`
	Version  string       `xml:"version,attr"`
	Requests []xmlMessage `xml:"request"`
	Events   []xmlMessage `xml:"event"`
	Enums    []xmlEnum    `xml:"enum"`
}

type xmlMessage struct {
	Name  string   `xml:"name,attr"`
	Type  string   `xml:"type,attr"`
	Since string   `xml:"since,attr"`
	Args  []xmlArg `xml:"arg"`
}

type xmlArg struct {
	Name      string `xml:"name,attr"`
	Type      string `xml:"type,attr"`
	Interface string `xml:"interface,attr"`
	Enum      string `xml:"enum,attr"`
	AllowNull string `xml:"allow-null,attr"`
}

type xmlEnum struct {
	Name     string     `xml:"name,attr"`
	Since    string     `xml:"since,attr"`
	Bitfield string     `xml:"bitfield,attr"`
	Entries  []xmlEntry `xml:"entry"`
}

type xmlEntry struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
	Since string `xml:"since,attr"`
}

func parseFile(path string) (*xmlProtocol, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var p xmlProtocol
	if err := xml.NewDecoder(f).Decode(&p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("parse %s: missing protocol name", path)
	}
	if len(p.Interfaces) == 0 {
		return nil, fmt.Errorf("parse %s: no interfaces", path)
	}
	return &p, nil
}
