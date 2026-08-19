//go:build linux

// Minimal Wayland client: connects, lists all advertised globals, exits.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland/examples/internal/shared"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dpy, _, globals, err := shared.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = dpy.Close() }()

	for _, g := range globals.All() {
		fmt.Printf("interface: '%s', version: %d, name: %d\n", g.Interface, g.Version, g.Name)
	}
	return nil
}
