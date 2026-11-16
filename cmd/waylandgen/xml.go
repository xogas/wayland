package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type xmlProtocol struct {
	Name       string         `xml:"name,attr"`
	Interfaces []xmlInterface `xml:"interface"`
}

type xmlInterface struct {
	Name     string       `xml:"name,attr"`
	Version  string       `xml:"version,attr"`
	Frozen   string       `xml:"frozen,attr"`
	Requests []xmlMessage `xml:"request"`
	Events   []xmlMessage `xml:"event"`
	Enums    []xmlEnum    `xml:"enum"`
}

type xmlMessage struct {
	Name            string   `xml:"name,attr"`
	Type            string   `xml:"type,attr"`
	Since           string   `xml:"since,attr"`
	DeprecatedSince string   `xml:"deprecated-since,attr"`
	Args            []xmlArg `xml:"arg"`
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
	Name            string `xml:"name,attr"`
	Value           string `xml:"value,attr"`
	Since           string `xml:"since,attr"`
	DeprecatedSince string `xml:"deprecated-since,attr"`
}

// sourceFile is one XML input, identified by a path relative to the generator
// root. Keeping the relative path keeps generated output independent of the
// host checkout location.
type sourceFile struct {
	Path string
	Rel  string
}

// parsedSource pairs an input file with its decoded XML.
type parsedSource struct {
	Source sourceFile
	XML    *xmlProtocol
}

// discoverXML returns every XML file under root, sorted by relative path.
func discoverXML(root string) ([]sourceFile, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	var files []sourceFile
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".xml" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, sourceFile{
			Path: path,
			Rel:  filepath.ToSlash(rel),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	if len(files) == 0 {
		return nil, fmt.Errorf("no XML files under %s", root)
	}
	return files, nil
}

// parseAll decodes every input file.
func parseAll(files []sourceFile) ([]parsedSource, error) {
	out := make([]parsedSource, 0, len(files))
	for _, f := range files {
		p, err := parseFile(f.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, parsedSource{Source: f, XML: p})
	}
	return out, nil
}

func parseFile(path string) (*xmlProtocol, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var p xmlProtocol
	dec := xml.NewDecoder(f)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Reject trailing elements after the document element.
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if _, ok := tok.(xml.StartElement); ok {
			return nil, fmt.Errorf("%s: trailing element after protocol", path)
		}
	}
	if p.Name == "" {
		return nil, fmt.Errorf("%s: missing protocol name", path)
	}
	if len(p.Interfaces) == 0 {
		return nil, fmt.Errorf("protocol %s has no interfaces", p.Name)
	}
	return &p, nil
}
