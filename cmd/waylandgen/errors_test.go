package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorCategories(t *testing.T) {
	t.Run("input", func(t *testing.T) {
		err := inputError("proto.xml", "version", errors.New("bad version"))
		var ge *GeneratorError
		if !errors.As(err, &ge) {
			t.Fatalf("inputError = %T, want *GeneratorError", err)
		}
		if ge.Kind != "input" {
			t.Fatalf("kind = %q, want input", ge.Kind)
		}
		if IsWriteError(err) {
			t.Fatal("input error reported as write error")
		}
		for _, want := range []string{"proto.xml", "version", "bad version"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q lacks %q", err, want)
			}
		}

		// A real generator failure keeps the same classification.
		genErr := generate(options{Root: filepath.Join(t.TempDir(), "missing"), Out: t.TempDir()})
		if genErr == nil {
			t.Fatal("generate with missing root succeeded")
		}
		var genTyped *GeneratorError
		if !errors.As(genErr, &genTyped) {
			t.Fatalf("generate error = %T, want *GeneratorError", genErr)
		}
		if genTyped.Kind != "input" || IsWriteError(genErr) {
			t.Fatalf("generate error kind = %q, want input", genTyped.Kind)
		}
	})

	t.Run("write", func(t *testing.T) {
		err := writeError("out.go", "write", errors.New("disk full"))
		var ge *GeneratorError
		if !errors.As(err, &ge) {
			t.Fatalf("writeError = %T, want *GeneratorError", err)
		}
		if ge.Kind != "write" || !IsWriteError(err) {
			t.Fatalf("write error = %+v, want write kind", ge)
		}
		for _, want := range []string{"out.go", "write", "disk full"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q lacks %q", err, want)
			}
		}
	})
}
