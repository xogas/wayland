package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	var cfg GenerateConfig
	inputFile := flag.String("i", "", "input XML file (required for single-file mode)")
	flag.StringVar(&cfg.OutDir, "o", ".", "output directory")
	flag.StringVar(&cfg.Package, "pkg", "", "package name (default: derived from output directory)")
	flag.StringVar(&cfg.TrimPrefix, "prefix", "", "prefix to trim from type names")
	flag.StringVar(&cfg.TrimSuffix, "suffix", "", "suffix to trim from type names")
	batchDir := flag.String("batch", "", "batch-generate from protocol root dir (disables -i)")
	flag.Parse()

	if *batchDir != "" {
		if err := runBatch(*batchDir, cfg.OutDir); err != nil {
			fmt.Fprintf(os.Stderr, "batch error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *inputFile == "" {
		fmt.Fprintln(os.Stderr, "usage: wayland-scanner -i <input.xml> [-o <dir>] [-pkg <name>] [-prefix <p>] [-suffix <s>]")
		fmt.Fprintln(os.Stderr, "       wayland-scanner -batch <root-dir> [-o <out-dir>]")
		os.Exit(1)
	}

	if cfg.Package == "" {
		cfg.Package = defaultPackage(cfg.OutDir)
	}
	cfg.Package = strings.ToLower(strings.ReplaceAll(cfg.Package, "-", ""))
	if cfg.Package == "" || cfg.Package == "." {
		cfg.Package = "wayland"
	}

	proto, err := Parse(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", *inputFile, err)
		os.Exit(1)
	}

	if err := Generate(proto, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error generating: %v\n", err)
		os.Exit(1)
	}
}

func defaultPackage(outDir string) string {
	abs, err := filepath.Abs(outDir)
	if err != nil {
		abs = outDir
	}
	return filepath.Base(abs)
}
