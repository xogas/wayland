package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
)

var reVerSuffix = regexp.MustCompile(`-v\d+$`)

const corePkg = "wayland"

var tiers = []string{"stable", "staging", "unstable", "experimental"}

// generateProtocol writes one XML file's bindings into outDir. An empty prefix
// is derived from the interface names.
func generateProtocol(xmlPath, outDir, pkg, prefix string) error {
	p, err := Parse(xmlPath)
	if err != nil {
		return err
	}
	if prefix == "" {
		prefix = autoPrefix(p.Interfaces)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	for _, in := range buildProtocol(p, pkg, prefix) {
		if err := writeGoFile(outDir, in); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(xmlPath), err)
		}
	}
	return nil
}

// A format error leaves the unformatted output beside the target as .debug.
func writeGoFile(outDir string, in goInterface) error {
	var buf bytes.Buffer
	for _, tmpl := range fileTemplates {
		if err := tmpl.Execute(&buf, in); err != nil {
			return fmt.Errorf("%s: render %s: %w", in.TypeName, tmpl.Name(), err)
		}
	}

	path := filepath.Join(outDir, snakeCase(in.TypeName)+"_gen.go")
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		debug := path + ".debug"
		_ = os.WriteFile(debug, buf.Bytes(), 0o644)
		return fmt.Errorf("format %s (raw output at %s): %w", path, debug, err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
