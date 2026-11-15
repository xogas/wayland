package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderDeterministic(t *testing.T) {
	xp, err := parseFile("testdata/core/wayland.xml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	model, err := build(xp)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	gen1, meta1, err := render(model)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	gen2, meta2, err := render(model)
	if err != nil {
		t.Fatalf("render second: %v", err)
	}
	if !bytes.Equal(gen1, gen2) || !bytes.Equal(meta1, meta2) {
		t.Fatal("render is not deterministic")
	}
	root := filepath.Join("..", "..")
	wantGen, err := os.ReadFile(filepath.Join(root, "protocol_gen.go"))
	if err != nil {
		t.Fatalf("read protocol_gen.go: %v", err)
	}
	wantMeta, err := os.ReadFile(filepath.Join(root, "protocol_meta_gen.go"))
	if err != nil {
		t.Fatalf("read protocol_meta_gen.go: %v", err)
	}
	if !bytes.Equal(gen1, wantGen) {
		t.Fatal("protocol_gen.go is stale; run go run ./cmd/waylandgen -root cmd/waylandgen/testdata/core -out .")
	}
	if !bytes.Equal(meta1, wantMeta) {
		t.Fatal("protocol_meta_gen.go is stale; run go run ./cmd/waylandgen -root cmd/waylandgen/testdata/core -out .")
	}
}

func TestRegistryBindWireArgs(t *testing.T) {
	xp, err := parseFile("testdata/core/wayland.xml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	model, err := build(xp)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, i := range model.Interfaces {
		if i.Name != "wl_registry" {
			continue
		}
		for _, r := range i.Requests {
			if r.Name != "bind" {
				continue
			}
			if len(r.Args) != 2 || r.Args[0].Name != "name" || r.Args[1].Name != "id" {
				t.Fatalf("wl_registry.bind args = %+v, want name/id only", r.Args)
			}
			return
		}
	}
	t.Fatal("wl_registry.bind not found")
}
