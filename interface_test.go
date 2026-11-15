package wayland

import "testing"

func TestInterfaceByNameStablePreference(t *testing.T) {
	iface, ok := InterfaceByName("xdg_surface")
	if !ok {
		t.Fatal("xdg_surface metadata missing")
	}
	if iface.Version != 7 {
		t.Fatalf("xdg_surface version = %d, want stable 7", iface.Version)
	}
	if _, ok := InterfaceByName("no_such_interface"); ok {
		t.Fatal("unknown interface resolved")
	}
	if got := MustInterface("wl_display").Version; got != 1 {
		t.Fatalf("wl_display version = %d", got)
	}
}
