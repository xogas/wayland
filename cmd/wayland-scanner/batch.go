package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// tiers are the wayland-protocols maturity levels, mirrored under protocol/.
var tiers = []string{"stable", "staging", "unstable", "experimental"}

// runBatch generates the core protocol into outBase and every extension
// protocol into outBase/protocol/<tier>/<pkg>/.
func runBatch(rootDir, outBase string) error {
	if err := genCore(rootDir, outBase); err != nil {
		return err
	}
	for _, tier := range tiers {
		if err := genTier(rootDir, outBase, tier); err != nil {
			return err
		}
	}
	return nil
}

// genCore generates wayland.xml at the batch root into the root package.
func genCore(rootDir, outBase string) error {
	coreXML := filepath.Join(rootDir, "wayland.xml")
	if _, err := os.Stat(coreXML); err != nil {
		return nil
	}
	fmt.Println("=== wayland core ===")
	proto, err := Parse(coreXML)
	if err != nil {
		return fmt.Errorf("wayland.xml: %w", err)
	}
	cfg := GenerateConfig{
		OutDir:     outBase,
		Package:    "wayland",
		TrimPrefix: "wl_",
	}
	if err := Generate(proto, cfg); err != nil {
		return fmt.Errorf("wayland.xml: %w", err)
	}
	return nil
}

// genTier generates all protocols of one maturity tier.
func genTier(rootDir, outBase, tier string) error {
	fmt.Printf("=== %s ===\n", tier)
	baseDir := filepath.Join(rootDir, tier)

	ents, err := os.ReadDir(baseDir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", baseDir, err)
	}

	usedPkgs := map[string]bool{}
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		xmlFiles, err := filepath.Glob(filepath.Join(baseDir, ent.Name(), "*.xml"))
		if err != nil {
			return fmt.Errorf("glob %s: %w", ent.Name(), err)
		}
		if err := genProtocolDir(xmlFiles, outBase, tier, usedPkgs); err != nil {
			return err
		}
	}
	return nil
}

// genProtocolDir generates all XML files of one protocol directory, resolving
// package name collisions within the tier by re-appending the version suffix.
func genProtocolDir(xmlFiles []string, outBase, tier string, usedPkgs map[string]bool) error {
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
			return fmt.Errorf("package name collision: %q used by multiple protocols in %s (use -pkg to disambiguate)", pkg, tier)
		}
		usedPkgs[pkg] = true

		outDir := filepath.Join(outBase, "protocol", tier, pkg)
		fmt.Printf("  %s -> protocol/%s/%s/\n", filepath.Base(xmlPath), tier, pkg)
		if err := genOne(xmlPath, outDir, pkg); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(xmlPath), err)
		}
	}
	return nil
}

func genOne(xmlPath, outDir, pkg string) error {
	proto, err := Parse(xmlPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	return Generate(proto, GenerateConfig{
		OutDir:     outDir,
		Package:    pkg,
		AutoPrefix: true,
	})
}

var reVerSuffix = regexp.MustCompile(`-v\d+$`)

// pkgNameFromFile derives the Go package name from an XML file name:
// "text-input-unstable-v3.xml" -> ("textinputunstable", "v3").
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
