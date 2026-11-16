// Command waylandgen generates Go bindings from Wayland protocol XML.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GeneratorError is the typed error for generator input, semantic and write
// failures. The command exits non-zero on either kind; tests distinguish them
// with errors.As and Kind.
type GeneratorError struct {
	Kind  string // "input" or "write"
	Path  string
	Field string
	Err   error
}

func (e *GeneratorError) Error() string {
	var b strings.Builder
	if e.Path != "" {
		b.WriteString(e.Path)
		b.WriteString(": ")
	}
	if e.Field != "" {
		b.WriteString(e.Field)
		b.WriteString(": ")
	}
	b.WriteString(e.Err.Error())
	return b.String()
}

func (e *GeneratorError) Unwrap() error { return e.Err }

func inputError(path, field string, err error) *GeneratorError {
	return &GeneratorError{Kind: "input", Path: path, Field: field, Err: err}
}

func writeError(path, field string, err error) *GeneratorError {
	return &GeneratorError{Kind: "write", Path: path, Field: field, Err: err}
}

// IsWriteError reports whether err is a generator write error (E-14).
func IsWriteError(err error) bool {
	var ge *GeneratorError
	return errors.As(err, &ge) && ge.Kind == "write"
}

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

// generate performs the whole pipeline: discover, DTD-validate, parse, build,
// validate, render in memory, then write. Nothing is written before rendering
// succeeds.
func generate(opt options) error {
	if opt.Root == "" {
		return inputError("", "root", fmt.Errorf("empty root"))
	}
	if opt.Out == "" {
		return inputError("", "out", fmt.Errorf("empty output directory"))
	}
	files, err := discoverXML(opt.Root)
	if err != nil {
		return err
	}
	if err := checkLayout(files); err != nil {
		return err
	}
	if err := validateDTD(opt.Root, files); err != nil {
		return err
	}
	parsed, err := parseAll(files)
	if err != nil {
		return err
	}
	c, err := buildCorpus(parsed)
	if err != nil {
		return err
	}
	outputs, err := renderCorpus(c)
	if err != nil {
		return err
	}
	return writeOutputs(opt.Out, outputs)
}

type stagedFile struct {
	tmp   string
	final string
	rel   string
}

func writeOutputs(out string, files []renderedFile) error {
	if len(files) == 0 {
		return writeError(out, "output", fmt.Errorf("no generated files"))
	}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		clean := filepath.Clean(filepath.FromSlash(f.Rel))
		if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
			return writeError(f.Rel, "output", fmt.Errorf("unsafe output path"))
		}
		if seen[clean] {
			return writeError(f.Rel, "output", fmt.Errorf("duplicate output path"))
		}
		seen[clean] = true
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return writeError(out, "mkdir", err)
	}
	var staged []stagedFile
	removeTemps := func() {
		for _, s := range staged {
			_ = os.Remove(s.tmp)
		}
	}
	for _, f := range files {
		final := filepath.Join(out, filepath.FromSlash(f.Rel))
		if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
			removeTemps()
			return writeError(f.Rel, "mkdir", err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(final), ".waylandgen-*")
		if err != nil {
			removeTemps()
			return writeError(f.Rel, "create", err)
		}
		name := tmp.Name()
		staged = append(staged, stagedFile{tmp: name, final: final, rel: f.Rel})
		if _, err := tmp.Write(f.Data); err != nil {
			_ = tmp.Close()
			removeTemps()
			return writeError(f.Rel, "write", err)
		}
		if err := tmp.Chmod(0o644); err != nil {
			_ = tmp.Close()
			removeTemps()
			return writeError(f.Rel, "chmod", err)
		}
		if err := tmp.Close(); err != nil {
			removeTemps()
			return writeError(f.Rel, "close", err)
		}
	}
	for i, s := range staged {
		if err := os.Rename(s.tmp, s.final); err != nil {
			for _, rest := range staged[i:] {
				_ = os.Remove(rest.tmp)
			}
			return writeError(s.rel, "rename", err)
		}
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "waylandgen:", err)
	os.Exit(1)
}
