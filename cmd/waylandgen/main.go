// Command waylandgen generates Go bindings from Wayland protocol XML.
package main

import (
	"flag"
	"fmt"
	"os"
)

type options struct {
	Root string
	Out  string
}

func main() {
	root := flag.String("root", "wayland-protocols", "input directory containing protocol XML files")
	out := flag.String("out", ".", "output module root")
	flag.Parse()
	if err := generate(options{Root: *root, Out: *out}); err != nil {
		fail(err)
	}
}

// generate performs the whole pipeline: discover, parse, build, render in
// memory, then write. Nothing is written before rendering succeeds.
func generate(opt options) error {
	files, err := discoverXML(opt.Root)
	if err != nil {
		return err
	}
	parsed, err := parseAll(files)
	if err != nil {
		return err
	}
	c := buildCorpus(parsed)
	outputs, err := renderCorpus(c)
	if err != nil {
		return err
	}
	return writeOutputs(opt.Out, outputs)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "waylandgen:", err)
	os.Exit(1)
}
