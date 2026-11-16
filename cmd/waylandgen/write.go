package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
