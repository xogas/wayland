//go:build linux

// Minimal Wayland client: connects, discovers all globals, prints them, exits.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xogas/wayland/examples/internal/shared"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dpy, _, globals, err := shared.Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer dpy.Close() //nolint: errcheck

	for _, g := range globals.All() {
		fmt.Printf("interface: '%s', version: %d, name: %d\n", g.Interface, g.Version, g.Name)
	}
}
