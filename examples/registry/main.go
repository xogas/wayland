package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/xogas/wayland"
)

type handler struct {
	globals *[]wayland.Global
}

func (h handler) Global(g wayland.Global) {
	*h.globals = append(*h.globals, g)
}

func (h handler) GlobalRemove(name uint32) {
	gs := *h.globals
	for i := range gs {
		if gs[i].Name == name {
			*h.globals = append(gs[:i], gs[i+1:]...)
			return
		}
	}
}

func run() error {
	d, err := wayland.Connect()
	if err != nil {
		return fmt.Errorf("connect: set WAYLAND_DISPLAY (with XDG_RUNTIME_DIR) or WAYLAND_SOCKET: %w", err)
	}
	defer func() {
		if err := d.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "registry: close:", err)
		}
	}()

	var globals []wayland.Global
	if _, err := d.GetRegistry(handler{globals: &globals}); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Roundtrip(ctx); err != nil {
		return err
	}

	if len(globals) == 0 {
		return fmt.Errorf("no globals advertised: zero globals received")
	}
	sort.Slice(globals, func(i, j int) bool {
		if globals[i].Interface != globals[j].Interface {
			return globals[i].Interface < globals[j].Interface
		}
		return globals[i].Name < globals[j].Name
	})
	for _, g := range globals {
		fmt.Printf("%-40s v%-3d name=%d\n", g.Interface, g.Version, g.Name)
	}

	required := []string{"wl_compositor", "wl_shm", "xdg_wm_base"}
	for _, name := range required {
		found := false
		for _, g := range globals {
			if g.Interface == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing required global %s", name)
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "registry:", err)
		os.Exit(1)
	}
}
