// Command wayland-scanner generates Go bindings from Wayland protocol XML.
package main

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func main() {
	outBase := flag.String("o", ".", "output directory")
	rootDir := flag.String("i", "wayland-protocols", "directory holding the protocol XML")
	flag.Parse()

	if err := run(os.Stdout, *rootDir, *outBase); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The core protocol lands in outBase, each extension in protocol/<tier>/<pkg>/.
func run(w io.Writer, rootDir, outBase string) error {
	coreXML := filepath.Join(rootDir, "wayland.xml")
	if _, err := os.Stat(coreXML); err == nil {
		fmt.Fprintln(w, "=== wayland core ===")
		if err := generateProtocol(coreXML, outBase, corePkg, "wl_"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", coreXML, err)
	}

	used := make(map[string]string) // package -> XML file that claimed it
	for _, tier := range tiers {
		fmt.Fprintf(w, "=== %s ===\n", tier)
		sources, err := planTier(filepath.Join(rootDir, tier), used)
		if err != nil {
			return err
		}
		for _, src := range sources {
			outDir := filepath.Join(outBase, "protocol", tier, src.pkg)
			fmt.Fprintf(w, "  %s -> protocol/%s/%s/\n", filepath.Base(src.xmlPath), tier, src.pkg)
			if err := generateProtocol(src.xmlPath, outDir, src.pkg, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

type source struct {
	xmlPath string
	pkg     string
}

// A collision is an error: silently dropping a file would ship a protocol with
// no bindings.
func planTier(tierDir string, used map[string]string) ([]source, error) {
	files, err := xmlFiles(tierDir)
	if err != nil {
		return nil, err
	}

	claimed := make(map[string]string, len(files))
	for _, path := range files {
		pkg, verSuffix := pkgNameFromFile(path)
		if _, taken := claimed[pkg]; taken || used[pkg] != "" {
			pkg += verSuffix
		}
		if prev, taken := claimed[pkg]; taken {
			return nil, fmt.Errorf("package %q claimed by both %s and %s", pkg, prev, path)
		}
		if prev := used[pkg]; prev != "" {
			return nil, fmt.Errorf("package %q claimed by both %s and %s", pkg, prev, path)
		}
		claimed[pkg] = path
	}

	out := make([]source, 0, len(claimed))
	for _, pkg := range slices.Sorted(maps.Keys(claimed)) {
		used[pkg] = claimed[pkg]
		out = append(out, source{xmlPath: claimed[pkg], pkg: pkg})
	}
	return out, nil
}

func xmlFiles(tierDir string) ([]string, error) {
	entries, err := os.ReadDir(tierDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", tierDir, err)
	}

	var files []string
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		matched, err := filepath.Glob(filepath.Join(tierDir, ent.Name(), "*.xml"))
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", ent.Name(), err)
		}
		files = append(files, matched...)
	}
	slices.Sort(files)
	return files, nil
}

// xdg-shell.xml -> ("xdgshell", "v1").
func pkgNameFromFile(xmlPath string) (pkg, verSuffix string) {
	name := strings.TrimSuffix(filepath.Base(xmlPath), ".xml")
	if m := reVerSuffix.FindString(name); m != "" {
		verSuffix = m[1:]
		name = name[:len(name)-len(m)]
	}
	pkg = strings.ToLower(strings.ReplaceAll(name, "-", ""))
	if pkg == "" {
		pkg = "protocol"
	}
	return pkg, verSuffix
}
