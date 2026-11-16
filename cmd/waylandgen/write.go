package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type stagedFile struct {
	tmp   string
	final string
	rel   string
}

func writeOutputs(out string, files []renderedFile) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", out, err)
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
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(final), err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(final), ".waylandgen-*")
		if err != nil {
			removeTemps()
			return fmt.Errorf("create temp for %s: %w", f.Rel, err)
		}
		name := tmp.Name()
		staged = append(staged, stagedFile{tmp: name, final: final, rel: f.Rel})
		if _, err := tmp.Write(f.Data); err != nil {
			_ = tmp.Close()
			removeTemps()
			return fmt.Errorf("write %s: %w", f.Rel, err)
		}
		if err := tmp.Chmod(0o644); err != nil {
			_ = tmp.Close()
			removeTemps()
			return fmt.Errorf("chmod %s: %w", f.Rel, err)
		}
		if err := tmp.Close(); err != nil {
			removeTemps()
			return fmt.Errorf("close %s: %w", f.Rel, err)
		}
	}
	for i, s := range staged {
		if err := os.Rename(s.tmp, s.final); err != nil {
			for _, rest := range staged[i:] {
				_ = os.Remove(rest.tmp)
			}
			return fmt.Errorf("rename %s: %w", s.rel, err)
		}
	}
	return nil
}
