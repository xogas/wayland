package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWaylandXML(t *testing.T) {
	proto, err := Parse("../../wayland-protocols/wayland.xml")
	if err != nil {
		t.Fatalf("Parse wayland.xml: %v", err)
	}

	var display *Interface
	for i := range proto.Interfaces {
		if proto.Interfaces[i].Name == "wl_display" {
			display = &proto.Interfaces[i]
			break
		}
	}
	if display == nil {
		t.Fatal("wl_display not found")
	}
	if display.Version != 1 {
		t.Errorf("wl_display version = %d, want 1", display.Version)
	}

	hasSync := false
	hasGetRegistry := false
	for _, req := range display.Requests {
		if req.Name == "sync" {
			hasSync = true
		}
		if req.Name == "get_registry" {
			hasGetRegistry = true
		}
	}
	if !hasSync {
		t.Error("wl_display missing sync request")
	}
	if !hasGetRegistry {
		t.Error("wl_display missing get_registry request")
	}

	hasError := false
	hasDeleteID := false
	for _, ev := range display.Events {
		if ev.Name == "error" {
			hasError = true
		}
		if ev.Name == "delete_id" {
			hasDeleteID = true
		}
	}
	if !hasError {
		t.Error("wl_display missing error event")
	}
	if !hasDeleteID {
		t.Error("wl_display missing delete_id event")
	}

	var shm *Interface
	for i := range proto.Interfaces {
		if proto.Interfaces[i].Name == "wl_shm" {
			shm = &proto.Interfaces[i]
			break
		}
	}
	if shm == nil {
		t.Fatal("wl_shm not found")
	}

	var formatEnum *Enum
	for i := range shm.Enums {
		if shm.Enums[i].Name == "format" {
			formatEnum = &shm.Enums[i]
			break
		}
	}
	if formatEnum == nil {
		t.Fatal("wl_shm format enum not found")
	}

	hasHexEntry := false
	for _, e := range formatEnum.Entries {
		if e.Value > 0xFF {
			hasHexEntry = true
			break
		}
	}
	if !hasHexEntry {
		t.Error("wl_shm format enum has no hex-value entries")
	}

	argb8888 := findEntry(formatEnum, "argb8888")
	if argb8888 == nil {
		t.Error("wl_shm format missing argb8888 entry")
	} else if IntValue(0) != argb8888.Value {
		t.Errorf("argb8888 value = %d, want 0", argb8888.Value)
	}

	c8 := findEntry(formatEnum, "c8")
	if c8 == nil {
		t.Error("wl_shm format missing c8 entry")
	} else if IntValue(0x20203843) != c8.Value {
		t.Errorf("c8 value = %d, want %d", c8.Value, 0x20203843)
	}

	var registry *Interface
	for i := range proto.Interfaces {
		if proto.Interfaces[i].Name == "wl_registry" {
			registry = &proto.Interfaces[i]
			break
		}
	}
	if registry == nil {
		t.Fatal("wl_registry not found")
	}

	var bind *Request
	for i := range registry.Requests {
		if registry.Requests[i].Name == "bind" {
			bind = &registry.Requests[i]
			break
		}
	}
	if bind == nil {
		t.Fatal("wl_registry.bind request not found")
	}

	var idArg *Arg
	for i := range bind.Args {
		if bind.Args[i].Name == "id" {
			idArg = &bind.Args[i]
			break
		}
	}
	if idArg == nil {
		t.Fatal("wl_registry.bind missing id arg")
	}
	if idArg.Interface != "" {
		t.Errorf("wl_registry.bind id arg has interface=%q, want empty", idArg.Interface)
	}
}

func findEntry(enum *Enum, name string) *Entry {
	for i := range enum.Entries {
		if enum.Entries[i].Name == name {
			return &enum.Entries[i]
		}
	}
	return nil
}

func TestParseXDGShellXML(t *testing.T) {
	proto, err := Parse("../../wayland-protocols/stable/xdg-shell/xdg-shell.xml")
	if err != nil {
		t.Fatalf("Parse xdg-shell.xml: %v", err)
	}

	var wmBase *Interface
	for i := range proto.Interfaces {
		if proto.Interfaces[i].Name == "xdg_wm_base" {
			wmBase = &proto.Interfaces[i]
			break
		}
	}
	if wmBase == nil {
		t.Fatal("xdg_wm_base not found")
	}

	var destroy *Request
	for i := range wmBase.Requests {
		if wmBase.Requests[i].Name == "destroy" {
			destroy = &wmBase.Requests[i]
			break
		}
	}
	if destroy == nil {
		t.Fatal("xdg_wm_base destroy request not found")
	}
	if destroy.Type != "destructor" {
		t.Errorf("xdg_wm_base destroy type = %q, want destructor", destroy.Type)
	}
}

func TestParseAllXML(t *testing.T) {
	protocolsDir := "../../wayland-protocols"
	err := filepath.WalkDir(protocolsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".xml") {
			return nil
		}
		_, perr := Parse(path)
		if perr != nil {
			rel, _ := filepath.Rel(protocolsDir, path)
			t.Errorf("Parse %s: %v", rel, perr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
