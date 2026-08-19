package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// corePkg is the Go package name used for the wayland core protocol.
const corePkg = "wayland"

// tiers lists the wayland-protocols tiers, in generation order.
var tiers = []string{"stable", "staging", "unstable", "experimental"}

func main() {
	outBase := flag.String("o", ".", "output directory")
	flag.Parse()

	if err := run("wayland-protocols", *outBase); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run generates the wayland core protocol and every extension protocol into
// outBase. Protocols are discovered under rootDir, tier by tier; each
// extension protocol lands in protocol/<tier>/<package>/.
func run(rootDir, outBase string) error {
	if err := generateCore(filepath.Join(rootDir, "wayland.xml"), outBase); err != nil {
		return err
	}

	usedPkgs := map[string]bool{}
	for _, tier := range tiers {
		fmt.Printf("=== %s ===\n", tier)
		sources, err := planTier(filepath.Join(rootDir, tier), usedPkgs)
		if err != nil {
			return err
		}
		for _, src := range sources {
			outDir := filepath.Join(outBase, "protocol", tier, src.pkg)
			fmt.Printf("  %s -> protocol/%s/%s/\n", filepath.Base(src.xmlPath), tier, src.pkg)
			if err := generateXML(src.xmlPath, outDir, src.pkg, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// generateCore generates the wayland core protocol, when present.
func generateCore(coreXML, outBase string) error {
	if _, err := os.Stat(coreXML); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", coreXML, err)
	}
	fmt.Println("=== wayland core ===")
	return generateXML(coreXML, outBase, corePkg, "wl_")
}

// protocolSource pairs a protocol XML file with the Go package name its
// generated code uses.
type protocolSource struct {
	xmlPath string
	pkg     string
}

// reVerSuffix matches a trailing version suffix in a protocol file name,
// e.g. the "v1" in xdg-activation-v1.xml.
var reVerSuffix = regexp.MustCompile(`-v\d+$`)

// planTier assigns a Go package name to every protocol XML file under
// tierDir. Names already claimed (by this tier or an earlier one, tracked
// in usedPkgs) get the file's version suffix appended; a collision that
// cannot be resolved is an error.
func planTier(tierDir string, usedPkgs map[string]bool) ([]protocolSource, error) {
	var xmlFiles []string
	ents, err := os.ReadDir(tierDir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", tierDir, err)
	}
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		files, err := filepath.Glob(filepath.Join(tierDir, ent.Name(), "*.xml"))
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", ent.Name(), err)
		}
		xmlFiles = append(xmlFiles, files...)
	}
	if len(xmlFiles) == 0 {
		return nil, nil
	}

	// Claim a package name per file, appending the version suffix on conflict.
	claimed := make(map[string]string, len(xmlFiles))
	for _, xmlPath := range xmlFiles {
		pkg, verSuffix := pkgNameFromFile(xmlPath)
		if _, dup := claimed[pkg]; dup || usedPkgs[pkg] {
			pkg += verSuffix
		}
		claimed[pkg] = xmlPath
	}

	pkgs := make([]string, 0, len(claimed))
	for pkg := range claimed {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	sources := make([]protocolSource, 0, len(pkgs))
	for _, pkg := range pkgs {
		if usedPkgs[pkg] {
			return nil, fmt.Errorf("package name collision: %q used by multiple protocols in %s", pkg, filepath.Base(tierDir))
		}
		usedPkgs[pkg] = true
		sources = append(sources, protocolSource{xmlPath: claimed[pkg], pkg: pkg})
	}
	return sources, nil
}

// pkgNameFromFile derives the Go package name from a protocol file name,
// lower-casing and dropping hyphens, e.g. xdg-activation-v1.xml ->
// ("xdgactivation", "v1"). An empty name falls back to "protocol".
func pkgNameFromFile(xmlPath string) (pkg, verSuffix string) {
	fname := strings.TrimSuffix(filepath.Base(xmlPath), ".xml")
	if m := reVerSuffix.FindString(fname); m != "" {
		verSuffix = m[1:]
		fname = fname[:len(fname)-len(m)]
	}
	pkg = strings.ToLower(strings.ReplaceAll(fname, "-", ""))
	if pkg == "" {
		pkg = "protocol"
	}
	return pkg, verSuffix
}

// generateXML parses xmlPath and generates the protocol into outDir. prefix
// is stripped from interface names to form Go type names, or derived from
// the protocol's interfaces when empty.
func generateXML(xmlPath, outDir, pkg, prefix string) error {
	proto, err := Parse(xmlPath)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(xmlPath), err)
	}
	if prefix == "" {
		prefix = autoPrefix(proto.Interfaces)
	}
	return Generate(proto, outDir, pkg, prefix)
}

// Generate renders one _gen.go file per interface in proto into outDir.
// pkg names the Go package of the generated files; prefix is stripped from
// interface names to form the Go type name.
func Generate(proto *Protocol, outDir, pkg, prefix string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	knownIface := make(map[string]bool, len(proto.Interfaces))
	for i := range proto.Interfaces {
		knownIface[proto.Interfaces[i].Name] = true
	}
	em := buildEnumMap(proto.Interfaces, prefix)

	for i := range proto.Interfaces {
		iface := &proto.Interfaces[i]
		g, err := convertInterface(iface, pkg, prefix, knownIface, em)
		if err != nil {
			return err
		}
		if err := writeInterface(outDir, g); err != nil {
			return err
		}
	}
	return nil
}

// writeInterface renders, gofmt-formats, and writes the _gen.go file of one
// interface. On a format error the raw output is kept next to the target
// path with a .debug suffix to aid diagnosis.
func writeInterface(outDir string, g GoInterface) error {
	var buf bytes.Buffer
	for _, tmpl := range fileTemplates {
		if err := tmpl.Execute(&buf, g); err != nil {
			return fmt.Errorf("%s: render %s: %w", g.TypeName, tmpl.Name(), err)
		}
	}

	path := filepath.Join(outDir, snakeCase(g.TypeName)+"_gen.go")
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		debugPath := path + ".debug"
		_ = os.WriteFile(debugPath, buf.Bytes(), 0o644)
		return fmt.Errorf("format %s (raw output at %s): %w", path, debugPath, err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
