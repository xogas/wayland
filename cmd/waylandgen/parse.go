package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// xmlProtocol is the encoding/xml view of one protocol file.
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
	Root bool
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
		return nil, inputError("", "root", fmt.Errorf("stat %s: %w", root, err))
	}
	if !info.IsDir() {
		return nil, inputError("", "root", fmt.Errorf("%s is not a directory", root))
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
			Root: filepath.ToSlash(rel) == "wayland.xml",
		})
		return nil
	})
	if err != nil {
		return nil, inputError("", "walk", fmt.Errorf("walk %s: %w", root, err))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	if len(files) == 0 {
		return nil, inputError(root, "input", fmt.Errorf("no XML files under %s", root))
	}
	return files, nil
}

// checkLayout validates the Wayland protocol directory layout: wayland.xml at
// the root, every other XML under <tier>/<protocol-dir>/.
func checkLayout(files []sourceFile) error {
	haveRoot := false
	for _, f := range files {
		if f.Rel == "wayland.xml" {
			haveRoot = true
			continue
		}
		parts := strings.Split(f.Rel, "/")
		if len(parts) != 3 {
			return inputError(f.Path, "layout", fmt.Errorf("want <tier>/<dir>/<name>.xml below %s, got %s", f.Rel, f.Rel))
		}
		for _, p := range parts {
			if p == "" || p == "." || p == ".." {
				return inputError(f.Path, "layout", fmt.Errorf("invalid path component in %s", f.Rel))
			}
		}
	}
	if !haveRoot {
		return inputError("", "layout", fmt.Errorf("missing wayland.xml at the generator root"))
	}
	return nil
}

// validateDTD runs xmllint --noout --dtdvalid for every input file. xmllint is
// a development-time dependency; a missing tool is a clear generator error.
func validateDTD(root string, files []sourceFile) error {
	dtd := filepath.Join(root, "wayland.dtd")
	if _, err := os.Stat(dtd); err != nil {
		return inputError(dtd, "wayland.dtd", fmt.Errorf("stat DTD: %w", err))
	}
	if _, err := exec.LookPath("xmllint"); err != nil {
		return inputError("", "xmllint", fmt.Errorf("xmllint not found (install libxml2-utils): %w", err))
	}
	for _, f := range files {
		cmd := exec.Command("xmllint", "--noout", "--dtdvalid", dtd, f.Path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return inputError(f.Path, "DTD", fmt.Errorf("%s", msg))
		}
	}
	return nil
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
		return nil, inputError(path, "open", err)
	}
	defer func() { _ = f.Close() }()
	var p xmlProtocol
	dec := xml.NewDecoder(f)
	if err := dec.Decode(&p); err != nil {
		return nil, inputError(path, "xml", err)
	}
	// Reject trailing elements after the document element.
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, inputError(path, "xml", err)
		}
		if _, ok := tok.(xml.StartElement); ok {
			return nil, inputError(path, "xml", fmt.Errorf("trailing element after protocol"))
		}
	}
	if p.Name == "" {
		return nil, inputError(path, "protocol.name", fmt.Errorf("missing protocol name"))
	}
	if len(p.Interfaces) == 0 {
		return nil, inputError(path, "interfaces", fmt.Errorf("protocol %s has no interfaces", p.Name))
	}
	return &p, nil
}
