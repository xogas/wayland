package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var tiers = []string{"stable", "staging", "unstable", "experimental"}

func main() {
	outBase := flag.String("o", ".", "output directory")
	flag.Parse()

	rootDir := "wayland-protocols"

	// Generate wayland core.
	coreXML := filepath.Join(rootDir, "wayland.xml")
	if _, err := os.Stat(coreXML); err == nil {
		fmt.Println("=== wayland core ===")
		proto, err := Parse(coreXML)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wayland.xml: %v\n", err)
			os.Exit(1)
		}
		if err := Generate(proto, *outBase, "wayland", "wl_"); err != nil {
			fmt.Fprintf(os.Stderr, "wayland.xml: %v\n", err)
			os.Exit(1)
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "stat %s: %v\n", coreXML, err)
		os.Exit(1)
	}

	// Generate extension protocols by tier.
	usedPkgs := map[string]bool{}
	for _, tier := range tiers {
		fmt.Printf("=== %s ===\n", tier)
		tierDir := filepath.Join(rootDir, tier)
		ents, err := os.ReadDir(tierDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read dir %s: %v\n", tierDir, err)
			os.Exit(1)
		}

		for _, ent := range ents {
			if !ent.IsDir() {
				continue
			}
			xmlFiles, err := filepath.Glob(filepath.Join(tierDir, ent.Name(), "*.xml"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "glob %s: %v\n", ent.Name(), err)
				os.Exit(1)
			}
			if len(xmlFiles) == 0 {
				continue
			}

			// Resolve package name collisions by re-appending version suffix.
			resolved := map[string]string{}
			for _, xf := range xmlFiles {
				pkg, verSuffix := pkgNameFromFile(xf)
				if other, ok := resolved[pkg]; (ok && other != xf) || usedPkgs[pkg] {
					pkg += verSuffix
				}
				resolved[pkg] = xf
			}

			pkgs := make([]string, 0, len(resolved))
			for pkg := range resolved {
				pkgs = append(pkgs, pkg)
			}
			sort.Strings(pkgs)

			for _, pkg := range pkgs {
				xmlPath := resolved[pkg]
				if usedPkgs[pkg] {
					fmt.Fprintf(os.Stderr, "package name collision: %q used by multiple protocols in %s\n", pkg, tier)
					os.Exit(1)
				}
				usedPkgs[pkg] = true

				outDir := filepath.Join(*outBase, "protocol", tier, pkg)
				fmt.Printf("  %s -> protocol/%s/%s/\n", filepath.Base(xmlPath), tier, pkg)
				if err := os.MkdirAll(outDir, 0755); err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					os.Exit(1)
				}
				proto, err := Parse(xmlPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(xmlPath), err)
					os.Exit(1)
				}
				if err := Generate(proto, outDir, pkg, autoPrefix(proto.Interfaces)); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(xmlPath), err)
					os.Exit(1)
				}
			}
		}
	}
}

var reVerSuffix = regexp.MustCompile(`-v\d+$`)

// Generate writes one _gen.go file per interface in the protocol.
func Generate(proto *Protocol, outDir, pkg, prefix string) error {
	knownIface := make(map[string]bool, len(proto.Interfaces))
	for i := range proto.Interfaces {
		knownIface[proto.Interfaces[i].Name] = true
	}
	em := buildEnumMap(proto.Interfaces, prefix)

	for i := range proto.Interfaces {
		iface := &proto.Interfaces[i]
		g := convertInterface(iface, pkg, prefix, knownIface, em)

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
			_ = os.WriteFile(debugPath, buf.Bytes(), 0644)
			return fmt.Errorf("format %s (raw output at %s): %w", path, debugPath, err)
		}
		if err := os.WriteFile(path, formatted, 0644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

func pkgNameFromFile(xmlPath string) (pkg string, verSuffix string) {
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
