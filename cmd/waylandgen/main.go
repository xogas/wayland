package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	root := flag.String("root", "wayland-protocols", "input directory containing protocol XML files")
	out := flag.String("out", ".", "output module root")
	flag.Parse()

	files, err := xmlFiles(*root)
	if err != nil {
		fail(err)
	}
	if len(files) != 1 {
		fail(fmt.Errorf("S-01 generator expects one protocol file, got %d under %s", len(files), *root))
	}
	xp, err := parseFile(files[0])
	if err != nil {
		fail(err)
	}
	model, err := build(xp)
	if err != nil {
		fail(err)
	}
	gen, meta, err := render(model)
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(fmt.Errorf("create output %s: %w", *out, err))
	}
	if err := writeAtomic(filepath.Join(*out, "protocol_gen.go"), gen); err != nil {
		fail(err)
	}
	if err := writeAtomic(filepath.Join(*out, "protocol_meta_gen.go"), meta); err != nil {
		fail(err)
	}
}

func xmlFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".xml" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".waylandgen-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	name := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(name)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "waylandgen:", err)
	os.Exit(1)
}
