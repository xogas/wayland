package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
)

// GenerateConfig controls code generation for a protocol file.
type GenerateConfig struct {
	OutDir     string
	Package    string
	TrimPrefix string
	TrimSuffix string
	AutoPrefix bool
}

// Generate writes one <name>_gen.go file per interface in the protocol.
func Generate(proto *Protocol, cfg GenerateConfig) error {
	if cfg.AutoPrefix {
		cfg.TrimPrefix = autoPrefix(proto.Interfaces)
	}

	ifaceSet := make(map[string]bool, len(proto.Interfaces))
	for i := range proto.Interfaces {
		ifaceSet[proto.Interfaces[i].Name] = true
	}

	for i := range proto.Interfaces {
		iface := &proto.Interfaces[i]
		if err := generateInterface(iface, cfg, ifaceSet); err != nil {
			return fmt.Errorf("%s: %w", iface.Name, err)
		}
	}
	return nil
}

func generateInterface(iface *Interface, cfg GenerateConfig, knownIface map[string]bool) error {
	injectSyntheticArgs(iface)
	tn := typeName(iface.Name, cfg.TrimPrefix, cfg.TrimSuffix)

	data, err := buildData(iface, tn, cfg, knownIface)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	for _, t := range fileTemplates {
		if err := t.Execute(&buf, data); err != nil {
			return fmt.Errorf("render %s: %w", t.Name(), err)
		}
	}

	path := filepath.Join(cfg.OutDir, snakeCase(tn)+"_gen.go")
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		debugPath := path + ".debug"
		_ = os.WriteFile(debugPath, buf.Bytes(), 0644)
		return fmt.Errorf("format %s (raw output at %s): %w", path, debugPath, err)
	}
	return os.WriteFile(path, formatted, 0644)
}
